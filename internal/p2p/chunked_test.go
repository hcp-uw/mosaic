package p2p

import (
	"bytes"
	"crypto/rand"
	"testing"

	"github.com/hcp-uw/mosaic/internal/api"
)

func TestChunkAssembler_RoundTrip(t *testing.T) {
	a := newChunkAssembler()
	payload := bytes.Repeat([]byte{0xAB}, 200_000)
	transferID := newTransferID()

	const chunk = chunkPayloadBytes
	total := uint32((len(payload) + chunk - 1) / chunk)

	var assembled []byte
	var done bool
	for i := uint32(0); i < total; i++ {
		start := int(i) * chunk
		end := start + chunk
		if end > len(payload) {
			end = len(payload)
		}
		out, complete, err := a.add(&api.ChunkedFrameData{
			TransferID: transferID,
			Index:      i,
			Total:      total,
			Payload:    payload[start:end],
		})
		if err != nil {
			t.Fatalf("add chunk %d: %v", i, err)
		}
		if complete {
			assembled = out
			done = true
		}
	}
	if !done {
		t.Fatal("transfer never completed")
	}
	if !bytes.Equal(assembled, payload) {
		t.Errorf("assembled bytes differ from input (got %d, want %d)", len(assembled), len(payload))
	}
}

func TestChunkAssembler_OutOfOrder(t *testing.T) {
	a := newChunkAssembler()
	payload := []byte("hello world this is a test payload")
	transferID := newTransferID()
	const chunk = 8
	total := uint32((len(payload) + chunk - 1) / chunk)

	// Build chunks then deliver in reverse order.
	chunks := make([]*api.ChunkedFrameData, total)
	for i := uint32(0); i < total; i++ {
		start := int(i) * chunk
		end := start + chunk
		if end > len(payload) {
			end = len(payload)
		}
		chunks[i] = &api.ChunkedFrameData{
			TransferID: transferID,
			Index:      i,
			Total:      total,
			Payload:    payload[start:end],
		}
	}

	var assembled []byte
	for i := int(total) - 1; i >= 0; i-- {
		out, complete, err := a.add(chunks[i])
		if err != nil {
			t.Fatalf("add: %v", err)
		}
		if complete {
			assembled = out
		}
	}
	if !bytes.Equal(assembled, payload) {
		t.Errorf("out-of-order assembly mismatch")
	}
}

func TestChunkAssembler_DuplicateIgnored(t *testing.T) {
	a := newChunkAssembler()
	c := &api.ChunkedFrameData{
		TransferID: 42,
		Index:      0,
		Total:      2,
		Payload:    []byte("first"),
	}
	if _, complete, err := a.add(c); err != nil || complete {
		t.Fatalf("first add: complete=%v err=%v", complete, err)
	}
	if _, complete, err := a.add(c); err != nil || complete {
		t.Errorf("duplicate add: complete=%v err=%v", complete, err)
	}
	out, complete, err := a.add(&api.ChunkedFrameData{
		TransferID: 42, Index: 1, Total: 2, Payload: []byte("second"),
	})
	if err != nil || !complete {
		t.Fatalf("final add: complete=%v err=%v", complete, err)
	}
	if string(out) != "firstsecond" {
		t.Errorf("got %q", out)
	}
}

func TestChunkAssembler_RejectsBadIndex(t *testing.T) {
	a := newChunkAssembler()
	if _, _, err := a.add(&api.ChunkedFrameData{TransferID: 1, Index: 5, Total: 3}); err == nil {
		t.Error("expected error for Index >= Total")
	}
	if _, _, err := a.add(&api.ChunkedFrameData{TransferID: 1, Index: 0, Total: 0}); err == nil {
		t.Error("expected error for Total == 0")
	}
}

func TestChunkAndSend_Small(t *testing.T) {
	// Messages below MaxFrameBytes should be sent as a single packet,
	// not chunked.
	var captured [][]byte
	write := func(b []byte) error {
		captured = append(captured, append([]byte(nil), b...))
		return nil
	}
	if err := chunkAndSend(write, "alice", []byte("small payload")); err != nil {
		t.Fatalf("chunkAndSend: %v", err)
	}
	if len(captured) != 1 {
		t.Errorf("expected 1 send, got %d", len(captured))
	}
}

func TestChunkAndSend_Large(t *testing.T) {
	payload := make([]byte, 200_000)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	var sent [][]byte
	write := func(b []byte) error {
		sent = append(sent, append([]byte(nil), b...))
		return nil
	}
	if err := chunkAndSend(write, "alice", payload); err != nil {
		t.Fatalf("chunkAndSend: %v", err)
	}
	if len(sent) < 2 {
		t.Fatalf("expected multiple sends for 200KB, got %d", len(sent))
	}
	// Reassemble the chunks via the assembler.
	a := newChunkAssembler()
	var got []byte
	for _, raw := range sent {
		msg, err := api.DeserializeMessage(raw)
		if err != nil {
			t.Fatalf("deserialize: %v", err)
		}
		if msg.Type != api.PeerChunkedFrame {
			t.Fatalf("expected PeerChunkedFrame, got %s", msg.Type)
		}
		c, err := msg.GetChunkedFrame()
		if err != nil {
			t.Fatal(err)
		}
		out, complete, err := a.add(c)
		if err != nil {
			t.Fatal(err)
		}
		if complete {
			got = out
		}
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("chunkAndSend → assemble round-trip mismatch")
	}
}
