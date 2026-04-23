package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"os"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/cli/shared"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers"
)

func StartServer() error {
	// remove old socket if present
	if _, err := os.Stat(shared.SocketPath); err == nil {
		os.Remove(shared.SocketPath)
	}

	ln, err := net.Listen("unix", shared.SocketPath)
	if err != nil {
		return fmt.Errorf("listen error: %w", err)
	}

	fmt.Println("Daemon listening at", shared.SocketPath)
	for {
		conn, err := ln.Accept()
		if err != nil {
			fmt.Println("accept error:", err)
			continue
		}
		go handleConn(conn)
	}
}
func handleConn(conn net.Conn) {
	defer conn.Close()

	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)

	var req protocol.Request
	if err := dec.Decode(&req); err != nil {
		err := enc.Encode(&protocol.Response{Ok: false, Message: "invalid JSON request"})
		if err != nil {
			fmt.Println("encode error:", err)
		}
		return
	}

	switch req.Command {
	case "joinNetwork":
		var joinReq protocol.JoinRequest
		handleWith(enc, req.Data, &joinReq, handlers.HandleJoin, "Join request failed.")
	case "statusNetwork":
		var statusReq protocol.NetworkStatusRequest
		handleWith(enc, req.Data, &statusReq, handlers.StatusNetwork, "Network status request failed.")
	case "statusNode":
		var nodeStatusReq protocol.NodeStatusRequest
		handleWith(enc, req.Data, &nodeStatusReq, handlers.StatusNode, "Node status request failed.")
	case "statusAccount":
		var accountReq protocol.StatusAccountRequest
		handleWith(enc, req.Data, &accountReq, handlers.StatusAccount, "Status account request failed.")
	case "loginStatus":
		var statusReq protocol.LoginStatusRequest
		handleWith(enc, req.Data, &statusReq, handlers.LoginStatus, "Login status request failed.")
	case "loginKey":
		var loginReq protocol.LoginKeyRequest
		handleWith(enc, req.Data, &loginReq, handlers.LoginKey, "Login request failed.")
	case "logout":
		var logoutReq protocol.LogoutRequest
		handleWith(enc, req.Data, &logoutReq, handlers.HandleLogout, "Logout request failed.")
	case "getPeers":
		var getPeersReq protocol.GetPeersRequest
		handleWith(enc, req.Data, &getPeersReq, handlers.GetPeers, "Get peers request failed.")
	case "setStorage":
		var setStorageReq protocol.SetStorageRequest
		handleWith(enc, req.Data, &setStorageReq, handlers.SetStorage, "Set storage request failed.")
	case "emptyStorage":
		var emptyStorageReq protocol.EmptyStorageRequest
		handleWith(enc, req.Data, &emptyStorageReq, handlers.EmptyStorage, "Empty storage request failed.")
	case "leaveNetwork":
		var leaveNetworkReq protocol.LeaveNetworkRequest
		handleWith(enc, req.Data, &leaveNetworkReq, handlers.LeaveNetwork, "Leave network request failed.")
	case "listFiles":
		var listFilesReq protocol.ListFilesRequest
		handleWith(enc, req.Data, &listFilesReq, handlers.ListFiles, "List files request failed.")
	case "uploadFile":
		var uploadReq protocol.UploadFileRequest
		handleWith(enc, req.Data, &uploadReq, handlers.UploadFile, "Upload file request failed.")
	case "uploadFolder":
		var uploadFolderReq protocol.UploadFolderRequest
		handleWith(enc, req.Data, &uploadFolderReq, handlers.UploadFolder, "Upload folder request failed.")
	case "downloadFile":
		var downloadReq protocol.DownloadFileRequest
		handleWith(enc, req.Data, &downloadReq, handlers.DownloadFile, "Download file request failed.")
	case "downloadFolder":
		var downloadFolderReq protocol.DownloadFolderRequest
		handleWith(enc, req.Data, &downloadFolderReq, handlers.DownloadFolder, "Download folder request failed.")
	case "deleteFile":
		var deleteReq protocol.DeleteFileRequest
		handleWith(enc, req.Data, &deleteReq, handlers.DeleteFile, "Delete file request failed.")
	case "deleteFolder":
		var deleteFolderReq protocol.DeleteFolderRequest
		handleWith(enc, req.Data, &deleteFolderReq, handlers.DeleteFolder, "Delete folder request failed.")
	case "renameFile":
		var renameReq protocol.RenameFileRequest
		handleWith(enc, req.Data, &renameReq, handlers.RenameFile, "Rename file request failed.")
	case "fileInfo":
		var fileInfoReq protocol.FileInfoRequest
		handleWith(enc, req.Data, &fileInfoReq, handlers.GetFileInfo, "File info request failed.")
	case "folderInfo":
		var folderInfoReq protocol.FolderInfoRequest
		handleWith(enc, req.Data, &folderInfoReq, handlers.GetFolderInfo, "Folder info request failed.")
	case "getVersion":
		var versionReq protocol.VersionRequest
		handleWith(enc, req.Data, &versionReq, handlers.GetVersion, "Get version request failed.")

	default:
		err := enc.Encode(&protocol.Response{Ok: false, Message: "unknown command"})
		if err != nil {
			fmt.Println("encode error:", err)
		}
	}
}

// Helper that takes a pointer to pre-declared struct
func handleWith[Req, Resp any](
	enc *json.Encoder,
	data interface{},
	reqPtr *Req,
	handler func(Req) Resp,
	errMsg string,
) {
	if err := toStruct(data, reqPtr); err != nil {
		err := enc.Encode(&protocol.Response{Ok: false, Message: errMsg})
		if err != nil {
			fmt.Println("encode error:", err)
		}
		return
	}
	resp := handler(*reqPtr)
	err := enc.Encode(&protocol.Response{Ok: true, Message: "", Data: resp})
	if err != nil {
		fmt.Println("encode error:", err)
	}
}

func toStruct(input interface{}, out interface{}) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}
