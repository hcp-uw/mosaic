package handlers

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/cli/shared"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
)

// DeleteStub removes the local file (stub or cached copy) without touching
// the local manifest entry or the network manifest. The file remains
// recoverable via 'mos download file <name>' or the next manifest sync.
func DeleteStub(req protocol.DeleteStubRequest) protocol.DeleteStubResponse {
	fmt.Println("Daemon: deleting local stub for", req.FilePath)

	filename := removePath(req.FilePath)
	mosaicDir := shared.MosaicDir()

	entries, err := filesystem.ReadManifest(mosaicDir)
	if err != nil || entries[filename].Name == "" {
		return protocol.DeleteStubResponse{
			Success:  false,
			Details:  fmt.Sprintf("file %q not found in local manifest", filename),
			FileName: filename,
		}
	}

	// Mark uncached first so the watcher's REMOVE event sees cached=false
	// and doesn't try to treat the deletion as a network removal.
	if err := filesystem.MarkUncachedInManifest(mosaicDir, filename); err != nil {
		fmt.Printf("Warning: could not mark %s uncached in manifest: %v\n", filename, err)
	}

	// Remove the real cached file if present.
	realPath := filepath.Join(mosaicDir, filename)
	if _, err := os.Stat(realPath); err == nil {
		if err := os.Remove(realPath); err != nil {
			return protocol.DeleteStubResponse{
				Success:  false,
				Details:  fmt.Sprintf("could not remove local file: %v", err),
				FileName: filename,
			}
		}
	}

	// Remove the stub file if present.
	if err := filesystem.RemoveStub(mosaicDir, filename); err != nil {
		fmt.Printf("Warning: could not remove stub for %s: %v\n", filename, err)
	}

	return protocol.DeleteStubResponse{
		Success:  true,
		Details:  fmt.Sprintf("local copy of %q removed; file remains in manifest and can be re-downloaded", filename),
		FileName: filename,
	}
}
