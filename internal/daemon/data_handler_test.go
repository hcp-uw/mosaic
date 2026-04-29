package daemon

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
	"github.com/hcp-uw/mosaic/internal/manifest"
)

func TestDataHandler_StoreThenGet(t *testing.T) {
	a := newAppForTest(t)

	// Build a signed store request with a fresh key (the request signer
	// is a different identity than the receiving node — that's normal).
	priv := mustEd25519PrivKey(t)
	payload := []byte("payload bytes")
	req := api.NewSignedStoreRequest(priv, payload)

	ack, err := a.handleStoreRequest(req, "fromPeer")
	if err != nil {
		t.Fatalf("handleStoreRequest: %v", err)
	}
	if err := ack.Verify(); err != nil {
		t.Fatalf("ack.Verify: %v", err)
	}

	resp := a.handleGetRequest(&api.GetRequest{Hash: req.Hash}, "fromPeer")
	if !bytes.Equal(resp.Data, payload) {
		t.Errorf("Get returned %q, want %q", resp.Data, payload)
	}
}

func TestDataHandler_StoreRejectsBadSignature(t *testing.T) {
	a := newAppForTest(t)
	priv := mustEd25519PrivKey(t)
	req := api.NewSignedStoreRequest(priv, []byte("hi"))
	req.Signature[0] ^= 0xff
	if _, err := a.handleStoreRequest(req, ""); err == nil {
		t.Fatal("expected handleStoreRequest to reject tampered signature")
	}
}

func TestDataHandler_StoreRespectsQuota(t *testing.T) {
	a := newAppForTest(t)
	if err := a.SetQuota(8); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}
	priv := mustEd25519PrivKey(t)
	req := api.NewSignedStoreRequest(priv, bytes.Repeat([]byte{1}, 32))
	if _, err := a.handleStoreRequest(req, ""); err == nil {
		t.Fatal("expected quota rejection")
	}
}

func TestDataHandler_GetMissing(t *testing.T) {
	a := newAppForTest(t)
	resp := a.handleGetRequest(&api.GetRequest{Hash: bytes.Repeat([]byte{0}, 32)}, "")
	if len(resp.Data) != 0 {
		t.Errorf("expected empty Data for missing shard, got %d bytes", len(resp.Data))
	}
}

func TestDataHandler_DeleteUnknownIsNoop(t *testing.T) {
	a := newAppForTest(t)
	priv := mustEd25519PrivKey(t)
	req := api.NewSignedDeleteRequest(priv, bytes.Repeat([]byte{0}, 32))
	if err := a.handleDeleteRequest(req, ""); err != nil {
		t.Errorf("expected no error for unknown shard delete, got %v", err)
	}
}

func TestDataHandler_ManifestUpdate_AppliesContracts(t *testing.T) {
	a := newAppForTest(t)
	priv := mustEd25519PrivKey(t)

	// Two valid store acks for two distinct hashes.
	hash1 := bytes.Repeat([]byte{1}, 32)
	hash2 := bytes.Repeat([]byte{2}, 32)
	ack1 := api.NewSignedStoreAck(priv, hash1)
	ack2 := api.NewSignedStoreAck(priv, hash2)
	rawAck1, _ := json.Marshal(ack1)
	rawAck2, _ := json.Marshal(ack2)

	a.handleManifestUpdate(&api.PeerManifestUpdateData{
		StoreAcks: []json.RawMessage{rawAck1, rawAck2},
	}, "")

	var h1 [32]byte
	copy(h1[:], hash1)
	if r := a.Manifest.Manifest().Replicas(h1); len(r) != 1 {
		t.Errorf("expected 1 replica for hash1, got %v", r)
	}
}

func TestDataHandler_ManifestUpdate_RejectsBadAck(t *testing.T) {
	a := newAppForTest(t)
	priv := mustEd25519PrivKey(t)
	ack := api.NewSignedStoreAck(priv, bytes.Repeat([]byte{3}, 32))
	ack.Signature[0] ^= 0xff
	rawAck, _ := json.Marshal(ack)
	a.handleManifestUpdate(&api.PeerManifestUpdateData{
		StoreAcks: []json.RawMessage{rawAck},
	}, "")
	// No replica should have been recorded for the bad ack.
	var h [32]byte
	copy(h[:], ack.Hash)
	if r := a.Manifest.Manifest().Replicas(h); len(r) != 0 {
		t.Errorf("expected no replicas for bad ack, got %v", r)
	}
}

func TestDataHandler_ManifestUpdate_FileMeta(t *testing.T) {
	a := newAppForTest(t)
	pub, priv, err := generateKey()
	if err != nil {
		t.Fatal(err)
	}
	meta := manifest.FileMeta{
		Filename:      "remote.txt",
		Size:          100,
		EncryptedSize: 128,
		DataShards:    4,
		ParityShards:  2,
		BlockSize:     32,
		Shards:        [][32]byte{},
		WrappedKey:    []byte("wk"),
	}
	meta.Sign(priv)
	raw, _ := json.Marshal(meta)
	a.handleManifestUpdate(&api.PeerManifestUpdateData{
		FileMetas: []json.RawMessage{raw},
	}, "")
	files := a.Manifest.Manifest().FilesOwnedBy(pub)
	if len(files) != 1 || files[0].Filename != "remote.txt" {
		t.Errorf("expected remote.txt in manifest after gossip, got %v", files)
	}
}

func TestDataHandler_ManifestUpdate_DeleteShard(t *testing.T) {
	a := newAppForTest(t)
	priv := mustEd25519PrivKey(t)

	// Pre-record a replica.
	hash := bytes.Repeat([]byte{4}, 32)
	var h [32]byte
	copy(h[:], hash)
	if err := a.Manifest.MarkShardStored(h, "p1", time.Now()); err != nil {
		t.Fatalf("MarkShardStored: %v", err)
	}

	// Then receive a signed delete via gossip.
	del := api.NewSignedDeleteRequest(priv, hash)
	rawDel, _ := json.Marshal(del)
	a.handleManifestUpdate(&api.PeerManifestUpdateData{
		DeleteShards: []json.RawMessage{rawDel},
	}, "")

	if r := a.Manifest.Manifest().Replicas(h); len(r) != 0 {
		t.Errorf("expected replicas cleared after delete contract, got %v", r)
	}
}
