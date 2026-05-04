package client

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/cli/shared"
)

// SendRequest sends a command to the daemon and returns the response.
// timeout controls how long to wait for the daemon to reply; use a large
// value (e.g. 2 * time.Minute) for commands that fetch data from peers.
func SendRequest(command string, data interface{}, timeout ...time.Duration) (*protocol.Response, error) {
	conn, err := net.DialTimeout("unix", shared.SocketPath, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to daemon (%s): %w", shared.SocketPath, err)
	}
	defer conn.Close()

	totalTimeout := 10 * time.Second
	if len(timeout) > 0 && timeout[0] > 0 {
		totalTimeout = timeout[0]
	}
	if err := conn.SetDeadline(time.Now().Add(totalTimeout)); err != nil {
		return nil, fmt.Errorf("failed to set connection deadline: %w", err)
	}

	req := protocol.Request{
		Command: command,
		Data:    data,
	}

	enc := json.NewEncoder(conn)
	if err := enc.Encode(req); err != nil {
		return nil, fmt.Errorf("failed sending request: %w", err)
	}

	dec := json.NewDecoder(conn)
	var resp protocol.Response
	if err := dec.Decode(&resp); err != nil {
		return nil, fmt.Errorf("failed reading response: %w", err)
	}

	return &resp, nil
}
