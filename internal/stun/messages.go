package stun

import "net"


type MessageType int

const (
	JoinMessage MessageType = iota
)

// wrapper type for messages 
// includes message type and message contents
type message struct {
	messageType MessageType
	contents []byte
}

type joinMessage struct {
	IP net.IP
	PublicKey []byte
}   
