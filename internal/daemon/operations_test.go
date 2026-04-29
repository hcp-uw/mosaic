package daemon

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/stun"
)

func TestListAndDelete(t *testing.T) {
	app, err := NewApp(t.TempDir())
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	defer app.Close()

	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte(name+" contents"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := app.UploadFile(path); err != nil {
			t.Fatalf("UploadFile %s: %v", name, err)
		}
	}

	resp := app.HandleListFiles(protocol.ListFilesRequest{})
	if !resp.Success {
		t.Fatalf("ListFiles success = false: %s", resp.Details)
	}
	if len(resp.Files) != 3 {
		t.Fatalf("listed %d files, want 3: %v", len(resp.Files), resp.Files)
	}

	if err := app.DeleteFile("b.txt"); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	resp = app.HandleListFiles(protocol.ListFilesRequest{})
	if len(resp.Files) != 2 {
		t.Fatalf("after delete listed %d files, want 2: %v", len(resp.Files), resp.Files)
	}
	for _, f := range resp.Files {
		if f == "b.txt" {
			t.Error("b.txt should be deleted")
		}
	}
}

func TestEmptyStorage(t *testing.T) {
	app, err := NewApp(t.TempDir())
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	defer app.Close()

	src := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(src, bytes.Repeat([]byte("x"), 1024), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := app.UploadFile(src); err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	// Uploader stores no shards locally (they live on peers).
	if _, err := app.EmptyStorage(); err != nil {
		t.Fatalf("EmptyStorage: %v", err)
	}
	if app.Store.UsedBytes() != 0 {
		t.Errorf("UsedBytes after empty = %d, want 0", app.Store.UsedBytes())
	}
}

func TestSetStorageQuota(t *testing.T) {
	app, err := NewApp(t.TempDir())
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	defer app.Close()

	const wantBytes = 100 << 20 // 100 MiB
	resp := app.HandleSetStorage(protocol.SetStorageRequest{Amount: wantBytes})
	if !resp.Success {
		t.Fatalf("SetStorage failed: %s", resp.Details)
	}
	if app.Quota() != wantBytes {
		t.Errorf("Quota = %d, want %d", app.Quota(), wantBytes)
	}

	// Persistence: reopen and check the value survives.
	if err := app.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	app2, err := NewApp(app.HomeDir)
	if err != nil {
		t.Fatalf("NewApp reopen: %v", err)
	}
	defer app2.Close()
	if app2.Quota() != wantBytes {
		t.Errorf("Quota after reopen = %d, want %d", app2.Quota(), wantBytes)
	}
}

func TestUploadDownload_TwoNodes(t *testing.T) {
	// Two daemons, paired through a local STUN. Owner uploads; peer
	// stores all shards; owner downloads entirely from peer.
	stunCfg := &stun.ServerConfig{
		ListenAddress: "127.0.0.1:0",
		ClientTimeout: 30 * time.Second,
		EnableLogging: false,
	}
	srv := stun.NewServer(stunCfg)
	if err := srv.Start(stunCfg); err != nil {
		t.Fatalf("Start STUN: %v", err)
	}
	defer srv.Stop()
	addr := srv.GetConn().LocalAddr().(*net.UDPAddr).String()

	owner, err := NewApp(t.TempDir())
	if err != nil {
		t.Fatalf("NewApp owner: %v", err)
	}
	defer owner.Close()
	peer, err := NewApp(t.TempDir())
	if err != nil {
		t.Fatalf("NewApp peer: %v", err)
	}
	defer peer.Close()

	// Owner joins first → leader.
	if r := owner.HandleJoin(protocol.JoinRequest{ServerAddress: addr}); !r.Success {
		t.Fatalf("owner join: %s", r.Details)
	}
	time.Sleep(100 * time.Millisecond)
	// Peer joins second → follower.
	if r := peer.HandleJoin(protocol.JoinRequest{ServerAddress: addr}); !r.Success {
		t.Fatalf("peer join: %s", r.Details)
	}

	// Wait for both to see each other AND for UDP hole-punching to settle.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(owner.P2P.GetConnectedPeers()) > 0 && len(peer.P2P.GetConnectedPeers()) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(owner.P2P.GetConnectedPeers()) == 0 {
		t.Fatal("owner has no connected peers")
	}
	if len(peer.P2P.GetConnectedPeers()) == 0 {
		t.Fatal("peer has no connected peers")
	}
	time.Sleep(1 * time.Second) // hole-punch settle

	// Upload from owner.
	src := filepath.Join(t.TempDir(), "shared.txt")
	payload := bytes.Repeat([]byte("two-node test "), 200)
	if err := os.WriteFile(src, payload, 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if _, err := owner.UploadFile(src); err != nil {
		t.Fatalf("UploadFile: %v", err)
	}

	// Owner must have zero local shards — all data lives on the peer.
	if owner.Store.UsedBytes() != 0 {
		t.Fatalf("owner has %d local bytes; want 0 (shards should be on peer)", owner.Store.UsedBytes())
	}

	// Give the manifest gossip a beat to settle.
	time.Sleep(200 * time.Millisecond)

	// Download from owner — all shards come from the peer over P2P.
	out := filepath.Join(t.TempDir(), "shared-restored.txt")
	if err := owner.DownloadFile("shared.txt", out); err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read restored: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("two-node round-trip differs (len=%d vs %d)", len(got), len(payload))
	}

	// Confirm peer is actually holding all shards.
	if peer.Store.UsedBytes() == 0 {
		t.Fatal("peer has no shards stored — distribution failed")
	}
	t.Logf("peer holds %d bytes of shards (distribution OK)", peer.Store.UsedBytes())

	// Large-file round-trip exercises the chunked transport for shards
	// that exceed a single UDP datagram.
	bigSrc := filepath.Join(t.TempDir(), "big.bin")
	bigPayload := bytes.Repeat([]byte("Mosaic chunked transport test. "), 16_000) // ~480 KB
	if err := os.WriteFile(bigSrc, bigPayload, 0o600); err != nil {
		t.Fatalf("write big src: %v", err)
	}
	beforeBig := peer.Store.UsedBytes()
	if _, err := owner.UploadFile(bigSrc); err != nil {
		t.Fatalf("UploadFile big: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if peer.Store.UsedBytes() <= beforeBig {
		t.Fatalf("peer used bytes did not grow after big upload: before=%d after=%d", beforeBig, peer.Store.UsedBytes())
	}
	bigOut := filepath.Join(t.TempDir(), "big-restored.bin")
	if err := owner.DownloadFile("big.bin", bigOut); err != nil {
		t.Fatalf("DownloadFile big: %v", err)
	}
	gotBig, err := os.ReadFile(bigOut)
	if err != nil {
		t.Fatalf("read big out: %v", err)
	}
	if !bytes.Equal(gotBig, bigPayload) {
		t.Errorf("big-file round-trip mismatch: got %d bytes, want %d", len(gotBig), len(bigPayload))
	}
	t.Logf("large-file round-trip OK: peer holds %d bytes (was %d)", peer.Store.UsedBytes(), beforeBig)
}

// setupFourNodeCluster starts a STUN server and four App instances (owner,
// b, c, d), waits for all to see three connected peers each, and returns a
// cleanup function. Tests that call this must call cleanup() when done.
func setupFourNodeCluster(t *testing.T) (owner, b, c, d *App, cleanup func()) {
	t.Helper()
	stunCfg := &stun.ServerConfig{
		ListenAddress: "127.0.0.1:0",
		ClientTimeout: 30 * time.Second,
		EnableLogging: false,
	}
	srv := stun.NewServer(stunCfg)
	if err := srv.Start(stunCfg); err != nil {
		t.Fatalf("Start STUN: %v", err)
	}
	addr := srv.GetConn().LocalAddr().(*net.UDPAddr).String()

	apps := make([]*App, 4)
	for i := range apps {
		app, err := NewApp(t.TempDir())
		if err != nil {
			t.Fatalf("NewApp %d: %v", i, err)
		}
		apps[i] = app
	}
	owner, b, c, d = apps[0], apps[1], apps[2], apps[3]

	// Owner joins first → leader, then b, c, d with small gaps.
	if r := owner.HandleJoin(protocol.JoinRequest{ServerAddress: addr}); !r.Success {
		t.Fatalf("owner join: %s", r.Details)
	}
	time.Sleep(100 * time.Millisecond)
	for i, app := range apps[1:] {
		if r := app.HandleJoin(protocol.JoinRequest{ServerAddress: addr}); !r.Success {
			t.Fatalf("node %d join: %s", i+1, r.Details)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The STUN server only pairs each follower with the leader; followers
	// learn about each other via the leader's gossip. The critical
	// requirement is that the owner (index 0) can reach all 3 followers so
	// it can distribute and later fetch shards from each of them. Followers
	// only need to reach the owner so they can send acks back.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(apps[0].P2P.GetConnectedPeers()) >= 3 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if n := len(apps[0].P2P.GetConnectedPeers()); n < 3 {
		t.Fatalf("owner has only %d connected peers after 10s, want 3", n)
	}
	for i, app := range apps[1:] {
		if len(app.P2P.GetConnectedPeers()) == 0 {
			t.Fatalf("node %d has no connected peers", i+1)
		}
	}
	time.Sleep(1 * time.Second) // hole-punch settle

	cleanup = func() {
		for _, app := range apps {
			app.Close()
		}
		srv.Stop()
	}
	return
}

func TestFourNodeRedundancy(t *testing.T) {
	// RS(4,2) needs any 4 of 6 shards. With 3 peers each holding 2 shards,
	// killing any one peer leaves exactly 4 shards — the minimum for reconstruction.
	cases := []struct {
		name string
		kill func(b, c, d *App)
	}{
		{"B_down", func(b, c, d *App) { b.P2P.DisconnectFromStun() }},
		{"C_down", func(b, c, d *App) { c.P2P.DisconnectFromStun() }},
		{"D_down", func(b, c, d *App) { d.P2P.DisconnectFromStun() }},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			owner, b, c, d, cleanup := setupFourNodeCluster(t)
			defer cleanup()
			owner.GetRequestTimeout = 500 * time.Millisecond

			src := filepath.Join(t.TempDir(), "redundancy.txt")
			payload := bytes.Repeat([]byte("four-node redundancy "), 200)
			if err := os.WriteFile(src, payload, 0o600); err != nil {
				t.Fatalf("write src: %v", err)
			}
			if _, err := owner.UploadFile(src); err != nil {
				t.Fatalf("upload: %v", err)
			}

			// Owner stores nothing locally — all shards live on peers.
			if owner.Store.UsedBytes() != 0 {
				t.Fatalf("owner has %d bytes; want 0", owner.Store.UsedBytes())
			}
			if b.Store.UsedBytes() == 0 {
				t.Fatal("B has no shards")
			}
			if c.Store.UsedBytes() == 0 {
				t.Fatal("C has no shards")
			}
			if d.Store.UsedBytes() == 0 {
				t.Fatal("D has no shards")
			}

			// Kill one peer; its 2 shards become unavailable.
			tc.kill(b, c, d)
			time.Sleep(100 * time.Millisecond)

			// Reconstruction must succeed with 4 remaining shards.
			out := filepath.Join(t.TempDir(), "restored.txt")
			if err := owner.DownloadFile("redundancy.txt", out); err != nil {
				t.Fatalf("download after %s: %v", tc.name, err)
			}
			got, err := os.ReadFile(out)
			if err != nil {
				t.Fatalf("read restored: %v", err)
			}
			if !bytes.Equal(got, payload) {
				t.Errorf("restored payload mismatch: got %d bytes, want %d", len(got), len(payload))
			}
			t.Logf("%s: reconstruction OK using 4/6 shards", tc.name)
		})
	}
}
