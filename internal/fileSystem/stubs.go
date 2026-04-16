package fileSystem

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// StubMeta is the JSON content written inside a .mosaic stub file.
// Finder shows the stub; the extension reads this to decide the badge color.
type StubMeta struct {
	Name        string `json:"name"`
	Size        int    `json:"size"`
	NodeID      int    `json:"nodeID"`
	DateAdded   string `json:"dateAdded"`
	Cached      bool   `json:"cached"`
	ContentHash string `json:"contentHash"` // SHA-256 hex; empty for legacy stubs
}

// WriteStub creates a <filename>.mosaic stub file in the Mosaic directory.
// Pass an empty string for contentHash if it is not yet known.
func WriteStub(mosaicDir, filename string, size, nodeID int, contentHash string) error {
	meta := StubMeta{
		Name:        filename,
		Size:        size,
		NodeID:      nodeID,
		DateAdded:   time.Now().Format("01-02-2006"),
		Cached:      false,
		ContentHash: contentHash,
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(mosaicDir, filename+".mosaic"), data, 0644)
}

// ReadStub reads and parses the stub metadata for a given filename.
func ReadStub(mosaicDir, filename string) (StubMeta, error) {
	data, err := os.ReadFile(filepath.Join(mosaicDir, filename+".mosaic"))
	if err != nil {
		return StubMeta{}, err
	}
	var meta StubMeta
	return meta, json.Unmarshal(data, &meta)
}

// RemoveStub deletes the .mosaic stub for a given filename.
func RemoveStub(mosaicDir, filename string) error {
	err := os.Remove(filepath.Join(mosaicDir, filename+".mosaic"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// MarkCached flips the cached flag to true on an existing stub.
func MarkCached(mosaicDir, filename string) error {
	stubPath := filepath.Join(mosaicDir, filename+".mosaic")
	data, err := os.ReadFile(stubPath)
	if err != nil {
		return err
	}
	var meta StubMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return err
	}
	meta.Cached = true
	updated, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(stubPath, updated, 0644)
}

// IsCached reports whether a stub exists and is marked as locally cached.
func IsCached(mosaicDir, filename string) bool {
	data, err := os.ReadFile(filepath.Join(mosaicDir, filename+".mosaic"))
	if err != nil {
		return false
	}
	var meta StubMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return false
	}
	return meta.Cached
}
