package persistence

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hcp-uw/mosaic/internal/networking/crdt"
)

// Test helpers

func createTempDir(t *testing.T) string {
	tempDir, err := os.MkdirTemp("", "persistence_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	
	// Cleanup will be handled by the test framework or explicitly by tests
	return tempDir
}

func cleanupTempDir(t *testing.T, dir string) {
	if err := os.RemoveAll(dir); err != nil {
		t.Logf("Warning: failed to cleanup temp dir %s: %v", dir, err)
	}
}

func generateTestKeys(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate test keys: %v", err)
	}
	return pubKey, privKey
}

// FileStorageBackend tests

func TestFileStorageBackend_BasicOperations(t *testing.T) {
	tempDir := createTempDir(t)
	defer cleanupTempDir(t, tempDir)

	backend, err := NewFileStorageBackend(tempDir)
	if err != nil {
		t.Fatalf("Failed to create backend: %v", err)
	}
	defer backend.Close()

	testPath := "test/file.txt"
	testData := []byte("test data content")

	// Test Write
	if err := backend.Write(testPath, testData); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Test Exists
	if !backend.Exists(testPath) {
		t.Fatal("File should exist after write")
	}

	// Test Read
	readData, err := backend.Read(testPath)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	if string(readData) != string(testData) {
		t.Errorf("Read data mismatch: expected %q, got %q", string(testData), string(readData))
	}

	// Test Delete
	if err := backend.Delete(testPath); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	if backend.Exists(testPath) {
		t.Fatal("File should not exist after delete")
	}
}

func TestFileStorageBackend_AtomicWrite(t *testing.T) {
	tempDir := createTempDir(t)
	defer cleanupTempDir(t, tempDir)

	backend, err := NewFileStorageBackend(tempDir)
	if err != nil {
		t.Fatalf("Failed to create backend: %v", err)
	}
	defer backend.Close()

	testPath := "atomic/test.txt"
	testData := []byte("atomic write test")

	// Test atomic write
	if err := backend.WriteAtomic(testPath, testData); err != nil {
		t.Fatalf("Atomic write failed: %v", err)
	}

	// Verify data
	readData, err := backend.Read(testPath)
	if err != nil {
		t.Fatalf("Read after atomic write failed: %v", err)
	}

	if string(readData) != string(testData) {
		t.Errorf("Atomic write data mismatch: expected %q, got %q", string(testData), string(readData))
	}
}

func TestFileStorageBackend_DirectoryOperations(t *testing.T) {
	tempDir := createTempDir(t)
	defer cleanupTempDir(t, tempDir)

	backend, err := NewFileStorageBackend(tempDir)
	if err != nil {
		t.Fatalf("Failed to create backend: %v", err)
	}
	defer backend.Close()

	testDir := "test/directory"

	// Test CreateDir
	if err := backend.CreateDir(testDir); err != nil {
		t.Fatalf("CreateDir failed: %v", err)
	}

	// Create some test files
	files := []string{"file1.txt", "file2.txt", "file3.txt"}
	for _, file := range files {
		filePath := filepath.Join(testDir, file)
		if err := backend.Write(filePath, []byte("test content")); err != nil {
			t.Fatalf("Failed to write test file %s: %v", file, err)
		}
	}

	// Test ListFiles
	listedFiles, err := backend.ListFiles(testDir)
	if err != nil {
		t.Fatalf("ListFiles failed: %v", err)
	}

	if len(listedFiles) != len(files) {
		t.Errorf("Expected %d files, got %d", len(files), len(listedFiles))
	}

	// Check that all files are listed
	for _, expectedFile := range files {
		found := false
		for _, listedFile := range listedFiles {
			if expectedFile == listedFile {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("File %s not found in listing", expectedFile)
		}
	}
}

// Identity store tests

func TestFileIdentityStore_KeyStorage(t *testing.T) {
	tempDir := createTempDir(t)
	defer cleanupTempDir(t, tempDir)

	backend, err := NewFileStorageBackend(tempDir)
	if err != nil {
		t.Fatalf("Failed to create backend: %v", err)
	}
	defer backend.Close()

	identityStore := NewFileIdentityStore(backend, "node")
	
	// Generate test keys
	publicKey, privateKey := generateTestKeys(t)
	passphrase := []byte("test passphrase")

	// Test storing private key
	if err := identityStore.StorePrivateKey(privateKey, passphrase); err != nil {
		t.Fatalf("Failed to store private key: %v", err)
	}

	// Test storing public key
	if err := identityStore.StorePublicKey(publicKey); err != nil {
		t.Fatalf("Failed to store public key: %v", err)
	}

	// Test HasIdentity
	if !identityStore.HasIdentity() {
		t.Fatal("Identity should exist after storing keys")
	}

	// Test loading private key
	loadedPrivateKey, err := identityStore.LoadPrivateKey(passphrase)
	if err != nil {
		t.Fatalf("Failed to load private key: %v", err)
	}

	if string(loadedPrivateKey) != string(privateKey) {
		t.Error("Loaded private key doesn't match original")
	}

	// Test loading public key
	loadedPublicKey, err := identityStore.LoadPublicKey()
	if err != nil {
		t.Fatalf("Failed to load public key: %v", err)
	}

	if string(loadedPublicKey) != string(publicKey) {
		t.Error("Loaded public key doesn't match original")
	}
}

func TestFileIdentityStore_WrongPassphrase(t *testing.T) {
	tempDir := createTempDir(t)
	defer cleanupTempDir(t, tempDir)

	backend, err := NewFileStorageBackend(tempDir)
	if err != nil {
		t.Fatalf("Failed to create backend: %v", err)
	}
	defer backend.Close()

	identityStore := NewFileIdentityStore(backend, "node")
	
	_, privateKey := generateTestKeys(t)
	passphrase := []byte("correct passphrase")
	wrongPassphrase := []byte("wrong passphrase")

	// Store private key
	if err := identityStore.StorePrivateKey(privateKey, passphrase); err != nil {
		t.Fatalf("Failed to store private key: %v", err)
	}

	// Try to load with wrong passphrase
	_, err = identityStore.LoadPrivateKey(wrongPassphrase)
	if err == nil {
		t.Fatal("Should have failed to load private key with wrong passphrase")
	}
}

func TestFileIdentityStore_Config(t *testing.T) {
	tempDir := createTempDir(t)
	defer cleanupTempDir(t, tempDir)

	backend, err := NewFileStorageBackend(tempDir)
	if err != nil {
		t.Fatalf("Failed to create backend: %v", err)
	}
	defer backend.Close()

	identityStore := NewFileIdentityStore(backend, "node")

	testConfig := map[string]interface{}{
		"test_key":   "test_value",
		"number":     42,
		"boolean":    true,
		"timestamp":  time.Now().Unix(),
	}

	// Store config
	if err := identityStore.StoreConfig(testConfig); err != nil {
		t.Fatalf("Failed to store config: %v", err)
	}

	// Load config
	loadedConfig, err := identityStore.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Verify config values (handle JSON number conversion)
	for key, expectedValue := range testConfig {
		if loadedValue, exists := loadedConfig[key]; exists {
			// Handle JSON number conversion issues
			switch key {
			case "number":
				if expectedInt, ok := expectedValue.(int); ok {
					if loadedFloat, ok := loadedValue.(float64); ok {
						if int(loadedFloat) != expectedInt {
							t.Errorf("Config value mismatch for %s: expected %v, got %v", key, expectedValue, loadedValue)
						}
					} else {
						t.Errorf("Config value type mismatch for %s: expected int, got %T", key, loadedValue)
					}
				}
			case "timestamp":
				if expectedInt, ok := expectedValue.(int64); ok {
					if loadedFloat, ok := loadedValue.(float64); ok {
						if int64(loadedFloat) != expectedInt {
							t.Errorf("Config value mismatch for %s: expected %v, got %v", key, expectedValue, loadedValue)
						}
					} else {
						t.Errorf("Config value type mismatch for %s: expected int64, got %T", key, loadedValue)
					}
				}
			default:
				if loadedValue != expectedValue {
					t.Errorf("Config value mismatch for %s: expected %v, got %v", key, expectedValue, loadedValue)
				}
			}
		} else {
			t.Errorf("Config key %s not found in loaded config", key)
		}
	}
}

// CRDT store tests

func TestFileCRDTStore_SaveLoad(t *testing.T) {
	tempDir := createTempDir(t)
	defer cleanupTempDir(t, tempDir)

	backend, err := NewFileStorageBackend(tempDir)
	if err != nil {
		t.Fatalf("Failed to create backend: %v", err)
	}
	defer backend.Close()

	crdtStore := NewFileCRDTStore(backend, "crdt")

	// Create test CRDT
	testCRDT := crdt.New()

	// Save CRDT
	if err := crdtStore.SaveCRDT(testCRDT); err != nil {
		t.Fatalf("Failed to save CRDT: %v", err)
	}

	// Load CRDT
	loadedCRDT, err := crdtStore.LoadCRDT()
	if err != nil {
		t.Fatalf("Failed to load CRDT: %v", err)
	}

	if loadedCRDT == nil {
		t.Fatal("Loaded CRDT is nil")
	}

	// Verify CRDT structure
	if len(loadedCRDT.GetJoinMessages()) != len(testCRDT.GetJoinMessages()) {
		t.Errorf("Join message count mismatch: expected %d, got %d", 
			len(testCRDT.GetJoinMessages()), len(loadedCRDT.GetJoinMessages()))
	}
}

func TestFileCRDTStore_Journal(t *testing.T) {
	tempDir := createTempDir(t)
	defer cleanupTempDir(t, tempDir)

	backend, err := NewFileStorageBackend(tempDir)
	if err != nil {
		t.Fatalf("Failed to create backend: %v", err)
	}
	defer backend.Close()

	crdtStore := NewFileCRDTStore(backend, "crdt")

	// Create test operations
	operations := []CRDTOperation{
		{
			Type:      "add_join_message",
			Timestamp: time.Now(),
			Data: map[string]interface{}{
				"node_id": "test_node_1",
				"action":  "join",
			},
		},
		{
			Type:      "add_store_operation",
			Timestamp: time.Now(),
			Data: map[string]interface{}{
				"file_hash": "abc123",
				"size":      1024,
			},
		},
	}

	// Append operations
	for _, operation := range operations {
		if err := crdtStore.AppendOperation(operation); err != nil {
			t.Fatalf("Failed to append operation: %v", err)
		}
	}

	// Load operations from a time before they were added
	since := time.Now().Add(-1 * time.Hour)
	loadedOps, err := crdtStore.LoadOperations(since)
	if err != nil {
		t.Fatalf("Failed to load operations: %v", err)
	}

	if len(loadedOps) != len(operations) {
		t.Errorf("Operation count mismatch: expected %d, got %d", len(operations), len(loadedOps))
	}
}

func TestFileCRDTStore_Snapshot(t *testing.T) {
	tempDir := createTempDir(t)
	defer cleanupTempDir(t, tempDir)

	backend, err := NewFileStorageBackend(tempDir)
	if err != nil {
		t.Fatalf("Failed to create backend: %v", err)
	}
	defer backend.Close()

	crdtStore := NewFileCRDTStore(backend, "crdt")

	// Create and save a CRDT
	testCRDT := crdt.New()
	if err := crdtStore.SaveCRDT(testCRDT); err != nil {
		t.Fatalf("Failed to save CRDT: %v", err)
	}

	// Create snapshot
	if err := crdtStore.CreateSnapshot(); err != nil {
		t.Fatalf("Failed to create snapshot: %v", err)
	}

	// Load from snapshot
	snapshotCRDT, err := crdtStore.LoadFromSnapshot()
	if err != nil {
		t.Fatalf("Failed to load from snapshot: %v", err)
	}

	if snapshotCRDT == nil {
		t.Fatal("Snapshot CRDT is nil")
	}
}

// Network store tests

func TestFileNetworkStore_PeerInfo(t *testing.T) {
	tempDir := createTempDir(t)
	defer cleanupTempDir(t, tempDir)

	backend, err := NewFileStorageBackend(tempDir)
	if err != nil {
		t.Fatalf("Failed to create backend: %v", err)
	}
	defer backend.Close()

	networkStore := NewFileNetworkStore(backend, "network")

	// Create test peer info
	publicKey, _ := generateTestKeys(t)
	peerInfo := PeerInfo{
		NodeID:    "test_peer_1",
		PublicKey: publicKey,
		Addresses: []string{"127.0.0.1:9090", "192.168.1.100:9090"},
		LastSeen:  time.Now(),
		Verified:  true,
	}

	// Store peer info
	if err := networkStore.StorePeerInfo(peerInfo.NodeID, peerInfo); err != nil {
		t.Fatalf("Failed to store peer info: %v", err)
	}

	// Load peer info
	loadedPeer, err := networkStore.LoadPeerInfo(peerInfo.NodeID)
	if err != nil {
		t.Fatalf("Failed to load peer info: %v", err)
	}

	// Verify peer info
	if loadedPeer.NodeID != peerInfo.NodeID {
		t.Errorf("Node ID mismatch: expected %s, got %s", peerInfo.NodeID, loadedPeer.NodeID)
	}

	if string(loadedPeer.PublicKey) != string(peerInfo.PublicKey) {
		t.Error("Public key mismatch")
	}

	if len(loadedPeer.Addresses) != len(peerInfo.Addresses) {
		t.Errorf("Address count mismatch: expected %d, got %d", len(peerInfo.Addresses), len(loadedPeer.Addresses))
	}
}

func TestFileNetworkStore_ConnectionMetrics(t *testing.T) {
	tempDir := createTempDir(t)
	defer cleanupTempDir(t, tempDir)

	backend, err := NewFileStorageBackend(tempDir)
	if err != nil {
		t.Fatalf("Failed to create backend: %v", err)
	}
	defer backend.Close()

	networkStore := NewFileNetworkStore(backend, "network")

	metrics := ConnectionMetrics{
		NodeID:             "test_peer",
		SuccessfulConns:    10,
		FailedConns:        2,
		LastSuccess:        time.Now(),
		LastFailure:        time.Now().Add(-1 * time.Hour),
		AvgResponseTime:    50 * time.Millisecond,
		TotalDataSent:      1024 * 1024,
		TotalDataReceived:  2048 * 1024,
	}

	// Store metrics
	if err := networkStore.StoreConnectionMetrics(metrics.NodeID, metrics); err != nil {
		t.Fatalf("Failed to store metrics: %v", err)
	}

	// Load metrics
	loadedMetrics, err := networkStore.LoadConnectionMetrics(metrics.NodeID)
	if err != nil {
		t.Fatalf("Failed to load metrics: %v", err)
	}

	// Verify metrics
	if loadedMetrics.NodeID != metrics.NodeID {
		t.Errorf("Node ID mismatch: expected %s, got %s", metrics.NodeID, loadedMetrics.NodeID)
	}

	if loadedMetrics.SuccessfulConns != metrics.SuccessfulConns {
		t.Errorf("Successful connections mismatch: expected %d, got %d", 
			metrics.SuccessfulConns, loadedMetrics.SuccessfulConns)
	}

	if loadedMetrics.TotalDataSent != metrics.TotalDataSent {
		t.Errorf("Data sent mismatch: expected %d, got %d", 
			metrics.TotalDataSent, loadedMetrics.TotalDataSent)
	}
}

// Integration tests

func TestNetworkNodeManager_Initialization(t *testing.T) {
	tempDir := createTempDir(t)
	defer cleanupTempDir(t, tempDir)

	nodeManager := NewNetworkNodeManager()
	passphrase := []byte("test passphrase")

	// Initialize node manager
	if err := nodeManager.Initialize(tempDir, passphrase); err != nil {
		t.Fatalf("Failed to initialize node manager: %v", err)
	}

	// Verify initialization
	identity := nodeManager.GetNodeIdentity()
	if identity.NodeID == "" {
		t.Fatal("Node ID should not be empty")
	}

	if len(identity.PublicKey) != ed25519.PublicKeySize {
		t.Errorf("Invalid public key size: expected %d, got %d", 
			ed25519.PublicKeySize, len(identity.PublicKey))
	}

	if len(identity.PrivateKey) != ed25519.PrivateKeySize {
		t.Errorf("Invalid private key size: expected %d, got %d", 
			ed25519.PrivateKeySize, len(identity.PrivateKey))
	}

	// Test CRDT retrieval
	localCRDT := nodeManager.GetLocalCRDT()
	if localCRDT == nil {
		t.Fatal("Local CRDT should not be nil")
	}

	// Shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	if err := nodeManager.Shutdown(ctx); err != nil {
		t.Fatalf("Failed to shutdown node manager: %v", err)
	}
}

func TestNetworkNodeManager_PeerManagement(t *testing.T) {
	tempDir := createTempDir(t)
	defer cleanupTempDir(t, tempDir)

	nodeManager := NewNetworkNodeManager()
	passphrase := []byte("test passphrase")

	if err := nodeManager.Initialize(tempDir, passphrase); err != nil {
		t.Fatalf("Failed to initialize: %v", err)
	}

	// Add test peers
	publicKey1, _ := generateTestKeys(t)
	publicKey2, _ := generateTestKeys(t)

	peers := []struct {
		nodeID    string
		publicKey ed25519.PublicKey
		addresses []string
	}{
		{"peer1", publicKey1, []string{"127.0.0.1:9091"}},
		{"peer2", publicKey2, []string{"127.0.0.1:9092", "192.168.1.100:9092"}},
	}

	// Add peer connections
	for _, peer := range peers {
		if err := nodeManager.AddPeerConnection(peer.nodeID, peer.publicKey, peer.addresses); err != nil {
			t.Fatalf("Failed to add peer %s: %v", peer.nodeID, err)
		}
	}

	// Update connection metrics
	for _, peer := range peers {
		err := nodeManager.UpdateConnectionMetrics(
			peer.nodeID, 
			true, 
			30*time.Millisecond, 
			512, 
			1024,
		)
		if err != nil {
			t.Fatalf("Failed to update metrics for peer %s: %v", peer.nodeID, err)
		}
	}

	// Get best peers
	bestPeers, err := nodeManager.GetBestPeers(10)
	if err != nil {
		t.Fatalf("Failed to get best peers: %v", err)
	}

	if len(bestPeers) != len(peers) {
		t.Errorf("Expected %d best peers, got %d", len(peers), len(bestPeers))
	}

	// Shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	nodeManager.Shutdown(ctx)
}

func TestPersistenceManager_FullWorkflow(t *testing.T) {
	tempDir := createTempDir(t)
	defer cleanupTempDir(t, tempDir)

	// Create persistence manager
	pm := NewFilePersistenceManager()
	if err := pm.Initialize(tempDir); err != nil {
		t.Fatalf("Failed to initialize persistence manager: %v", err)
	}

	// Test identity operations
	publicKey, privateKey := generateTestKeys(t)
	passphrase := []byte("test passphrase")

	identityStore := pm.Identity()
	if err := identityStore.StorePrivateKey(privateKey, passphrase); err != nil {
		t.Fatalf("Failed to store private key: %v", err)
	}

	if err := identityStore.StorePublicKey(publicKey); err != nil {
		t.Fatalf("Failed to store public key: %v", err)
	}

	// Test CRDT operations
	crdtStore := pm.CRDT()
	testCRDT := crdt.New()
	if err := crdtStore.SaveCRDT(testCRDT); err != nil {
		t.Fatalf("Failed to save CRDT: %v", err)
	}

	// Test network operations
	networkStore := pm.Network()
	networkConfig := NetworkConfig{
		NodeID:           "test_node",
		ListenAddresses:  []string{"127.0.0.1:9090"},
		MaxPeers:         50,
		ConnectionTimeout: 30 * time.Second,
	}

	if err := networkStore.StoreNetworkConfig(networkConfig); err != nil {
		t.Fatalf("Failed to store network config: %v", err)
	}

	// Test backup creation
	if err := pm.Backup(); err != nil {
		t.Fatalf("Failed to create backup: %v", err)
	}

	// Start auto-save
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	if err := pm.StartAutoSave(ctx, 500*time.Millisecond); err != nil {
		t.Fatalf("Failed to start auto-save: %v", err)
	}

	// Wait for auto-save cycle
	time.Sleep(600 * time.Millisecond)

	// Shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := pm.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Failed to shutdown persistence manager: %v", err)
	}
}

// Benchmarks

func BenchmarkFileStorageBackend_Write(b *testing.B) {
	tempDir, _ := os.MkdirTemp("", "bench_test_*")
	defer os.RemoveAll(tempDir)

	backend, _ := NewFileStorageBackend(tempDir)
	defer backend.Close()

	testData := []byte("benchmark test data that is somewhat longer to simulate real usage patterns")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path := fmt.Sprintf("bench/file_%d.txt", i)
		backend.Write(path, testData)
	}
}

func BenchmarkFileStorageBackend_AtomicWrite(b *testing.B) {
	tempDir, _ := os.MkdirTemp("", "bench_test_*")
	defer os.RemoveAll(tempDir)

	backend, _ := NewFileStorageBackend(tempDir)
	defer backend.Close()

	testData := []byte("benchmark test data for atomic writes with sufficient content to measure performance")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path := fmt.Sprintf("bench/atomic_%d.txt", i)
		backend.WriteAtomic(path, testData)
	}
}

func BenchmarkIdentityStore_KeyOperations(b *testing.B) {
	tempDir, _ := os.MkdirTemp("", "bench_test_*")
	defer os.RemoveAll(tempDir)

	backend, _ := NewFileStorageBackend(tempDir)
	defer backend.Close()

	identityStore := NewFileIdentityStore(backend, "bench_node")
	publicKey, privateKey := generateTestKeys(&testing.T{})
	passphrase := []byte("benchmark passphrase")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		identityStore.StorePrivateKey(privateKey, passphrase)
		identityStore.StorePublicKey(publicKey)
		identityStore.LoadPrivateKey(passphrase)
		identityStore.LoadPublicKey()
	}
}