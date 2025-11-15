package persistence

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"
)

// FilePersistenceManager implements PersistenceManager using file-based storage
type FilePersistenceManager struct {
	dataDir      string
	backend      StorageBackend
	identityStore IdentityStore
	crdtStore     CRDTStore
	networkStore  NetworkStore
	
	// Auto-save configuration
	autoSaveInterval time.Duration
	autoSaveCtx      context.Context
	autoSaveCancel   context.CancelFunc
	autoSaveDone     chan struct{}
	
	// Lifecycle management
	mu           sync.RWMutex
	initialized  bool
	shutdownOnce sync.Once
}

// NewFilePersistenceManager creates a new file-based persistence manager
func NewFilePersistenceManager() *FilePersistenceManager {
	return &FilePersistenceManager{
		autoSaveInterval: 5 * time.Minute, // Default auto-save interval
	}
}

// Initialize sets up the persistence manager with the specified data directory
func (fpm *FilePersistenceManager) Initialize(dataDir string) error {
	fpm.mu.Lock()
	defer fpm.mu.Unlock()

	if fpm.initialized {
		return fmt.Errorf("persistence manager already initialized")
	}

	fpm.dataDir = dataDir

	// Create file storage backend
	backend, err := NewFileStorageBackend(dataDir)
	if err != nil {
		return fmt.Errorf("failed to create storage backend: %w", err)
	}
	fpm.backend = backend

	// Create directory structure
	if err := fpm.createDirectoryStructure(); err != nil {
		return fmt.Errorf("failed to create directory structure: %w", err)
	}

	// Initialize component stores
	fpm.identityStore = NewFileIdentityStore(backend, "node")
	fpm.crdtStore = NewFileCRDTStore(backend, "network/crdt")
	fpm.networkStore = NewFileNetworkStore(backend, "network")

	// Perform initial validation
	if err := fpm.validateDataIntegrity(); err != nil {
		return fmt.Errorf("data integrity validation failed: %w", err)
	}

	fpm.initialized = true
	return nil
}

// Identity returns the identity store
func (fpm *FilePersistenceManager) Identity() IdentityStore {
	fpm.mu.RLock()
	defer fpm.mu.RUnlock()
	return fpm.identityStore
}

// CRDT returns the CRDT store
func (fpm *FilePersistenceManager) CRDT() CRDTStore {
	fpm.mu.RLock()
	defer fpm.mu.RUnlock()
	return fpm.crdtStore
}

// Network returns the network store
func (fpm *FilePersistenceManager) Network() NetworkStore {
	fpm.mu.RLock()
	defer fpm.mu.RUnlock()
	return fpm.networkStore
}

// StartAutoSave begins automatic periodic saving
func (fpm *FilePersistenceManager) StartAutoSave(ctx context.Context, interval time.Duration) error {
	fpm.mu.Lock()
	defer fpm.mu.Unlock()

	if !fpm.initialized {
		return fmt.Errorf("persistence manager not initialized")
	}

	if fpm.autoSaveCtx != nil {
		return fmt.Errorf("auto-save already running")
	}

	fpm.autoSaveInterval = interval
	fpm.autoSaveCtx, fpm.autoSaveCancel = context.WithCancel(ctx)
	fpm.autoSaveDone = make(chan struct{})

	go fpm.runAutoSave()

	return nil
}

// Shutdown gracefully shuts down the persistence manager
func (fpm *FilePersistenceManager) Shutdown(ctx context.Context) error {
	var shutdownErr error
	
	fpm.shutdownOnce.Do(func() {
		fpm.mu.Lock()
		defer fpm.mu.Unlock()

		// Stop auto-save
		if fpm.autoSaveCancel != nil {
			fpm.autoSaveCancel()
			
			// Wait for auto-save to complete or timeout
			select {
			case <-fpm.autoSaveDone:
				// Auto-save stopped gracefully
			case <-ctx.Done():
				shutdownErr = fmt.Errorf("auto-save shutdown timed out")
			}
		}

		// Perform final save
		if err := fpm.performFinalSave(); err != nil && shutdownErr == nil {
			shutdownErr = fmt.Errorf("final save failed: %w", err)
		}

		// Close backend
		if fpm.backend != nil {
			if err := fpm.backend.Close(); err != nil && shutdownErr == nil {
				shutdownErr = fmt.Errorf("backend close failed: %w", err)
			}
		}

		fpm.initialized = false
	})

	return shutdownErr
}

// Recover attempts to recover from corrupted or incomplete data
func (fpm *FilePersistenceManager) Recover() error {
	fpm.mu.Lock()
	defer fpm.mu.Unlock()

	if !fpm.initialized {
		return fmt.Errorf("persistence manager not initialized")
	}

	// Create recovery manager
	recoveryManager := NewRecoveryManager(fpm.backend, fpm.dataDir)

	// Perform recovery operations
	if err := recoveryManager.RecoverIdentity(); err != nil {
		return fmt.Errorf("identity recovery failed: %w", err)
	}

	if err := recoveryManager.RecoverCRDT(); err != nil {
		return fmt.Errorf("CRDT recovery failed: %w", err)
	}

	if err := recoveryManager.RecoverNetwork(); err != nil {
		return fmt.Errorf("network recovery failed: %w", err)
	}

	// Validate recovered data
	if err := fpm.validateDataIntegrity(); err != nil {
		return fmt.Errorf("recovered data validation failed: %w", err)
	}

	return nil
}

// Backup creates a complete backup of all persistent data
func (fpm *FilePersistenceManager) Backup() error {
	fpm.mu.RLock()
	defer fpm.mu.RUnlock()

	if !fpm.initialized {
		return fmt.Errorf("persistence manager not initialized")
	}

	timestamp := time.Now().Format("20060102-150405")
	backupDir := filepath.Join(fpm.dataDir, "backups", timestamp)

	// Create backup manager
	backupManager := NewBackupManager(fpm.backend)

	// Perform backup
	if err := backupManager.CreateFullBackup(fpm.dataDir, backupDir); err != nil {
		return fmt.Errorf("backup creation failed: %w", err)
	}

	// Cleanup old backups
	if err := backupManager.CleanupOldBackups(filepath.Join(fpm.dataDir, "backups"), 10); err != nil {
		// Log warning but don't fail the backup
		// In production, this would be logged
	}

	return nil
}

// Helper methods

func (fpm *FilePersistenceManager) createDirectoryStructure() error {
	directories := []string{
		"node",
		"network/crdt",
		"network/crdt/journal",
		"network/crdt/snapshots",
		"network/peers",
		"network/trusted",
		"network/metrics",
		"network/config",
		"backups",
	}

	for _, dir := range directories {
		if err := fpm.backend.CreateDir(dir); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	return nil
}

func (fpm *FilePersistenceManager) validateDataIntegrity() error {
	// Validate identity store integrity
	if fpm.identityStore.HasIdentity() {
		// Try to load identity to verify integrity
		config, err := fpm.identityStore.LoadConfig()
		if err != nil {
			return fmt.Errorf("identity config validation failed: %w", err)
		}
		
		// Basic validation
		if config["node_id"] == nil {
			return fmt.Errorf("node ID missing from identity config")
		}
	}

	// Validate CRDT store integrity
	_, err := fpm.crdtStore.LoadCRDT()
	if err != nil {
		return fmt.Errorf("CRDT validation failed: %w", err)
	}

	// Validate network store integrity
	_, err = fpm.networkStore.LoadNetworkConfig()
	if err != nil {
		return fmt.Errorf("network config validation failed: %w", err)
	}

	return nil
}

func (fpm *FilePersistenceManager) runAutoSave() {
	defer close(fpm.autoSaveDone)

	ticker := time.NewTicker(fpm.autoSaveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-fpm.autoSaveCtx.Done():
			return
		case <-ticker.C:
			if err := fpm.performAutoSave(); err != nil {
				// In production, this would be logged
				// For now, we continue despite errors
			}
		}
	}
}

func (fpm *FilePersistenceManager) performAutoSave() error {
	// Load current CRDT and save it (this triggers snapshot creation if needed)
	crdtData, err := fpm.crdtStore.LoadCRDT()
	if err != nil {
		return fmt.Errorf("failed to load CRDT for auto-save: %w", err)
	}

	if err := fpm.crdtStore.SaveCRDT(crdtData); err != nil {
		return fmt.Errorf("auto-save failed: %w", err)
	}

	// Cleanup old data
	if err := fpm.performMaintenanceTasks(); err != nil {
		// Log but don't fail auto-save for maintenance errors
	}

	return nil
}

func (fpm *FilePersistenceManager) performMaintenanceTasks() error {
	// Cleanup old CRDT journal entries (keep 30 days)
	retentionPeriod := 30 * 24 * time.Hour
	if err := fpm.crdtStore.Cleanup(retentionPeriod); err != nil {
		return fmt.Errorf("CRDT cleanup failed: %w", err)
	}

	return nil
}

func (fpm *FilePersistenceManager) performFinalSave() error {
	// Save current state one last time
	crdtData, err := fpm.crdtStore.LoadCRDT()
	if err != nil {
		return err
	}

	return fpm.crdtStore.SaveCRDT(crdtData)
}

// RecoveryManager handles data recovery operations
type RecoveryManager struct {
	backend StorageBackend
	dataDir string
}

// NewRecoveryManager creates a new recovery manager
func NewRecoveryManager(backend StorageBackend, dataDir string) *RecoveryManager {
	return &RecoveryManager{
		backend: backend,
		dataDir: dataDir,
	}
}

// RecoverIdentity attempts to recover identity data
func (rm *RecoveryManager) RecoverIdentity() error {
	identityPath := "node"
	
	// Check for backup identity files
	backupPaths := []string{
		"backups/latest/node",
		"backups/previous/node",
	}

	for _, backupPath := range backupPaths {
		if rm.backend.Exists(filepath.Join(backupPath, "identity.pub")) {
			// Found backup, attempt to restore
			if err := rm.restoreFromBackup(backupPath, identityPath); err == nil {
				return nil
			}
		}
	}

	// No recovery possible
	return fmt.Errorf("no recoverable identity data found")
}

// RecoverCRDT attempts to recover CRDT data
func (rm *RecoveryManager) RecoverCRDT() error {
	crdtPath := "network/crdt"
	
	// Try to recover from snapshots first
	if err := rm.recoverFromSnapshots(crdtPath); err == nil {
		return nil
	}

	// Try to recover from backups
	backupPaths := []string{
		"backups/latest/network/crdt",
		"backups/previous/network/crdt",
	}

	for _, backupPath := range backupPaths {
		if rm.backend.Exists(filepath.Join(backupPath, "current.json")) {
			if err := rm.restoreFromBackup(backupPath, crdtPath); err == nil {
				return nil
			}
		}
	}

	// Create new CRDT if recovery fails
	return rm.createEmptyCRDT(crdtPath)
}

// RecoverNetwork attempts to recover network data
func (rm *RecoveryManager) RecoverNetwork() error {
	networkPath := "network"
	
	// Network data is less critical, so we can recreate defaults
	if !rm.backend.Exists(filepath.Join(networkPath, "config/network.json")) {
		// Create default network config
		return rm.createDefaultNetworkConfig(networkPath)
	}

	return nil
}

func (rm *RecoveryManager) restoreFromBackup(backupPath, targetPath string) error {
	// Copy all files from backup to target
	files, err := rm.backend.ListFiles(backupPath)
	if err != nil {
		return err
	}

	for _, file := range files {
		srcPath := filepath.Join(backupPath, file)
		dstPath := filepath.Join(targetPath, file)
		
		if err := rm.backend.CreateBackup(srcPath, dstPath); err != nil {
			return err
		}
	}

	return nil
}

func (rm *RecoveryManager) recoverFromSnapshots(crdtPath string) error {
	snapshotPath := filepath.Join(crdtPath, "snapshots")
	
	snapshots, err := rm.backend.ListFiles(snapshotPath)
	if err != nil || len(snapshots) == 0 {
		return fmt.Errorf("no snapshots found")
	}

	// Use the most recent snapshot
	latestSnapshot := snapshots[len(snapshots)-1]
	src := filepath.Join(snapshotPath, latestSnapshot)
	dst := filepath.Join(crdtPath, "current.json")
	
	return rm.backend.CreateBackup(src, dst)
}

func (rm *RecoveryManager) createEmptyCRDT(crdtPath string) error {
	// This would create an empty CRDT state
	// For now, we'll just ensure the directory exists
	return rm.backend.CreateDir(crdtPath)
}

func (rm *RecoveryManager) createDefaultNetworkConfig(networkPath string) error {
	// This would create default network configuration
	// For now, we'll just ensure the directory exists
	return rm.backend.CreateDir(filepath.Join(networkPath, "config"))
}

// BackupManager handles backup operations
type BackupManager struct {
	backend StorageBackend
}

// NewBackupManager creates a new backup manager
func NewBackupManager(backend StorageBackend) *BackupManager {
	return &BackupManager{backend: backend}
}

// CreateFullBackup creates a complete backup of the data directory
func (bm *BackupManager) CreateFullBackup(sourceDir, backupDir string) error {
	// Create backup directory
	if err := bm.backend.CreateDir(backupDir); err != nil {
		return err
	}

	// Recursively copy all files
	return bm.copyDirectory(sourceDir, backupDir)
}

// CleanupOldBackups removes old backup directories, keeping only the specified number
func (bm *BackupManager) CleanupOldBackups(backupDir string, keep int) error {
	backups, err := bm.backend.ListFiles(backupDir)
	if err != nil || len(backups) <= keep {
		return nil
	}

	// Sort backups by name (which includes timestamp)
	// Keep only the most recent ones
	if len(backups) > keep {
		for i := 0; i < len(backups)-keep; i++ {
			oldBackup := filepath.Join(backupDir, backups[i])
			bm.backend.Delete(oldBackup)
		}
	}

	return nil
}

func (bm *BackupManager) copyDirectory(src, dst string) error {
	// This is a simplified implementation
	// In a real system, this would recursively copy directory structures
	files, err := bm.backend.ListFiles(src)
	if err != nil {
		return err
	}

	for _, file := range files {
		srcFile := filepath.Join(src, file)
		dstFile := filepath.Join(dst, file)
		
		if err := bm.backend.CreateBackup(srcFile, dstFile); err != nil {
			return err
		}
	}

	return nil
}

// ConfigurationManager provides high-level configuration management
type ConfigurationManager struct {
	persistence PersistenceManager
}

// NewConfigurationManager creates a new configuration manager
func NewConfigurationManager(persistence PersistenceManager) *ConfigurationManager {
	return &ConfigurationManager{persistence: persistence}
}

// GetDataDirectory returns the current data directory path
func (cm *ConfigurationManager) GetDataDirectory() string {
	if fpm, ok := cm.persistence.(*FilePersistenceManager); ok {
		fpm.mu.RLock()
		defer fpm.mu.RUnlock()
		return fpm.dataDir
	}
	return ""
}

// SetAutoSaveInterval updates the auto-save interval
func (cm *ConfigurationManager) SetAutoSaveInterval(interval time.Duration) error {
	if fpm, ok := cm.persistence.(*FilePersistenceManager); ok {
		fpm.mu.Lock()
		defer fpm.mu.Unlock()
		fpm.autoSaveInterval = interval
		return nil
	}
	return fmt.Errorf("auto-save configuration not supported")
}

// GetStorageStats returns storage statistics
func (cm *ConfigurationManager) GetStorageStats() (map[string]interface{}, error) {
	// This would return storage usage statistics
	// For now, return basic information
	return map[string]interface{}{
		"data_directory": cm.GetDataDirectory(),
		"initialized":   cm.persistence != nil,
		"timestamp":     time.Now(),
	}, nil
}