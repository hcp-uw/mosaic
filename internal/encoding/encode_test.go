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

// --- ComputeBlockSize ---

func TestComputeBlockSize_SmallFile(t *testing.T) {
	// 20 KB file with 10 data shards → 2 KB block size, not 20 MB.
	bs := ComputeBlockSize(20*1024, 10)
	if bs != 2*1024 {
		t.Errorf("expected 2048, got %d", bs)
	}
}

func TestComputeBlockSize_ZeroFile(t *testing.T) {
	bs := ComputeBlockSize(0, 10)
	if bs < 1 {
		t.Errorf("block size must be at least 1, got %d", bs)
	}
}

func TestComputeBlockSize_LargeFileCapped(t *testing.T) {
	// 10 GB file with 10 shards → would be 1 GB per shard, should be capped at 20 MB.
	bs := ComputeBlockSize(10*1024*1024*1024, 10)
	if bs != 20*1024*1024 {
		t.Errorf("expected 20 MB cap, got %d", bs)
	}
}

// --- ShardSizeIsProportionalToFileSize verifies the shard output isn't bloated ---

func TestEncodeFile_ShardSizeProportionalToInput(t *testing.T) {
	const dataShards = 4
	const parityShards = 2
	const totalShards = dataShards + parityShards

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".bin"), 0755); err != nil {
		t.Fatal(err)
	}

	// Write a 20 KB file — the bug produced hundreds of MB of shards for this.
	fileSize := 20 * 1024
	fileName := "small.txt"
	data := make([]byte, fileSize)
	for i := range data {
		data[i] = byte(i % 256)
	}
	if err := os.WriteFile(filepath.Join(dir, fileName), data, 0644); err != nil {
		t.Fatal(err)
	}

	enc, err := NewEncoder(dataShards, parityShards, dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.EncodeFile(fileName); err != nil {
		t.Fatal(err)
	}

	// Each shard should be roughly fileSize/dataShards, not 20 MB.
	expectedBlockSize := ComputeBlockSize(fileSize, dataShards)
	if enc.BlockSize() != expectedBlockSize {
		t.Errorf("BlockSize: got %d, want %d", enc.BlockSize(), expectedBlockSize)
	}

	// Total shard output should be ≤ 2× the file size (data + parity overhead),
	// not hundreds of MB.
	shardDir := filepath.Join(dir, ".bin", fileName)
	entries, err := os.ReadDir(shardDir)
	if err != nil {
		t.Fatalf("shard dir not found: %v", err)
	}
	var totalShardBytes int64
	for _, e := range entries {
		info, _ := e.Info()
		totalShardBytes += info.Size()
	}

	// Allow 3× headroom for parity + alignment padding.
	maxAllowed := int64(fileSize * 3)
	if totalShardBytes > maxAllowed {
		t.Errorf("total shard output %d bytes exceeds %d (3× file size %d) — bloat detected",
			totalShardBytes, maxAllowed, fileSize)
	}
	t.Logf("file=%d bytes, total shards=%d bytes (%.1f×)", fileSize, totalShardBytes,
		float64(totalShardBytes)/float64(fileSize))
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

