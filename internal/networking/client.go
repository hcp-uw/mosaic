// Package networking provides a client API for peer-to-peer networking with CRDT synchronization
package networking

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"net"
	"time"

	"github.com/hcp-uw/mosaic/internal/networking/connection"
	"github.com/hcp-uw/mosaic/internal/networking/crdt"
	"github.com/hcp-uw/mosaic/internal/networking/protocol"
	"github.com/hcp-uw/mosaic/internal/networking/transport"
)

// Public types
type (
	// NodeIP represents a network node IP address
	NodeIP = protocol.NodeIP

	// CRDT represents a conflict-free replicated data type for peer network management
	CRDT = crdt.CRDT

	// Event represents an event that occurred during CRDT operations
	Event = crdt.Event

	// StoreStatus represents the status of a store operation
	StoreStatus = protocol.StoreStatus

	// JoinMessage represents a network join request
	JoinMessage = protocol.JoinMessage
)

// Store status constants
const (
	StoreAccepted = protocol.StoreAccepted
	StoreRejected = protocol.StoreRejected
	StorePartial  = protocol.StorePartial
	StorePending  = protocol.StorePending
)

// Connection types
type (
	// PeerConnection represents a connection to a network peer
	PeerConnection = *connection.ExtendedConnection[[]byte, []byte]

	// StunConnection represents a connection to a STUN server
	StunConnection = *connection.ExtendedConnection[[]byte, []byte]
)

// NetworkConnections holds connections to the leader and peers
type NetworkConnections struct {
	Leader PeerConnection
	Peers  []PeerConnection
}

// Error types
type (
	EstablishConnectionError struct {
		Err error
	}

	GetPeerCRDTError struct {
		Err error
	}

	EstablishStunConnectionError struct {
		Err error
	}
)

func (e EstablishConnectionError) Error() string     { return e.Err.Error() }
func (e GetPeerCRDTError) Error() string            { return e.Err.Error() }
func (e EstablishStunConnectionError) Error() string { return e.Err.Error() }

// Public API functions

// NewCRDT creates a new initialized CRDT
func NewCRDT() *CRDT {
	return crdt.New()
}

// EstablishStunConnection creates a connection to a STUN server
func EstablishStunConnection(nodeIP NodeIP) (StunConnection, error) {
	ipStr := net.IP(nodeIP).String() + ":8080" // Default port for STUN
	return connection.NewConnection(ipStr)
}

// EstablishPeerConnection creates a connection to a network peer
func EstablishPeerConnection(nodeIP NodeIP, privateKey ed25519.PrivateKey) (PeerConnection, error) {
	ipStr := net.IP(nodeIP).String() + ":9090" // Different port for peer connections
	router := protocol.NewMessageRouter(privateKey)
	return connection.NewConnectionWithRouter(ipStr, router)
}

// EstablishPeerConnectionWithCRDT creates a connection to a network peer with CRDT support
func EstablishPeerConnectionWithCRDT(nodeIP NodeIP, privateKey ed25519.PrivateKey, localCRDT *CRDT) (PeerConnection, error) {
	ipStr := net.IP(nodeIP).String() + ":9090"
	router := protocol.NewMessageRouterWithCRDT(privateKey, localCRDT)
	return connection.NewConnectionWithRouter(ipStr, router)
}

// GetPeerCRDT retrieves CRDT data from a peer
func GetPeerCRDT(conn PeerConnection) (*CRDT, error) {
	// Create CRDT request
	request := protocol.GetCRDTRequest{
		Timestamp: time.Now(),
	}

	// Send request to peer and receive response
	response, err := SendPeerMessage[protocol.GetCRDTRequest, protocol.GetCRDTResponse](
		conn, protocol.PeerGetCRDTMessage, request)
	if err != nil {
		return nil, fmt.Errorf("failed to get CRDT from peer: %w", err)
	}

	// Convert the response CRDT to our type
	if responseCRDT, ok := response.CRDT.(*CRDT); ok {
		return responseCRDT, nil
	}

	// If not the expected type, return error
	return nil, fmt.Errorf("received invalid CRDT type from peer")
}

// MergeCRDTs merges CRDT data with a peer and returns events
func MergeCRDTs(peerConn PeerConnection, localCRDT *CRDT, privateKey ed25519.PrivateKey, publicKey ed25519.PublicKey, nodeID string) ([]Event, error) {
	// Create our join message if we're new
	joinMsg := protocol.JoinMessage{
		NodeID:    nodeID,
		PublicKey: publicKey,
	}

	// Sign our join message
	signedJoinMsg, err := protocol.NewSignedJoinMessage(joinMsg, privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign join message: %w", err)
	}

	// Add to our local CRDT first
	localEvents, err := localCRDT.AddJoinMessage(nodeID, signedJoinMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to add join message to local CRDT: %w", err)
	}

	// Create merge request with our CRDT
	request := protocol.MergeCRDTRequest{
		CRDT:      localCRDT,
		Timestamp: time.Now(),
	}

	// Send merge request to peer and receive response with events
	response, err := SendPeerMessage[protocol.MergeCRDTRequest, protocol.MergeCRDTResponse](
		peerConn, protocol.PeerMergeCRDTMessage, request)
	if err != nil {
		return localEvents, fmt.Errorf("failed to send merge CRDT request: %w", err)
	}

	// Convert response events to our type
	var peerEvents []Event
	for _, event := range response.Events {
		if evt, ok := event.(Event); ok {
			peerEvents = append(peerEvents, evt)
		}
	}

	// Combine local events with peer response events
	allEvents := append(localEvents, peerEvents...)

	return allEvents, nil
}

// GetPeerIPs retrieves the list of peer IP addresses from a connection
func GetPeerIPs(conn PeerConnection) ([]NodeIP, error) {
	// Create request for peer IPs
	request := protocol.GetPeerIPsRequest{
		Timestamp: time.Now(),
	}

	// Send request to peer and receive response
	response, err := SendPeerMessage[protocol.GetPeerIPsRequest, protocol.GetPeerIPsResponse](
		conn, protocol.PeerGetIPsMessage, request)
	if err != nil {
		return nil, fmt.Errorf("failed to get peer IPs: %w", err)
	}

	return response.PeerIPs, nil
}

// VerifyPeerPublicKey verifies a peer's public key
func VerifyPeerPublicKey(conn PeerConnection, expectedPublicKey ed25519.PublicKey) (bool, bool, error) {
	// Create request to verify peer's public key
	request := protocol.VerifyPeerKeyRequest{
		PublicKey: expectedPublicKey,
		Timestamp: time.Now(),
	}

	// Send request to peer and receive response
	response, err := SendPeerMessage[protocol.VerifyPeerKeyRequest, protocol.VerifyPeerKeyResponse](
		conn, protocol.PeerVerifyKeyMessage, request)
	if err != nil {
		return false, false, fmt.Errorf("failed to verify peer public key: %w", err)
	}

	return response.IsValid, response.InCRDT, nil
}

// VerifyInCRDT checks if a public key exists in the CRDT
func VerifyInCRDT(crdt *CRDT, publicKey ed25519.PublicKey) error {
	// Check if our public key exists in the join messages
	for _, joinMsg := range crdt.JoinMessages {
		if bytes.Equal(joinMsg.Data.PublicKey, publicKey) {
			return nil // Found our public key, we're verified as part of the network
		}
	}

	return fmt.Errorf("public key not found in CRDT - not recognized as network member")
}

// PeerIsNew checks if a public key represents a new peer (not in CRDT)
func PeerIsNew(publicKey ed25519.PublicKey, crdt *CRDT) bool {
	for _, joinMsg := range crdt.JoinMessages {
		if bytes.Equal(joinMsg.Data.PublicKey, publicKey) {
			return false
		}
	}
	return true
}

// SetData stores data with a peer and updates the local CRDT
func SetData(peerConn PeerConnection, data []byte, privateKey ed25519.PrivateKey, localCRDT *CRDT) error {
	// 1. Create hash of the data
	hasher := sha256.New()
	hasher.Write(data)
	dataHash := hasher.Sum(nil)

	// 2. Create signed store request
	storeRequest := protocol.StoreRequest{
		DataHash:  dataHash,
		Size:      int64(len(data)),
		Timestamp: time.Now(),
	}

	signedStoreRequest, err := protocol.NewSignedStoreRequest(storeRequest, privateKey)
	if err != nil {
		return fmt.Errorf("failed to sign store request: %w", err)
	}

	// 3. Send store request message to peer and receive acknowledgment
	signedStoreAck, err := SendPeerMessage[*protocol.SignedStoreRequest, *protocol.SignedStoreAcknowledge](
		peerConn, protocol.PeerDataMessage, signedStoreRequest)
	if err != nil {
		return fmt.Errorf("failed to send store request to peer: %w", err)
	}

	// 4. Verify the acknowledgment
	if err := signedStoreAck.Verify(); err != nil {
		return fmt.Errorf("store acknowledgment verification failed: %w", err)
	}

	// Check if the acknowledgment is for our request
	if !bytes.Equal(signedStoreAck.Data.RequestHash, dataHash) {
		return fmt.Errorf("acknowledgment request hash mismatch")
	}

	if signedStoreAck.Data.Status != protocol.StoreAccepted {
		return fmt.Errorf("peer did not accept the store request, status: %v", signedStoreAck.Data.Status)
	}

	// 5. Create store operation message and add to local CRDT
	storeOpMessage := &protocol.StoreOperationMessage{
		SignedStoreRequest:     signedStoreRequest,
		SignedStoreAcknowledge: signedStoreAck,
	}

	// Add to local CRDT
	fileHash := fmt.Sprintf("%x", dataHash)
	_, err = localCRDT.AddStoreOperation(fileHash, storeOpMessage)
	if err != nil {
		return fmt.Errorf("failed to add store operation to local CRDT: %w", err)
	}

	return nil
}

// MOSJoinNetwork performs the complete MOS network join workflow
func MOSJoinNetwork(stunConnection StunConnection, publicKey ed25519.PublicKey, privateKey ed25519.PrivateKey) (*NetworkConnections, error) {
	// 1. Check that there's no mos background process going on already
	// TODO: Add check for existing MOS background process

	// 2. leaderIp <- join_network()
	leaderIp := SendStunMessage(stunConnection, protocol.JoinNetworkMessage)

	// 3. leader <- establish_conn(leaderIp)
	leaderConn, err := EstablishPeerConnection(leaderIp, privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to establish connection to leader: %w", err)
	}

	// 4. peersCrdt <- get_peerCrdt(leader)
	peersCrdt, err := GetPeerCRDT(leaderConn)
	if err != nil {
		return nil, fmt.Errorf("failed to get peer CRDT: %w", err)
	}

	// 5. verifyInCrdt(peersCrdt)
	if err := VerifyInCRDT(peersCrdt, publicKey); err != nil {
		return nil, fmt.Errorf("CRDT verification failed: %w", err)
	}

	// 6. peerIps <- get_peer_ips(leader)
	peerIps, err := GetPeerIPs(leaderConn)
	if err != nil {
		return nil, fmt.Errorf("failed to get peer IPs: %w", err)
	}

	// 7. Establish connections to all peers
	var peerConnections []PeerConnection

	for _, peerIp := range peerIps {
		// Establish peer connection
		peerConn, err := EstablishPeerConnection(peerIp, privateKey)
		if err != nil {
			return nil, fmt.Errorf("failed to establish peer connection to %v: %w", peerIp, err)
		}

		peerConnections = append(peerConnections, peerConn)
	}

	return &NetworkConnections{
		Leader: leaderConn,
		Peers:  peerConnections,
	}, nil
}

// SendStunMessage sends a message to a STUN server and returns the response
func SendStunMessage(conn StunConnection, messageType protocol.StunMessageCode) NodeIP {
	message := protocol.StunMessage{
		MessageType: messageType,
		Timestamp:   time.Now().Unix(),
	}

	response, err := connection.Send[protocol.StunMessage, NodeIP](conn, message, 10*time.Second)
	if err != nil {
		panic(fmt.Sprintf("SendMessage failed: %v", err))
	}

	return response
}

// SendPeerMessage sends a typed message to a peer and returns the typed response
func SendPeerMessage[TRequest any, TResponse any](conn PeerConnection, messageType protocol.PeerMessageCode, data TRequest) (TResponse, error) {
	message := protocol.PeerMessage[TRequest]{
		MessageType: messageType,
		Data:        data,
		Timestamp:   time.Now().Unix(),
	}

	return connection.Send[protocol.PeerMessage[TRequest], TResponse](conn, message, 10*time.Second)
}

// Testing utilities - these should only be used in tests

// EstablishMockPeerConnection creates a peer connection using a mock dialer for testing
func EstablishMockPeerConnection(nodeIP NodeIP, privateKey ed25519.PrivateKey, dialer transport.UDPDialerInterface) (PeerConnection, error) {
	ipStr := net.IP(nodeIP).String() + ":9090"
	router := protocol.NewMessageRouter(privateKey)
	return connection.NewConnectionWithDialer(ipStr, router, dialer)
}

// EstablishMockStunConnection creates a STUN connection using a mock dialer for testing
func EstablishMockStunConnection(nodeIP NodeIP, dialer transport.UDPDialerInterface) (StunConnection, error) {
	ipStr := net.IP(nodeIP).String() + ":8080"
	return connection.NewConnectionWithDialer(ipStr, nil, dialer)
}