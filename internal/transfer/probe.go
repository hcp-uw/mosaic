package transfer

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
	"github.com/hcp-uw/mosaic/internal/p2p"
)

// probeChans tracks in-flight ShardProbe requests waiting for a peer's ack.
// Key: "fileHash:shardIndex:nonce" → chan string (receives peer's content hash or "").
var probeChans sync.Map

const probeTimeout = 3 * time.Second

// ProbeShardAtPeer asks peerID to prove it holds shardIndex of fileHash by
// responding with SHA-256(nonce || shard_bytes). Returns true only when the
// peer responds with the hash that matches our local copy of the same shard.
//
// A false return means the peer doesn't have the shard, didn't respond in
// time, or produced a wrong hash (possible forgery attempt).
func ProbeShardAtPeer(fileHash string, shardIndex int, peerID string, client *p2p.Client) bool {
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return false
	}
	nonce := hex.EncodeToString(nonceBytes)

	expectedHash, err := hashShardFileWithNonce(fileHash, shardIndex, nonceBytes)
	if err != nil {
		// We don't have the shard locally — can't compute expected hash.
		return false
	}

	key := fmt.Sprintf("%s:%d:%s", fileHash, shardIndex, nonce)
	ch := make(chan string, 1)
	probeChans.Store(key, ch)
	defer probeChans.Delete(key)

	if err := client.SendToPeer(peerID, api.NewShardProbeMessage(client.GetID(), api.ShardProbeData{
		FileHash:   fileHash,
		ShardIndex: shardIndex,
		Nonce:      nonce,
	})); err != nil {
		return false
	}

	var result bool
	select {
	case reportedHash := <-ch:
		result = reportedHash == expectedHash
	case <-time.After(probeTimeout):
		result = false
	}
	client.RecordProbeResult(peerID, result)
	return result
}

// HandleShardProbe responds to a ShardProbe from a peer. If we hold the
// shard we compute SHA-256(nonce || shard_bytes) and send it back as proof.
// No response is sent when we don't hold the shard — absence of response
// signals "don't have it" to the prober.
func HandleShardProbe(msg *api.Message, client *p2p.Client) {
	d, err := msg.GetShardProbeData()
	if err != nil {
		return
	}

	nonceBytes, err := hex.DecodeString(d.Nonce)
	if err != nil {
		return
	}

	contentHash, err := hashShardFileWithNonce(d.FileHash, d.ShardIndex, nonceBytes)
	if err != nil {
		return // we don't have this shard
	}

	client.SendToPeer(msg.Sign.PubKey, api.NewShardProbeAckMessage(client.GetID(), api.ShardProbeAckData{ //nolint:errcheck
		FileHash:    d.FileHash,
		ShardIndex:  d.ShardIndex,
		Nonce:       d.Nonce,
		ContentHash: contentHash,
	}))
}

// HandleShardProbeAck delivers a peer's proof response to the waiting
// ProbeShardAtPeer call via probeChans.
func HandleShardProbeAck(msg *api.Message) {
	d, err := msg.GetShardProbeAckData()
	if err != nil {
		return
	}
	key := fmt.Sprintf("%s:%d:%s", d.FileHash, d.ShardIndex, d.Nonce)
	if v, ok := probeChans.Load(key); ok {
		ch := v.(chan string)
		select {
		case ch <- d.ContentHash:
		default:
		}
	}
}

// hashShardFileWithNonce computes SHA-256(nonce || shard_file_bytes) for the
// given shard. Returns an error if the shard file is not found locally.
func hashShardFileWithNonce(fileHash string, shardIndex int, nonce []byte) (string, error) {
	path := filepath.Join(ShardsDir(), fileHash,
		fmt.Sprintf("shard%d_%s.dat", shardIndex, fileHash))
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	h.Write(nonce)
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
