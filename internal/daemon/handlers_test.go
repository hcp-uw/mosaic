package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
)

// newAppForTest returns a freshly initialized App rooted in t.TempDir().
func newAppForTest(t *testing.T) *App {
	t.Helper()
	a, err := NewApp(t.TempDir())
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestHandleGetVersion(t *testing.T) {
	a := newAppForTest(t)
	resp := a.HandleGetVersion()
	if !resp.Success || resp.Version != Version {
		t.Errorf("GetVersion: %+v, want %s", resp, Version)
	}
}

func TestHandleLoginAndLogout(t *testing.T) {
	a := newAppForTest(t)
	loginResp := a.HandleLoginKey(protocol.LoginKeyRequest{})
	if !loginResp.Success {
		t.Errorf("LoginKey success = false")
	}
	if loginResp.Username != a.Identity.PublicKeyHex()[:16] {
		t.Errorf("LoginKey username = %q, want %q", loginResp.Username, a.Identity.PublicKeyHex()[:16])
	}

	logoutResp := a.HandleLogout(protocol.LogoutRequest{})
	if !logoutResp.Success {
		t.Errorf("Logout success = false")
	}
}

func TestHandleStatusAccount(t *testing.T) {
	a := newAppForTest(t)
	resp := a.HandleStatusAccount(protocol.StatusAccountRequest{})
	if !resp.Success {
		t.Fatal("StatusAccount success = false")
	}
	if len(resp.Nodes) != 1 {
		t.Errorf("Nodes = %v, want one entry", resp.Nodes)
	}
	if resp.Nodes[0] != a.Identity.PublicKeyHex() {
		t.Errorf("Node id mismatch: %s", resp.Nodes[0])
	}
}

func TestHandleStatusNetwork_NotJoined(t *testing.T) {
	a := newAppForTest(t)
	resp := a.HandleStatusNetwork(protocol.NetworkStatusRequest{})
	if !resp.Success {
		t.Fatal("StatusNetwork success = false")
	}
	if resp.Peers != 0 {
		t.Errorf("Peers = %d, want 0 when not joined", resp.Peers)
	}
}

func TestHandleSetStorageThenStatus(t *testing.T) {
	a := newAppForTest(t)
	const want = 50 << 20 // 50 MiB in bytes
	if r := a.HandleSetStorage(protocol.SetStorageRequest{Amount: want}); !r.Success {
		t.Fatalf("SetStorage failed: %v", r)
	}
	resp := a.HandleStatusAccount(protocol.StatusAccountRequest{})
	if resp.GivenStorage != want {
		t.Errorf("GivenStorage = %d, want %d", resp.GivenStorage, want)
	}
	if resp.AvailableStorage != want {
		t.Errorf("AvailableStorage = %d, want %d (no usage yet)", resp.AvailableStorage, want)
	}
}

func TestHandleListAndInfo(t *testing.T) {
	a := newAppForTest(t)
	src := filepath.Join(t.TempDir(), "doc.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := a.UploadFile(src); err != nil {
		t.Fatalf("UploadFile: %v", err)
	}

	listResp := a.HandleListFiles(protocol.ListFilesRequest{})
	if !listResp.Success || len(listResp.Files) != 1 || listResp.Files[0] != "doc.txt" {
		t.Errorf("ListFiles: %+v", listResp)
	}

	infoResp := a.HandleFileInfo(protocol.FileInfoRequest{FilePath: "doc.txt"})
	if !infoResp.Success {
		t.Fatalf("FileInfo: %+v", infoResp)
	}
	if infoResp.FileName != "doc.txt" || infoResp.Size != 5 {
		t.Errorf("FileInfo: %+v", infoResp)
	}
}

func TestHandleDeleteFile_Missing(t *testing.T) {
	a := newAppForTest(t)
	resp := a.HandleDeleteFile(protocol.DeleteFileRequest{FilePath: "nope.txt"})
	if resp.Success {
		t.Error("expected failure deleting nonexistent file")
	}
	if !strings.Contains(resp.Details, "not in manifest") {
		t.Errorf("unexpected error message: %q", resp.Details)
	}
}

func TestHandleEmptyStorage(t *testing.T) {
	a := newAppForTest(t)
	src := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(src, []byte("payload"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := a.UploadFile(src); err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	resp := a.HandleEmptyStorage(protocol.EmptyStorageRequest{})
	if !resp.Success {
		t.Fatalf("EmptyStorage: %v", resp)
	}
	if a.Store.UsedBytes() != 0 {
		t.Errorf("UsedBytes after empty = %d, want 0", a.Store.UsedBytes())
	}
}

func TestHandleGetPeers_NotJoined(t *testing.T) {
	a := newAppForTest(t)
	resp := a.HandleGetPeers(protocol.GetPeersRequest{})
	if !resp.Success {
		t.Errorf("GetPeers success = false")
	}
	if len(resp.Peers) != 0 {
		t.Errorf("expected no peers when not joined")
	}
}

func TestHandleStatusNode(t *testing.T) {
	a := newAppForTest(t)
	resp := a.HandleStatusNode(protocol.NodeStatusRequest{ID: "abc"})
	if !resp.Success || resp.ID != "abc" {
		t.Errorf("StatusNode: %+v", resp)
	}
}

func TestHandleUploadFolder(t *testing.T) {
	a := newAppForTest(t)
	dir := t.TempDir()
	for _, n := range []string{"x.txt", "y.txt"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(n), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	resp := a.HandleUploadFolder(protocol.UploadFolderRequest{FolderPath: dir})
	if !resp.Success {
		t.Fatalf("UploadFolder: %+v", resp)
	}
	files := a.HandleListFiles(protocol.ListFilesRequest{}).Files
	if len(files) != 2 {
		t.Errorf("expected 2 files after folder upload, got %d", len(files))
	}
}
