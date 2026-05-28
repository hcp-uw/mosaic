package turn

import (
	"encoding/binary"
	"fmt"
)

// Relay protocol opcodes (first byte of every UDP frame).
const (
	OpRelayRegister byte = 0x10 // client → server: register peer ID
	OpRelayConnect  byte = 0x11 // client → server: request relay to target peer
	OpRelayData     byte = 0x12 // bidirectional: data payload
	OpRelayPing     byte = 0x13 // keep-alive ping
	OpRelayPong     byte = 0x14 // keep-alive pong
	OpRelayReady    byte = 0x15 // server → client: relay session established
)

const maxPeerIDLen = 255

// EncodeRegister returns: [0x10][peerIDLen uint8][peerID bytes]
func EncodeRegister(peerID string) []byte {
	b := make([]byte, 2+len(peerID))
	b[0] = OpRelayRegister
	b[1] = byte(len(peerID))
	copy(b[2:], peerID)
	return b
}

// EncodeConnect returns: [0x11][myIDLen uint8][myID][targetIDLen uint8][targetID]
func EncodeConnect(myID, targetID string) []byte {
	b := make([]byte, 3+len(myID)+len(targetID))
	i := 0
	b[i] = OpRelayConnect
	i++
	b[i] = byte(len(myID))
	i++
	copy(b[i:], myID)
	i += len(myID)
	b[i] = byte(len(targetID))
	i++
	copy(b[i:], targetID)
	return b
}

// EncodeData returns: [0x12][peerIDLen uint8][peerID][dataLen uint32 LE][data]
func EncodeData(peerID string, data []byte) []byte {
	b := make([]byte, 2+len(peerID)+4+len(data))
	i := 0
	b[i] = OpRelayData
	i++
	b[i] = byte(len(peerID))
	i++
	copy(b[i:], peerID)
	i += len(peerID)
	binary.LittleEndian.PutUint32(b[i:], uint32(len(data)))
	i += 4
	copy(b[i:], data)
	return b
}

// EncodePing returns: [0x13]
func EncodePing() []byte { return []byte{OpRelayPing} }

// EncodePong returns: [0x14]
func EncodePong() []byte { return []byte{OpRelayPong} }

// EncodeReady returns: [0x15][peerIDLen uint8][peerID]
func EncodeReady(peerID string) []byte {
	b := make([]byte, 2+len(peerID))
	b[0] = OpRelayReady
	b[1] = byte(len(peerID))
	copy(b[2:], peerID)
	return b
}

// DecodeRegister parses the peerID from a RelayRegister payload (opcode already stripped).
func DecodeRegister(b []byte) (peerID string, err error) {
	if len(b) < 1 {
		return "", fmt.Errorf("relay: register too short")
	}
	idLen := int(b[0])
	if len(b) < 1+idLen {
		return "", fmt.Errorf("relay: register truncated")
	}
	return string(b[1 : 1+idLen]), nil
}

// DecodeConnect parses myID and targetID from a RelayConnect payload (opcode stripped).
func DecodeConnect(b []byte) (myID, targetID string, err error) {
	if len(b) < 1 {
		return "", "", fmt.Errorf("relay: connect too short")
	}
	myLen := int(b[0])
	if len(b) < 1+myLen+1 {
		return "", "", fmt.Errorf("relay: connect truncated (myID)")
	}
	myID = string(b[1 : 1+myLen])
	rest := b[1+myLen:]
	targetLen := int(rest[0])
	if len(rest) < 1+targetLen {
		return "", "", fmt.Errorf("relay: connect truncated (targetID)")
	}
	targetID = string(rest[1 : 1+targetLen])
	return myID, targetID, nil
}

// DecodeData parses peerID and payload from a RelayData frame (opcode stripped).
func DecodeData(b []byte) (peerID string, data []byte, err error) {
	if len(b) < 1 {
		return "", nil, fmt.Errorf("relay: data too short")
	}
	idLen := int(b[0])
	if len(b) < 1+idLen+4 {
		return "", nil, fmt.Errorf("relay: data truncated (header)")
	}
	peerID = string(b[1 : 1+idLen])
	dataLen := int(binary.LittleEndian.Uint32(b[1+idLen:]))
	if len(b) < 1+idLen+4+dataLen {
		return "", nil, fmt.Errorf("relay: data truncated (payload)")
	}
	data = b[1+idLen+4 : 1+idLen+4+dataLen]
	return peerID, data, nil
}

// DecodeReady parses peerID from a RelayReady payload (opcode stripped).
func DecodeReady(b []byte) (peerID string, err error) {
	if len(b) < 1 {
		return "", fmt.Errorf("relay: ready too short")
	}
	idLen := int(b[0])
	if len(b) < 1+idLen {
		return "", fmt.Errorf("relay: ready truncated")
	}
	return string(b[1 : 1+idLen]), nil
}
