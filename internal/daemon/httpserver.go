package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
)

const HTTPPort = ":7777"

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

	fmt.Println("HTTP API listening on", HTTPPort)
	return http.ListenAndServe(HTTPPort, mux)
}

// GET /files
func handleFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resp := handlers.ListFiles(protocol.ListFilesRequest{})
	mosaicDir := filepath.Join(os.Getenv("HOME"), "Mosaic")

	files := make([]FileWithStatus, 0, len(resp.Files))
	for _, name := range resp.Files {
		info := handlers.GetFileInfo(protocol.FileInfoRequest{FilePath: name})
		files = append(files, FileWithStatus{
			Name:      name,
			Size:      info.Size,
			NodeID:    info.NodeID,
			DateAdded: info.DateAdded,
			IsCached:  filesystem.IsCached(mosaicDir, name),
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

	mosaicDir := filepath.Join(os.Getenv("HOME"), "Mosaic")

	switch {
	case r.Method == http.MethodGet && sub == "info":
		// Return FileWithStatus so isCached is included for badge decisions.
		info := handlers.GetFileInfo(protocol.FileInfoRequest{FilePath: name})
		writeJSON(w, http.StatusOK, FileWithStatus{
			Name:      info.FileName,
			Size:      info.Size,
			NodeID:    info.NodeID,
			DateAdded: info.DateAdded,
			IsCached:  filesystem.IsCached(mosaicDir, name),
		})

	case r.Method == http.MethodDelete && sub == "":
		// DeleteFile handler already calls RemoveStub internally.
		resp := handlers.DeleteFile(protocol.DeleteFileRequest{FilePath: name})
		writeJSON(w, http.StatusOK, resp)

	case r.Method == http.MethodPost && sub == "fetch":
		resp := handlers.DownloadFile(protocol.DownloadFileRequest{FilePath: name})
		if resp.Success {
			stubPath := filepath.Join(mosaicDir, name+".mosaic")

			// If no manifest entry exists (file came from a peer), add a minimal one.
			if !filesystem.IsInManifest(mosaicDir, name) {
				_ = filesystem.AddToManifest(mosaicDir, name, 0, 0)
			}

			// Delete the stub — the real file now lives alongside it.
			// Suppress the watcher so it doesn't interpret this as a user-initiated delete.
			if GlobalWatcher != nil {
				GlobalWatcher.SuppressNext(stubPath)
			}
			_ = os.Remove(stubPath)

			filesystem.MarkCachedInManifest(mosaicDir, name)
		}
		writeJSON(w, http.StatusOK, resp)

	default:
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		fmt.Println("HTTP encode error:", err)
	}
}
