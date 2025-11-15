package transport

import (
	"net"
	"testing"
	"time"
)

func TestMockUDPConn_Basic(t *testing.T) {
	conn := NewMockUDPConn("127.0.0.1:8080")

	// Test basic write
	data := []byte("test data")
	n, err := conn.Write(data)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != len(data) {
		t.Errorf("Expected %d bytes written, got %d", len(data), n)
	}

	// Verify data was written
	written := conn.GetWrittenData()
	if len(written) != 1 {
		t.Fatalf("Expected 1 write, got %d", len(written))
	}
	if string(written[0]) != string(data) {
		t.Errorf("Expected %q, got %q", string(data), string(written[0]))
	}
}

func TestMockUDPConn_ReadWrite(t *testing.T) {
	conn := NewMockUDPConn("127.0.0.1:8080")

	// Setup read data
	testData := []byte("response data")
	conn.AddReadData(testData)

	// Write some data
	writeData := []byte("request data")
	conn.Write(writeData)

	// Read the response
	buffer := make([]byte, 1024)
	n, addr, err := conn.ReadFromUDP(buffer)
	if err != nil {
		t.Fatalf("ReadFromUDP failed: %v", err)
	}

	if n != len(testData) {
		t.Errorf("Expected %d bytes read, got %d", len(testData), n)
	}

	if string(buffer[:n]) != string(testData) {
		t.Errorf("Expected %q, got %q", string(testData), string(buffer[:n]))
	}

	expectedAddr := "127.0.0.1:8080"
	if addr.String() != expectedAddr {
		t.Errorf("Expected address %s, got %s", expectedAddr, addr.String())
	}
}

func TestMockUDPDialer_Basic(t *testing.T) {
	dialer := NewMockUDPDialer()

	// Test dialing
	addr := "127.0.0.1:9090"
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		t.Fatalf("Failed to resolve address: %v", err)
	}

	conn, err := dialer.DialUDP("udp4", nil, udpAddr)
	if err != nil {
		t.Fatalf("DialUDP failed: %v", err)
	}

	// Verify we can get the connection
	mockConn := dialer.GetConnection(addr)
	if mockConn == nil {
		t.Fatal("Expected connection to be created")
	}

	// Test writing to the connection
	testData := []byte("test data")
	n, err := conn.Write(testData)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != len(testData) {
		t.Errorf("Expected %d bytes written, got %d", len(testData), n)
	}

	// Verify data was written
	written := mockConn.GetWrittenData()
	if len(written) != 1 {
		t.Fatalf("Expected 1 write, got %d", len(written))
	}
	if string(written[0]) != string(testData) {
		t.Errorf("Expected %q, got %q", string(testData), string(written[0]))
	}
}

func TestMockUDPConn_Timeout(t *testing.T) {
	conn := NewMockUDPConn("127.0.0.1:8080")

	// Set a short deadline
	deadline := time.Now().Add(50 * time.Millisecond)
	err := conn.SetReadDeadline(deadline)
	if err != nil {
		t.Fatalf("SetReadDeadline failed: %v", err)
	}

	// Try to read without any data - should timeout
	buffer := make([]byte, 1024)
	start := time.Now()
	_, _, err = conn.ReadFromUDP(buffer)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Expected timeout error, but got none")
	}

	// Should have taken approximately the timeout duration
	if elapsed < 40*time.Millisecond || elapsed > 100*time.Millisecond {
		t.Errorf("Expected timeout around 50ms, but took %v", elapsed)
	}
}