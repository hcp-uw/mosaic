package encoding

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --- Tests for NewEncoder ---

func TestNewEncoder_Success(t *testing.T) {
	tmpIn := t.TempDir()
	tmpOut := t.TempDir()

	enc, err := NewEncoder(4, 2, tmpOut, tmpIn)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if enc == nil {
		t.Fatal("expected encoder, got nil")
	}
	if enc.shards != 4 {
		t.Errorf("expected shards=4, got %d", enc.shards)
	}
	if enc.parity != 2 {
		t.Errorf("expected parity=2, got %d", enc.parity)
	}
}

func TestNewEncoder_InvalidShardCounts(t *testing.T) {
	tmpIn := t.TempDir()
	tmpOut := t.TempDir()

	_, err := NewEncoder(0, 2, tmpOut, tmpIn)
	if err == nil {
		t.Fatal("expected error for invalid shard counts, got nil")
	}

	_, err = NewEncoder(4, 0, tmpOut, tmpIn)
	if err == nil {
		t.Fatal("expected error for invalid shard counts, got nil")
	}
}

func TestNewEncoder_InvalidDirectories(t *testing.T) {
	tmp := t.TempDir()
	invalidPath := filepath.Join(tmp, "nonexistent")

	_, err := NewEncoder(4, 2, tmp, invalidPath)
	if err == nil {
		t.Fatal("expected error for invalid input dir, got nil")
	}
}

// --- Benchmark-like test for EncodeFile throughput ---

func TestEncodeFile_Performance(t *testing.T) {
	tmpIn := t.TempDir()
	tmpOut := t.TempDir()

	// create a large sample file (e.g. 50 MB)
	fileSize := 200 * 1024 * 1024
	fileName := "largefile.dat"
	filePath := filepath.Join(tmpOut, fileName)

	data := make([]byte, fileSize)
	for i := range data {
		data[i] = byte(i % 256)
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		t.Fatalf("failed to write input file: %v", err)
	}

	enc, err := NewEncoder(4, 2, tmpOut, tmpIn)
	if err != nil {
		t.Fatalf("failed to create encoder: %v", err)
	}

	// ensure .bin dir exists
	if err := os.MkdirAll(filepath.Join(tmpOut, ".bin"), 0755); err != nil {
		t.Fatalf("failed to create .bin dir: %v", err)
	}

	start := time.Now()
	if err := enc.EncodeFile(fileName); err != nil {
		t.Fatalf("EncodeFile failed: %v", err)
	}
	elapsed := time.Since(start).Seconds()

	// compute throughput
	mb := float64(fileSize) / (1024.0 * 1024.0)
	throughput := mb / elapsed
	t.Logf("encoded %.2f MB in %.2f s (%.2f MB/s)", mb, elapsed, throughput)

	// basic correctness check: shard files exist
	shardDir := filepath.Join(tmpOut, ".bin", fileName)
	files, err := os.ReadDir(shardDir)
	if err != nil {
		t.Fatalf("failed to read shard dir: %v", err)
	}

	expectedCount := enc.shards + enc.parity
	if len(files) != expectedCount {
		t.Errorf("expected %d shard files, got %d", expectedCount, len(files))
	}
}

