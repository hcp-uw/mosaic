package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
)

// Downloads a folder from the network. TODO: implement real folder download.
func DownloadFolder(req protocol.DownloadFolderRequest) protocol.DownloadFolderResponse {
	fmt.Println("Daemon: handling folder download for", req.FolderPath)
	return protocol.DownloadFolderResponse{
		Success:          true,
		Details:          "Download processed by daemon",
		FolderName:       removePath(req.FolderPath),
		AvailableStorage: helpers.AvailableStorage(),
	}
}
