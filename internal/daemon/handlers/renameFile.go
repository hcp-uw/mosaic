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

// RenameFile renames a file on the network and updates all local state.
func RenameFile(req protocol.RenameFileRequest) protocol.RenameFileResponse {
	oldName := removePath(req.FilePath)
	newName := removePath(req.NewName)
	fmt.Printf("Daemon: renaming %s → %s\n", oldName, newName)

	mosaicDir := shared.MosaicDir()

	if !filesystem.IsInManifest(mosaicDir, oldName) {
		return protocol.RenameFileResponse{
			Success: false,
			Details: oldName + " is not tracked on the network",
		}
	}

	// Rename the real cached file if it exists.
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

	// Rename the stub if the file was remote only.
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

	// Update the network manifest: append "rename" block, write, broadcast.
	if aesKey, err := filesystem.LoadOrCreateNetworkKey(shared.NetworkKeyPath()); err == nil {
		if kp, kerr := filesystem.LoadOrCreateUserKey(shared.UserKeyPath()); kerr == nil {
			if nm, err := filesystem.ReadNetworkManifest(mosaicDir, aesKey); err == nil {
				if aerr := filesystem.AppendBlockRename(&nm, helpers.GetAccountID(), oldName, newName, kp); aerr != nil {
					fmt.Println("Warning: could not append rename block for", oldName, "-", aerr)
				} else if werr := filesystem.WriteNetworkManifestLocked(mosaicDir, aesKey, nm); werr != nil {
					fmt.Println("Warning: could not write network manifest for", oldName, "-", werr)
				} else {
					BroadcastNetworkManifest(nm)
				}
			}
		} else {
			fmt.Println("Warning: could not load user key:", kerr)
		}
	}

	return protocol.RenameFileResponse{
		Success:     true,
		Details:     fmt.Sprintf("renamed %s to %s", oldName, newName),
		FileName:    newName,
		Username:    helpers.GetUsername(),
		CurrentNode: helpers.GetNodeID(),
	}
}
