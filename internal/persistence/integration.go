package persistence

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"sync"
	"time"

	"github.com/hcp-uw/mosaic/internal/networking/crdt"
)

// NetworkNodeManager integrates persistence with networking components
type NetworkNodeManager struct {
	persistence      PersistenceManager
	identityManager  *IdentityManager
	networkManager   *NetworkStoreManager
	
	// Current runtime state
	localCRDT        *crdt.CRDT
	nodeIdentity     NodeIdentity
	networkConfig    *NetworkConfig
	
	// Synchronization
	mu               sync.RWMutex
	initialized      bool
	autoSaveEnabled  bool
}

// NodeIdentity represents the current node's identity
type NodeIdentity struct {
	NodeID     string
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey
	CreatedAt  time.Time
	Config     map[string]interface{}
}

// NewNetworkNodeManager creates a new network node manager
func NewNetworkNodeManager() *NetworkNodeManager {
	return &NetworkNodeManager{}
}

// Initialize sets up the network node with persistence
func (nnm *NetworkNodeManager) Initialize(dataDir string, passphrase []byte) error {
	nnm.mu.Lock()
	defer nnm.mu.Unlock()

	if nnm.initialized {
		return fmt.Errorf("network node manager already initialized")
	}

	// Create persistence manager
	nnm.persistence = NewFilePersistenceManager()
	if err := nnm.persistence.Initialize(dataDir); err != nil {
		return fmt.Errorf("failed to initialize persistence: %w", err)
	}

	// Initialize identity manager
	nnm.identityManager = NewIdentityManager(nnm.persistence.Identity())
	
	// Initialize network manager
	nnm.networkManager = NewNetworkStoreManager(nnm.persistence.Network())

	// Load or generate identity
	publicKey, privateKey, err := nnm.identityManager.GenerateOrLoadIdentity(passphrase)
	if err != nil {
		return fmt.Errorf("failed to initialize identity: %w", err)
	}

	// Load identity config
	config, err := nnm.persistence.Identity().LoadConfig()
	if err != nil {
		config = make(map[string]interface{})
	}

	nnm.nodeIdentity = NodeIdentity{
		NodeID:     fmt.Sprintf("%x", publicKey[:8]), // Use first 8 bytes of public key as node ID
		PublicKey:  publicKey,
		PrivateKey: privateKey,
		CreatedAt:  time.Now(),
		Config:     config,
	}

	// Load or create CRDT
	nnm.localCRDT, err = nnm.persistence.CRDT().LoadCRDT()
	if err != nil {
		return fmt.Errorf("failed to load CRDT: %w", err)
	}

	// Load network configuration
	nnm.networkConfig, err = nnm.persistence.Network().LoadNetworkConfig()
	if err != nil {
		return fmt.Errorf("failed to load network config: %w", err)
	}

	// Update network config with our node ID
	nnm.networkConfig.NodeID = nnm.nodeIdentity.NodeID

	nnm.initialized = true
	return nil
}

// GetNodeIdentity returns the current node's identity
func (nnm *NetworkNodeManager) GetNodeIdentity() NodeIdentity {
	nnm.mu.RLock()
	defer nnm.mu.RUnlock()
	return nnm.nodeIdentity
}

// GetLocalCRDT returns the current local CRDT
func (nnm *NetworkNodeManager) GetLocalCRDT() *crdt.CRDT {
	nnm.mu.RLock()
	defer nnm.mu.RUnlock()
	return nnm.localCRDT
}

// UpdateCRDT updates the local CRDT and persists it
func (nnm *NetworkNodeManager) UpdateCRDT(updatedCRDT *crdt.CRDT) error {
	nnm.mu.Lock()
	defer nnm.mu.Unlock()

	if !nnm.initialized {
		return fmt.Errorf("network node manager not initialized")
	}

	// Update local CRDT
	nnm.localCRDT = updatedCRDT

	// Persist the updated CRDT
	if err := nnm.persistence.CRDT().SaveCRDT(updatedCRDT); err != nil {
		return fmt.Errorf("failed to persist CRDT: %w", err)
	}

	// Log CRDT update operation
	operation := CRDTOperation{
		Type:      "crdt_update",
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"node_id":    nnm.nodeIdentity.NodeID,
			"join_count": len(updatedCRDT.GetJoinMessages()),
			"file_count": len(updatedCRDT.GetFileManifest()),
		},
	}

	if err := nnm.persistence.CRDT().AppendOperation(operation); err != nil {
		// Log warning but don't fail the update
	}

	return nil
}

// AddPeerConnection records a successful peer connection
func (nnm *NetworkNodeManager) AddPeerConnection(nodeID string, publicKey ed25519.PublicKey, addresses []string) error {
	nnm.mu.Lock()
	defer nnm.mu.Unlock()

	if !nnm.initialized {
		return fmt.Errorf("network node manager not initialized")
	}

	// Create peer info
	peerInfo := PeerInfo{
		NodeID:    nodeID,
		PublicKey: publicKey,
		Addresses: addresses,
		LastSeen:  time.Now(),
		Verified:  true, // Assume verified if we successfully connected
	}

	// Store peer info
	if err := nnm.persistence.Network().StorePeerInfo(nodeID, peerInfo); err != nil {
		return fmt.Errorf("failed to store peer info: %w", err)
	}

	return nil
}

// UpdateConnectionMetrics updates connection quality metrics for a peer
func (nnm *NetworkNodeManager) UpdateConnectionMetrics(nodeID string, successful bool, responseTime time.Duration, dataSent, dataReceived int64) error {
	return nnm.networkManager.UpdatePeerQuality(nodeID, successful, responseTime, dataSent, dataReceived)
}

// GetBestPeers returns the best-performing known peers
func (nnm *NetworkNodeManager) GetBestPeers(limit int) ([]PeerInfo, error) {
	return nnm.networkManager.GetTopPeers(limit)
}

// StartAutoSave begins automatic persistence of state
func (nnm *NetworkNodeManager) StartAutoSave(ctx context.Context, interval time.Duration) error {
	nnm.mu.Lock()
	defer nnm.mu.Unlock()

	if !nnm.initialized {
		return fmt.Errorf("network node manager not initialized")
	}

	if nnm.autoSaveEnabled {
		return fmt.Errorf("auto-save already enabled")
	}

	if err := nnm.persistence.StartAutoSave(ctx, interval); err != nil {
		return fmt.Errorf("failed to start auto-save: %w", err)
	}

	nnm.autoSaveEnabled = true
	return nil
}

// Shutdown gracefully shuts down the network node manager
func (nnm *NetworkNodeManager) Shutdown(ctx context.Context) error {
	nnm.mu.Lock()
	defer nnm.mu.Unlock()

	if !nnm.initialized {
		return nil
	}

	// Save final state
	if nnm.localCRDT != nil {
		if err := nnm.persistence.CRDT().SaveCRDT(nnm.localCRDT); err != nil {
			return fmt.Errorf("failed to save final CRDT state: %w", err)
		}
	}

	// Shutdown persistence manager
	if err := nnm.persistence.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to shutdown persistence: %w", err)
	}

	nnm.initialized = false
	nnm.autoSaveEnabled = false

	return nil
}

// CreateBackup creates a complete backup of node data
func (nnm *NetworkNodeManager) CreateBackup() error {
	nnm.mu.RLock()
	defer nnm.mu.RUnlock()

	if !nnm.initialized {
		return fmt.Errorf("network node manager not initialized")
	}

	return nnm.persistence.Backup()
}

// NetworkingIntegrationHelper provides helper functions for networking integration
type NetworkingIntegrationHelper struct {
	nodeManager *NetworkNodeManager
}

// NewNetworkingIntegrationHelper creates a new integration helper
func NewNetworkingIntegrationHelper(nodeManager *NetworkNodeManager) *NetworkingIntegrationHelper {
	return &NetworkingIntegrationHelper{nodeManager: nodeManager}
}

// CreateNetworkingClientWithPersistence creates a networking client integrated with persistence
func (nih *NetworkingIntegrationHelper) CreateNetworkingClientWithPersistence() (*PersistentNetworkingClient, error) {
	identity := nih.nodeManager.GetNodeIdentity()
	localCRDT := nih.nodeManager.GetLocalCRDT()

	return &PersistentNetworkingClient{
		nodeManager: nih.nodeManager,
		nodeID:      identity.NodeID,
		publicKey:   identity.PublicKey,
		privateKey:  identity.PrivateKey,
		localCRDT:   localCRDT,
	}, nil
}

// PersistentNetworkingClient extends the networking client with persistence
type PersistentNetworkingClient struct {
	nodeManager *NetworkNodeManager
	nodeID      string
	publicKey   ed25519.PublicKey
	privateKey  ed25519.PrivateKey
	localCRDT   *crdt.CRDT
}

// JoinNetworkWithPersistence performs network join with automatic persistence
func (pnc *PersistentNetworkingClient) JoinNetworkWithPersistence(bootstrapPeers []string) error {
	// This would integrate with the existing MOSJoinNetwork function
	// but add automatic persistence of discovered peers and CRDT updates

	for _, peerAddr := range bootstrapPeers {
		// Attempt to connect to bootstrap peer
		// This is a placeholder - would use actual networking code
		success := pnc.attemptConnection(peerAddr)
		
		if success {
			// Record successful peer connection
			peerNodeID := "peer_" + peerAddr // Simplified
			err := pnc.nodeManager.AddPeerConnection(
				peerNodeID, 
				pnc.publicKey, // Placeholder - would be actual peer key
				[]string{peerAddr},
			)
			if err != nil {
				return fmt.Errorf("failed to record peer connection: %w", err)
			}

			// Update connection metrics
			pnc.nodeManager.UpdateConnectionMetrics(
				peerNodeID,
				true,                    // successful
				50*time.Millisecond,     // response time
				1024,                    // data sent
				2048,                    // data received
			)
		}
	}

	return nil
}

// MergeCRDTWithPersistence merges CRDT data and persists the result
func (pnc *PersistentNetworkingClient) MergeCRDTWithPersistence(peerCRDT *crdt.CRDT) ([]crdt.Event, error) {
	// Merge peer CRDT with local CRDT
	events, err := pnc.localCRDT.Merge(peerCRDT)
	if err != nil {
		return nil, fmt.Errorf("CRDT merge failed: %w", err)
	}

	// Persist the updated CRDT
	if err := pnc.nodeManager.UpdateCRDT(pnc.localCRDT); err != nil {
		return events, fmt.Errorf("failed to persist merged CRDT: %w", err)
	}

	return events, nil
}

// SetDataWithPersistence stores data and persists the operation
func (pnc *PersistentNetworkingClient) SetDataWithPersistence(data []byte, peerNodeID string) error {
	// This would integrate with the existing SetData function
	// but add automatic persistence of the store operation

	// Create store operation for journaling
	operation := CRDTOperation{
		Type:      "set_data",
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"requester_node": pnc.nodeID,
			"target_peer":    peerNodeID,
			"data_size":      len(data),
			"data_hash":      fmt.Sprintf("%x", data[:min(32, len(data))]),
		},
	}

	// Log the operation
	if err := pnc.nodeManager.persistence.CRDT().AppendOperation(operation); err != nil {
		return fmt.Errorf("failed to log set data operation: %w", err)
	}

	return nil
}

// Helper methods

func (pnc *PersistentNetworkingClient) attemptConnection(peerAddr string) bool {
	// Placeholder for actual connection logic
	// In real implementation, this would use the networking package
	return true // Assume success for demonstration
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// StateRecoveryManager handles recovery of networking state after crashes
type StateRecoveryManager struct {
	nodeManager *NetworkNodeManager
}

// NewStateRecoveryManager creates a new state recovery manager
func NewStateRecoveryManager(nodeManager *NetworkNodeManager) *StateRecoveryManager {
	return &StateRecoveryManager{nodeManager: nodeManager}
}

// RecoverNetworkingState recovers networking state from persistent storage
func (srm *StateRecoveryManager) RecoverNetworkingState() (*NetworkingState, error) {
	identity := srm.nodeManager.GetNodeIdentity()
	localCRDT := srm.nodeManager.GetLocalCRDT()

	// Get known peers
	knownPeers, err := srm.nodeManager.networkManager.GetTopPeers(100)
	if err != nil {
		return nil, fmt.Errorf("failed to recover known peers: %w", err)
	}

	// Build networking state
	state := &NetworkingState{
		NodeIdentity:  identity,
		LocalCRDT:     localCRDT,
		KnownPeers:    knownPeers,
		RecoveredAt:   time.Now(),
		ConnectionMap: make(map[string]ConnectionStatus),
	}

	// Populate connection status for known peers
	for _, peer := range knownPeers {
		metrics, err := srm.nodeManager.persistence.Network().LoadConnectionMetrics(peer.NodeID)
		if err == nil {
			state.ConnectionMap[peer.NodeID] = ConnectionStatus{
				PeerInfo:         peer,
				Metrics:          *metrics,
				LastAttempt:      metrics.LastSuccess,
				LastSuccessful:   metrics.LastSuccess.After(metrics.LastFailure),
				RecommendRetry:   time.Now().Sub(metrics.LastSuccess) < 5*time.Minute,
			}
		}
	}

	return state, nil
}

// NetworkingState represents the complete networking state
type NetworkingState struct {
	NodeIdentity  NodeIdentity                   `json:"node_identity"`
	LocalCRDT     *crdt.CRDT                    `json:"local_crdt"`
	KnownPeers    []PeerInfo                    `json:"known_peers"`
	RecoveredAt   time.Time                     `json:"recovered_at"`
	ConnectionMap map[string]ConnectionStatus   `json:"connection_map"`
}

// ConnectionStatus represents the status of a connection to a peer
type ConnectionStatus struct {
	PeerInfo         PeerInfo           `json:"peer_info"`
	Metrics          ConnectionMetrics  `json:"metrics"`
	LastAttempt      time.Time         `json:"last_attempt"`
	LastSuccessful   bool              `json:"last_successful"`
	RecommendRetry   bool              `json:"recommend_retry"`
}

// GetRecommendedPeers returns peers recommended for connection attempts
func (ns *NetworkingState) GetRecommendedPeers() []PeerInfo {
	var recommended []PeerInfo
	
	for _, peer := range ns.KnownPeers {
		status, exists := ns.ConnectionMap[peer.NodeID]
		if !exists || status.RecommendRetry {
			recommended = append(recommended, peer)
		}
	}

	return recommended
}

// GetConnectionSuccessRate returns the overall connection success rate
func (ns *NetworkingState) GetConnectionSuccessRate() float64 {
	if len(ns.ConnectionMap) == 0 {
		return 0.0
	}

	successful := 0
	for _, status := range ns.ConnectionMap {
		if status.LastSuccessful {
			successful++
		}
	}

	return float64(successful) / float64(len(ns.ConnectionMap))
}