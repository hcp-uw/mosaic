package persistence

import (
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileStorageBackend implements StorageBackend using the local filesystem
type FileStorageBackend struct {
	baseDir string
	mu      sync.RWMutex
}

// NewFileStorageBackend creates a new file-based storage backend
func NewFileStorageBackend(baseDir string) (*FileStorageBackend, error) {
	// Ensure base directory exists
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, &PersistenceError{
			Op:   "create_base_dir",
			Path: baseDir,
			Err:  err,
		}
	}

	return &FileStorageBackend{
		baseDir: baseDir,
	}, nil
}

// Write writes data to the specified path
func (fs *FileStorageBackend) Write(path string, data []byte) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	fullPath := fs.fullPath(path)
	
	// Ensure parent directory exists
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return &PersistenceError{
			Op:   "create_parent_dir",
			Path: path,
			Err:  err,
		}
	}

	// Write the file
	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		return &PersistenceError{
			Op:   "write_file",
			Path: path,
			Err:  err,
		}
	}

	return nil
}

// WriteAtomic writes data atomically using a temporary file and rename
func (fs *FileStorageBackend) WriteAtomic(path string, data []byte) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	fullPath := fs.fullPath(path)
	
	// Ensure parent directory exists
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return &PersistenceError{
			Op:   "create_parent_dir",
			Path: path,
			Err:  err,
		}
	}

	// Create temporary file in the same directory
	tempPath, err := fs.createTempFile(dir)
	if err != nil {
		return &PersistenceError{
			Op:   "create_temp_file",
			Path: path,
			Err:  err,
		}
	}

	// Clean up temp file if something goes wrong
	defer func() {
		if err := os.Remove(tempPath); err != nil && !os.IsNotExist(err) {
			// Log warning but don't fail the operation
		}
	}()

	// Write to temporary file
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return &PersistenceError{
			Op:   "write_temp_file",
			Path: path,
			Err:  err,
		}
	}

	// Atomic rename
	if err := os.Rename(tempPath, fullPath); err != nil {
		return &PersistenceError{
			Op:   "atomic_rename",
			Path: path,
			Err:  err,
		}
	}

	return nil
}

// Read reads data from the specified path
func (fs *FileStorageBackend) Read(path string) ([]byte, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	fullPath := fs.fullPath(path)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, &PersistenceError{
			Op:   "read_file",
			Path: path,
			Err:  err,
		}
	}

	return data, nil
}

// Exists checks if a file exists at the specified path
func (fs *FileStorageBackend) Exists(path string) bool {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	fullPath := fs.fullPath(path)
	_, err := os.Stat(fullPath)
	return err == nil
}

// Delete removes the file at the specified path
func (fs *FileStorageBackend) Delete(path string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	fullPath := fs.fullPath(path)
	if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
		return &PersistenceError{
			Op:   "delete_file",
			Path: path,
			Err:  err,
		}
	}

	return nil
}

// CreateDir creates a directory at the specified path
func (fs *FileStorageBackend) CreateDir(path string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	fullPath := fs.fullPath(path)
	if err := os.MkdirAll(fullPath, 0755); err != nil {
		return &PersistenceError{
			Op:   "create_dir",
			Path: path,
			Err:  err,
		}
	}

	return nil
}

// ListFiles lists all files in the specified directory
func (fs *FileStorageBackend) ListFiles(dir string) ([]string, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	fullPath := fs.fullPath(dir)
	entries, err := os.ReadDir(fullPath)
	if err != nil {
		return nil, &PersistenceError{
			Op:   "list_dir",
			Path: dir,
			Err:  err,
		}
	}

	var files []string
	for _, entry := range entries {
		if !entry.IsDir() {
			files = append(files, entry.Name())
		}
	}

	return files, nil
}

// CreateBackup creates a backup copy of the source file
func (fs *FileStorageBackend) CreateBackup(sourcePath, backupPath string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	srcFullPath := fs.fullPath(sourcePath)
	backupFullPath := fs.fullPath(backupPath)

	// Ensure backup directory exists
	backupDir := filepath.Dir(backupFullPath)
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return &PersistenceError{
			Op:   "create_backup_dir",
			Path: backupPath,
			Err:  err,
		}
	}

	// Copy file
	if err := fs.copyFile(srcFullPath, backupFullPath); err != nil {
		return &PersistenceError{
			Op:   "backup_file",
			Path: sourcePath,
			Err:  err,
		}
	}

	return nil
}

// Close performs cleanup operations
func (fs *FileStorageBackend) Close() error {
	// File storage doesn't require special cleanup
	return nil
}

// Helper methods

// fullPath constructs the full filesystem path
func (fs *FileStorageBackend) fullPath(path string) string {
	return filepath.Join(fs.baseDir, filepath.Clean(path))
}

// createTempFile creates a temporary file in the specified directory
func (fs *FileStorageBackend) createTempFile(dir string) (string, error) {
	// Generate random suffix
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}

	tempName := fmt.Sprintf(".tmp-%x-%d", suffix, time.Now().UnixNano())
	return filepath.Join(dir, tempName), nil
}

// copyFile copies a file from source to destination
func (fs *FileStorageBackend) copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		return err
	}

	// Ensure data is written to disk
	return destFile.Sync()
}

// SafeFileWriter provides additional safety for file operations
type SafeFileWriter struct {
	backend StorageBackend
	validator Validator
}

// NewSafeFileWriter creates a new safe file writer with validation
func NewSafeFileWriter(backend StorageBackend, validator Validator) *SafeFileWriter {
	return &SafeFileWriter{
		backend:   backend,
		validator: validator,
	}
}

// WriteWithValidation writes data with validation and checksum
func (sfw *SafeFileWriter) WriteWithValidation(path string, data []byte) error {
	// Validate data before writing
	if sfw.validator != nil {
		if err := sfw.validator.Validate(data); err != nil {
			return fmt.Errorf("data validation failed: %w", err)
		}
	}

	// Write main data
	if err := sfw.backend.WriteAtomic(path, data); err != nil {
		return err
	}

	// Write checksum file if validator supports it
	if sfw.validator != nil {
		checksum := sfw.validator.CalculateChecksum(data)
		checksumPath := path + ".checksum"
		if err := sfw.backend.WriteAtomic(checksumPath, checksum); err != nil {
			// Clean up main file if checksum write fails
			sfw.backend.Delete(path)
			return fmt.Errorf("failed to write checksum: %w", err)
		}
	}

	return nil
}

// ReadWithValidation reads data and verifies its integrity
func (sfw *SafeFileWriter) ReadWithValidation(path string) ([]byte, error) {
	// Read main data
	data, err := sfw.backend.Read(path)
	if err != nil {
		return nil, err
	}

	// Verify checksum if available
	if sfw.validator != nil {
		checksumPath := path + ".checksum"
		if sfw.backend.Exists(checksumPath) {
			expectedChecksum, err := sfw.backend.Read(checksumPath)
			if err != nil {
				return nil, fmt.Errorf("failed to read checksum: %w", err)
			}

			if !sfw.validator.VerifyChecksum(data, expectedChecksum) {
				return nil, fmt.Errorf("checksum validation failed for %s", path)
			}
		}
	}

	// Final validation of data structure
	if sfw.validator != nil {
		if err := sfw.validator.Validate(data); err != nil {
			return nil, fmt.Errorf("data validation failed: %w", err)
		}
	}

	return data, nil
}

// FileRotator manages log rotation and cleanup
type FileRotator struct {
	maxFiles int
	maxSize  int64
	backend  StorageBackend
}

// NewFileRotator creates a new file rotator
func NewFileRotator(backend StorageBackend, maxFiles int, maxSize int64) *FileRotator {
	return &FileRotator{
		maxFiles: maxFiles,
		maxSize:  maxSize,
		backend:  backend,
	}
}

// RotateIfNeeded rotates files if they exceed size limits
func (fr *FileRotator) RotateIfNeeded(basePath string) error {
	if !fr.backend.Exists(basePath) {
		return nil
	}

	// Check file size
	data, err := fr.backend.Read(basePath)
	if err != nil {
		return err
	}

	if int64(len(data)) <= fr.maxSize {
		return nil // No rotation needed
	}

	// Rotate existing files
	for i := fr.maxFiles - 1; i >= 1; i-- {
		oldPath := fmt.Sprintf("%s.%d", basePath, i)
		newPath := fmt.Sprintf("%s.%d", basePath, i+1)
		
		if fr.backend.Exists(oldPath) {
			if i == fr.maxFiles-1 {
				// Delete the oldest file
				fr.backend.Delete(oldPath)
			} else {
				// Read and write to new location
				data, err := fr.backend.Read(oldPath)
				if err != nil {
					continue // Skip this rotation
				}
				fr.backend.WriteAtomic(newPath, data)
				fr.backend.Delete(oldPath)
			}
		}
	}

	// Rotate current file to .1
	rotatedPath := basePath + ".1"
	if err := fr.backend.CreateBackup(basePath, rotatedPath); err != nil {
		return err
	}

	// Clear current file
	return fr.backend.WriteAtomic(basePath, []byte{})
}