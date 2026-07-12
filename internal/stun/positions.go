package stun

import (
	"encoding/json"
	"os"
	"sync"
)

// positionStore persists the mapping from a client's stable node ID to the queue
// position it was first assigned. Restoring these on startup fixes the
// restart-race: after the STUN server restarts, a returning node reclaims its
// original position instead of getting a fresh one based on who reconnects first.
//
// If path is empty the store is in-memory only (no persistence) — used by tests
// and ephemeral deployments.
type positionStore struct {
	mu        sync.Mutex
	path      string
	positions map[string]int // nodeID → queuePosition
}

// loadPositionStore reads the persisted positions from path (if it exists) and
// returns a ready store. A missing or unreadable file yields an empty store —
// persistence is best-effort and never blocks the server from starting.
func loadPositionStore(path string) *positionStore {
	s := &positionStore{path: path, positions: make(map[string]int)}
	if path == "" {
		return s
	}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &s.positions) // corrupt file → start empty
	}
	return s
}

// get returns the stored position for nodeID.
func (s *positionStore) get(nodeID string) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pos, ok := s.positions[nodeID]
	return pos, ok
}

// put records nodeID → pos and flushes to disk (best-effort).
func (s *positionStore) put(nodeID string, pos int) {
	s.mu.Lock()
	s.positions[nodeID] = pos
	path := s.path
	var data []byte
	if path != "" {
		data, _ = json.Marshal(s.positions)
	}
	s.mu.Unlock()

	if path != "" && data != nil {
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, data, 0600); err == nil {
			_ = os.Rename(tmp, path)
		}
	}
}

// maxPosition returns the highest stored position, or 0 if the store is empty.
// The server seeds its queue counter with this so newly-assigned positions never
// collide with a restored one.
func (s *positionStore) maxPosition() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	maxPos := 0
	for _, p := range s.positions {
		if p > maxPos {
			maxPos = p
		}
	}
	return maxPos
}
