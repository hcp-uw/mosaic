package client

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/cli/shared"
)

func SendRequest(command string, data interface{}) (*protocol.Response, error) {
	conn, err := net.DialTimeout("unix", shared.SocketPath, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to daemon (%s): %w", shared.SocketPath, err)
	}
	defer conn.Close()

	totalTimeout := 10 * time.Second
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
