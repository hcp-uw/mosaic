package fileSystem

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const ManifestFilename = ".mosaic-manifest.json"

// ManifestEntry is the canonical metadata record for a file on the network.
// It persists across stub deletion (i.e. after a file is cached locally).
type ManifestEntry struct {
	Name        string `json:"name"`
	Size        int    `json:"size"`
	NodeID      int    `json:"nodeID"`
	DateAdded   string `json:"dateAdded"`
	Cached      bool   `json:"cached"`
	ContentHash string `json:"contentHash"` // SHA-256 hex of original file; empty for legacy entries
}

var manifestMu sync.Mutex

func manifestPath(mosaicDir string) string {
	return filepath.Join(mosaicDir, ManifestFilename)
}

// ReadManifest returns all entries keyed by filename.
func ReadManifest(mosaicDir string) (map[string]ManifestEntry, error) {
	manifestMu.Lock()
	defer manifestMu.Unlock()
	return readManifestLocked(mosaicDir)
}

func readManifestLocked(mosaicDir string) (map[string]ManifestEntry, error) {
	data, err := os.ReadFile(manifestPath(mosaicDir))
	if os.IsNotExist(err) {
		return map[string]ManifestEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	var entries map[string]ManifestEntry
	return entries, json.Unmarshal(data, &entries)
}

func writeManifestLocked(mosaicDir string, entries map[string]ManifestEntry) error {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	tmp := manifestPath(mosaicDir) + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, manifestPath(mosaicDir))
}

// AddToManifest inserts or replaces an entry. Pass an empty string for
// contentHash if it is not yet known (e.g. for legacy or remote-only entries).
func AddToManifest(mosaicDir, name string, size, nodeID int, contentHash string) error {
	manifestMu.Lock()
	defer manifestMu.Unlock()
	entries, err := readManifestLocked(mosaicDir)
	if err != nil {
		return err
	}
	entries[name] = ManifestEntry{
		Name:        name,
		Size:        size,
		NodeID:      nodeID,
		DateAdded:   time.Now().Format("01-02-2006"),
		Cached:      false,
		ContentHash: contentHash,
	}
	return writeManifestLocked(mosaicDir, entries)
}

// GetManifestEntry returns the manifest entry for name, or an error if not found.
func GetManifestEntry(mosaicDir, name string) (ManifestEntry, error) {
	manifestMu.Lock()
	defer manifestMu.Unlock()
	entries, err := readManifestLocked(mosaicDir)
	if err != nil {
		return ManifestEntry{}, err
	}
	entry, ok := entries[name]
	if !ok {
		return ManifestEntry{}, os.ErrNotExist
	}
	return entry, nil
}

// RemoveFromManifest deletes the entry for name.
func RemoveFromManifest(mosaicDir, name string) error {
	manifestMu.Lock()
	defer manifestMu.Unlock()
	entries, err := readManifestLocked(mosaicDir)
	if err != nil {
		return err
	}
	delete(entries, name)
	return writeManifestLocked(mosaicDir, entries)
}

// RenameInManifest moves the entry from oldName to newName.
func RenameInManifest(mosaicDir, oldName, newName string) error {
	manifestMu.Lock()
	defer manifestMu.Unlock()
	entries, err := readManifestLocked(mosaicDir)
	if err != nil {
		return err
	}
	entry, ok := entries[oldName]
	if !ok {
		return nil
	}
	entry.Name = newName
	entries[newName] = entry
	delete(entries, oldName)
	return writeManifestLocked(mosaicDir, entries)
}

// MarkCachedInManifest flips the cached flag to true.
func MarkCachedInManifest(mosaicDir, name string) error {
	manifestMu.Lock()
	defer manifestMu.Unlock()
	entries, err := readManifestLocked(mosaicDir)
	if err != nil {
		return err
	}
	entry, ok := entries[name]
	if !ok {
		return nil
	}
	entry.Cached = true
	entries[name] = entry
	return writeManifestLocked(mosaicDir, entries)
}

// RestoreManifestEntry re-inserts a previously removed entry exactly as-is,
// preserving the original date, size, and cached state.
func RestoreManifestEntry(mosaicDir string, entry ManifestEntry) error {
	manifestMu.Lock()
	defer manifestMu.Unlock()
	entries, err := readManifestLocked(mosaicDir)
	if err != nil {
		return err
	}
	entries[entry.Name] = entry
	return writeManifestLocked(mosaicDir, entries)
}

// IsInManifest reports whether the file is tracked on the network.
func IsInManifest(mosaicDir, name string) bool {
	manifestMu.Lock()
	defer manifestMu.Unlock()
	entries, err := readManifestLocked(mosaicDir)
	if err != nil {
		return false
	}
	_, ok := entries[name]
	return ok
}
