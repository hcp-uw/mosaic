package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"os"

	"github.com/hcp-uw/mosaic/internal/cli/handlers"
	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/cli/shared"
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

/*
func handleConn(conn net.Conn) {
	defer conn.Close()

	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)

	var req protocol.Request
	if err := dec.Decode(&req); err != nil {
		enc.Encode(&protocol.Response{Ok: false, Message: "invalid JSON request"})
		return
	}

	switch req.Command {

	case "uploadFile":
		var up protocol.UploadRequest
		if err := toStruct(req.Data, &up); err != nil {
			enc.Encode(&protocol.Response{Ok: false, Message: "Upload request failed."})
			return
		}
		resp := handlers.HandleUpload(up)
		enc.Encode(&protocol.Response{Ok: true, Message: "", Data: resp})

	case "statusNetwork":
		var up protocol.NetworkStatusRequest
		if err := toStruct(req.Data, &up); err != nil {
			enc.Encode(&protocol.Response{Ok: false, Message: "Network status request failed."})
			return
		}
		resp := handlers.StatusNetwork(up)
		enc.Encode(&protocol.Response{Ok: true, Message: "", Data: resp})

	case "joinNetwork":
		var up protocol.JoinRequest
		if err := toStruct(req.Data, &up); err != nil {
			enc.Encode(&protocol.Response{Ok: false, Message: "Join request failed."})
			return
		}
		resp := handlers.HandleJoin(up)
		enc.Encode(&protocol.Response{Ok: true, Message: "", Data: resp})

	case "statusNode":
		var up protocol.NodeStatusRequest
		if err := toStruct(req.Data, &up); err != nil {
			enc.Encode(&protocol.Response{Ok: false, Message: "Node status request failed."})
			return
		}
		resp := handlers.StatusNode(up)
		enc.Encode(&protocol.Response{Ok: true, Message: "", Data: resp})
	case "loginKey":
		var up protocol.LoginKeyRequest
		if err := toStruct(req.Data, &up); err != nil {
			enc.Encode(&protocol.Response{Ok: false, Message: "Login request failed."})
			return
		}
		resp := handlers.LoginKey(up)
		enc.Encode(&protocol.Response{Ok: true, Message: "", Data: resp})
	case "statusAccount":
		var up protocol.StatusAccountRequest
		if err := toStruct(req.Data, &up); err != nil {
			enc.Encode(&protocol.Response{Ok: false, Message: "Login request failed."})
			return
		}
		resp := handlers.StatusAccount(up)
		enc.Encode(&protocol.Response{Ok: true, Message: "", Data: resp})
	default:
		enc.Encode(&protocol.Response{Ok: false, Message: "unknown command"})
	}
}

// toStruct safely converts interface{} into a target struct using JSON round-tripping.
func toStruct(input interface{}, out interface{}) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}
*/
