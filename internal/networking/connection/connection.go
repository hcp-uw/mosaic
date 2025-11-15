package connection

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/hcp-uw/mosaic/internal/networking/transport"
)

// RouterInterface defines the interface for message routing
type RouterInterface interface {
	RouteMessage(rawMessage []byte) ([]byte, error)
}

// ExtendedConnection provides UDP connection management with keep-alive and message routing
type ExtendedConnection[TRecv any, TSend any] struct {
	conn           transport.UDPConnInterface
	addr           *net.UDPAddr
	lastSent       time.Time
	lastSentMu     sync.RWMutex
	writeMu        sync.Mutex
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
	closedOnce     sync.Once
	messageHandler func(TRecv) TSend
	messageRouter  RouterInterface
	sendInProgress chan bool
}

// NewConnection creates and starts a new connection without a message handler
func NewConnection(ipv4Addr string) (*ExtendedConnection[[]byte, []byte], error) {
	return newConnectionInternal[[]byte, []byte](ipv4Addr, nil, nil, transport.DefaultDialer)
}

// NewConnectionWithRouter creates and starts a new connection with a message router
func NewConnectionWithRouter(ipv4Addr string, router RouterInterface) (*ExtendedConnection[[]byte, []byte], error) {
	ec, err := newConnectionInternal[[]byte, []byte](ipv4Addr, nil, router, transport.DefaultDialer)
	if err != nil {
		return nil, err
	}

	// Start the message handler routine
	ec.wg.Add(1)
	go ec.handleIncomingMessages()

	return ec, nil
}

// NewConnectionWithDialer creates a new connection using a custom UDP dialer (useful for testing)
func NewConnectionWithDialer(ipv4Addr string, router RouterInterface, dialer transport.UDPDialerInterface) (*ExtendedConnection[[]byte, []byte], error) {
	ec, err := newConnectionInternal[[]byte, []byte](ipv4Addr, nil, router, dialer)
	if err != nil {
		return nil, err
	}

	// Start the message handler routine if router is provided
	if router != nil {
		ec.wg.Add(1)
		go ec.handleIncomingMessages()
	}

	return ec, nil
}

// newConnectionInternal is the internal implementation
func newConnectionInternal[TRecv any, TSend any](ipv4Addr string, handler func(TRecv) TSend, router RouterInterface, dialer transport.UDPDialerInterface) (*ExtendedConnection[TRecv, TSend], error) {
	// Parse the UDP address
	udpAddr, err := net.ResolveUDPAddr("udp4", ipv4Addr)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve address: %w", err)
	}

	// Dial the UDP connection using the provided dialer
	conn, err := dialer.DialUDP("udp4", nil, udpAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to dial UDP: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	ec := &ExtendedConnection[TRecv, TSend]{
		conn:           conn,
		addr:           udpAddr,
		lastSent:       time.Now(),
		ctx:            ctx,
		cancel:         cancel,
		sendInProgress: make(chan bool, 1),
		messageHandler: handler,
		messageRouter:  router,
	}

	// Start the keep-alive routine
	ec.wg.Add(1)
	go ec.keepAlive()

	return ec, nil
}

// Send sends data of type TSend and waits for a response of type TRecv
func Send[TSend any, TRecv any](ec *ExtendedConnection[[]byte, []byte], data TSend, timeout time.Duration) (TRecv, error) {
	var zero TRecv

	select {
	case <-ec.ctx.Done():
		return zero, fmt.Errorf("connection closed")
	default:
	}

	// Signal that Send is in progress (pause handler)
	select {
	case ec.sendInProgress <- true:
	default:
		// Channel might be full, that's OK
	}
	defer func() {
		// Signal that Send is complete (resume handler)
		select {
		case ec.sendInProgress <- false:
		default:
			// Channel might be full, that's OK
		}
	}()

	// Serialize the data
	serialized, err := json.Marshal(data)
	if err != nil {
		return zero, fmt.Errorf("failed to serialize: %w", err)
	}

	// Send the data
	if err := ec.sendRaw(serialized); err != nil {
		return zero, err
	}

	// Wait for response with timeout
	ctx, cancel := context.WithTimeout(ec.ctx, timeout)
	defer cancel()

	return receiveWithTimeout[TRecv](ctx, ec.conn)
}

// receiveWithTimeout waits for a response and deserializes it
func receiveWithTimeout[T any](ctx context.Context, conn transport.UDPConnInterface) (T, error) {
	var result T

	// Create a buffer for receiving
	buffer := make([]byte, 65536) // Max UDP packet size

	// Set read deadline based on context
	deadline, ok := ctx.Deadline()
	if ok {
		conn.SetReadDeadline(deadline)
	}
	defer conn.SetReadDeadline(time.Time{}) // Clear deadline after

	// Channel to handle the read operation
	type readResult struct {
		n   int
		err error
	}
	readChan := make(chan readResult, 1)

	go func() {
		n, _, err := conn.ReadFromUDP(buffer)
		readChan <- readResult{n: n, err: err}
	}()

	select {
	case <-ctx.Done():
		return result, fmt.Errorf("timeout waiting for response")
	case res := <-readChan:
		if res.err != nil {
			return result, fmt.Errorf("read failed: %w", res.err)
		}

		// Deserialize the response
		if err := json.Unmarshal(buffer[:res.n], &result); err != nil {
			return result, fmt.Errorf("failed to deserialize response: %w", res.err)
		}

		return result, nil
	}
}

// sendRaw is the internal send method that updates lastSent
func (ec *ExtendedConnection[TRecv, TSend]) sendRaw(data []byte) error {
	ec.writeMu.Lock()
	defer ec.writeMu.Unlock()

	_, err := ec.conn.Write(data)
	if err != nil {
		return fmt.Errorf("write failed: %w", err)
	}

	ec.lastSentMu.Lock()
	ec.lastSent = time.Now()
	ec.lastSentMu.Unlock()

	return nil
}

// Close stops the connection and cleans up resources
func (ec *ExtendedConnection[TRecv, TSend]) Close() error {
	var err error
	ec.closedOnce.Do(func() {
		ec.cancel()
		ec.wg.Wait()
		err = ec.conn.Close()
	})
	return err
}

// RemoteAddr returns the remote address
func (ec *ExtendedConnection[TRecv, TSend]) RemoteAddr() net.Addr {
	return ec.addr
}

// keepAlive maintains the connection by sending packets every 5 seconds if idle
func (ec *ExtendedConnection[TRecv, TSend]) keepAlive() {
	defer ec.wg.Done()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ec.ctx.Done():
			return
		case <-ticker.C:
			ec.lastSentMu.RLock()
			timeSinceLastSend := time.Since(ec.lastSent)
			ec.lastSentMu.RUnlock()

			if timeSinceLastSend >= 5*time.Second {
				// Send keep-alive packet
				if err := ec.sendRaw([]byte{0x00}); err != nil {
					fmt.Printf("keep-alive send failed: %v\n", err)
				}
			}
		}
	}
}

// handleIncomingMessages processes unsolicited incoming messages
func (ec *ExtendedConnection[TRecv, TSend]) handleIncomingMessages() {
	defer ec.wg.Done()

	buffer := make([]byte, 65536)

	for {
		select {
		case <-ec.ctx.Done():
			return
		case <-ec.sendInProgress:
			// Send is in progress, pause handling
			// Wait for Send to finish
			<-ec.sendInProgress
			continue
		default:
			// Check if we have a handler or router
			if ec.messageHandler == nil && ec.messageRouter == nil {
				return // No handler or router set
			}

			// Set a short read timeout to avoid blocking indefinitely
			ec.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, _, err := ec.conn.ReadFromUDP(buffer)
			ec.conn.SetReadDeadline(time.Time{}) // Clear deadline

			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue // Timeout is expected, continue loop
				}
				continue // Other errors, continue loop
			}

			var responseData []byte

			if ec.messageRouter != nil {
				// Use message router for handling
				responseData, err = ec.messageRouter.RouteMessage(buffer[:n])
				if err != nil {
					continue // Failed to route message
				}
			} else if ec.messageHandler != nil {
				// Use legacy generic message handler
				var incomingMsg TRecv
				if err := json.Unmarshal(buffer[:n], &incomingMsg); err != nil {
					continue // Invalid message format, skip
				}

				// Call handler with typed message
				response := ec.messageHandler(incomingMsg)

				// Send response back
				responseData, err = json.Marshal(response)
				if err != nil {
					continue // Failed to serialize response
				}
			}

			if len(responseData) > 0 {
				ec.sendRaw(responseData)
			}
		}
	}
}