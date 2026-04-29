package p2p

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
	"github.com/hcp-uw/mosaic/internal/stun"
)

// pairedClients spins up a STUN server and returns two paired clients
// (leader = first, follower = second).
func pairedClients(t *testing.T) (server *stun.Server, leader, follower *Client, cleanup func()) {
	t.Helper()
	cfg := &stun.ServerConfig{
		ListenAddress: "127.0.0.1:0",
		ClientTimeout: 30 * time.Second,
		EnableLogging: false,
	}
	server = stun.NewServer(cfg)
	if err := server.Start(cfg); err != nil {
		t.Fatalf("Start STUN server: %v", err)
	}
	addr := server.GetConn().LocalAddr().(*net.UDPAddr)

	leader, err := NewClient(DefaultClientConfig(addr.String()))
	if err != nil {
		t.Fatalf("NewClient leader: %v", err)
	}
	follower, err = NewClient(DefaultClientConfig(addr.String()))
	if err != nil {
		t.Fatalf("NewClient follower: %v", err)
	}

	leaderPaired := make(chan struct{}, 1)
	followerPaired := make(chan struct{}, 1)
	leader.OnPeerAssigned(func(*PeerInfo) {
		select {
		case leaderPaired <- struct{}{}:
		default:
		}
	})
	follower.OnPeerAssigned(func(*PeerInfo) {
		select {
		case followerPaired <- struct{}{}:
		default:
		}
	})

	if err := leader.ConnectToStun(); err != nil {
		t.Fatalf("ConnectToStun leader: %v", err)
	}
	// Give the server a beat to mark the first client as leader before
	// the second registers.
	time.Sleep(50 * time.Millisecond)
	if err := follower.ConnectToStun(); err != nil {
		t.Fatalf("ConnectToStun follower: %v", err)
	}

	wait := func(ch chan struct{}, name string) {
		select {
		case <-ch:
		case <-time.After(3 * time.Second):
			t.Fatalf("timeout waiting for %s pairing", name)
		}
	}
	wait(leaderPaired, "leader")
	wait(followerPaired, "follower")

	// Leader needs the follower in its peers map and vice versa, plus
	// connected via the existing socket so SendToPeer works.
	leader.mutex.RLock()
	for _, p := range leader.peers {
		leader.mutex.RUnlock()
		if err := leader.ConnectToPeer(p); err != nil {
			t.Fatalf("leader ConnectToPeer: %v", err)
		}
		leader.mutex.RLock()
		break
	}
	leader.mutex.RUnlock()

	follower.mutex.RLock()
	for _, p := range follower.peers {
		follower.mutex.RUnlock()
		if err := follower.ConnectToPeer(p); err != nil {
			t.Fatalf("follower ConnectToPeer: %v", err)
		}
		follower.mutex.RLock()
		break
	}
	follower.mutex.RUnlock()

	cleanup = func() {
		leader.DisconnectFromStun()
		follower.DisconnectFromStun()
		server.Stop()
	}
	return server, leader, follower, cleanup
}

func TestDataOps_StoreThenGet(t *testing.T) {
	_, leader, follower, cleanup := pairedClients(t)
	defer cleanup()

	// Follower acts as the storage node.
	store := map[string][]byte{}
	var storeMu sync.Mutex

	follower.SetDataHandler(DataHandler{
		OnStoreRequest: func(req *api.SignedStoreRequest, _ string) (*api.SignedStoreAck, error) {
			if err := req.Verify(); err != nil {
				t.Errorf("follower verify store: %v", err)
				return nil, err
			}
			storeMu.Lock()
			store[string(req.Hash)] = append([]byte(nil), req.Data...)
			storeMu.Unlock()
			// The follower needs its own keypair to sign the ack.
			return api.NewSignedStoreAck(followerPriv, req.Hash), nil
		},
		OnGetRequest: func(req *api.GetRequest, _ string) *api.GetResponse {
			storeMu.Lock()
			defer storeMu.Unlock()
			if data, ok := store[string(req.Hash)]; ok {
				return &api.GetResponse{Hash: req.Hash, Data: data}
			}
			return &api.GetResponse{Hash: req.Hash}
		},
	})

	// Find the follower's peer ID from the leader's perspective.
	peerID := pickPeerID(leader)

	payload := []byte("the answer is 42")
	req := api.NewSignedStoreRequest(leaderPriv, payload)

	ack, err := leader.SendStoreRequest(peerID, req, 2*time.Second)
	if err != nil {
		t.Fatalf("SendStoreRequest: %v", err)
	}
	if err := ack.Verify(); err != nil {
		t.Fatalf("ack.Verify: %v", err)
	}
	if string(ack.Hash) != string(req.Hash) {
		t.Errorf("ack hash mismatch")
	}

	// Now fetch it back.
	resp, err := leader.SendGetRequest(peerID, &api.GetRequest{Hash: req.Hash}, 2*time.Second)
	if err != nil {
		t.Fatalf("SendGetRequest: %v", err)
	}
	if string(resp.Data) != string(payload) {
		t.Errorf("Get returned %q, want %q", resp.Data, payload)
	}
}

func TestDataOps_DeleteRequest(t *testing.T) {
	_, leader, follower, cleanup := pairedClients(t)
	defer cleanup()

	deleted := make(chan [32]byte, 1)
	follower.SetDataHandler(DataHandler{
		OnDeleteRequest: func(req *api.SignedDeleteRequest, _ string) error {
			if err := req.Verify(); err != nil {
				return err
			}
			var h [32]byte
			copy(h[:], req.Hash)
			deleted <- h
			return nil
		},
	})

	peerID := pickPeerID(leader)
	hash := sha256.Sum256([]byte("doomed shard"))
	delReq := api.NewSignedDeleteRequest(leaderPriv, hash[:])
	if err := leader.SendDeleteRequest(peerID, delReq); err != nil {
		t.Fatalf("SendDeleteRequest: %v", err)
	}

	select {
	case got := <-deleted:
		if got != hash {
			t.Errorf("delete handler got hash %x, want %x", got[:4], hash[:4])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for delete handler")
	}
}

func TestDataOps_ManifestUpdateBroadcast(t *testing.T) {
	_, leader, follower, cleanup := pairedClients(t)
	defer cleanup()

	got := make(chan int, 1)
	follower.SetDataHandler(DataHandler{
		OnManifestUpdate: func(data *api.PeerManifestUpdateData, _ string) {
			got <- len(data.StoreAcks)
		},
	})

	if err := leader.BroadcastManifestUpdate(api.PeerManifestUpdateData{
		StoreAcks: make([]json.RawMessage, 3),
	}); err != nil {
		t.Fatalf("BroadcastManifestUpdate: %v", err)
	}
	select {
	case n := <-got:
		if n != 3 {
			t.Errorf("ack count = %d, want 3", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for manifest update")
	}
}

// pickPeerID returns the first paired peer's ID, or fails the test.
func pickPeerID(c *Client) string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	for id := range c.peers {
		return id
	}
	return ""
}

// Test fixture keys, generated once per test binary.
var (
	leaderPub, leaderPriv     = mustEdKey()
	followerPub, followerPriv = mustEdKey()
)

var _ = leaderPub
var _ = followerPub

func mustEdKey() (ed25519.PublicKey, ed25519.PrivateKey) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	return pub, priv
}
