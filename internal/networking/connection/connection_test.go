package connection

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/hcp-uw/mosaic/internal/networking/transport"
)

func TestNewConnection(t *testing.T) {
	// Setup mock dialer
	mockDialer := transport.NewMockUDPDialer()

	// Create connection
	conn, err := NewConnectionWithDialer("127.0.0.1:8080", nil, mockDialer)
	if err != nil {
		t.Fatalf("Failed to create connection: %v", err)
	}
	defer conn.Close()

	// Verify connection was created
	expectedAddr := "127.0.0.1:8080"
	if conn.RemoteAddr().String() != expectedAddr {
		t.Errorf("Expected remote address %s, got %s", expectedAddr, conn.RemoteAddr().String())
	}
}

func TestSend(t *testing.T) {
	// Setup mock dialer
	mockDialer := transport.NewMockUDPDialer()

	// Create connection
	conn, err := NewConnectionWithDialer("127.0.0.1:8080", nil, mockDialer)
	if err != nil {
		t.Fatalf("Failed to create connection: %v", err)
	}
	defer conn.Close()

	// Get the mock UDP connection
	mockUDPConn := mockDialer.GetConnection("127.0.0.1:8080")
	if mockUDPConn == nil {
		t.Fatal("Mock UDP connection was not created")
	}

	// Setup mock response
	type TestResponse struct {
		Message string `json:"message"`
		Success bool   `json:"success"`
	}

	expectedResponse := TestResponse{
		Message: "Hello World",
		Success: true,
	}

	responseData, err := json.Marshal(expectedResponse)
	if err != nil {
		t.Fatalf("Failed to marshal response: %v", err)
	}

	// Configure mock to return this data when read
	mockUDPConn.AddReadData(responseData)

	// Send test data
	type TestRequest struct {
		Data string `json:"data"`
		ID   int    `json:"id"`
	}

	request := TestRequest{
		Data: "test data",
		ID:   42,
	}

	// Perform the Send operation
	response, err := Send[TestRequest, TestResponse](conn, request, 5*time.Second)
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// Verify response
	if response.Message != expectedResponse.Message {
		t.Errorf("Expected message %q, got %q", expectedResponse.Message, response.Message)
	}

	if response.Success != expectedResponse.Success {
		t.Errorf("Expected success %v, got %v", expectedResponse.Success, response.Success)
	}

	// Verify that data was written to the connection
	writtenData := mockUDPConn.GetWrittenData()
	if len(writtenData) != 1 {
		t.Fatalf("Expected 1 write, got %d", len(writtenData))
	}

	// Verify the written data matches our request
	var sentRequest TestRequest
	if err := json.Unmarshal(writtenData[0], &sentRequest); err != nil {
		t.Fatalf("Failed to unmarshal written data: %v", err)
	}

	if sentRequest.Data != request.Data || sentRequest.ID != request.ID {
		t.Errorf("Sent data doesn't match: expected %+v, got %+v", request, sentRequest)
	}
}

func TestSendTimeout(t *testing.T) {
	// Setup mock dialer
	mockDialer := transport.NewMockUDPDialer()

	// Create connection
	conn, err := NewConnectionWithDialer("127.0.0.1:8080", nil, mockDialer)
	if err != nil {
		t.Fatalf("Failed to create connection: %v", err)
	}
	defer conn.Close()

	// Send test data with short timeout (no response data configured)
	type TestRequest struct {
		Data string `json:"data"`
	}

	type TestResponse struct {
		Response string `json:"response"`
	}

	request := TestRequest{Data: "test"}

	// This should timeout since no response data is configured
	_, err = Send[TestRequest, TestResponse](conn, request, 100*time.Millisecond)
	if err == nil {
		t.Fatal("Expected timeout error, but Send succeeded")
	}

	// Verify error mentions timeout or read failure (both indicate timeout)
	errStr := err.Error()
	if errStr != "timeout waiting for response" && errStr != "read failed: read udp: timeout" {
		t.Errorf("Expected timeout-related error, got: %v", err)
	}
}

func TestConnectionWithRouter(t *testing.T) {
	// Create a simple mock router
	mockRouter := &mockMessageRouter{}

	// Setup mock dialer
	mockDialer := transport.NewMockUDPDialer()

	// Create connection with router
	conn, err := NewConnectionWithDialer("127.0.0.1:9090", mockRouter, mockDialer)
	if err != nil {
		t.Fatalf("Failed to create connection: %v", err)
	}
	defer conn.Close()

	// Verify connection was created
	expectedAddr := "127.0.0.1:9090"
	if conn.RemoteAddr().String() != expectedAddr {
		t.Errorf("Expected remote address %s, got %s", expectedAddr, conn.RemoteAddr().String())
	}

	// Give time for message handler to start
	time.Sleep(10 * time.Millisecond)

	t.Logf("Connection with router test completed successfully")
}

// mockMessageRouter is a simple router for testing
type mockMessageRouter struct{}

func (m *mockMessageRouter) RouteMessage(rawMessage []byte) ([]byte, error) {
	// Simple echo response
	response := map[string]interface{}{
		"echo": "received message",
		"size": len(rawMessage),
	}
	return json.Marshal(response)
}