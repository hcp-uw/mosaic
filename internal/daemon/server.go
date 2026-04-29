package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/cli/shared"
	"github.com/hcp-uw/mosaic/internal/p2p"
)

// StartServer launches the daemon. The home directory defaults to
// $MOSAIC_HOME or ~/.mosaic.
func StartServer() error {
	home := os.Getenv("MOSAIC_HOME")
	if home == "" {
		hd, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("user home: %w", err)
		}
		home = filepath.Join(hd, ".mosaic")
	}
	app, err := NewApp(home)
	if err != nil {
		return fmt.Errorf("init app: %w", err)
	}
	defer app.Close()

	if _, err := os.Stat(shared.SocketPath()); err == nil {
		os.Remove(shared.SocketPath())
	}
	ln, err := net.Listen("unix", shared.SocketPath())
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer ln.Close()
	defer os.Remove(shared.SocketPath())

	fmt.Printf("Daemon listening at %s (home %s, identity %s)\n", shared.SocketPath(), home, app.Identity.PublicKeyHex()[:16]+"...")
	for {
		conn, err := ln.Accept()
		if err != nil {
			fmt.Println("accept error:", err)
			continue
		}
		go handleConn(app, conn)
	}
}

func handleConn(app *App, conn net.Conn) {
	defer conn.Close()
	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)

	var req protocol.Request
	if err := dec.Decode(&req); err != nil {
		_ = enc.Encode(&protocol.Response{Ok: false, Message: "invalid JSON request"})
		return
	}

	switch req.Command {
	case "joinNetwork":
		var r protocol.JoinRequest
		if err := toStruct(req.Data, &r); err != nil {
			respondErr(enc, "Join request failed.")
			return
		}
		respondOK(enc, app.HandleJoin(r))

	case "leaveNetwork":
		var r protocol.LeaveNetworkRequest
		_ = toStruct(req.Data, &r)
		respondOK(enc, app.HandleLeaveNetwork(r))

	case "loginKey":
		var r protocol.LoginKeyRequest
		_ = toStruct(req.Data, &r)
		respondOK(enc, app.HandleLoginKey(r))

	case "logout":
		var r protocol.LogoutRequest
		_ = toStruct(req.Data, &r)
		respondOK(enc, app.HandleLogout(r))

	case "uploadFile":
		var r protocol.UploadFileRequest
		if err := toStruct(req.Data, &r); err != nil {
			respondErr(enc, "Upload request failed.")
			return
		}
		respondOK(enc, app.HandleUploadFile(r))

	case "uploadFolder":
		var r protocol.UploadFolderRequest
		_ = toStruct(req.Data, &r)
		respondOK(enc, app.HandleUploadFolder(r))

	case "downloadFile":
		var r protocol.DownloadFileRequest
		_ = toStruct(req.Data, &r)
		respondOK(enc, app.HandleDownloadFile(r))

	case "downloadFolder":
		var r protocol.DownloadFolderRequest
		_ = toStruct(req.Data, &r)
		respondOK(enc, app.HandleDownloadFolder(r))

	case "deleteFile":
		var r protocol.DeleteFileRequest
		_ = toStruct(req.Data, &r)
		respondOK(enc, app.HandleDeleteFile(r))

	case "deleteFolder":
		var r protocol.DeleteFolderRequest
		_ = toStruct(req.Data, &r)
		respondOK(enc, app.HandleDeleteFolder(r))

	case "listFiles":
		var r protocol.ListFilesRequest
		_ = toStruct(req.Data, &r)
		respondOK(enc, app.HandleListFiles(r))

	case "fileInfo":
		var r protocol.FileInfoRequest
		_ = toStruct(req.Data, &r)
		respondOK(enc, app.HandleFileInfo(r))

	case "folderInfo":
		var r protocol.FolderInfoRequest
		_ = toStruct(req.Data, &r)
		respondOK(enc, app.HandleFolderInfo(r))

	case "setStorage":
		var r protocol.SetStorageRequest
		_ = toStruct(req.Data, &r)
		respondOK(enc, app.HandleSetStorage(r))

	case "emptyStorage":
		var r protocol.EmptyStorageRequest
		_ = toStruct(req.Data, &r)
		respondOK(enc, app.HandleEmptyStorage(r))

	case "statusNetwork":
		var r protocol.NetworkStatusRequest
		_ = toStruct(req.Data, &r)
		respondOK(enc, app.HandleStatusNetwork(r))

	case "statusNode":
		var r protocol.NodeStatusRequest
		_ = toStruct(req.Data, &r)
		respondOK(enc, app.HandleStatusNode(r))

	case "statusAccount":
		var r protocol.StatusAccountRequest
		_ = toStruct(req.Data, &r)
		respondOK(enc, app.HandleStatusAccount(r))

	case "getPeers":
		var r protocol.GetPeersRequest
		_ = toStruct(req.Data, &r)
		respondOK(enc, app.HandleGetPeers(r))

	case "getVersion":
		respondOK(enc, app.HandleGetVersion())

	default:
		respondErr(enc, "unknown command")
	}
}

func respondOK(enc *json.Encoder, data any) {
	if err := enc.Encode(&protocol.Response{Ok: true, Data: data}); err != nil {
		fmt.Println("encode error:", err)
	}
}

func respondErr(enc *json.Encoder, msg string) {
	if err := enc.Encode(&protocol.Response{Ok: false, Message: msg}); err != nil {
		fmt.Println("encode error:", err)
	}
}

func toStruct(input any, out any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// CurrentP2P is exposed for tests; callers may attach their own P2P
// client in lieu of one created by JoinNetwork.
func (a *App) CurrentP2P() *p2p.Client { return a.P2P }
