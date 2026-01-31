```
# P2P Network Protocols
Essentially moving the client logic out of the stun server and splitting it up into several files 

 mosaic/
├── internal/
│   ├── p2p/
│   │   ├── client.go           # Main client with connection logic
│   │   ├── state.go            # State management
│   │   ├── peer.go             # Peer handling
│   │   ├── callbacks.go        # Event callbacks
│   │   |── message_handler.go  # Message routing
│   │   └── server_handler.go   # Deals with connections to server
│   │   └── connection_handler.go   # Deals with ping/pong keeping connections alive
│   ├── api/
│   │   └── message.go          # Protocol messages -> Future seperated between Stun messages and user messages
```
            
