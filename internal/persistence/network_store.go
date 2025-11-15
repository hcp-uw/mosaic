package persistence

import (
	"crypto/ed25519"
	"fmt"
	"path/filepath"
	"sort"
	"time"
)

// FileNetworkStore implements NetworkStore using file-based storage
type FileNetworkStore struct {
	backend       StorageBackend
	serializer    Serializer
	basePath      string
	peersPath     string
	trustedPath   string
	metricsPath   string
	configPath    string
}

// NewFileNetworkStore creates a new file-based network store
func NewFileNetworkStore(backend StorageBackend, basePath string) *FileNetworkStore {
	return &FileNetworkStore{
		backend:     backend,
		serializer:  &JSONSerializer{},
		basePath:    basePath,
		peersPath:   basePath + "/peers",
		trustedPath: basePath + "/trusted",
		metricsPath: basePath + "/metrics",
		configPath:  basePath + "/config",
	}
}

// PeerDatabase represents the complete peer information database
type PeerDatabase struct {
	Version      string              `json:"version"`
	LastUpdated  time.Time          `json:"last_updated"`
	Peers        map[string]PeerInfo `json:"peers"`
	TotalPeers   int                `json:"total_peers"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

// TrustedPeerDatabase represents the trusted peer certificate database
type TrustedPeerDatabase struct {
	Version         string                      `json:"version"`
	LastUpdated     time.Time                  `json:"last_updated"`
	Certificates    map[string]TrustedPeerCert `json:"certificates"`
	TotalCerts      int                        `json:"total_certificates"`
	ExpirySchedule  []CertExpiryInfo           `json:"expiry_schedule,omitempty"`
}

// CertExpiryInfo tracks certificate expiration
type CertExpiryInfo struct {
	NodeID    string    `json:"node_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// MetricsDatabase represents the connection metrics database
type MetricsDatabase struct {
	Version        string                        `json:"version"`
	LastUpdated    time.Time                    `json:"last_updated"`
	Metrics        map[string]ConnectionMetrics  `json:"metrics"`
	TotalEntries   int                          `json:"total_entries"`
	PerformanceStats map[string]interface{}     `json:"performance_stats,omitempty"`
}

// StorePeerInfo stores information about a discovered peer
func (fns *FileNetworkStore) StorePeerInfo(nodeID string, info PeerInfo) error {
	if nodeID == "" {
		return fmt.Errorf("node ID cannot be empty")
	}

	// Load existing database
	db, err := fns.loadPeerDatabase()
	if err != nil {
		return fmt.Errorf("failed to load peer database: %w", err)
	}

	// Update peer information
	info.LastSeen = time.Now()
	db.Peers[nodeID] = info
	db.TotalPeers = len(db.Peers)
	db.LastUpdated = time.Now()

	// Update metadata
	if db.Metadata == nil {
		db.Metadata = make(map[string]interface{})
	}
	db.Metadata["last_peer_update"] = nodeID
	db.Metadata["verified_peers"] = fns.countVerifiedPeers(db.Peers)

	// Save database
	return fns.savePeerDatabase(db)
}

// LoadPeerInfo loads information about a specific peer
func (fns *FileNetworkStore) LoadPeerInfo(nodeID string) (*PeerInfo, error) {
	if nodeID == "" {
		return nil, fmt.Errorf("node ID cannot be empty")
	}

	db, err := fns.loadPeerDatabase()
	if err != nil {
		return nil, fmt.Errorf("failed to load peer database: %w", err)
	}

	info, exists := db.Peers[nodeID]
	if !exists {
		return nil, fmt.Errorf("peer %s not found", nodeID)
	}

	return &info, nil
}

// ListKnownPeers returns all known peer information
func (fns *FileNetworkStore) ListKnownPeers() ([]PeerInfo, error) {
	db, err := fns.loadPeerDatabase()
	if err != nil {
		return nil, fmt.Errorf("failed to load peer database: %w", err)
	}

	peers := make([]PeerInfo, 0, len(db.Peers))
	for _, info := range db.Peers {
		peers = append(peers, info)
	}

	// Sort by last seen time (most recent first)
	sort.Slice(peers, func(i, j int) bool {
		return peers[i].LastSeen.After(peers[j].LastSeen)
	})

	return peers, nil
}

// StoreTrustedPeer stores a trusted peer certificate
func (fns *FileNetworkStore) StoreTrustedPeer(nodeID string, cert TrustedPeerCert) error {
	if nodeID == "" {
		return fmt.Errorf("node ID cannot be empty")
	}

	// Validate certificate
	if err := fns.validateCertificate(cert); err != nil {
		return fmt.Errorf("certificate validation failed: %w", err)
	}

	// Load existing database
	db, err := fns.loadTrustedDatabase()
	if err != nil {
		return fmt.Errorf("failed to load trusted peer database: %w", err)
	}

	// Store certificate
	db.Certificates[nodeID] = cert
	db.TotalCerts = len(db.Certificates)
	db.LastUpdated = time.Now()

	// Update expiry schedule
	db.ExpirySchedule = fns.buildExpirySchedule(db.Certificates)

	// Save database
	return fns.saveTrustedDatabase(db)
}

// LoadTrustedPeer loads a trusted peer certificate
func (fns *FileNetworkStore) LoadTrustedPeer(nodeID string) (*TrustedPeerCert, error) {
	if nodeID == "" {
		return nil, fmt.Errorf("node ID cannot be empty")
	}

	db, err := fns.loadTrustedDatabase()
	if err != nil {
		return nil, fmt.Errorf("failed to load trusted peer database: %w", err)
	}

	cert, exists := db.Certificates[nodeID]
	if !exists {
		return nil, fmt.Errorf("trusted peer %s not found", nodeID)
	}

	// Check if certificate has expired
	if time.Now().After(cert.ExpiresAt) {
		return nil, fmt.Errorf("certificate for peer %s has expired", nodeID)
	}

	return &cert, nil
}

// StoreConnectionMetrics stores connection quality metrics for a peer
func (fns *FileNetworkStore) StoreConnectionMetrics(nodeID string, metrics ConnectionMetrics) error {
	if nodeID == "" {
		return fmt.Errorf("node ID cannot be empty")
	}

	// Load existing database
	db, err := fns.loadMetricsDatabase()
	if err != nil {
		return fmt.Errorf("failed to load metrics database: %w", err)
	}

	// Store metrics
	metrics.NodeID = nodeID
	db.Metrics[nodeID] = metrics
	db.TotalEntries = len(db.Metrics)
	db.LastUpdated = time.Now()

	// Update performance statistics
	if db.PerformanceStats == nil {
		db.PerformanceStats = make(map[string]interface{})
	}
	fns.updatePerformanceStats(db.PerformanceStats, db.Metrics)

	// Save database
	return fns.saveMetricsDatabase(db)
}

// LoadConnectionMetrics loads connection metrics for a specific peer
func (fns *FileNetworkStore) LoadConnectionMetrics(nodeID string) (*ConnectionMetrics, error) {
	if nodeID == "" {
		return nil, fmt.Errorf("node ID cannot be empty")
	}

	db, err := fns.loadMetricsDatabase()
	if err != nil {
		return nil, fmt.Errorf("failed to load metrics database: %w", err)
	}

	metrics, exists := db.Metrics[nodeID]
	if !exists {
		return nil, fmt.Errorf("metrics for peer %s not found", nodeID)
	}

	return &metrics, nil
}

// StoreNetworkConfig stores network configuration
func (fns *FileNetworkStore) StoreNetworkConfig(config NetworkConfig) error {
	// Validate config
	if err := fns.validateNetworkConfig(config); err != nil {
		return fmt.Errorf("network config validation failed: %w", err)
	}

	configData := map[string]interface{}{
		"version":    "1.0",
		"updated_at": time.Now(),
		"config":     config,
	}

	data, err := fns.serializer.Serialize(configData)
	if err != nil {
		return fmt.Errorf("failed to serialize network config: %w", err)
	}

	configPath := filepath.Join(fns.configPath, "network.json")
	
	// Ensure config directory exists
	if err := fns.backend.CreateDir(fns.configPath); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	return fns.backend.WriteAtomic(configPath, data)
}

// LoadNetworkConfig loads network configuration
func (fns *FileNetworkStore) LoadNetworkConfig() (*NetworkConfig, error) {
	configPath := filepath.Join(fns.configPath, "network.json")
	
	if !fns.backend.Exists(configPath) {
		// Return default configuration
		return fns.getDefaultNetworkConfig(), nil
	}

	data, err := fns.backend.Read(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read network config: %w", err)
	}

	var configData map[string]interface{}
	if err := fns.serializer.Deserialize(data, &configData); err != nil {
		return nil, fmt.Errorf("failed to deserialize network config: %w", err)
	}

	configInterface, exists := configData["config"]
	if !exists {
		return nil, fmt.Errorf("config section not found")
	}

	// Convert interface{} back to NetworkConfig
	configBytes, err := fns.serializer.Serialize(configInterface)
	if err != nil {
		return nil, fmt.Errorf("failed to reserialize config: %w", err)
	}

	var config NetworkConfig
	if err := fns.serializer.Deserialize(configBytes, &config); err != nil {
		return nil, fmt.Errorf("failed to deserialize network config struct: %w", err)
	}

	return &config, nil
}

// Helper methods

func (fns *FileNetworkStore) loadPeerDatabase() (*PeerDatabase, error) {
	peersFile := filepath.Join(fns.peersPath, "known.json")
	
	if !fns.backend.Exists(peersFile) {
		// Return empty database
		return &PeerDatabase{
			Version:     "1.0",
			LastUpdated: time.Now(),
			Peers:       make(map[string]PeerInfo),
			TotalPeers:  0,
		}, nil
	}

	data, err := fns.backend.Read(peersFile)
	if err != nil {
		return nil, err
	}

	var db PeerDatabase
	if err := fns.serializer.Deserialize(data, &db); err != nil {
		return nil, err
	}

	// Initialize map if nil
	if db.Peers == nil {
		db.Peers = make(map[string]PeerInfo)
	}

	return &db, nil
}

func (fns *FileNetworkStore) savePeerDatabase(db *PeerDatabase) error {
	// Ensure peers directory exists
	if err := fns.backend.CreateDir(fns.peersPath); err != nil {
		return err
	}

	data, err := fns.serializer.Serialize(db)
	if err != nil {
		return err
	}

	peersFile := filepath.Join(fns.peersPath, "known.json")
	return fns.backend.WriteAtomic(peersFile, data)
}

func (fns *FileNetworkStore) loadTrustedDatabase() (*TrustedPeerDatabase, error) {
	trustedFile := filepath.Join(fns.trustedPath, "trusted.json")
	
	if !fns.backend.Exists(trustedFile) {
		// Return empty database
		return &TrustedPeerDatabase{
			Version:        "1.0",
			LastUpdated:    time.Now(),
			Certificates:   make(map[string]TrustedPeerCert),
			TotalCerts:     0,
			ExpirySchedule: []CertExpiryInfo{},
		}, nil
	}

	data, err := fns.backend.Read(trustedFile)
	if err != nil {
		return nil, err
	}

	var db TrustedPeerDatabase
	if err := fns.serializer.Deserialize(data, &db); err != nil {
		return nil, err
	}

	// Initialize map if nil
	if db.Certificates == nil {
		db.Certificates = make(map[string]TrustedPeerCert)
	}

	return &db, nil
}

func (fns *FileNetworkStore) saveTrustedDatabase(db *TrustedPeerDatabase) error {
	// Ensure trusted directory exists
	if err := fns.backend.CreateDir(fns.trustedPath); err != nil {
		return err
	}

	data, err := fns.serializer.Serialize(db)
	if err != nil {
		return err
	}

	trustedFile := filepath.Join(fns.trustedPath, "trusted.json")
	return fns.backend.WriteAtomic(trustedFile, data)
}

func (fns *FileNetworkStore) loadMetricsDatabase() (*MetricsDatabase, error) {
	metricsFile := filepath.Join(fns.metricsPath, "metrics.json")
	
	if !fns.backend.Exists(metricsFile) {
		// Return empty database
		return &MetricsDatabase{
			Version:          "1.0",
			LastUpdated:      time.Now(),
			Metrics:          make(map[string]ConnectionMetrics),
			TotalEntries:     0,
			PerformanceStats: make(map[string]interface{}),
		}, nil
	}

	data, err := fns.backend.Read(metricsFile)
	if err != nil {
		return nil, err
	}

	var db MetricsDatabase
	if err := fns.serializer.Deserialize(data, &db); err != nil {
		return nil, err
	}

	// Initialize maps if nil
	if db.Metrics == nil {
		db.Metrics = make(map[string]ConnectionMetrics)
	}
	if db.PerformanceStats == nil {
		db.PerformanceStats = make(map[string]interface{})
	}

	return &db, nil
}

func (fns *FileNetworkStore) saveMetricsDatabase(db *MetricsDatabase) error {
	// Ensure metrics directory exists
	if err := fns.backend.CreateDir(fns.metricsPath); err != nil {
		return err
	}

	data, err := fns.serializer.Serialize(db)
	if err != nil {
		return err
	}

	metricsFile := filepath.Join(fns.metricsPath, "metrics.json")
	return fns.backend.WriteAtomic(metricsFile, data)
}

func (fns *FileNetworkStore) countVerifiedPeers(peers map[string]PeerInfo) int {
	count := 0
	for _, peer := range peers {
		if peer.Verified {
			count++
		}
	}
	return count
}

func (fns *FileNetworkStore) validateCertificate(cert TrustedPeerCert) error {
	if cert.NodeID == "" {
		return fmt.Errorf("certificate node ID cannot be empty")
	}
	
	if len(cert.PublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid public key size: expected %d, got %d", ed25519.PublicKeySize, len(cert.PublicKey))
	}
	
	if cert.ExpiresAt.Before(time.Now()) {
		return fmt.Errorf("certificate has already expired")
	}
	
	if cert.IssuedAt.After(cert.ExpiresAt) {
		return fmt.Errorf("certificate issued date is after expiry date")
	}
	
	return nil
}

func (fns *FileNetworkStore) buildExpirySchedule(certs map[string]TrustedPeerCert) []CertExpiryInfo {
	var schedule []CertExpiryInfo
	
	for nodeID, cert := range certs {
		schedule = append(schedule, CertExpiryInfo{
			NodeID:    nodeID,
			ExpiresAt: cert.ExpiresAt,
		})
	}
	
	// Sort by expiry date
	sort.Slice(schedule, func(i, j int) bool {
		return schedule[i].ExpiresAt.Before(schedule[j].ExpiresAt)
	})
	
	return schedule
}

func (fns *FileNetworkStore) updatePerformanceStats(stats map[string]interface{}, metrics map[string]ConnectionMetrics) {
	if len(metrics) == 0 {
		return
	}

	var totalSuccessful, totalFailed int
	var totalDataSent, totalDataReceived int64
	var totalResponseTime time.Duration
	var responseTimeCount int

	for _, metric := range metrics {
		totalSuccessful += metric.SuccessfulConns
		totalFailed += metric.FailedConns
		totalDataSent += metric.TotalDataSent
		totalDataReceived += metric.TotalDataReceived
		
		if metric.AvgResponseTime > 0 {
			totalResponseTime += metric.AvgResponseTime
			responseTimeCount++
		}
	}

	stats["total_successful_connections"] = totalSuccessful
	stats["total_failed_connections"] = totalFailed
	stats["total_data_sent"] = totalDataSent
	stats["total_data_received"] = totalDataReceived
	
	if responseTimeCount > 0 {
		stats["avg_response_time_ms"] = totalResponseTime.Milliseconds() / int64(responseTimeCount)
	}
	
	if totalSuccessful+totalFailed > 0 {
		stats["success_rate"] = float64(totalSuccessful) / float64(totalSuccessful+totalFailed)
	}

	stats["last_updated"] = time.Now()
}

func (fns *FileNetworkStore) validateNetworkConfig(config NetworkConfig) error {
	if config.NodeID == "" {
		return fmt.Errorf("node ID cannot be empty")
	}
	
	if len(config.ListenAddresses) == 0 {
		return fmt.Errorf("at least one listen address is required")
	}
	
	if config.MaxPeers <= 0 {
		return fmt.Errorf("max peers must be greater than 0")
	}
	
	if config.ConnectionTimeout <= 0 {
		return fmt.Errorf("connection timeout must be greater than 0")
	}
	
	return nil
}

func (fns *FileNetworkStore) getDefaultNetworkConfig() *NetworkConfig {
	return &NetworkConfig{
		NodeID:           "default-node",
		ListenAddresses:  []string{"127.0.0.1:9090"},
		BootstrapPeers:   []string{},
		MaxPeers:         50,
		ConnectionTimeout: 30 * time.Second,
		RetryIntervals:   []time.Duration{
			1 * time.Second,
			5 * time.Second,
			15 * time.Second,
			30 * time.Second,
		},
	}
}

// NetworkStoreManager provides high-level operations for network state
type NetworkStoreManager struct {
	store NetworkStore
}

// NewNetworkStoreManager creates a new network store manager
func NewNetworkStoreManager(store NetworkStore) *NetworkStoreManager {
	return &NetworkStoreManager{store: store}
}

// CleanupExpiredCertificates removes expired certificates
func (nsm *NetworkStoreManager) CleanupExpiredCertificates() error {
	// This would be implemented to find and remove expired certificates
	// For brevity, we'll leave this as a placeholder
	return nil
}

// GetTopPeers returns the best-performing peers based on metrics
func (nsm *NetworkStoreManager) GetTopPeers(limit int) ([]PeerInfo, error) {
	peers, err := nsm.store.ListKnownPeers()
	if err != nil {
		return nil, err
	}

	// Filter verified peers only
	var verifiedPeers []PeerInfo
	for _, peer := range peers {
		if peer.Verified {
			verifiedPeers = append(verifiedPeers, peer)
		}
	}

	// Sort by connection quality (this would use actual metrics)
	sort.Slice(verifiedPeers, func(i, j int) bool {
		// For now, just sort by last seen time
		return verifiedPeers[i].LastSeen.After(verifiedPeers[j].LastSeen)
	})

	if limit > 0 && len(verifiedPeers) > limit {
		verifiedPeers = verifiedPeers[:limit]
	}

	return verifiedPeers, nil
}

// UpdatePeerQuality updates peer quality metrics based on connection results
func (nsm *NetworkStoreManager) UpdatePeerQuality(nodeID string, successful bool, responseTime time.Duration, dataSent, dataReceived int64) error {
	// Load existing metrics or create new ones
	metrics, err := nsm.store.LoadConnectionMetrics(nodeID)
	if err != nil {
		// Create new metrics if not found
		metrics = &ConnectionMetrics{
			NodeID: nodeID,
		}
	}

	// Update metrics
	if successful {
		metrics.SuccessfulConns++
		metrics.LastSuccess = time.Now()
		
		// Update average response time
		if metrics.AvgResponseTime == 0 {
			metrics.AvgResponseTime = responseTime
		} else {
			// Simple moving average
			metrics.AvgResponseTime = (metrics.AvgResponseTime + responseTime) / 2
		}
	} else {
		metrics.FailedConns++
		metrics.LastFailure = time.Now()
	}

	metrics.TotalDataSent += dataSent
	metrics.TotalDataReceived += dataReceived

	// Store updated metrics
	return nsm.store.StoreConnectionMetrics(nodeID, *metrics)
}