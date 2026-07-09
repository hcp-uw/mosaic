package p2p

import (
	"bytes"
	"encoding/binary"
	"testing"
)

type nopWriteCloser struct{ *bytes.Buffer }

func (nopWriteCloser) Close() error { return nil }

// TestWriteQUICFrame verifies the frame written for a QUIC-routed control message
// uses the exact 4-byte little-endian length prefix that handleQUICStream reads,
// so a large manifest sent over QUIC is parsed correctly on the other side.
func TestWriteQUICFrame(t *testing.T) {
	var buf bytes.Buffer
	frame := []byte{0x02, 0xAA, 0xBB, 0xCC, 0xDD} // 0x02 = session-encrypted envelope
	if err := writeQUICFrame(nopWriteCloser{&buf}, frame); err != nil {
		t.Fatalf("writeQUICFrame: %v", err)
	}
	out := buf.Bytes()
	if len(out) != 4+len(frame) {
		t.Fatalf("framed length: got %d, want %d", len(out), 4+len(frame))
	}
	if gotLen := binary.LittleEndian.Uint32(out[:4]); gotLen != uint32(len(frame)) {
		t.Errorf("length prefix: got %d, want %d", gotLen, len(frame))
	}
	if !bytes.Equal(out[4:], frame) {
		t.Errorf("payload mismatch: got %x, want %x", out[4:], frame)
	}
}
