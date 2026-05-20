package handlers

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/hcp-uw/mosaic/internal/transfer"
)

// OpKind identifies the type of long-running network operation.
type OpKind string

const (
	OpUpload       OpKind = "upload"
	OpDownload     OpKind = "download"
	OpRedistribute OpKind = "redistribute"
)

// ActiveOp describes the current in-flight operation.
type ActiveOp struct {
	Kind        OpKind `json:"kind"`
	Description string `json:"description"`
	StartedAt   time.Time `json:"startedAt"`
}

var (
	activeOpMu      sync.Mutex
	activeOp        *ActiveOp
	cancelRequested atomic.Bool
)

// TryAcquireOp sets the global op if idle and returns true.
// Returns false (without changing state) if an op is already running.
func TryAcquireOp(kind OpKind, description string) bool {
	activeOpMu.Lock()
	defer activeOpMu.Unlock()
	if activeOp != nil {
		return false
	}
	cancelRequested.Store(false)
	transfer.ResetCancelFlags()
	activeOp = &ActiveOp{Kind: kind, Description: description, StartedAt: time.Now()}
	return true
}

// CancelActiveOp signals the running operation to stop at its next checkpoint.
func CancelActiveOp() {
	cancelRequested.Store(true)
	transfer.CancelUpload()
	transfer.CancelDownload()
}

// IsOpCancelRequested returns true if the active operation has been cancelled.
func IsOpCancelRequested() bool {
	return cancelRequested.Load()
}

// GetActiveOp returns a copy of the current op, or nil when idle.
func GetActiveOp() *ActiveOp {
	activeOpMu.Lock()
	defer activeOpMu.Unlock()
	if activeOp == nil {
		return nil
	}
	cp := *activeOp
	return &cp
}

// ReleaseOp clears the global op, allowing the next operation to proceed.
func ReleaseOp() {
	activeOpMu.Lock()
	activeOp = nil
	activeOpMu.Unlock()
}
