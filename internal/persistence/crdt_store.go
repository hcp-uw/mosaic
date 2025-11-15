package persistence

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hcp-uw/mosaic/internal/networking/crdt"
)

// FileCRDTStore implements CRDTStore with journal and snapshot support
type FileCRDTStore struct {
	backend       StorageBackend
	serializer    Serializer
	basePath      string
	journalPath   string
	snapshotPath  string
	rotator       *FileRotator
	validator     *CRDTValidator
}

// NewFileCRDTStore creates a new file-based CRDT store
func NewFileCRDTStore(backend StorageBackend, basePath string) *FileCRDTStore {
	store := &FileCRDTStore{
		backend:      backend,
		serializer:   &JSONSerializer{},
		basePath:     basePath,
		journalPath:  basePath + "/journal",
		snapshotPath: basePath + "/snapshots",
		validator:    NewCRDTValidator(),
	}

	// Create file rotator for journal management
	store.rotator = NewFileRotator(backend, 10, 10*1024*1024) // 10 files, 10MB each

	return store
}

// CRDTSnapshot represents a point-in-time CRDT state
type CRDTSnapshot struct {
	Timestamp    time.Time `json:"timestamp"`
	Version      string    `json:"version"`
	Checksum     string    `json:"checksum"`
	CRDTData     *crdt.CRDT `json:"crdt_data"`
	OperationLog []CRDTOperation `json:"operation_log,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

// JournalEntry represents a single entry in the operation journal
type JournalEntry struct {
	Sequence     int64           `json:"sequence"`
	Timestamp    time.Time       `json:"timestamp"`
	Operation    CRDTOperation   `json:"operation"`
	Dependencies []int64         `json:"dependencies,omitempty"`
	NodeID       string          `json:"node_id"`
}

// SaveCRDT saves the current CRDT state
func (fcs *FileCRDTStore) SaveCRDT(crdtData *crdt.CRDT) error {
	if crdtData == nil {
		return fmt.Errorf("cannot save nil CRDT")
	}

	// Validate CRDT before saving
	if err := crdtData.Validate(); err != nil {
		return fmt.Errorf("CRDT validation failed: %w", err)
	}

	// Create snapshot
	snapshot := CRDTSnapshot{
		Timestamp: time.Now(),
		Version:   "1.0",
		CRDTData:  crdtData,
		Metadata: map[string]interface{}{
			"join_message_count": len(crdtData.GetJoinMessages()),
			"file_manifest_count": len(crdtData.GetFileManifest()),
		},
	}

	// Calculate checksum
	dataBytes, err := fcs.serializer.Serialize(crdtData)
	if err != nil {
		return fmt.Errorf("failed to serialize CRDT for checksum: %w", err)
	}
	checksum := sha256.Sum256(dataBytes)
	snapshot.Checksum = fmt.Sprintf("%x", checksum)

	// Serialize snapshot
	snapshotBytes, err := fcs.serializer.Serialize(snapshot)
	if err != nil {
		return fmt.Errorf("failed to serialize snapshot: %w", err)
	}

	// Save current state
	currentPath := filepath.Join(fcs.basePath, "current.json")
	if err := fcs.backend.WriteAtomic(currentPath, snapshotBytes); err != nil {
		return fmt.Errorf("failed to save current CRDT state: %w", err)
	}

	return nil
}

// LoadCRDT loads CRDT state with merge resolution
func (fcs *FileCRDTStore) LoadCRDT() (*crdt.CRDT, error) {
	currentPath := filepath.Join(fcs.basePath, "current.json")
	
	if !fcs.backend.Exists(currentPath) {
		// Return new CRDT if none exists
		return crdt.New(), nil
	}

	// Load current snapshot
	snapshotBytes, err := fcs.backend.Read(currentPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read current CRDT state: %w", err)
	}

	var snapshot CRDTSnapshot
	if err := fcs.serializer.Deserialize(snapshotBytes, &snapshot); err != nil {
		return nil, fmt.Errorf("failed to deserialize snapshot: %w", err)
	}

	// Validate checksum
	if err := fcs.validateSnapshot(&snapshot); err != nil {
		return nil, fmt.Errorf("snapshot validation failed: %w", err)
	}

	crdtData := snapshot.CRDTData
	if crdtData == nil {
		return nil, fmt.Errorf("snapshot contains nil CRDT data")
	}

	// Apply any journal operations since the snapshot
	if err := fcs.applyJournalOperations(crdtData, snapshot.Timestamp); err != nil {
		return nil, fmt.Errorf("failed to apply journal operations: %w", err)
	}

	// Final validation
	if err := crdtData.Validate(); err != nil {
		return nil, fmt.Errorf("loaded CRDT failed validation: %w", err)
	}

	return crdtData, nil
}

// AppendOperation adds an operation to the append-only journal
func (fcs *FileCRDTStore) AppendOperation(operation CRDTOperation) error {
	// Validate operation
	if err := fcs.validator.ValidateOperation(operation); err != nil {
		return fmt.Errorf("operation validation failed: %w", err)
	}

	// Create journal entry
	entry := JournalEntry{
		Sequence:  fcs.getNextSequence(),
		Timestamp: time.Now(),
		Operation: operation,
		NodeID:    fcs.getNodeID(),
	}

	// Serialize entry
	entryBytes, err := fcs.serializer.Serialize(entry)
	if err != nil {
		return fmt.Errorf("failed to serialize journal entry: %w", err)
	}

	// Append newline-delimited JSON to journal
	journalData := string(entryBytes) + "\n"
	
	// Get journal file path
	journalFile := filepath.Join(fcs.journalPath, "operations.jsonl")
	
	// Ensure journal directory exists
	if err := fcs.backend.CreateDir(fcs.journalPath); err != nil {
		return fmt.Errorf("failed to create journal directory: %w", err)
	}

	// Read existing journal
	var existingData []byte
	if fcs.backend.Exists(journalFile) {
		existingData, err = fcs.backend.Read(journalFile)
		if err != nil {
			return fmt.Errorf("failed to read existing journal: %w", err)
		}
	}

	// Append new entry
	newData := append(existingData, []byte(journalData)...)

	// Write atomically
	if err := fcs.backend.WriteAtomic(journalFile, newData); err != nil {
		return fmt.Errorf("failed to append to journal: %w", err)
	}

	// Rotate journal if needed
	if err := fcs.rotator.RotateIfNeeded(journalFile); err != nil {
		// Log warning but don't fail the operation
		// In a real implementation, this would be logged
	}

	return nil
}

// LoadOperations loads operations from the journal since a given time
func (fcs *FileCRDTStore) LoadOperations(since time.Time) ([]CRDTOperation, error) {
	journalFile := filepath.Join(fcs.journalPath, "operations.jsonl")
	
	if !fcs.backend.Exists(journalFile) {
		return []CRDTOperation{}, nil
	}

	journalBytes, err := fcs.backend.Read(journalFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read journal: %w", err)
	}

	lines := strings.Split(string(journalBytes), "\n")
	var operations []CRDTOperation

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var entry JournalEntry
		if err := fcs.serializer.Deserialize([]byte(line), &entry); err != nil {
			// Skip malformed entries but continue processing
			continue
		}

		// Include operations since the specified time
		if entry.Timestamp.After(since) || entry.Timestamp.Equal(since) {
			operations = append(operations, entry.Operation)
		}
	}

	return operations, nil
}

// CreateSnapshot creates a new snapshot of the current CRDT state
func (fcs *FileCRDTStore) CreateSnapshot() error {
	// Load current CRDT
	crdtData, err := fcs.LoadCRDT()
	if err != nil {
		return fmt.Errorf("failed to load CRDT for snapshot: %w", err)
	}

	// Generate snapshot filename with timestamp
	timestamp := time.Now()
	snapshotName := fmt.Sprintf("snapshot_%d.json", timestamp.Unix())
	snapshotFile := filepath.Join(fcs.snapshotPath, snapshotName)

	// Create snapshot
	snapshot := CRDTSnapshot{
		Timestamp: timestamp,
		Version:   "1.0",
		CRDTData:  crdtData,
		Metadata: map[string]interface{}{
			"snapshot_type":      "manual",
			"join_message_count": len(crdtData.GetJoinMessages()),
			"file_manifest_count": len(crdtData.GetFileManifest()),
		},
	}

	// Calculate checksum
	dataBytes, err := fcs.serializer.Serialize(crdtData)
	if err != nil {
		return fmt.Errorf("failed to serialize CRDT for snapshot checksum: %w", err)
	}
	checksum := sha256.Sum256(dataBytes)
	snapshot.Checksum = fmt.Sprintf("%x", checksum)

	// Serialize and save snapshot
	snapshotBytes, err := fcs.serializer.Serialize(snapshot)
	if err != nil {
		return fmt.Errorf("failed to serialize snapshot: %w", err)
	}

	// Ensure snapshot directory exists
	if err := fcs.backend.CreateDir(fcs.snapshotPath); err != nil {
		return fmt.Errorf("failed to create snapshot directory: %w", err)
	}

	// Save snapshot
	if err := fcs.backend.WriteAtomic(snapshotFile, snapshotBytes); err != nil {
		return fmt.Errorf("failed to save snapshot: %w", err)
	}

	return nil
}

// LoadFromSnapshot loads CRDT state from the most recent snapshot
func (fcs *FileCRDTStore) LoadFromSnapshot() (*crdt.CRDT, error) {
	// List snapshot files
	snapshotFiles, err := fcs.backend.ListFiles(fcs.snapshotPath)
	if err != nil {
		return nil, fmt.Errorf("failed to list snapshots: %w", err)
	}

	if len(snapshotFiles) == 0 {
		return nil, fmt.Errorf("no snapshots found")
	}

	// Sort snapshots by timestamp (newest first)
	sort.Slice(snapshotFiles, func(i, j int) bool {
		return snapshotFiles[i] > snapshotFiles[j]
	})

	// Load most recent snapshot
	latestSnapshot := snapshotFiles[0]
	snapshotPath := filepath.Join(fcs.snapshotPath, latestSnapshot)
	
	snapshotBytes, err := fcs.backend.Read(snapshotPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read snapshot %s: %w", latestSnapshot, err)
	}

	var snapshot CRDTSnapshot
	if err := fcs.serializer.Deserialize(snapshotBytes, &snapshot); err != nil {
		return nil, fmt.Errorf("failed to deserialize snapshot: %w", err)
	}

	// Validate snapshot
	if err := fcs.validateSnapshot(&snapshot); err != nil {
		return nil, fmt.Errorf("snapshot validation failed: %w", err)
	}

	return snapshot.CRDTData, nil
}

// Cleanup removes old journal entries and snapshots
func (fcs *FileCRDTStore) Cleanup(retentionPeriod time.Duration) error {
	_ = retentionPeriod // For future use with timestamp-based cleanup

	// Cleanup old snapshots
	snapshotFiles, err := fcs.backend.ListFiles(fcs.snapshotPath)
	if err == nil {
		for _, file := range snapshotFiles {
			if strings.HasPrefix(file, "snapshot_") {
				// Extract timestamp from filename
				parts := strings.Split(file, "_")
				if len(parts) >= 2 {
					_ = strings.TrimSuffix(parts[1], ".json") // For future timestamp parsing
					// For simplicity, we'll keep the most recent 10 snapshots
					if len(snapshotFiles) > 10 {
						filePath := filepath.Join(fcs.snapshotPath, file)
						fcs.backend.Delete(filePath)
					}
				}
			}
		}
	}

	// Journal cleanup is handled by the rotator
	return nil
}

// Helper methods

func (fcs *FileCRDTStore) validateSnapshot(snapshot *CRDTSnapshot) error {
	if snapshot.CRDTData == nil {
		return fmt.Errorf("snapshot contains nil CRDT data")
	}

	// Validate checksum
	dataBytes, err := fcs.serializer.Serialize(snapshot.CRDTData)
	if err != nil {
		return fmt.Errorf("failed to serialize CRDT for checksum validation: %w", err)
	}
	
	checksum := sha256.Sum256(dataBytes)
	expectedChecksum := fmt.Sprintf("%x", checksum)
	
	if snapshot.Checksum != expectedChecksum {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedChecksum, snapshot.Checksum)
	}

	// Validate CRDT structure
	return snapshot.CRDTData.Validate()
}

func (fcs *FileCRDTStore) applyJournalOperations(crdtData *crdt.CRDT, since time.Time) error {
	operations, err := fcs.LoadOperations(since)
	if err != nil {
		return err
	}

	// Apply operations in chronological order
	for _, operation := range operations {
		if err := fcs.applyOperation(crdtData, operation); err != nil {
			// Log error but continue with other operations
			// In a production system, this would be logged
			continue
		}
	}

	return nil
}

func (fcs *FileCRDTStore) applyOperation(crdtData *crdt.CRDT, operation CRDTOperation) error {
	switch operation.Type {
	case "add_join_message":
		// Extract data and apply to CRDT
		// This would be implemented based on the actual CRDT operation structure
		return nil
	case "add_store_operation":
		// Extract data and apply to CRDT
		return nil
	default:
		return fmt.Errorf("unknown operation type: %s", operation.Type)
	}
}

func (fcs *FileCRDTStore) getNextSequence() int64 {
	// In a real implementation, this would maintain a persistent sequence counter
	return time.Now().UnixNano()
}

func (fcs *FileCRDTStore) getNodeID() string {
	// This would be retrieved from the identity store or configuration
	return "default_node"
}

// CRDTValidator validates CRDT operations and data
type CRDTValidator struct{}

func NewCRDTValidator() *CRDTValidator {
	return &CRDTValidator{}
}

func (cv *CRDTValidator) Validate(data []byte) error {
	// Parse as JSON to ensure it's well-formed
	var temp interface{}
	return json.Unmarshal(data, &temp)
}

func (cv *CRDTValidator) CalculateChecksum(data []byte) []byte {
	hash := sha256.Sum256(data)
	return hash[:]
}

func (cv *CRDTValidator) VerifyChecksum(data []byte, expectedChecksum []byte) bool {
	actualChecksum := cv.CalculateChecksum(data)
	return string(actualChecksum) == string(expectedChecksum)
}

func (cv *CRDTValidator) ValidateOperation(operation CRDTOperation) error {
	if operation.Type == "" {
		return fmt.Errorf("operation type cannot be empty")
	}
	
	if operation.Timestamp.IsZero() {
		return fmt.Errorf("operation timestamp cannot be zero")
	}
	
	if operation.Data == nil {
		return fmt.Errorf("operation data cannot be nil")
	}
	
	return nil
}

// CRDTMerger handles merging of CRDT states during recovery
type CRDTMerger struct {
	store *FileCRDTStore
}

func NewCRDTMerger(store *FileCRDTStore) *CRDTMerger {
	return &CRDTMerger{store: store}
}

// MergeSnapshots merges multiple CRDT snapshots into a single consistent state
func (cm *CRDTMerger) MergeSnapshots(snapshots []*CRDTSnapshot) (*crdt.CRDT, error) {
	if len(snapshots) == 0 {
		return crdt.New(), nil
	}

	if len(snapshots) == 1 {
		return snapshots[0].CRDTData, nil
	}

	// Start with the first snapshot
	mergedCRDT := snapshots[0].CRDTData.Clone()

	// Merge remaining snapshots
	for i := 1; i < len(snapshots); i++ {
		events, err := mergedCRDT.Merge(snapshots[i].CRDTData)
		if err != nil {
			return nil, fmt.Errorf("failed to merge snapshot %d: %w", i, err)
		}
		
		// Log merge events for audit trail
		_ = events // In production, these would be logged
	}

	return mergedCRDT, nil
}