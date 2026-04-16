package handlers

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
)

// RenameFile renames a file on the network and updates all local state.
func RenameFile(req protocol.RenameFileRequest) protocol.RenameFileResponse {
	oldName := removePath(req.FilePath)
	newName := removePath(req.NewName)
	fmt.Printf("Daemon: renaming %s → %s\n", oldName, newName)

	mosaicDir := filepath.Join(os.Getenv("HOME"), "Mosaic")

	// we check if ts file even exists in our manifest
	if !filesystem.IsInManifest(mosaicDir, oldName) {
		return protocol.RenameFileResponse{
			Success: false,
			Details: oldName + " is not tracked on the network",
		}
	}

	// Rename the real cached file if it exists
	oldReal := filepath.Join(mosaicDir, oldName)
	newReal := filepath.Join(mosaicDir, newName)
	if _, err := os.Stat(oldReal); err == nil {
		if err := os.Rename(oldReal, newReal); err != nil {
			return protocol.RenameFileResponse{
				Success: false,
				Details: fmt.Sprintf("could not rename local file: %v", err),
			}
		}
	}

	// Rename the stub if the file was remote only
	oldStub := filepath.Join(mosaicDir, oldName+".mosaic")
	newStub := filepath.Join(mosaicDir, newName+".mosaic")
	if _, err := os.Stat(oldStub); err == nil {
		if err := os.Rename(oldStub, newStub); err != nil {
			fmt.Printf("Warning: could not rename stub for %s: %v\n", oldName, err)
		}
	}

	// Update the manifest.
	if err := filesystem.RenameInManifest(mosaicDir, oldName, newName); err != nil {
		return protocol.RenameFileResponse{
			Success: false,
			Details: fmt.Sprintf("could not update manifest: %v", err),
		}
	}

	// Update the network manifest: decrypt own section, mutate, encrypt+sign, write, broadcast.
	if aesKey, err := filesystem.LoadOrCreateNetworkKey(networkKeyPath()); err == nil {
		if kp, kerr := filesystem.LoadOrCreateUserKey(userKeyPath()); kerr == nil {
			if nm, err := filesystem.ReadAndDecryptNetworkManifest(mosaicDir, aesKey, helpers.GetAccountID(), kp.Private); err == nil {
				nm = filesystem.RenameFileInNetwork(nm, helpers.GetAccountID(), oldName, newName)
				if werr := filesystem.EncryptSignAndWriteNetworkManifest(mosaicDir, aesKey, nm, helpers.GetAccountID(), kp); werr != nil {
					fmt.Println("Warning: could not update network manifest for", oldName, "-", werr)
				} else {
					BroadcastNetworkManifest(nm)
				}
			}
		} else {
			fmt.Println("Warning: could not load user key:", kerr)
		}
	}

	// TODO: this is where we do the actual network renaming in other nodes

	return protocol.RenameFileResponse{
		Success:     true,
		Details:     fmt.Sprintf("renamed %s to %s", oldName, newName),
		FileName:    newName,
		Username:    helpers.GetUsername(),
		CurrentNode: helpers.GetNodeID(),
	}
}
