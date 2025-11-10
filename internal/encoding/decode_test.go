 package encoding

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestDecodeShards_Performance runs full encode→decode and prints throughput (MB/s)
func TestDecodeShards_Performance(t *testing.T) {
	tmpIn := t.TempDir()
	tmpOut := t.TempDir()

	// prepare a moderately large file (e.g., 50 MB)
	fileSize := 50 * 1024 * 1024
	fileName := "bigfile.txt"
	inFilePath := filepath.Join(tmpOut, fileName)
	data := make([]byte, fileSize)
	for i := range data {
		data[i] = byte(i % 256)
	}

	if err := os.WriteFile(inFilePath, data, 0644); err != nil {
		t.Fatalf("failed to write input file: %v", err)
	}

	// create encoder
	enc, err := NewEncoder(4, 2, tmpOut, tmpIn)
	if err != nil {
		t.Fatalf("failed to create encoder: %v", err)
	}

	// make .bin dir for encoded output
	if err := os.MkdirAll(filepath.Join(tmpOut, ".bin"), 0755); err != nil {
		t.Fatalf("failed to make .bin dir: %v", err)
	}

	// encode
	startEncode := time.Now()
	if err := enc.EncodeFile(fileName); err != nil {
		t.Fatalf("EncodeFile failed: %v", err)
	}
	encodeElapsed := time.Since(startEncode).Seconds()

	// move shards to decoder input (.bin/<fileName>)
	shardDir := filepath.Join(tmpOut, ".bin", fileName)
	shards, err := os.ReadDir(shardDir)
	if err != nil {
		t.Fatalf("failed to read shard dir: %v", err)
	}
	targetShardDir := filepath.Join(tmpIn, fileName)
	if err := os.MkdirAll(targetShardDir, 0755); err != nil {
		t.Fatalf("failed to create shard dir: %v", err)
	}
	for _, shard := range shards {
		src := filepath.Join(shardDir, shard.Name())
		dst := filepath.Join(targetShardDir, shard.Name())
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("failed to read shard: %v", err)
		}
		if err := os.WriteFile(dst, b, 0644); err != nil {
			t.Fatalf("failed to write shard: %v", err)
		}
	}

	// decode
	startDecode := time.Now()
	if err := enc.DecodeShards(fileName, len(data)); err != nil {
		t.Fatalf("DecodeShards failed: %v", err)
	}
	decodeElapsed := time.Since(startDecode).Seconds()

	// verify output
	decodedPath := filepath.Join(tmpOut, fileName)
	decodedData, err := os.ReadFile(decodedPath)
	if err != nil {
		t.Fatalf("failed to read decoded file: %v", err)
	}

	if !bytes.Equal(decodedData, data) {
		t.Fatalf("decoded file does not match original data")
	}

	// calculate and log throughput
	mb := float64(fileSize) / (1024 * 1024)
	encodeThroughput := mb / encodeElapsed
	decodeThroughput := mb / decodeElapsed

	t.Logf("Encode: %.2f MB in %.2f s (%.2f MB/s)", mb, encodeElapsed, encodeThroughput)
	t.Logf("Decode: %.2f MB in %.2f s (%.2f MB/s)", mb, decodeElapsed, decodeThroughput)
}

