package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/protocol"
)

func HandleUpload(req protocol.UploadRequest) protocol.UploadResponse {
	fmt.Println("Daemon: handling upload for", req.Path)

	return protocol.UploadResponse{
		Success: true,
		Details: "Upload processed by daemon",
	}
}
