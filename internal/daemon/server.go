package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"os"

	"github.com/hcp-uw/mosaic/internal/cli/handlers"
	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/cli/shared"
)

func StartServer() error {
	// remove old socket if present
	if _, err := os.Stat(shared.SocketPath); err == nil {
		os.Remove(shared.SocketPath)
	}

	ln, err := net.Listen("unix", shared.SocketPath)
	if err != nil {
		return fmt.Errorf("listen error: %w", err)
	}

	fmt.Println("Daemon listening at", shared.SocketPath)
	for {
		conn, err := ln.Accept()
		if err != nil {
			fmt.Println("accept error:", err)
			continue
		}
		go handleConn(conn)
	}
}

func handleConn(conn net.Conn) {
	defer conn.Close()

	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)

	var req protocol.Request
	if err := dec.Decode(&req); err != nil {
		enc.Encode(&protocol.Response{Ok: false, Message: "invalid JSON request"})
		return
	}

	switch req.Command {

	case "uploadFile":
		var up protocol.UploadRequest
		if err := toStruct(req.Data, &up); err != nil {
			enc.Encode(&protocol.Response{Ok: false, Message: "Upload request failed."})
			return
		}
		resp := handlers.HandleUpload(up)
		// this line is terminal output if it works
		message := fmt.Sprintf("File '%s' uploaded successfully to network.\n- Available storage remaining: %d GB.\n",
			resp.Name, resp.AvailableStorage)
		enc.Encode(&protocol.Response{Ok: true, Message: message, Data: resp})

	case "statusNetwork":
		var up protocol.NetworkStatusRequest
		if err := toStruct(req.Data, &up); err != nil {
			enc.Encode(&protocol.Response{Ok: false, Message: "Network status request failed."})
			return
		}
		resp := handlers.StatusNetwork(up)
		message := fmt.Sprintf("Network Status:\n- Total Network Storage: %d GB\n- Your Available Storage: %d GB\n- Number of Peers: %d\n",
			resp.NetworkStorage, resp.AvailableStorage, resp.Peers)
		enc.Encode(&protocol.Response{Ok: true, Message: message, Data: resp})

	case "joinNetwork":
		var up protocol.JoinRequest
		if err := toStruct(req.Data, &up); err != nil {
			enc.Encode(&protocol.Response{Ok: false, Message: "Join request failed."})
			return
		}
		resp := handlers.HandleJoin(up)
		message := fmt.Sprintf("Joined network successfully.\n- Connected to %d peers.\n", resp.Peers)
		enc.Encode(&protocol.Response{Ok: true, Message: message, Data: resp})

	case "statusNode":
		var up protocol.NodeStatusRequest
		if err := toStruct(req.Data, &up); err != nil {
			enc.Encode(&protocol.Response{Ok: false, Message: "Node status request failed."})
			return
		}
		resp := handlers.StatusNode(up)
		message := fmt.Sprintf("Node status processed successfully.\n- Node ID: %s@node-%v\n- Storage Shared: %d GB\n", resp.Username, resp.ID, resp.StorageShare)
		enc.Encode(&protocol.Response{Ok: true, Message: message, Data: resp})
	case "loginKey":
		var up protocol.LoginKeyRequest
		if err := toStruct(req.Data, &up); err != nil {
			enc.Encode(&protocol.Response{Ok: false, Message: "Login request failed."})
			return
		}
		resp := handlers.LoginKey(up)
		message := fmt.Sprintf("Logged in with key successfully.\n- Current Node: %s@node-%v\n", resp.Username, resp.CurrentNode)
		enc.Encode(&protocol.Response{Ok: true, Message: message, Data: resp})
	default:
		enc.Encode(&protocol.Response{Ok: false, Message: "unknown command"})
	}
}

// toStruct safely converts interface{} into a target struct using JSON round-tripping.
func toStruct(input interface{}, out interface{}) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}
