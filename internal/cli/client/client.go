package client

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/cli/shared"
)

func SendRequest(command string, data interface{}) (*protocol.Response, error) {
	var conn net.Conn
	var err error

	if runtime.GOOS == "windows" {
		// Windows: connect via TCP using port file
		conn, err = connectWindows()
	} else {
		// macOS/Linux: connect via Unix socket
		conn, err = net.DialTimeout("unix", shared.SocketPath, 2*time.Second)
	}

	if err != nil {
		if runtime.GOOS == "windows" {
			return nil, fmt.Errorf("failed to connect to daemon: %w (is daemon running?)", err)
		}
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

// connectWindows connects to the daemon on Windows using TCP
func connectWindows() (net.Conn, error) {
	portFile := shared.GetPortFile()
	
	// Read port from file
	portData, err := os.ReadFile(portFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read port file %s: %w", portFile, err)
	}

	port, err := strconv.Atoi(strings.TrimSpace(string(portData)))
	if err != nil {
		return nil, fmt.Errorf("invalid port in file: %w", err)
	}

	// Connect to TCP socket
	return net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
}