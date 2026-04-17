package handlers

import (
	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
)

// LoginStatus returns the current session state without contacting the auth server.
func LoginStatus(_ protocol.LoginStatusRequest) protocol.LoginStatusResponse {
	s, err := helpers.LoadSession()
	if err != nil {
		return protocol.LoginStatusResponse{LoggedIn: false}
	}
	return protocol.LoginStatusResponse{
		LoggedIn:   true,
		Username:   s.Username,
		AccountID:  s.AccountID,
		NodeNumber: s.NodeNumber,
		PublicKey:  s.PublicKey,
		ExpiresAt:  s.ExpiresAt,
	}
}
