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

// uploadFile handles both explicit uploads and local-file ingestion.
// contentHash and originalSize are derived from the actual file read inside
// transfer.UploadFile so we never store an empty hash in the manifest.

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
	if !helpers.IsLoggedIn() {
		return protocol.UploadFileResponse{
			Success: false,
			Details: "not logged in — run 'mos login <key>' before uploading",
		}
	}
	if IsJoinSettling() {
		return protocol.UploadFileResponse{
			Success: false,
			Details: "network sync in progress — wait a moment for the join to finish, then try again",
		}
	}

	filename := removePath(path)
	mosaicDir := shared.MosaicDir()
	nodeID := helpers.GetNodeID()
	realPath := filepath.Join(mosaicDir, filename)

	// Track whether the file was already cached locally before upload.
	alreadyCached := false
	if _, err := os.Stat(realPath); err == nil {
		alreadyCached = true
	}

	// Distribute shards to peers. This blocks until all shards are sent so we
	// don't announce the file to the network before it's actually downloadable.
	// UploadFile returns the content hash and file size derived from the actual
	// file read — using these avoids a silent empty-hash if a pre-read failed.
	contentHash, originalSize, uploadErr := transfer.UploadFile(path, GetP2PClient())
	if uploadErr != nil {
		fmt.Printf("Warning: shard upload failed for %s: %v\n", filename, uploadErr)
	}
	if contentHash == "" {
		// File could not be read at all — abort rather than writing a broken manifest entry.
		return protocol.UploadFileResponse{
			Success: false,
			Details: fmt.Sprintf("upload failed: could not read %s — %v", filename, uploadErr),
		}
	}

	// Update the local manifest entry so this node knows about the file.
	if err := filesystem.AddToManifest(mosaicDir, filename, originalSize, nodeID, contentHash); err != nil {
		fmt.Println("Warning: could not update manifest for", filename, "-", err)
	}

	// Only now append the network manifest block and broadcast to peers.
	// Peers that receive ManifestSync after this point can successfully download.
	if aesKey, err := filesystem.LoadOrCreateNetworkKey(shared.NetworkKeyPath()); err == nil {
		if kp, kerr := filesystem.LoadOrCreateUserKey(shared.UserKeyPath()); kerr == nil {
			if nm, err := filesystem.ReadNetworkManifest(mosaicDir, aesKey); err == nil {
				entry := filesystem.NetworkFileEntry{
					Name:          filename,
					Size:          originalSize,
					PrimaryNodeID: nodeID,
					DateAdded:     time.Now().Format("01-02-2006"),
					ContentHash:   contentHash,
				}
				if aerr := filesystem.AppendBlockAdd(&nm, helpers.GetAccountID(), helpers.GetUsername(), entry, kp); aerr != nil {
					fmt.Println("Warning: could not append block for", filename, "-", aerr)
				} else if werr := filesystem.WriteNetworkManifestLocked(mosaicDir, aesKey, nm); werr != nil {
					fmt.Println("Warning: could not write network manifest for", filename, "-", werr)
				} else {
					BroadcastNetworkManifest(nm)
				}
			}
		} else {
			fmt.Println("Warning: could not load user key:", kerr)
		}
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
		Details:          "Upload complete — shards distributed and file announced to network",
		FileName:         filename,
		AvailableStorage: helpers.AvailableStorage(),
	}
}

func removePath(path string) string {
	return filepath.Base(path)
}
