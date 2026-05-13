package transfer

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"sync"
	"time"
)

// ──────────────────────────────────────────────────────────
// Binary shard frame encode / decode
//
// Frame layout (all integers little-endian):
//
//	[0]           magic byte (0x01)
//	[1..32]       fileHash  — 32 raw bytes (hex-decoded SHA-256)
//	[33]          filename length (uint8, max 255)
//	[34..33+fnL]  filename  — UTF-8 bytes
//	[+4]          fileSize  — uint32
//	[+1]          shardIndex — uint8
//	[+4]          chunkIndex — uint32
//	[+4]          totalChunks — uint32
//	[+1]          totalDataShards — uint8
//	[+1]          totalShards — uint8
//	[+4]          data length — uint32
//	[rest]        AES-GCM encrypted shard data
//
// ──────────────────────────────────────────────────────────
type binaryShardChunk struct {
	fileHash        string
	fileName        string
	fileSize        int
	shardIndex      int
	chunkIndex      int
	totalChunks     int
	totalDataShards int
	totalShards     int
	data            []byte
}

func encodeBinaryShardChunk(c binaryShardChunk) ([]byte, error) {
	hashBytes, err := hex.DecodeString(c.fileHash)
	if err != nil || len(hashBytes) != 32 {
		return nil, fmt.Errorf("invalid fileHash")
	}
	fnBytes := []byte(c.fileName)
	if len(fnBytes) > 255 {
		return nil, fmt.Errorf("filename too long")
	}

	hdrSize := 1 + 32 + 1 + len(fnBytes) + 4 + 1 + 4 + 4 + 1 + 1 + 4
	frame := make([]byte, hdrSize+len(c.data))
	off := 0

	frame[off] = binaryMagic
	off++
	copy(frame[off:], hashBytes)
	off += 32
	frame[off] = byte(len(fnBytes))
	off++
	copy(frame[off:], fnBytes)
	off += len(fnBytes)
	binary.LittleEndian.PutUint32(frame[off:], uint32(c.fileSize))
	off += 4
	frame[off] = byte(c.shardIndex)
	off++
	binary.LittleEndian.PutUint32(frame[off:], uint32(c.chunkIndex))
	off += 4
	binary.LittleEndian.PutUint32(frame[off:], uint32(c.totalChunks))
	off += 4
	frame[off] = byte(c.totalDataShards)
	off++
	frame[off] = byte(c.totalShards)
	off++
	binary.LittleEndian.PutUint32(frame[off:], uint32(len(c.data)))
	off += 4
	copy(frame[off:], c.data)

	return frame, nil
}

func decodeBinaryShardChunk(frame []byte) (*binaryShardChunk, error) {
	// minimum header without filename: 1+32+1+4+1+4+4+1+1+4 = 53 bytes
	if len(frame) < 53 {
		return nil, fmt.Errorf("frame too short (%d bytes)", len(frame))
	}
	if frame[0] != binaryMagic {
		return nil, fmt.Errorf("not a binary shard frame (magic=%02x)", frame[0])
	}

	off := 1
	fileHash := hex.EncodeToString(frame[off : off+32])
	off += 32

	fnLen := int(frame[off])
	off++
	if len(frame) < off+fnLen+4+1+4+4+1+1+4 {
		return nil, fmt.Errorf("frame too short for header")
	}
	fileName := string(frame[off : off+fnLen])
	off += fnLen

	fileSize := int(binary.LittleEndian.Uint32(frame[off:]))
	off += 4
	shardIndex := int(frame[off])
	off++
	chunkIndex := int(binary.LittleEndian.Uint32(frame[off:]))
	off += 4
	totalChunks := int(binary.LittleEndian.Uint32(frame[off:]))
	off += 4
	totalDataShards := int(frame[off])
	off++
	totalShards := int(frame[off])
	off++
	dataLen := int(binary.LittleEndian.Uint32(frame[off:]))
	off += 4

	if len(frame) < off+dataLen {
		return nil, fmt.Errorf("frame data truncated (need %d, have %d)", off+dataLen, len(frame))
	}

	return &binaryShardChunk{
		fileHash:        fileHash,
		fileName:        fileName,
		fileSize:        fileSize,
		shardIndex:      shardIndex,
		chunkIndex:      chunkIndex,
		totalChunks:     totalChunks,
		totalDataShards: totalDataShards,
		totalShards:     totalShards,
		data:            frame[off : off+dataLen],
	}, nil
}

// ──────────────────────────────────────────────────────────
// Adaptive UDP pacer
// ──────────────────────────────────────────────────────────

// adaptivePacer paces UDP sends using AIMD (additive-increase /
// multiplicative-decrease), with kernel WriteTo latency as the congestion
// signal. A fast WriteTo means the OS send buffer has headroom → speed up.
// A slow/blocking WriteTo means the buffer is backing up → slow down.
// A hard send error triggers an immediate rate halving.
//
// Bounds: 100 µs–100 ms between sends (10,000/sec down to 10/sec).
// Start:  2 ms (500/sec × 8 KB ≈ 32 Mbps) — conservative for TURN/WiFi.
type adaptivePacer struct {
	mu       sync.Mutex
	interval time.Duration
	nextSend time.Time
}

const (
	pacerInitInterval = 2 * time.Millisecond   // 500 sends/sec start
	pacerMinInterval  = 100 * time.Microsecond // 10,000 sends/sec ceiling
	pacerMaxInterval  = 100 * time.Millisecond // 10 sends/sec floor
)

var (
	udpPacer = &adaptivePacer{interval: pacerInitInterval}
	initOnce sync.Once
)

// wait blocks until the next send slot is available, then reserves it.
// Resets if idle to prevent a burst after a quiet period.
func (p *adaptivePacer) wait() {
	p.mu.Lock()
	now := time.Now()
	if p.nextSend.Before(now) {
		p.nextSend = now // idle reset — no burst catch-up
	}
	sleep := p.nextSend.Sub(now)
	p.nextSend = p.nextSend.Add(p.interval)
	p.mu.Unlock()
	if sleep > 0 {
		time.Sleep(sleep)
	}
}

// adjust tunes the interval after each send.
//   - Hard error            → double the interval (halve throughput)
//   - WriteTo slow (> 5 ms) → +25 % interval (kernel buffer backing up)
//   - WriteTo fast (< 500 µs) → −5 % interval (room to go faster)
func (p *adaptivePacer) adjust(writeLatency time.Duration, sendOK bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cur := p.interval
	switch {
	case !sendOK:
		p.interval = min(cur*2, pacerMaxInterval)
	case writeLatency > 5*time.Millisecond:
		p.interval = min(time.Duration(float64(cur)*1.25), pacerMaxInterval)
	case writeLatency < 500*time.Microsecond:
		p.interval = max(time.Duration(float64(cur)*0.95), pacerMinInterval)
	}
}

// Init resets the adaptive pacer to its initial rate. Safe to call multiple
// times; only the first call takes effect.
func Init(_ context.Context) {
	initOnce.Do(func() {
		udpPacer.mu.Lock()
		udpPacer.interval = pacerInitInterval
		udpPacer.nextSend = time.Time{}
		udpPacer.mu.Unlock()
	})
}

// sendFrameViaQUIC writes a binary shard frame to a QUIC send stream using
// the 4-byte LE length-prefix framing that handleQUICStream expects.
func sendFrameViaQUIC(w io.WriteCloser, frame []byte) error {
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(frame)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err := w.Write(frame)
	return err
}
