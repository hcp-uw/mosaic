package persistence

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"time"

	"github.com/hcp-uw/mosaic/internal/networking/crdt"
)

// StorageBackend defines the interface for persistent storage operations
type StorageBackend interface {
	// Basic file operations
	Write(path string, data []byte) error
	Read(path string) ([]byte, error)
	Exists(path string) bool
	Delete(path string) error
	
	// Atomic operations for consistency
	WriteAtomic(path string, data []byte) error
	
	// Directory operations
	CreateDir(path string) error
	ListFiles(dir string) ([]string, error)
	
	// Backup operations
	CreateBackup(sourcePath, backupPath string) error
	
	// Cleanup
	Close() error
}

// Serializer defines the interface for data serialization
type Serializer interface {
	Serialize(data interface{}) ([]byte, error)
	Deserialize(data []byte, target interface{}) error
	ContentType() string
}

// IdentityStore manages secure storage of cryptographic identities
type IdentityStore interface {
	// Store and load private keys with encryption
	StorePrivateKey(key ed25519.PrivateKey, passphrase []byte) error
	LoadPrivateKey(passphrase []byte) (ed25519.PrivateKey, error)
	
	// Store and load public keys (no encryption needed)
	StorePublicKey(key ed25519.PublicKey) error
	LoadPublicKey() (ed25519.PublicKey, error)
	
	// Check if identity exists
	HasIdentity() bool
	
	// Node configuration
	StoreConfig(config map[string]interface{}) error
	LoadConfig() (map[string]interface{}, error)
}

// CRDTStore manages persistence of CRDT state with conflict resolution
type CRDTStore interface {
	// Save current CRDT state
	SaveCRDT(crdt *crdt.CRDT) error
	
	// Load CRDT state with merge resolution
	LoadCRDT() (*crdt.CRDT, error)
	
	// Append-only journal for operations
	AppendOperation(operation CRDTOperation) error
	LoadOperations(since time.Time) ([]CRDTOperation, error)
	
	// Snapshot management
	CreateSnapshot() error
	LoadFromSnapshot() (*crdt.CRDT, error)
	
	// Cleanup old data
	Cleanup(retentionPeriod time.Duration) error
}

// NetworkStore manages persistence of network state and peer information  
type NetworkStore interface {
	// Peer discovery cache
	StorePeerInfo(nodeID string, info PeerInfo) error
	LoadPeerInfo(nodeID string) (*PeerInfo, error)
	ListKnownPeers() ([]PeerInfo, error)
	
	// Trusted peer certificates
	StoreTrustedPeer(nodeID string, cert TrustedPeerCert) error
	LoadTrustedPeer(nodeID string) (*TrustedPeerCert, error)
	
	// Connection state and metrics
	StoreConnectionMetrics(nodeID string, metrics ConnectionMetrics) error
	LoadConnectionMetrics(nodeID string) (*ConnectionMetrics, error)
	
	// Network configuration
	StoreNetworkConfig(config NetworkConfig) error
	LoadNetworkConfig() (*NetworkConfig, error)
}

// PersistenceManager coordinates all persistence operations
type PersistenceManager interface {
	// Component stores
	Identity() IdentityStore
	CRDT() CRDTStore
	Network() NetworkStore
	
	// Lifecycle management
	Initialize(dataDir string) error
	Shutdown(ctx context.Context) error
	
	// Background operations
	StartAutoSave(ctx context.Context, interval time.Duration) error
	
	// Recovery operations
	Recover() error
	Backup() error
}

// Data structures for persistence

// CRDTOperation represents a single CRDT operation for journaling
type CRDTOperation struct {
	Type      string                 `json:"type"`
	Timestamp time.Time             `json:"timestamp"`
	Data      map[string]interface{} `json:"data"`
	Signature []byte                 `json:"signature,omitempty"`
}

// PeerInfo contains cached information about discovered peers
type PeerInfo struct {
	NodeID     string           `json:"node_id"`
	PublicKey  ed25519.PublicKey `json:"public_key"`
	Addresses  []string         `json:"addresses"`
	LastSeen   time.Time        `json:"last_seen"`
	Verified   bool             `json:"verified"`
}

// TrustedPeerCert represents a trusted peer certificate
type TrustedPeerCert struct {
	NodeID    string           `json:"node_id"`
	PublicKey ed25519.PublicKey `json:"public_key"`
	IssuedAt  time.Time        `json:"issued_at"`
	ExpiresAt time.Time        `json:"expires_at"`
	Signature []byte           `json:"signature"`
}

// ConnectionMetrics tracks connection quality and history
type ConnectionMetrics struct {
	NodeID           string        `json:"node_id"`
	SuccessfulConns  int          `json:"successful_connections"`
	FailedConns      int          `json:"failed_connections"`
	LastSuccess      time.Time    `json:"last_success"`
	LastFailure      time.Time    `json:"last_failure"`
	AvgResponseTime  time.Duration `json:"avg_response_time"`
	TotalDataSent    int64        `json:"total_data_sent"`
	TotalDataReceived int64       `json:"total_data_received"`
}

// NetworkConfig contains network-wide configuration
type NetworkConfig struct {
	NodeID           string   `json:"node_id"`
	ListenAddresses  []string `json:"listen_addresses"`
	BootstrapPeers   []string `json:"bootstrap_peers"`
	MaxPeers         int      `json:"max_peers"`
	ConnectionTimeout time.Duration `json:"connection_timeout"`
	RetryIntervals   []time.Duration `json:"retry_intervals"`
}

// JSONSerializer implements Serializer using JSON
type JSONSerializer struct{}

func (s *JSONSerializer) Serialize(data interface{}) ([]byte, error) {
	return json.Marshal(data)
}

func (s *JSONSerializer) Deserialize(data []byte, target interface{}) error {
	return json.Unmarshal(data, target)
}

func (s *JSONSerializer) ContentType() string {
	return "application/json"
}

// Error types for persistence operations

// PersistenceError represents errors in persistence operations
type PersistenceError struct {
	Op   string // Operation being performed
	Path string // File path involved
	Err  error  // Underlying error
}

func (e *PersistenceError) Error() string {
	return "persistence error in " + e.Op + " at " + e.Path + ": " + e.Err.Error()
}

func (e *PersistenceError) Unwrap() error {
	return e.Err
}

// Helper interfaces for advanced operations

// Compactor defines interface for data compaction
type Compactor interface {
	Compact(reader io.Reader, writer io.Writer) error
	EstimateCompactionRatio(size int64) float64
}

// Validator defines interface for data validation
type Validator interface {
	Validate(data []byte) error
	CalculateChecksum(data []byte) []byte
	VerifyChecksum(data []byte, checksum []byte) bool
}