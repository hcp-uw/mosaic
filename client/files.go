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

	"github.com/klauspost/reedsolomon"
)

const (
	// Reed-Solomon erasure coding: 10 data + 4 parity = 14 shards. Any 10 of the
	// 14 are enough to reconstruct the file, so up to 4 shards can be lost.
	dataShards   = 10
	parityShards = 4
	numShards    = dataShards + parityShards // 14

	stubExt      = ".mosaic"
	pollInterval = time.Second
)

// manifest is the content of a .mosaic stub: enough to locate every shard on
// the network and reconstruct + verify the file.
type manifest struct {
	Name           string   `json:"name"`            // original base name, e.g. "report.pdf"
	Size           int64    `json:"size"`            // original plaintext size in bytes
	CiphertextSize int64    `json:"ciphertext_size"` // encrypted payload size in bytes
	SHA256         string   `json:"sha256"`          // hex digest of original plaintext
	Identity       string   `json:"identity"`        // public identity derived from the user key
	DataShards     int      `json:"data_shards"`     // Reed-Solomon data shard count
	ParityShards   int      `json:"parity_shards"`   // Reed-Solomon parity shard count
	Shards         []string `json:"shards"`          // shard addresses, indexed 0..n-1
	Created        string   `json:"created"`         // RFC3339 timestamp
}

// encodeShards Reed-Solomon encodes data into dataShards+parityShards equal-size
// shards. All shards are the same length (the last data shard is zero-padded);
// rehydrate trims back to the original size.
func encodeShards(data []byte) ([][]byte, error) {
	if len(data) == 0 {
		// Split rejects empty input; represent an empty file as empty shards.
		shards := make([][]byte, numShards)
		for i := range shards {
			shards[i] = []byte{}
		}
		return shards, nil
	}
	enc, err := reedsolomon.New(dataShards, parityShards)
	if err != nil {
		return nil, err
	}
	shards, err := enc.Split(data)
	if err != nil {
		return nil, err
	}
	if err := enc.Encode(shards); err != nil {
		return nil, err
	}
	return shards, nil
}

// decodeShards reconstructs the original bytes from shards (some may be nil),
// given the data/parity split and original size. It needs at least dataShards
// present.
func decodeShards(shards [][]byte, dShards, pShards int, size int64) ([]byte, error) {
	if size == 0 {
		return []byte{}, nil
	}
	enc, err := reedsolomon.New(dShards, pShards)
	if err != nil {
		return nil, err
	}
	if err := enc.ReconstructData(shards); err != nil {
		return nil, fmt.Errorf("reconstruct: %w", err)
	}
	var buf bytes.Buffer
	if err := enc.Join(&buf, shards, int(size)); err != nil {
		return nil, fmt.Errorf("join: %w", err)
	}
	return buf.Bytes(), nil
}

// shardFile erasure-codes path into numShards shards, distributes them across
// the network, writes a .mosaic stub, and removes the original. It aborts
// (leaving the original in place) if any shard cannot be confirmed stored.
func (c *client) shardFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	name := filepath.Base(path)
	nameToken, err := encryptFilenameToken(c.key, name)
	if err != nil {
		return fmt.Errorf("encrypt filename token: %w", err)
	}
	encData, err := encryptData(c.key, data)
	if err != nil {
		return fmt.Errorf("encrypt: %w", err)
	}

	shards, err := encodeShards(encData)
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}

	nodes, err := c.discoverNodes(discoveryTimeout)
	if err != nil {
		return fmt.Errorf("discover nodes: %w", err)
	}
	if len(nodes) == 0 {
		return fmt.Errorf("no other nodes connected to store shards on; aborting")
	}
	log.Printf("client: distributing %d shards (%d data + %d parity) across %d node(s)", numShards, dataShards, parityShards, len(nodes))

	addrs := make([]string, numShards)
	for i, shard := range shards {
		addr := makeShardAddress(c.id, nameToken, int64(len(encData)), i)
		target := nodes[i%len(nodes)] // round-robin: each shard to one node
		ok, err := c.storeShardNet(target, addr, shard, rpcTimeout)
		if err != nil {
			return fmt.Errorf("store shard %d on %s: %w", i, target, err)
		}
		if !ok {
			return fmt.Errorf("shard %d (%s) was not confirmed stored by %s; aborting", i, addr, target)
		}
		addrs[i] = addr
	}

	m := manifest{
		Name:           name,
		Size:           int64(len(data)),
		CiphertextSize: int64(len(encData)),
		SHA256:         digest,
		Identity:       c.id,
		DataShards:     dataShards,
		ParityShards:   parityShards,
		Shards:         addrs,
		Created:        time.Now().UTC().Format(time.RFC3339),
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

// rehydrate reconstructs the file described by a .mosaic stub. It fetches the
// shards from the network — tolerating up to ParityShards missing ones —
// Reed-Solomon decodes them, verifies the checksum, writes the file next to the
// stub (stub kept), and optionally opens it.
func (c *client) rehydrate(stubPath string, openAfter bool) error {
	mb, err := os.ReadFile(stubPath)
	if err != nil {
		return err
	}
	var m manifest
	if err := json.Unmarshal(mb, &m); err != nil {
		return fmt.Errorf("not a valid %s stub: %w", stubExt, err)
	}
	if m.Identity != "" && m.Identity != c.id {
		return fmt.Errorf("stub identity %q does not match local identity %q", m.Identity, c.id)
	}

	if m.CiphertextSize <= 0 && len(m.Shards) > 0 {
		parsed, err := parseShardAddress(m.Shards[0])
		if err == nil {
			m.CiphertextSize = parsed.CiphertextSize
		}
	}
	if m.CiphertextSize < 0 {
		return fmt.Errorf("invalid ciphertext size in stub")
	}
	if m.DataShards == 0 {
		m.DataShards = dataShards
	}
	if m.ParityShards == 0 {
		m.ParityShards = parityShards
	}

	// Fetch shards, leaving missing ones nil. We only need DataShards of them.
	shards := make([][]byte, len(m.Shards))
	present := 0
	for i, addr := range m.Shards {
		data, err := c.retrieveShardNet(addr, rpcTimeout)
		if err != nil {
			continue // missing; reconstruct from parity if enough survive
		}
		shards[i] = data
		present++
	}
	if present < m.DataShards {
		return fmt.Errorf("only %d of %d shards available; need %d to reconstruct", present, len(m.Shards), m.DataShards)
	}

	buf, err := decodeShards(shards, m.DataShards, m.ParityShards, m.CiphertextSize)
	if err != nil {
		return err
	}
	plain, err := decryptData(c.key, buf)
	if err != nil {
		return fmt.Errorf("decrypt: %w", err)
	}
	sum := sha256.Sum256(plain)
	if m.SHA256 != "" && hex.EncodeToString(sum[:]) != m.SHA256 {
		got := hex.EncodeToString(sum[:])
		return fmt.Errorf("checksum mismatch: got %s, want %s", got, m.SHA256)
	}
	if m.Size > 0 && int64(len(plain)) != m.Size {
		return fmt.Errorf("size mismatch after decrypt: got %d, want %d", len(plain), m.Size)
	}

	outPath := strings.TrimSuffix(stubPath, stubExt)
	if err := os.WriteFile(outPath, plain, 0o644); err != nil {
		return err
	}
	log.Printf("client: rehydrated %q (%d bytes, %d/%d shards present, checksum ok)", filepath.Base(outPath), m.Size, present, len(m.Shards))

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
	log.Printf("client: node serving the network; watching %s — drop files in to shard them", base)
	go c.runStubSync(base)
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
