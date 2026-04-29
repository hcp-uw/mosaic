package p2p

import (
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
)

// DataHandler holds the application-level callbacks the daemon installs
// to react to incoming P2P data operations. Any callback may be nil — in
// which case the message is dropped (logged via the error callback).
type DataHandler struct {
	OnStoreRequest    func(req *api.SignedStoreRequest, fromPeer string) (*api.SignedStoreAck, error)
	OnDeleteRequest   func(req *api.SignedDeleteRequest, fromPeer string) error
	OnGetRequest      func(req *api.GetRequest, fromPeer string) *api.GetResponse
	OnManifestUpdate  func(data *api.PeerManifestUpdateData, fromPeer string)
	OnStoreAck        func(ack *api.SignedStoreAck, fromPeer string)
	OnGetResponse     func(resp *api.GetResponse, fromPeer string)
}

// dataOpsState holds per-Client mutable state for tracking in-flight
// requests. It lives on Client (set up in NewClient).
type dataOpsState struct {
	mu             sync.Mutex
	handler        DataHandler
	storeAcks      map[string][]chan *api.SignedStoreAck // hex(hash) -> waiters
	getResps       map[string][]chan *api.GetResponse
	chunks         *chunkAssembler
	pubkeyToPeerID map[string]string // hex(ed25519 pubkey) → IP:port connection key
}

func newDataOpsState() *dataOpsState {
	return &dataOpsState{
		storeAcks:      map[string][]chan *api.SignedStoreAck{},
		getResps:       map[string][]chan *api.GetResponse{},
		chunks:         newChunkAssembler(),
		pubkeyToPeerID: map[string]string{},
	}
}

// RegisterPubkeyMapping records that the peer with the given ed25519
// pubkey hex is reachable via peerID (the IP:port key in c.peers).
// Called after receiving a signed store ack so DownloadFile can resolve
// manifest replica IDs (pubkey hex) to connection keys (IP:port).
func (c *Client) RegisterPubkeyMapping(pubkeyHex, peerID string) {
	c.dataOps.mu.Lock()
	c.dataOps.pubkeyToPeerID[pubkeyHex] = peerID
	c.dataOps.mu.Unlock()
}

// SetDataHandler installs application-level callbacks. Pass an empty
// DataHandler to clear them.
func (c *Client) SetDataHandler(h DataHandler) {
	c.dataOps.mu.Lock()
	c.dataOps.handler = h
	c.dataOps.mu.Unlock()
}

// SendStoreRequest sends a signed store request to peerID and blocks
// until the matching ack arrives or timeout elapses.
func (c *Client) SendStoreRequest(peerID string, req *api.SignedStoreRequest, timeout time.Duration) (*api.SignedStoreAck, error) {
	key := hex.EncodeToString(req.Hash)
	ch := make(chan *api.SignedStoreAck, 1)
	c.registerStoreAckWaiter(key, ch)
	defer c.unregisterStoreAckWaiter(key, ch)

	msg := api.NewPeerStoreRequestMessage(c.id, req)
	if err := c.SendToPeer(peerID, msg); err != nil {
		return nil, fmt.Errorf("send store request: %w", err)
	}
	select {
	case ack := <-ch:
		return ack, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout waiting for store ack from %s", peerID)
	}
}

// SendGetRequest sends an unsigned shard fetch request and waits for the
// peer's response (matched by hash).
func (c *Client) SendGetRequest(peerID string, req *api.GetRequest, timeout time.Duration) (*api.GetResponse, error) {
	key := hex.EncodeToString(req.Hash)
	ch := make(chan *api.GetResponse, 1)
	c.registerGetResponseWaiter(key, ch)
	defer c.unregisterGetResponseWaiter(key, ch)

	msg := api.NewPeerGetRequestMessage(c.id, req)
	if err := c.SendToPeer(peerID, msg); err != nil {
		return nil, fmt.Errorf("send get request: %w", err)
	}
	select {
	case resp := <-ch:
		return resp, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout waiting for get response from %s", peerID)
	}
}

// SendDeleteRequest fires a signed delete request without waiting for
// confirmation. Deletion is best-effort; the manifest tombstone is the
// authoritative record.
func (c *Client) SendDeleteRequest(peerID string, req *api.SignedDeleteRequest) error {
	msg := api.NewPeerDeleteRequestMessage(c.id, req)
	return c.SendToPeer(peerID, msg)
}

// BroadcastDeleteRequest sends the delete to every connected peer.
func (c *Client) BroadcastDeleteRequest(req *api.SignedDeleteRequest) error {
	msg := api.NewPeerDeleteRequestMessage(c.id, req)
	return c.SendToAllPeers(msg)
}

// BroadcastManifestUpdate gossips contracts to every connected peer.
func (c *Client) BroadcastManifestUpdate(data api.PeerManifestUpdateData) error {
	msg := api.NewPeerManifestUpdateMessage(c.id, data)
	return c.SendToAllPeers(msg)
}

func (c *Client) registerStoreAckWaiter(key string, ch chan *api.SignedStoreAck) {
	c.dataOps.mu.Lock()
	c.dataOps.storeAcks[key] = append(c.dataOps.storeAcks[key], ch)
	c.dataOps.mu.Unlock()
}

func (c *Client) unregisterStoreAckWaiter(key string, ch chan *api.SignedStoreAck) {
	c.dataOps.mu.Lock()
	defer c.dataOps.mu.Unlock()
	waiters := c.dataOps.storeAcks[key]
	for i, w := range waiters {
		if w == ch {
			c.dataOps.storeAcks[key] = append(waiters[:i], waiters[i+1:]...)
			break
		}
	}
	if len(c.dataOps.storeAcks[key]) == 0 {
		delete(c.dataOps.storeAcks, key)
	}
}

func (c *Client) registerGetResponseWaiter(key string, ch chan *api.GetResponse) {
	c.dataOps.mu.Lock()
	c.dataOps.getResps[key] = append(c.dataOps.getResps[key], ch)
	c.dataOps.mu.Unlock()
}

func (c *Client) unregisterGetResponseWaiter(key string, ch chan *api.GetResponse) {
	c.dataOps.mu.Lock()
	defer c.dataOps.mu.Unlock()
	waiters := c.dataOps.getResps[key]
	for i, w := range waiters {
		if w == ch {
			c.dataOps.getResps[key] = append(waiters[:i], waiters[i+1:]...)
			break
		}
	}
	if len(c.dataOps.getResps[key]) == 0 {
		delete(c.dataOps.getResps, key)
	}
}

// Receive-side dispatch ----------------------------------------------------
// Called from processPeerMessage when a data-op message arrives.

func (c *Client) handleStoreRequest(msg *api.Message) {
	req, err := msg.GetStoreRequest()
	if err != nil {
		c.notifyError(fmt.Errorf("parse store request: %w", err))
		return
	}
	c.dataOps.mu.Lock()
	h := c.dataOps.handler.OnStoreRequest
	c.dataOps.mu.Unlock()
	if h == nil {
		c.notifyError(fmt.Errorf("received PeerStoreRequest but no handler installed"))
		return
	}
	ack, err := h(req, msg.Sign.PubKey)
	if err != nil {
		c.notifyError(fmt.Errorf("OnStoreRequest: %w", err))
		return
	}
	if ack == nil {
		return
	}
	respMsg := api.NewPeerStoreAckMessage(c.id, ack)
	if err := c.SendToPeer(msg.Sign.PubKey, respMsg); err != nil {
		c.notifyError(fmt.Errorf("send store ack: %w", err))
	}
}

func (c *Client) handleStoreAck(msg *api.Message) {
	ack, err := msg.GetStoreAck()
	if err != nil {
		c.notifyError(fmt.Errorf("parse store ack: %w", err))
		return
	}
	key := hex.EncodeToString(ack.Hash)
	c.dataOps.mu.Lock()
	waiters := c.dataOps.storeAcks[key]
	h := c.dataOps.handler.OnStoreAck
	c.dataOps.mu.Unlock()
	for _, w := range waiters {
		select {
		case w <- ack:
		default:
		}
	}
	if h != nil {
		h(ack, msg.Sign.PubKey)
	}
}

func (c *Client) handleGetRequest(msg *api.Message) {
	req, err := msg.GetGetRequest()
	if err != nil {
		c.notifyError(fmt.Errorf("parse get request: %w", err))
		return
	}
	c.dataOps.mu.Lock()
	h := c.dataOps.handler.OnGetRequest
	c.dataOps.mu.Unlock()
	if h == nil {
		c.notifyError(fmt.Errorf("received PeerGetRequest but no handler installed"))
		return
	}
	resp := h(req, msg.Sign.PubKey)
	if resp == nil {
		return
	}
	respMsg := api.NewPeerGetResponseMessage(c.id, resp)
	if err := c.SendToPeer(msg.Sign.PubKey, respMsg); err != nil {
		c.notifyError(fmt.Errorf("send get response: %w", err))
	}
}

func (c *Client) handleGetResponse(msg *api.Message) {
	resp, err := msg.GetGetResponse()
	if err != nil {
		c.notifyError(fmt.Errorf("parse get response: %w", err))
		return
	}
	key := hex.EncodeToString(resp.Hash)
	c.dataOps.mu.Lock()
	waiters := c.dataOps.getResps[key]
	h := c.dataOps.handler.OnGetResponse
	c.dataOps.mu.Unlock()
	for _, w := range waiters {
		select {
		case w <- resp:
		default:
		}
	}
	if h != nil {
		h(resp, msg.Sign.PubKey)
	}
}

func (c *Client) handleDeleteRequest(msg *api.Message) {
	req, err := msg.GetDeleteRequest()
	if err != nil {
		c.notifyError(fmt.Errorf("parse delete request: %w", err))
		return
	}
	c.dataOps.mu.Lock()
	h := c.dataOps.handler.OnDeleteRequest
	c.dataOps.mu.Unlock()
	if h == nil {
		return
	}
	if err := h(req, msg.Sign.PubKey); err != nil {
		c.notifyError(fmt.Errorf("OnDeleteRequest: %w", err))
	}
}

// handleChunkedFrame buffers an incoming chunk and, when the full
// message is reassembled, dispatches it back through processPeerMessage
// as if it had arrived as a single datagram.
func (c *Client) handleChunkedFrame(msg *api.Message) {
	chunk, err := msg.GetChunkedFrame()
	if err != nil {
		c.notifyError(fmt.Errorf("parse chunk: %w", err))
		return
	}
	assembled, complete, err := c.dataOps.chunks.add(chunk)
	if err != nil {
		c.notifyError(fmt.Errorf("chunk reassembly: %w", err))
		return
	}
	if !complete {
		return
	}
	c.processPeerMessage(assembled)
}

func (c *Client) handleManifestUpdate(msg *api.Message) {
	data, err := msg.GetManifestUpdate()
	if err != nil {
		c.notifyError(fmt.Errorf("parse manifest update: %w", err))
		return
	}
	c.dataOps.mu.Lock()
	h := c.dataOps.handler.OnManifestUpdate
	c.dataOps.mu.Unlock()
	if h == nil {
		return
	}
	h(data, msg.Sign.PubKey)
}
