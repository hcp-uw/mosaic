package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	numShards    = 14
	stubExt      = ".mosaic"
	pollInterval = time.Second
)

// manifest is the content of a .mosaic stub: enough to locate every shard on
// the network and verify the reconstructed file.
type manifest struct {
	Name    string   `json:"name"`    // original base name, e.g. "report.pdf"
	Size    int64    `json:"size"`    // original size in bytes
	SHA256  string   `json:"sha256"`  // hex digest of the original file
	Shards  []string `json:"shards"`  // shard addresses, in reconstruction order
	Created string   `json:"created"` // RFC3339 timestamp
}

// splitN divides data into exactly n contiguous chunks. Trailing chunks may be
// empty when len(data) < n.
func splitN(data []byte, n int) [][]byte {
	chunks := make([][]byte, n)
	size := (len(data) + n - 1) / n // ceil; 0 when data is empty
	for i := 0; i < n; i++ {
		start := i * size
		if start > len(data) {
			start = len(data)
		}
		end := start + size
		if end > len(data) {
			end = len(data)
		}
		chunks[i] = data[start:end]
	}
	return chunks
}

// shardFile splits path into shards, stores them across the network, writes a
// .mosaic stub, and removes the original. It aborts (leaving the original in
// place) if any shard cannot be confirmed stored on at least one peer.
func (c *client) shardFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])

	chunks := splitN(data, numShards)
	addrs := make([]string, numShards)
	for i, chunk := range chunks {
		addr := fmt.Sprintf("%s.%02d", digest, i)
		n, err := c.storeShardNet(addr, chunk, rpcTimeout)
		if err != nil {
			return fmt.Errorf("store shard %d: %w", i, err)
		}
		if n == 0 {
			return fmt.Errorf("shard %d (%s) was not stored by any peer; aborting (is anyone else connected?)", i, addr)
		}
		addrs[i] = addr
	}

	m := manifest{
		Name:    filepath.Base(path),
		Size:    int64(len(data)),
		SHA256:  digest,
		Shards:  addrs,
		Created: time.Now().UTC().Format(time.RFC3339),
	}
	mb, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	stubPath := path + stubExt
	if err := os.WriteFile(stubPath, mb, 0o644); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("wrote stub but failed to remove original: %w", err)
	}
	log.Printf("client: sharded %q into %d shards -> %s", filepath.Base(path), numShards, filepath.Base(stubPath))
	return nil
}

// rehydrate reconstructs the file described by a .mosaic stub, fetching every
// shard from the network and verifying the result. It writes the file next to
// the stub (stub kept) and optionally opens it.
func (c *client) rehydrate(stubPath string, openAfter bool) error {
	mb, err := os.ReadFile(stubPath)
	if err != nil {
		return err
	}
	var m manifest
	if err := json.Unmarshal(mb, &m); err != nil {
		return fmt.Errorf("not a valid %s stub: %w", stubExt, err)
	}

	var buf bytes.Buffer
	for i, addr := range m.Shards {
		data, err := c.retrieveShardNet(addr, rpcTimeout)
		if err != nil {
			return fmt.Errorf("shard %d/%d: %w", i+1, len(m.Shards), err)
		}
		buf.Write(data)
	}

	if int64(buf.Len()) != m.Size {
		return fmt.Errorf("reconstructed size %d != expected %d", buf.Len(), m.Size)
	}
	sum := sha256.Sum256(buf.Bytes())
	if got := hex.EncodeToString(sum[:]); got != m.SHA256 {
		return fmt.Errorf("checksum mismatch: got %s, want %s", got, m.SHA256)
	}

	outPath := strings.TrimSuffix(stubPath, stubExt)
	if err := os.WriteFile(outPath, buf.Bytes(), 0o644); err != nil {
		return err
	}
	log.Printf("client: rehydrated %q (%d bytes, checksum ok)", filepath.Base(outPath), m.Size)

	if openAfter && runtime.GOOS == "darwin" {
		if err := exec.Command("open", outPath).Run(); err != nil {
			log.Printf("client: open %q: %v", outPath, err)
		}
	}
	return nil
}

// watch polls the Mosaic base directory and shards any newly added, stable file
// into a .mosaic stub. Files that already have a stub sibling, hidden files, and
// stubs themselves are left alone.
func (c *client) watch(base string) {
	log.Printf("client: watching %s — drop files in to shard them", base)
	seen := make(map[string]os.FileInfo) // path -> last poll's stat, for stability
	for {
		entries, err := os.ReadDir(base)
		if err != nil {
			log.Printf("client: read %s: %v", base, err)
			time.Sleep(pollInterval)
			continue
		}
		present := make(map[string]bool)
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || strings.HasPrefix(name, ".") || strings.HasSuffix(name, stubExt) {
				continue
			}
			path := filepath.Join(base, name)
			present[path] = true

			// Skip files that already have a stub (e.g. a freshly rehydrated file).
			if _, err := os.Stat(path + stubExt); err == nil {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			prev, ok := seen[path]
			seen[path] = info
			// Only shard once the file has been unchanged across one poll, so we
			// don't grab a file mid-copy.
			if !ok || prev.Size() != info.Size() || !prev.ModTime().Equal(info.ModTime()) {
				continue
			}
			if err := c.shardFile(path); err != nil {
				log.Printf("client: shard %q: %v", name, err)
			}
			delete(seen, path)
		}
		// Forget files that disappeared.
		for path := range seen {
			if !present[path] {
				delete(seen, path)
			}
		}
		time.Sleep(pollInterval)
	}
}
