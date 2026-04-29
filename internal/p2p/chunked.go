package p2p

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
)

// MaxFrameBytes is the largest serialized datagram we ever put on the
// wire. macOS caps UDP datagrams at net.inet.udp.maxdgram (default 9216
// bytes); some Linux configs are similarly tight. We stay safely under.
const MaxFrameBytes = 8 * 1024

// chunkPayloadBytes is the raw bytes per ChunkedFrame. JSON encodes byte
// slices as base64 (×4/3 expansion), so 4 KB raw → ~5.5 KB encoded plus
// the envelope — well under MaxFrameBytes.
const chunkPayloadBytes = 4 * 1024

// transferTTL bounds how long a partially-assembled transfer is held in
// memory before its slot is reclaimed.
const transferTTL = 30 * time.Second

// chunkAssembler reassembles ChunkedFrame messages back into the
// original serialized message.
type chunkAssembler struct {
	mu        sync.Mutex
	transfers map[uint64]*chunkedTransfer
}

type chunkedTransfer struct {
	chunks   [][]byte
	received uint32
	total    uint32
	created  time.Time
}

func newChunkAssembler() *chunkAssembler {
	return &chunkAssembler{transfers: map[uint64]*chunkedTransfer{}}
}

// add records a chunk and, if this completes the transfer, returns the
// reassembled bytes. The second return is true when assembly is complete.
func (a *chunkAssembler) add(c *api.ChunkedFrameData) ([]byte, bool, error) {
	if c.Total == 0 || c.Index >= c.Total {
		return nil, false, fmt.Errorf("invalid chunk Index=%d Total=%d", c.Index, c.Total)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.gc()

	t, ok := a.transfers[c.TransferID]
	if !ok {
		t = &chunkedTransfer{
			chunks:  make([][]byte, c.Total),
			total:   c.Total,
			created: time.Now(),
		}
		a.transfers[c.TransferID] = t
	}
	if t.total != c.Total {
		// Conflicting chunk metadata — drop the transfer.
		delete(a.transfers, c.TransferID)
		return nil, false, fmt.Errorf("chunk Total mismatch (got %d, had %d)", c.Total, t.total)
	}
	if t.chunks[c.Index] != nil {
		// Duplicate chunk — keep the existing data.
		return nil, false, nil
	}
	t.chunks[c.Index] = append([]byte(nil), c.Payload...)
	t.received++
	if t.received != t.total {
		return nil, false, nil
	}
	// Assemble.
	total := 0
	for _, chunk := range t.chunks {
		total += len(chunk)
	}
	out := make([]byte, 0, total)
	for _, chunk := range t.chunks {
		out = append(out, chunk...)
	}
	delete(a.transfers, c.TransferID)
	return out, true, nil
}

// gc reclaims transfers older than transferTTL. Caller must hold a.mu.
func (a *chunkAssembler) gc() {
	cutoff := time.Now().Add(-transferTTL)
	for id, t := range a.transfers {
		if t.created.Before(cutoff) {
			delete(a.transfers, id)
		}
	}
}

// newTransferID returns a fresh random uint64.
func newTransferID() uint64 {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return binary.BigEndian.Uint64(buf[:])
}

// chunkAndSend writes data to addr, fragmenting if it exceeds MaxFrameBytes.
// senderID is included in each chunk's Sign field so the receiver can
// associate the transfer with a peer if needed.
func chunkAndSend(write func([]byte) error, senderID string, data []byte) error {
	if len(data) <= MaxFrameBytes {
		return write(data)
	}
	transferID := newTransferID()
	total := uint32((len(data) + chunkPayloadBytes - 1) / chunkPayloadBytes)
	for i := uint32(0); i < total; i++ {
		start := int(i) * chunkPayloadBytes
		end := start + chunkPayloadBytes
		if end > len(data) {
			end = len(data)
		}
		frame := api.NewPeerChunkedFrameMessage(senderID, transferID, i, total, data[start:end])
		raw, err := frame.Serialize()
		if err != nil {
			return fmt.Errorf("serialize chunk %d: %w", i, err)
		}
		if err := write(raw); err != nil {
			return fmt.Errorf("send chunk %d: %w", i, err)
		}
	}
	return nil
}
