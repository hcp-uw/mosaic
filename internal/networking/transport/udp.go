package transport

import (
	"net"
	"time"
)

// UDPConnInterface abstracts the core UDP connection operations
type UDPConnInterface interface {
	Write([]byte) (int, error)
	ReadFromUDP([]byte) (int, *net.UDPAddr, error)
	SetReadDeadline(time.Time) error
	Close() error
}

// UDPDialerInterface abstracts UDP connection creation
type UDPDialerInterface interface {
	DialUDP(network string, laddr, raddr *net.UDPAddr) (UDPConnInterface, error)
}

// realUDPConn wraps net.UDPConn to implement UDPConnInterface
type realUDPConn struct {
	*net.UDPConn
}

// Ensure realUDPConn implements UDPConnInterface
var _ UDPConnInterface = (*realUDPConn)(nil)

// Write implements UDPConnInterface
func (c *realUDPConn) Write(data []byte) (int, error) {
	return c.UDPConn.Write(data)
}

// ReadFromUDP implements UDPConnInterface
func (c *realUDPConn) ReadFromUDP(buffer []byte) (int, *net.UDPAddr, error) {
	return c.UDPConn.ReadFromUDP(buffer)
}

// SetReadDeadline implements UDPConnInterface
func (c *realUDPConn) SetReadDeadline(t time.Time) error {
	return c.UDPConn.SetReadDeadline(t)
}

// Close implements UDPConnInterface
func (c *realUDPConn) Close() error {
	return c.UDPConn.Close()
}

// realUDPDialer implements UDPDialerInterface using the standard net package
type realUDPDialer struct{}

// Ensure realUDPDialer implements UDPDialerInterface
var _ UDPDialerInterface = (*realUDPDialer)(nil)

// DialUDP implements UDPDialerInterface
func (d *realUDPDialer) DialUDP(network string, laddr, raddr *net.UDPAddr) (UDPConnInterface, error) {
	conn, err := net.DialUDP(network, laddr, raddr)
	if err != nil {
		return nil, err
	}
	return &realUDPConn{UDPConn: conn}, nil
}

// DefaultDialer is the default production UDP dialer
var DefaultDialer UDPDialerInterface = &realUDPDialer{}