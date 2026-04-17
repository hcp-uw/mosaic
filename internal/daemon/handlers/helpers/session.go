package helpers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const sessionFilename = ".mosaic-session"
const nodeFilename = ".mosaic-node"

// Session holds the identity fields returned by the auth server after a
// successful login. Written to ~/.mosaic-session (0600) by the login handler
// and read by GetAccountID, GetUsername, and GetNodeID.
type Session struct {
	AccountID  int    `json:"accountID"`
	Username   string `json:"username"`
	NodeNumber int    `json:"nodeNumber"` // 1, 2, 3... shown as "node-1" etc.
	PublicKey  string `json:"publicKey"`
	ExpiresAt  string `json:"expiresAt"` // RFC3339
}

func sessionPath() string {
	return filepath.Join(os.Getenv("HOME"), sessionFilename)
}

func nodePath() string {
	return filepath.Join(os.Getenv("HOME"), nodeFilename)
}

// SaveNodeNumber writes the node number to ~/.mosaic-node (0600).
// This file survives logout so the same machine always gets the same node number.
func SaveNodeNumber(nodeNumber int) error {
	return os.WriteFile(nodePath(), []byte(fmt.Sprintf("%d", nodeNumber)), 0600)
}

// LoadNodeNumber reads the persisted node number for this machine.
// Returns 0 if no node number has been assigned yet (first ever login).
func LoadNodeNumber() int {
	data, err := os.ReadFile(nodePath())
	if err != nil {
		return 0
	}
	var n int
	fmt.Sscanf(string(data), "%d", &n)
	return n
}

// SaveSession writes the session to disk (0600).
func SaveSession(s Session) error {
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("could not marshal session: %w", err)
	}
	return os.WriteFile(sessionPath(), data, 0600)
}

// LoadSession reads and returns the current session.
// Returns an error if no session exists or if it has expired.
func LoadSession() (Session, error) {
	data, err := os.ReadFile(sessionPath())
	if os.IsNotExist(err) {
		return Session{}, fmt.Errorf("not logged in — run 'mos login key <key>'")
	}
	if err != nil {
		return Session{}, fmt.Errorf("could not read session: %w", err)
	}

	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return Session{}, fmt.Errorf("could not parse session: %w", err)
	}

	if s.ExpiresAt != "" {
		exp, err := time.Parse(time.RFC3339, s.ExpiresAt)
		if err == nil && time.Now().After(exp) {
			_ = ClearSession()
			return Session{}, fmt.Errorf("session expired — run 'mos login key <key>'")
		}
	}

	return s, nil
}

// ClearSession removes the session file (called on logout).
func ClearSession() error {
	err := os.Remove(sessionPath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
