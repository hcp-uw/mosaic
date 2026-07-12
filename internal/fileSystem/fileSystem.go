package fileSystem

import (
	"fmt"
	"os"
)

// StartMount creates the ~/Mosaic directory if it doesn't exist.
// This stub-file approach replaces the previous FUSE mount.
func StartMount(mountPoint string) error {
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		return fmt.Errorf("could not create Mosaic directory: %w", err)
	}
	return nil
}

// StopMount is a no-op for the stub-file approach.
func StopMount(_ string) {}
