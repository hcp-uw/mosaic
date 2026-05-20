package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/cli/shared"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
	"github.com/hcp-uw/mosaic/internal/transfer"
)

func httpPort() string {
	if p := os.Getenv("MOSAIC_HTTP_PORT"); p != "" {
		return ":" + p
	}
	return ":7777"
}

// FileWithStatus extends file info with a sync-status field for the Finder badge.
type FileWithStatus struct {
	Name      string `json:"name"`
	Size      int    `json:"size"`
	NodeID    int    `json:"nodeID"`
	DateAdded string `json:"dateAdded"`
	IsCached  bool   `json:"isCached"`
}

// StartHTTPServer starts a localhost HTTP server that the Finder Sync extension
// (and any other thin UI bridge) uses to query and control the daemon.
//
//	GET  /files              → list all files + sync status
//	GET  /files/{name}/info  → metadata, peers, size, etc.
//	DELETE /files/{name}     → delete from network
//	POST /files/{name}/fetch → trigger download / cache locally
func StartHTTPServer() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/files", handleFiles)
	mux.HandleFunc("/files/", handleFileByName)
	mux.HandleFunc("/upload-progress", handleUploadProgress)
	mux.HandleFunc("/download-progress", handleDownloadProgress)
	mux.HandleFunc("/join-sync-status", handleJoinSyncStatus)
	mux.HandleFunc("/active-op", handleActiveOp)
	mux.HandleFunc("/cancel-op", handleCancelOp)
	mux.HandleFunc("/network-status", handleNetworkStatus)

	port := httpPort()
	fmt.Println("HTTP API listening on", port)
	return http.ListenAndServe(port, mux)
}

// GET /upload-progress — returns current shard dispatch counts for the active upload.
func handleUploadProgress(w http.ResponseWriter, r *http.Request) {
	dispatched, total := transfer.GetUploadProgress()
	writeJSON(w, http.StatusOK, struct {
		Dispatched int `json:"dispatched"`
		Total      int `json:"total"`
	}{dispatched, total})
}

// GET /download-progress — returns shard fetch progress for the active FetchFileBytes call.
func handleDownloadProgress(w http.ResponseWriter, r *http.Request) {
	received, needed := transfer.GetDownloadProgress()
	writeJSON(w, http.StatusOK, struct {
		Received int `json:"received"`
		Needed   int `json:"needed"`
	}{received, needed})
}

// POST /cancel-op — signals the active operation to stop at its next checkpoint.
func handleCancelOp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	handlers.CancelActiveOp()
	writeJSON(w, http.StatusOK, map[string]bool{"cancelled": true})
}

// GET /active-op — returns the current in-flight operation, or null when idle.
func handleActiveOp(w http.ResponseWriter, r *http.Request) {
	op := handlers.GetActiveOp()
	if op == nil {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	writeJSON(w, http.StatusOK, op)
}

// GET /join-sync-status — returns whether the post-join manifest+shard sync has settled.
func handleJoinSyncStatus(w http.ResponseWriter, r *http.Request) {
	settled, manifestSynced, shardsReceived, elapsedMs := handlers.GetJoinSyncStatus()
	writeJSON(w, http.StatusOK, struct {
		Settled        bool  `json:"settled"`
		ManifestSynced bool  `json:"manifestSynced"`
		ShardsReceived int   `json:"shardsReceived"`
		ElapsedMs      int64 `json:"elapsedMs"`
	}{settled, manifestSynced, shardsReceived, elapsedMs})
}

// GET /files
func handleFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resp := handlers.ListFiles(protocol.ListFilesRequest{})
	mosaicDir := shared.MosaicDir()

	files := make([]FileWithStatus, 0, len(resp.Files))
	for _, f := range resp.Files {
		info := handlers.GetFileInfo(protocol.FileInfoRequest{FilePath: f.Name})
		files = append(files, FileWithStatus{
			Name:      f.Name,
			Size:      info.Size,
			NodeID:    info.NodeID,
			DateAdded: info.DateAdded,
			IsCached:  filesystem.IsCached(mosaicDir, f.Name),
		})
	}

	writeJSON(w, http.StatusOK, files)
}

// GET /files/{name}/info  |  DELETE /files/{name}  |  POST /files/{name}/fetch
func handleFileByName(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/files/"), "/", 2)
	name := parts[0]
	sub := ""
	if len(parts) == 2 {
		sub = parts[1]
	}

	if name == "" {
		http.NotFound(w, r)
		return
	}

	mosaicDir := shared.MosaicDir()

	switch {
	case r.Method == http.MethodGet && sub == "info":
		info := handlers.GetFileInfo(protocol.FileInfoRequest{FilePath: name})
		writeJSON(w, http.StatusOK, FileWithStatus{
			Name:      info.FileName,
			Size:      info.Size,
			NodeID:    info.NodeID,
			DateAdded: info.DateAdded,
			IsCached:  filesystem.IsCached(mosaicDir, name),
		})

	case r.Method == http.MethodDelete && sub == "":
		resp := handlers.DeleteFile(protocol.DeleteFileRequest{FilePath: name})
		writeJSON(w, http.StatusOK, resp)

	case r.Method == http.MethodPost && sub == "rename":
		var body struct {
			NewName string `json:"newName"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.NewName == "" {
			http.Error(w, "missing newName", http.StatusBadRequest)
			return
		}
		if GlobalWatcher != nil {
			GlobalWatcher.SuppressNext(filepath.Join(mosaicDir, name))
			GlobalWatcher.SuppressNext(filepath.Join(mosaicDir, name+".mosaic"))
			GlobalWatcher.SuppressNext(filepath.Join(mosaicDir, body.NewName))
			GlobalWatcher.SuppressNext(filepath.Join(mosaicDir, body.NewName+".mosaic"))
		}
		resp := handlers.RenameFile(protocol.RenameFileRequest{FilePath: name, NewName: body.NewName})
		writeJSON(w, http.StatusOK, resp)

	case r.Method == http.MethodPost && sub == "fetch":
		resp := handlers.DownloadFile(protocol.DownloadFileRequest{FilePath: name})
		writeJSON(w, http.StatusOK, resp)

	default:
		http.NotFound(w, r)
	}
}

// GET /network-status — returns peer connection state for the status window.
func handleNetworkStatus(w http.ResponseWriter, r *http.Request) {
	type peerEntry struct {
		ID         string `json:"id"`
		Address    string `json:"address"`
		ViaTURN    bool   `json:"viaTURN"`
		QUICActive bool   `json:"quicActive"`
	}
	type response struct {
		Connected bool        `json:"connected"`
		PeerCount int         `json:"peerCount"`
		Peers     []peerEntry `json:"peers"`
	}

	client := handlers.GetP2PClient()
	if client == nil || !client.IsPeerCommunicationAvailable() {
		writeJSON(w, http.StatusOK, response{Connected: false, Peers: []peerEntry{}})
		return
	}

	raw := client.GetConnectedPeers()
	peers := make([]peerEntry, 0, len(raw))
	for _, p := range raw {
		addr := ""
		if p.Address != nil {
			addr = p.Address.String()
		}
		peers = append(peers, peerEntry{ID: p.ID, Address: addr, ViaTURN: p.ViaTURN, QUICActive: p.QUICConn != nil})
	}
	writeJSON(w, http.StatusOK, response{Connected: true, PeerCount: len(peers), Peers: peers})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		fmt.Println("HTTP encode error:", err)
	}
}
