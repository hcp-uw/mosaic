package handlers

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/cli/shared"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
)

// Deletes a file from the network and returns an DeleteFileResponse
func DeleteFile(req protocol.DeleteFileRequest) protocol.DeleteFileResponse {
	fmt.Println("Daemon: handling delete for", req.FilePath)

	filename := removePath(req.FilePath)

	mosaicDir := shared.MosaicDir()
	// Remove the stub (if it exists — cached files won't have one).
	if err := filesystem.RemoveStub(mosaicDir, filename); err != nil {
		fmt.Println("Warning: could not remove stub for", filename, "-", err)
	}
	// Remove the real cached file (if it exists).
	realPath := filepath.Join(mosaicDir, filename)
	if _, err := os.Stat(realPath); err == nil {
		if err := os.Remove(realPath); err != nil {
			fmt.Println("Warning: could not remove cached file", filename, "-", err)
		}
	}
	// Remove from manifest.
	if err := filesystem.RemoveFromManifest(mosaicDir, filename); err != nil {
		fmt.Println("Warning: could not update manifest for", filename, "-", err)
	}

	// Update the network manifest: decrypt own section, mutate, encrypt+sign, write, broadcast.
	if aesKey, err := filesystem.LoadOrCreateNetworkKey(shared.NetworkKeyPath()); err == nil {
		if kp, kerr := filesystem.LoadOrCreateUserKey(shared.UserKeyPath()); kerr == nil {
			if nm, err := filesystem.ReadAndDecryptNetworkManifest(mosaicDir, aesKey, helpers.GetAccountID(), kp.Private); err == nil {
				nm = filesystem.RemoveFileFromNetwork(nm, helpers.GetAccountID(), filename)
				if werr := filesystem.EncryptSignAndWriteNetworkManifest(mosaicDir, aesKey, nm, helpers.GetAccountID(), kp); werr != nil {
					fmt.Println("Warning: could not update network manifest for", filename, "-", werr)
				} else {
					BroadcastNetworkManifest(nm)
				}
			}
		} else {
			fmt.Println("Warning: could not load user key:", kerr)
		}
	}

	return protocol.DeleteFileResponse{
		Success:          true,
		Details:          "Delete processed by daemon",
		FileName:         filename,
		AvailableStorage: helpers.AvailableStorage(),
	}
}
