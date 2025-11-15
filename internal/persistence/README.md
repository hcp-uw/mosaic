# Mosaic Persistence System

## Overview

The Mosaic persistence system provides a comprehensive, layered architecture for storing and retrieving network state, identity information, and CRDT data with strong consistency guarantees and automatic recovery capabilities.

## Architecture

```
┌─────────────────────────────────────┐
│ Application Layer                   │
├─────────────────────────────────────┤
│ NetworkNodeManager                  │
│ - Identity management               │
│ - CRDT state coordination          │
│ - Peer connection tracking         │
├─────────────────────────────────────┤
│ PersistenceManager                  │
│ - Component coordination           │
│ - Auto-save & recovery             │
│ - Backup management                │
├─────────────────────────────────────┤
│ Component Stores                    │
│ - IdentityStore (encrypted keys)   │
│ - CRDTStore (journal + snapshots)  │
│ - NetworkStore (peer cache)        │
├─────────────────────────────────────┤
│ FileStorageBackend                  │
│ - Atomic operations                │
│ - Corruption protection            │
│ - Directory management             │
└─────────────────────────────────────┘
```

## Directory Structure

```
./mosaic-data/
├── node/
│   ├── identity.enc          # Encrypted private key
│   ├── identity.pub          # Public key
│   └── config.json          # Node configuration
├── network/
│   ├── crdt/
│   │   ├── current.json     # Current CRDT state
│   │   ├── snapshots/       # Periodic snapshots
│   │   └── journal/         # Operation log
│   ├── peers/
│   │   └── known.json       # Known peer cache
│   ├── trusted/
│   │   └── trusted.json     # Trusted certificates
│   ├── metrics/
│   │   └── metrics.json     # Connection metrics
│   └── config/
│       └── network.json     # Network configuration
└── backups/                 # Automatic backups
```

## Core Components

### 1. NetworkNodeManager

The main integration point that combines identity, CRDT, and network state management.

```go
// Initialize node with persistence
nodeManager := NewNetworkNodeManager()
passphrase := []byte("secure passphrase")

err := nodeManager.Initialize("/path/to/data", passphrase)
if err != nil {
    log.Fatal(err)
}

// Get node identity
identity := nodeManager.GetNodeIdentity()
fmt.Printf("Node ID: %s\n", identity.NodeID)

// Update CRDT and persist automatically
updatedCRDT := crdt.New()
err = nodeManager.UpdateCRDT(updatedCRDT)

// Start automatic persistence
ctx := context.Background()
err = nodeManager.StartAutoSave(ctx, 5*time.Minute)

// Graceful shutdown
shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
nodeManager.Shutdown(shutdownCtx)
```

### 2. Identity Storage

Secure storage of cryptographic keys with encryption at rest.

```go
// Access identity store through persistence manager
identityStore := persistenceManager.Identity()

// Generate or load existing identity
identityManager := NewIdentityManager(identityStore)
publicKey, privateKey, err := identityManager.GenerateOrLoadIdentity(passphrase)

// Store additional configuration
config := map[string]interface{}{
    "node_version": "1.0.0",
    "features": []string{"crdt", "p2p"},
}
err = identityStore.StoreConfig(config)
```

### 3. CRDT Persistence

Append-only journal with periodic snapshots for CRDT state management.

```go
// Access CRDT store
crdtStore := persistenceManager.CRDT()

// Save current CRDT state
localCRDT := crdt.New()
err := crdtStore.SaveCRDT(localCRDT)

// Log operations for audit trail
operation := CRDTOperation{
    Type: "add_join_message",
    Timestamp: time.Now(),
    Data: map[string]interface{}{
        "node_id": "peer_123",
        "action": "join",
    },
}
err = crdtStore.AppendOperation(operation)

// Create snapshots for fast recovery
err = crdtStore.CreateSnapshot()

// Cleanup old data
retentionPeriod := 30 * 24 * time.Hour
err = crdtStore.Cleanup(retentionPeriod)
```

### 4. Network State Management

Caching of peer information and connection metrics.

```go
// Access network store
networkStore := persistenceManager.Network()

// Store discovered peer
peerInfo := PeerInfo{
    NodeID: "discovered_peer",
    PublicKey: peerPublicKey,
    Addresses: []string{"192.168.1.100:9090"},
    LastSeen: time.Now(),
    Verified: true,
}
err := networkStore.StorePeerInfo(peerInfo.NodeID, peerInfo)

// Track connection quality
metrics := ConnectionMetrics{
    NodeID: "discovered_peer",
    SuccessfulConns: 5,
    FailedConns: 1,
    AvgResponseTime: 50 * time.Millisecond,
    TotalDataSent: 1024,
    TotalDataReceived: 2048,
}
err = networkStore.StoreConnectionMetrics(metrics.NodeID, metrics)

// Get best performing peers
networkManager := NewNetworkStoreManager(networkStore)
bestPeers, err := networkManager.GetTopPeers(10)
```

## Advanced Features

### Automatic Backup and Recovery

```go
// Create manual backup
err := persistenceManager.Backup()

// Automatic recovery from corruption
err = persistenceManager.Recover()

// Recovery with specific state
recoveryManager := NewStateRecoveryManager(nodeManager)
networkState, err := recoveryManager.RecoverNetworkingState()
```

### Integration with Networking

```go
// Create integrated networking client
helper := NewNetworkingIntegrationHelper(nodeManager)
client, err := helper.CreateNetworkingClientWithPersistence()

// Network operations with automatic persistence
err = client.JoinNetworkWithPersistence([]string{"bootstrap1:9090", "bootstrap2:9090"})

// CRDT merge with persistence
events, err := client.MergeCRDTWithPersistence(peerCRDT)

// Data storage with operation logging
err = client.SetDataWithPersistence(data, "target_peer")
```

## Security Features

### Encrypted Identity Storage

- Private keys encrypted with AES-256-GCM
- PBKDF2-based key derivation with 10,000 iterations
- Random salt generation for each encryption
- Secure passphrase-based access

### Data Integrity

- Atomic file operations prevent corruption
- SHA-256 checksums for data validation
- Cryptographic signatures for CRDT operations
- Backup verification and recovery procedures

### Access Control

- Passphrase-protected identity access
- Trusted peer certificate management
- Signature verification for all CRDT operations
- Secure peer authentication

## Performance Characteristics

### Storage Efficiency

- JSON serialization with compression
- Incremental CRDT snapshots
- Log rotation for journal files
- Automatic cleanup of old data

### Operation Performance

- File-based storage: ~1ms for small operations
- Atomic writes: ~2-5ms with fsync
- Identity operations: ~10-50ms (encryption overhead)
- CRDT operations: ~1-10ms depending on size

### Memory Usage

- Lazy loading of network state
- Streaming for large CRDT operations
- Configurable cache sizes
- Background garbage collection

## Configuration

### Auto-save Settings

```go
// Configure auto-save interval
configManager := NewConfigurationManager(persistenceManager)
err := configManager.SetAutoSaveInterval(2 * time.Minute)

// Get storage statistics
stats, err := configManager.GetStorageStats()
```

### Custom Storage Backend

```go
// Implement custom storage backend
type CustomBackend struct {
    // Custom implementation
}

func (cb *CustomBackend) Write(path string, data []byte) error {
    // Custom write logic
}

// Use custom backend
customBackend := &CustomBackend{}
persistenceManager := &FilePersistenceManager{backend: customBackend}
```

## Error Handling

### Common Error Types

- `PersistenceError`: File system operation failures
- Identity errors: Encryption/decryption failures
- CRDT errors: Validation and merge conflicts
- Network errors: Peer information corruption

### Recovery Strategies

1. **Automatic Recovery**: Try backup files, previous snapshots
2. **Manual Recovery**: User intervention for critical failures
3. **Partial Recovery**: Continue with available data
4. **Factory Reset**: Start fresh if all recovery fails

## Best Practices

### Initialization

```go
// Always check for existing identity before generating new keys
if !identityStore.HasIdentity() {
    // Generate new identity
} else {
    // Load existing identity with proper error handling
}
```

### Shutdown

```go
// Always use timeout contexts for shutdown
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

if err := nodeManager.Shutdown(ctx); err != nil {
    log.Printf("Shutdown warning: %v", err)
    // Force shutdown if timeout
}
```

### Data Validation

```go
// Validate data before persistence
if err := localCRDT.Validate(); err != nil {
    return fmt.Errorf("CRDT validation failed: %w", err)
}

// Check storage space before large operations
stats, err := configManager.GetStorageStats()
```

### Monitoring

```go
// Monitor persistence operations
operation := CRDTOperation{
    Type: "monitoring",
    Timestamp: time.Now(),
    Data: map[string]interface{}{
        "storage_used": storageSize,
        "peer_count": peerCount,
    },
}
crdtStore.AppendOperation(operation)
```

## Testing

The persistence system includes comprehensive tests:

```bash
# Run all persistence tests
go test ./internal/persistence -v

# Run specific test categories
go test ./internal/persistence -run TestFileStorageBackend -v
go test ./internal/persistence -run TestIdentityStore -v
go test ./internal/persistence -run TestCRDTStore -v
go test ./internal/persistence -run TestNetworkStore -v

# Run benchmarks
go test ./internal/persistence -bench=. -v
```

## Migration and Versioning

### Future Compatibility

- Version markers in all stored files
- Backward compatibility for old formats
- Migration procedures for schema changes
- Graceful handling of unknown data

### Data Migration

```go
// Check data version and migrate if needed
version, err := checkDataVersion(dataDir)
if err == nil && version < currentVersion {
    err = migrateData(dataDir, version, currentVersion)
}
```

This persistence system provides a robust foundation for the Mosaic P2P network, ensuring data durability, consistency, and security while maintaining high performance and ease of use.