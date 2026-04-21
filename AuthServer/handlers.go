package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// ── Register ─────────────────────────────────────────────────────────────────

type registerRequest struct {
	Username string `json:"username"`
	LoginKey string `json:"loginKey"`
}

type registerResponse struct {
	Success   bool   `json:"success"`
	AccountID string `json:"accountID,omitempty"` // zero-padded 9-digit display string
	Details   string `json:"details"`
}

// POST /auth/register
func handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "could not parse request body", http.StatusBadRequest)
		return
	}

	req.Username = strings.TrimSpace(req.Username)
	req.LoginKey = strings.TrimSpace(req.LoginKey)

	if req.Username == "" || req.LoginKey == "" {
		jsonError(w, "username and loginKey are required", http.StatusBadRequest)
		return
	}

	acc, err := createAccount(req.Username, req.LoginKey)
	if err != nil {
		if err.Error() == "username already taken" {
			jsonError(w, "username already taken", http.StatusConflict)
			return
		}
		jsonError(w, fmt.Sprintf("could not create account: %v", err), http.StatusInternalServerError)
		return
	}

	fmt.Printf("[register] username=%s accountID=%s\n", acc.Username, formatAccountID(acc.AccountID))
	jsonOK(w, registerResponse{
		Success:   true,
		AccountID: formatAccountID(acc.AccountID),
		Details:   "account created",
	})
}

// ── Login ─────────────────────────────────────────────────────────────────────

type loginRequest struct {
	Username   string `json:"username"`
	LoginKey   string `json:"loginKey"`
	PublicKey  string `json:"publicKey"`  // hex PKIX DER of the HKDF-derived ECDSA key
	NodeNumber int    `json:"nodeNumber"` // 0 = new device, non-zero = reuse existing node
}

type loginResponse struct {
	Success    bool   `json:"success"`
	Token      string `json:"token,omitempty"`
	NodeNumber int    `json:"nodeNumber,omitempty"`
	Details    string `json:"details"`
}

// POST /auth/login
// Validates the login key, assigns or creates a node, records the derived public
// key, and returns a signed JWT. The client writes this to ~/.mosaic-session.
//
// NodeNumber behaviour:
//   - 0: first login on this device — creates node-1, node-2, etc.
//   - non-zero: re-login on a known device — reuses that node number
func handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "could not parse request body", http.StatusBadRequest)
		return
	}

	req.Username = strings.TrimSpace(req.Username)
	req.LoginKey = strings.TrimSpace(req.LoginKey)
	req.PublicKey = strings.TrimSpace(req.PublicKey)

	if req.Username == "" || req.LoginKey == "" {
		jsonError(w, "username and loginKey are required", http.StatusBadRequest)
		return
	}

	acc := lookupByUsername(req.Username)
	if acc == nil {
		jsonError(w, "invalid username or login key", http.StatusUnauthorized)
		return
	}

	if !verifyLoginKey(acc, req.LoginKey) {
		jsonError(w, "invalid username or login key", http.StatusUnauthorized)
		return
	}

	// Record the public key on first login. Deterministic derivation means it
	// never changes — every device with the same login key produces the same key.
	if req.PublicKey != "" && acc.PublicKey == "" {
		if err := setPublicKey(acc.AccountID, req.PublicKey); err != nil {
			jsonError(w, "could not record public key", http.StatusInternalServerError)
			return
		}
		acc.PublicKey = req.PublicKey
	}

	// Resolve the node for this session.
	var node *Node
	if req.NodeNumber != 0 {
		// Re-login on a known device — reuse the node if it still exists,
		// otherwise create a fresh one (handles DB resets gracefully).
		node = lookupNode(acc.AccountID, req.NodeNumber)
		if node == nil {
			var err error
			node, err = createNode(acc.AccountID)
			if err != nil {
				jsonError(w, fmt.Sprintf("could not create node: %v", err), http.StatusInternalServerError)
				return
			}
		}
	} else {
		// First login on this device — create a new node.
		var err error
		node, err = createNode(acc.AccountID)
		if err != nil {
			jsonError(w, fmt.Sprintf("could not create node: %v", err), http.StatusInternalServerError)
			return
		}
	}

	token, err := IssueToken(acc, node)
	if err != nil {
		jsonError(w, "could not issue token", http.StatusInternalServerError)
		return
	}

	fmt.Printf("[login] username=%s accountID=%s node-%d\n",
		acc.Username, formatAccountID(acc.AccountID), node.NodeNumber)

	jsonOK(w, loginResponse{
		Success:    true,
		Token:      token,
		NodeNumber: node.NodeNumber,
		Details:    fmt.Sprintf("logged in on node-%d", node.NodeNumber),
	})
}

// ── Token Verification ────────────────────────────────────────────────────────

type verifyRequest struct {
	Token string `json:"token"`
}

type verifyResponse struct {
	Success    bool   `json:"success"`
	AccountID  int    `json:"accountID,omitempty"`
	Username   string `json:"username,omitempty"`
	NodeNumber int    `json:"nodeNumber,omitempty"`
	Details    string `json:"details"`
}

// POST /auth/verify
// Called by the STUN server to authenticate connecting clients.
func handleVerify(w http.ResponseWriter, r *http.Request) {
	var req verifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "could not parse request body", http.StatusBadRequest)
		return
	}
	claims, err := VerifyToken(req.Token)
	if err != nil {
		jsonError(w, fmt.Sprintf("invalid token: %v", err), http.StatusUnauthorized)
		return
	}
	jsonOK(w, verifyResponse{
		Success:    true,
		AccountID:  claims.AccountID,
		Username:   claims.Username,
		NodeNumber: claims.NodeNumber,
		Details:    "ok",
	})
}

// ── Server Public Key ────────────────────────────────────────────────────────

type serverPubKeyResponse struct {
	Success   bool   `json:"success"`
	PublicKey string `json:"publicKey"` // PEM-encoded ECDSA P-256 public key
	Details   string `json:"details"`
}

// GET /auth/pubkey/server
// Returns the server's ECDSA public key so daemons can verify JWTs locally.
func handleServerPubKey(w http.ResponseWriter, r *http.Request) {
	pem, err := ServerPublicKeyPEM()
	if err != nil {
		jsonError(w, "could not serialize server public key", http.StatusInternalServerError)
		return
	}
	jsonOK(w, serverPubKeyResponse{Success: true, PublicKey: pem, Details: "ok"})
}

// ── Public Key Lookup ─────────────────────────────────────────────────────────

type pubKeyResponse struct {
	Success   bool   `json:"success"`
	AccountID string `json:"accountID,omitempty"`
	Username  string `json:"username,omitempty"`
	PublicKey string `json:"publicKey,omitempty"`
	Details   string `json:"details"`
}

// GET /auth/pubkey/{userID}
// Returns the canonical public key for an account ID. Peers call this during
// MergeNetworkManifest to confirm that the public key embedded in a manifest
// entry actually belongs to the claimed accountID.
func handlePubKey(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("userID")
	accountID, err := strconv.Atoi(idStr)
	if err != nil {
		jsonError(w, "invalid userID", http.StatusBadRequest)
		return
	}

	acc := lookupByID(accountID)
	if acc == nil {
		jsonError(w, "account not found", http.StatusNotFound)
		return
	}

	if acc.PublicKey == "" {
		jsonError(w, "no public key registered — user has not logged in yet", http.StatusNotFound)
		return
	}

	jsonOK(w, pubKeyResponse{
		Success:   true,
		AccountID: formatAccountID(acc.AccountID),
		Username:  acc.Username,
		PublicKey: acc.PublicKey,
		Details:   "ok",
	})
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]any{
		"success": false,
		"details": msg,
	})
}
