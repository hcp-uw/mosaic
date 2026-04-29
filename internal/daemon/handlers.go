package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/p2p"
)

// Version is reported via `mos version`. Bump on releases.
const Version = "0.1.0-mvp"

// --- Identity / network -------------------------------------------------

func (a *App) HandleJoin(req protocol.JoinRequest) protocol.JoinResponse {
	if a.P2P != nil {
		return protocol.JoinResponse{Success: false, Details: "already joined"}
	}
	cfg := p2p.DefaultClientConfig(req.ServerAddress)
	client, err := p2p.NewClient(cfg)
	if err != nil {
		return protocol.JoinResponse{Success: false, Details: fmt.Sprintf("create client: %v", err)}
	}
	client.OnPeerAssigned(func(peer *p2p.PeerInfo) {
		_ = client.ConnectToPeer(peer)
	})
	a.SetP2P(client)
	if err := client.ConnectToStun(); err != nil {
		a.P2P = nil
		return protocol.JoinResponse{Success: false, Details: fmt.Sprintf("connect stun: %v", err)}
	}
	_ = a.SetStunAddress(req.ServerAddress)
	return protocol.JoinResponse{Success: true, Details: "Network joined."}
}

func (a *App) HandleLeaveNetwork(_ protocol.LeaveNetworkRequest) protocol.LeaveNetworkResponse {
	if a.P2P == nil {
		return protocol.LeaveNetworkResponse{Success: false, Details: "not joined"}
	}
	_ = a.P2P.DisconnectFromStun()
	a.P2P = nil
	return protocol.LeaveNetworkResponse{Success: true, Details: "Left network."}
}

func (a *App) HandleLoginKey(_ protocol.LoginKeyRequest) protocol.LoginKeyResponse {
	// MVP: identity is loaded at daemon start. LoginKey reports the
	// active identity's short ID.
	return protocol.LoginKeyResponse{
		Success:  true,
		Details:  "Identity loaded.",
		Username: a.Identity.PublicKeyHex()[:16],
	}
}

func (a *App) HandleLogout(_ protocol.LogoutRequest) protocol.LogoutResponse {
	// MVP: a single persistent identity per daemon. Logout disconnects
	// from the network but keeps the identity file on disk.
	if a.P2P != nil {
		_ = a.P2P.DisconnectFromStun()
		a.P2P = nil
	}
	return protocol.LogoutResponse{
		Success:  true,
		Details:  "Logged out.",
		Username: a.Identity.PublicKeyHex()[:16],
	}
}

// --- File operations ---------------------------------------------------

func (a *App) HandleUploadFile(req protocol.UploadFileRequest) protocol.UploadFileResponse {
	meta, err := a.UploadFile(req.Path)
	if err != nil {
		return protocol.UploadFileResponse{
			Success: false,
			Details: err.Error(),
		}
	}
	return protocol.UploadFileResponse{
		Success:          true,
		Details:          fmt.Sprintf("Uploaded %d shards.", len(meta.Shards)),
		FileName:         meta.Filename,
		AvailableStorage: availStorage(a),
	}
}

func (a *App) HandleUploadFolder(req protocol.UploadFolderRequest) protocol.UploadFolderResponse {
	count := 0
	err := filepath.WalkDir(req.FolderPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if _, err := a.UploadFile(path); err != nil {
			return err
		}
		count++
		return nil
	})
	if err != nil {
		return protocol.UploadFolderResponse{Success: false, Details: err.Error()}
	}
	return protocol.UploadFolderResponse{
		Success:          true,
		Details:          fmt.Sprintf("Uploaded %d files.", count),
		FolderName:       filepath.Base(req.FolderPath),
		AvailableStorage: availStorage(a),
	}
}

func (a *App) HandleDownloadFile(req protocol.DownloadFileRequest) protocol.DownloadFileResponse {
	// req.FilePath is expected to be an absolute path (resolved by the
	// CLI before sending so that the user's working directory wins).
	// req.Filename is the logical name in the manifest; falls back to
	// filepath.Base(FilePath) for backwards compatibility.
	filename := req.Filename
	if filename == "" {
		filename = filepath.Base(req.FilePath)
	}
	out := req.FilePath
	if !filepath.IsAbs(out) {
		return protocol.DownloadFileResponse{
			Success:  false,
			Details:  "download path must be absolute",
			FileName: filename,
		}
	}
	if err := a.DownloadFile(filename, out); err != nil {
		return protocol.DownloadFileResponse{Success: false, Details: err.Error(), FileName: filename}
	}
	return protocol.DownloadFileResponse{
		Success:          true,
		Details:          "Downloaded.",
		FileName:         filename,
		AvailableStorage: availStorage(a),
	}
}

func (a *App) HandleDownloadFolder(req protocol.DownloadFolderRequest) protocol.DownloadFolderResponse {
	// MVP: we don't model folders explicitly; treat the request as a
	// best-effort download of every file whose name shares the prefix.
	prefix := filepath.Base(req.FolderPath)
	count := 0
	for _, f := range a.ListFiles() {
		if !strings.HasPrefix(f.Filename, prefix) {
			continue
		}
		_ = a.DownloadFile(f.Filename, filepath.Join(req.FolderPath, f.Filename))
		count++
	}
	return protocol.DownloadFolderResponse{
		Success:    true,
		Details:    fmt.Sprintf("Downloaded %d files.", count),
		FolderName: prefix,
	}
}

func (a *App) HandleDeleteFile(req protocol.DeleteFileRequest) protocol.DeleteFileResponse {
	filename := filepath.Base(req.FilePath)
	if err := a.DeleteFile(filename); err != nil {
		return protocol.DeleteFileResponse{Success: false, Details: err.Error(), FileName: filename}
	}
	return protocol.DeleteFileResponse{
		Success:          true,
		Details:          "Deleted.",
		FileName:         filename,
		AvailableStorage: availStorage(a),
	}
}

func (a *App) HandleDeleteFolder(req protocol.DeleteFolderRequest) protocol.DeleteFolderResponse {
	prefix := filepath.Base(req.FolderName)
	count := 0
	for _, f := range a.ListFiles() {
		if !strings.HasPrefix(f.Filename, prefix) {
			continue
		}
		if err := a.DeleteFile(f.Filename); err == nil {
			count++
		}
	}
	return protocol.DeleteFolderResponse{
		Success:    true,
		Details:    fmt.Sprintf("Deleted %d files.", count),
		FolderName: prefix,
	}
}

func (a *App) HandleListFiles(_ protocol.ListFilesRequest) protocol.ListFilesResponse {
	files := a.ListFiles()
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, f.Filename)
	}
	return protocol.ListFilesResponse{
		Success:  true,
		Details:  fmt.Sprintf("%d files.", len(names)),
		Username: a.Identity.PublicKeyHex()[:16],
		Files:    names,
	}
}

func (a *App) HandleFileInfo(req protocol.FileInfoRequest) protocol.FileInfoResponse {
	filename := filepath.Base(req.FilePath)
	meta, err := a.FileInfo(filename)
	if err != nil {
		return protocol.FileInfoResponse{Success: false, Details: err.Error(), FileName: filename}
	}
	return protocol.FileInfoResponse{
		Success:   true,
		FileName:  meta.Filename,
		Username:  a.Identity.PublicKeyHex()[:16],
		DateAdded: meta.CreatedAt.Format(time.RFC3339),
		Size:      int(meta.Size),
	}
}

func (a *App) HandleFolderInfo(req protocol.FolderInfoRequest) protocol.FolderInfoResponse {
	prefix := filepath.Base(req.FolderName)
	var totalSize uint64
	var count int
	earliest := time.Now()
	for _, f := range a.ListFiles() {
		if !strings.HasPrefix(f.Filename, prefix) {
			continue
		}
		count++
		totalSize += f.Size
		if f.CreatedAt.Before(earliest) {
			earliest = f.CreatedAt
		}
	}
	return protocol.FolderInfoResponse{
		Success:       true,
		FolderName:    prefix,
		DateAdded:     earliest.Format(time.RFC3339),
		Size:          int(totalSize),
		NumberOfFiles: count,
	}
}

// --- Storage management ------------------------------------------------

func (a *App) HandleSetStorage(req protocol.SetStorageRequest) protocol.SetStorageResponse {
	// Amount is interpreted as bytes — the CLI converts user-friendly
	// units before sending.
	if req.Amount < 0 {
		return protocol.SetStorageResponse{Success: false, Details: "amount must be non-negative"}
	}
	if err := a.SetQuota(uint64(req.Amount)); err != nil {
		return protocol.SetStorageResponse{Success: false, Details: err.Error()}
	}
	return protocol.SetStorageResponse{
		Success:          true,
		Details:          "Quota updated.",
		NodeStorage:      req.Amount,
		AvailableStorage: availStorage(a),
		Username:         a.Identity.PublicKeyHex()[:16],
	}
}

func (a *App) HandleEmptyStorage(_ protocol.EmptyStorageRequest) protocol.EmptyStorageResponse {
	freed, err := a.EmptyStorage()
	if err != nil {
		return protocol.EmptyStorageResponse{Success: false, Details: err.Error()}
	}
	return protocol.EmptyStorageResponse{
		Success:          true,
		Details:          "Storage emptied.",
		StorageDeleted:   int(freed),
		AvailableStorage: availStorage(a),
		Username:         a.Identity.PublicKeyHex()[:16],
	}
}

// --- Status ------------------------------------------------------------

func (a *App) HandleStatusNetwork(_ protocol.NetworkStatusRequest) protocol.NetworkStatusResponse {
	peers := 0
	if a.P2P != nil {
		peers = len(a.P2P.GetConnectedPeers())
	}
	return protocol.NetworkStatusResponse{
		Success:          true,
		Details:          "Network status.",
		NetworkStorage:   int(a.Quota()), // MVP: only know our own contribution
		AvailableStorage: availStorage(a),
		StorageUsed:      int(a.Store.UsedBytes()),
		Peers:            peers,
	}
}

func (a *App) HandleStatusNode(req protocol.NodeStatusRequest) protocol.NodeStatusResponse {
	return protocol.NodeStatusResponse{
		Success:      true,
		Details:      "Node status.",
		Username:     a.Identity.PublicKeyHex()[:16],
		ID:           req.ID,
		StorageShare: int(a.Quota()),
	}
}

func (a *App) HandleStatusAccount(_ protocol.StatusAccountRequest) protocol.StatusAccountResponse {
	return protocol.StatusAccountResponse{
		Success:          true,
		Details:          "Account status.",
		Nodes:            []string{a.Identity.PublicKeyHex()},
		GivenStorage:     int(a.Quota()),
		AvailableStorage: availStorage(a),
		UsedStorage:      int(a.Store.UsedBytes()),
		Username:         a.Identity.PublicKeyHex()[:16],
	}
}

func (a *App) HandleGetPeers(_ protocol.GetPeersRequest) protocol.GetPeersResponse {
	if a.P2P == nil {
		return protocol.GetPeersResponse{Success: true, Details: "Not joined.", Peers: nil}
	}
	connected := a.P2P.GetConnectedPeers()
	out := make([]protocol.Peer, 0, len(connected))
	for _, p := range connected {
		out = append(out, protocol.Peer{
			Username: shortID(p.ID),
		})
	}
	return protocol.GetPeersResponse{
		Success: true,
		Details: fmt.Sprintf("%d peers.", len(out)),
		Peers:   out,
	}
}

func (a *App) HandleGetVersion() protocol.VersionResponse {
	return protocol.VersionResponse{
		Success: true,
		Details: "Version.",
		Version: Version,
	}
}

func shortID(id string) string {
	if len(id) > 16 {
		return id[:16]
	}
	return id
}

// availStorage returns the available bytes as a signed int. The sentinel
// value -1 means "unlimited" (no quota configured).
func availStorage(a *App) int {
	if a.Quota() == 0 {
		return -1
	}
	return int(a.AvailableBytes())
}
