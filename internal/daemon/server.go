package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"os"

	"github.com/hcp-uw/mosaic/internal/handlers"
	"github.com/hcp-uw/mosaic/internal/protocol"
	"github.com/hcp-uw/mosaic/internal/shared"
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
			enc.Encode(&protocol.Response{Ok: false, Message: "bad upload request"})
			return
		}
		resp := handlers.HandleUpload(up)
		enc.Encode(&protocol.Response{Ok: true, Message: "ok", Data: resp})

	/*
		case "status":
			resp := handlers.HandleStatus()
			enc.Encode(&protocol.Response{Ok: true, Message: "ok", Data: resp})

		case "join":
			var j protocol.JoinRequest
			if err := toStruct(req.Data, &j); err != nil {
				enc.Encode(&protocol.Response{Ok: false, Message: "bad join request"})
				return
			}
			resp := handlers.HandleJoin(j)
			enc.Encode(&protocol.Response{Ok: true, Message: "ok", Data: resp})
	*/
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
