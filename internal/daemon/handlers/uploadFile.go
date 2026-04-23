package handlers

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/cli/shared"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
	"github.com/hcp-uw/mosaic/internal/transfer"
)

// UploadFile uploads a file to the network.
// If the file is already cached locally (e.g. a re-upload), it re-fetches
// from the network to update the local copy.
func UploadFile(req protocol.UploadFileRequest) protocol.UploadFileResponse {
	fmt.Println("Daemon: handling upload for", req.Path)
	return uploadFile(req.Path, false)
}

// IngestLocalFile registers a file that already exists in ~/Mosaic/ (e.g. dragged
// in by the user) without re-fetching — the local copy is already correct.
func IngestLocalFile(path string) protocol.UploadFileResponse {
	fmt.Println("Daemon: ingesting local file", path)
	return uploadFile(path, true)
}

func uploadFile(path string, keepLocal bool) protocol.UploadFileResponse {
	filename := removePath(path)

	originalSize := 0
	if info, err := os.Stat(path); err == nil {
		originalSize = int(info.Size())
	}

	mosaicDir := shared.MosaicDir()
	nodeID := helpers.GetNodeID()
	realPath := filepath.Join(mosaicDir, filename)

	// TODO: distribute file shards to peers here.

	contentHash, _ := sha256File(path)

	// Update the manifest entry with the latest size and content hash.
	if err := filesystem.AddToManifest(mosaicDir, filename, originalSize, nodeID, contentHash); err != nil {
		fmt.Println("Warning: could not update manifest for", filename, "-", err)
	}

	// Update the network manifest: decrypt own section, mutate, encrypt+sign, write, broadcast.
	if aesKey, err := filesystem.LoadOrCreateNetworkKey(shared.NetworkKeyPath()); err == nil {
		if kp, kerr := filesystem.LoadOrCreateUserKey(shared.UserKeyPath()); kerr == nil {
			if nm, err := filesystem.ReadAndDecryptNetworkManifest(mosaicDir, aesKey, helpers.GetAccountID(), kp.Private); err == nil {
				entry := filesystem.NetworkFileEntry{
					Name:          filename,
					Size:          originalSize,
					PrimaryNodeID: nodeID,
					DateAdded:     time.Now().Format("01-02-2006"),
					ContentHash:   contentHash,
				}
				nm = filesystem.AddFileToNetwork(nm, helpers.GetAccountID(), helpers.GetUsername(), entry)
				if werr := filesystem.EncryptSignAndWriteNetworkManifest(mosaicDir, aesKey, nm, helpers.GetAccountID(), kp); werr != nil {
					fmt.Println("Warning: could not update network manifest for", filename, "-", werr)
				} else {
					BroadcastNetworkManifest(nm)
					go transfer.UploadFile(path, GetP2PClient())
				}
			}
		} else {
			fmt.Println("Warning: could not load user key:", kerr)
		}
	}

	alreadyCached := false
	if _, err := os.Stat(realPath); err == nil {
		alreadyCached = true
	}

	switch {
	case keepLocal:
		// File was dragged in — it's already the correct local copy, mark cached.
		if err := filesystem.MarkCachedInManifest(mosaicDir, filename); err != nil {
			fmt.Println("Warning: could not mark cached for", filename, "-", err)
		}

	case alreadyCached:
		// Re-upload of an existing cached file — re-fetch from network to update local copy.
		fmt.Println("Daemon: re-fetching", filename, "after upload to update local cache")
		fetchResp := DownloadFile(protocol.DownloadFileRequest{FilePath: filename})
		if !fetchResp.Success {
			fmt.Println("Warning: re-fetch after upload failed for", filename, "-", fetchResp.Details)
		}

	default:
		// File not cached — write a stub so Finder shows the remote-only placeholder.
		if err := filesystem.WriteStub(mosaicDir, filename, originalSize, nodeID, contentHash); err != nil {
			fmt.Println("Warning: could not write stub for", filename, "-", err)
		}
	}

	return protocol.UploadFileResponse{
		Success:          true,
		Details:          "Upload processed by daemon",
		FileName:         filename,
		AvailableStorage: helpers.AvailableStorage(),
	}
}

func removePath(path string) string {
	return filepath.Base(path)
}
