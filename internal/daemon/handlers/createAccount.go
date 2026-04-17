package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
)

type registerRequest struct {
	Username string `json:"username"`
	LoginKey string `json:"loginKey"`
}

type registerServerResponse struct {
	Success   bool   `json:"success"`
	AccountID string `json:"accountID"`
	Details   string `json:"details"`
}

// CreateAccount registers a new account on the auth server.
func CreateAccount(req protocol.CreateAccountRequest) protocol.CreateAccountResponse {
	fmt.Println("Daemon: creating account for", req.Username)

	req.Username = strings.TrimSpace(req.Username)
	req.LoginKey = strings.TrimSpace(req.LoginKey)

	if req.Username == "" || req.LoginKey == "" {
		return protocol.CreateAccountResponse{
			Success: false,
			Details: "username and login key are required",
		}
	}

	body, _ := json.Marshal(registerRequest{
		Username: req.Username,
		LoginKey: req.LoginKey,
	})

	resp, err := http.Post(authServerURL()+"/auth/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return protocol.CreateAccountResponse{
			Success: false,
			Details: fmt.Sprintf("could not reach auth server: %v", err),
		}
	}
	defer resp.Body.Close()

	var serverResp registerServerResponse
	if err := json.NewDecoder(resp.Body).Decode(&serverResp); err != nil {
		return protocol.CreateAccountResponse{
			Success: false,
			Details: "could not parse auth server response",
		}
	}

	if !serverResp.Success {
		return protocol.CreateAccountResponse{
			Success: false,
			Details: serverResp.Details,
		}
	}

	fmt.Printf("Daemon: account created — username=%s accountID=%s\n", req.Username, serverResp.AccountID)

	return protocol.CreateAccountResponse{
		Success:   true,
		Details:   fmt.Sprintf("Account created. You can now log in with: mos login key %s <your-key>", req.Username),
		AccountID: serverResp.AccountID,
		Username:  req.Username,
	}
}
