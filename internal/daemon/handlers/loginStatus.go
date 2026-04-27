package handlers

import (
	"os"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/cli/shared"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
)

// LoginStatus returns the current session state including whether the
// ECDSA keypair file exists on disk.
func LoginStatus(_ protocol.LoginStatusRequest) protocol.LoginStatusResponse {
	s, err := helpers.LoadSession()
	if err != nil {
		return protocol.LoginStatusResponse{LoggedIn: false}
	}

	_, keyErr := os.Stat(shared.UserKeyPath())
	hasKeyPair := keyErr == nil

	return protocol.LoginStatusResponse{
		LoggedIn:   true,
		PublicKey:  s.PublicKey,
		HasKeyPair: hasKeyPair,
	}
}
