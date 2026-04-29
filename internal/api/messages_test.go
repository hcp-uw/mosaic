package api

import (
	"net"
	"testing"
)

func TestMessageRoundTrip(t *testing.T) {
	cases := []*Message{
		NewClientRegisterMessage(),
		NewServerAssignedLeaderMessage(),
		NewWaitingForPeerMessage(),
		NewClientPingMessage(NewSignature("alice")),
		NewPeerPingMessage(NewSignature("alice")),
		NewPeerPongMessage(NewSignature("bob")),
		NewServerErrorMessage("oops", "OOPS"),
		NewRegisterSuccessMessage("ok", "127.0.0.1:9000"),
		NewPeerTextMessage("hi", "alice"),
		NewNewPeerJoinerMessage("leader", "joiner", "127.0.0.1:1"),
	}
	for _, m := range cases {
		raw, err := m.Serialize()
		if err != nil {
			t.Fatalf("Serialize %s: %v", m.Type, err)
		}
		got, err := DeserializeMessage(raw)
		if err != nil {
			t.Fatalf("Deserialize %s: %v", m.Type, err)
		}
		if got.Type != m.Type {
			t.Errorf("type round-trip mismatch: got %s, want %s", got.Type, m.Type)
		}
	}
}

func TestPeerAssignmentMessage(t *testing.T) {
	addr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:1234")
	m := NewPeerAssignmentMessage(addr, "abc")
	raw, err := m.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	got, _ := DeserializeMessage(raw)
	data, err := got.GetPeerAssignmentData()
	if err != nil {
		t.Fatalf("GetPeerAssignmentData: %v", err)
	}
	if data.PeerID != "abc" || data.PeerAddress != "127.0.0.1:1234" {
		t.Errorf("got %+v", data)
	}
}

func TestCurrentMembersMessage(t *testing.T) {
	addr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:1234")
	m := NewCurrentMembersMessage(map[string]*net.UDPAddr{"peer1": addr}, "leader")
	raw, _ := m.Serialize()
	got, _ := DeserializeMessage(raw)
	data, err := got.GetCurrentMembersData()
	if err != nil {
		t.Fatalf("GetCurrentMembersData: %v", err)
	}
	if data.Members["peer1"] != "127.0.0.1:1234" {
		t.Errorf("Members = %+v", data.Members)
	}
}

func TestExtractor_WrongTypeRejected(t *testing.T) {
	m := NewPeerPingMessage(NewSignature("a"))
	if _, err := m.GetCurrentMembersData(); err != ErrInvalidMessageType {
		t.Errorf("expected ErrInvalidMessageType, got %v", err)
	}
	if _, err := m.GetServerErrorData(); err != ErrInvalidMessageType {
		t.Errorf("expected ErrInvalidMessageType, got %v", err)
	}
}

func TestNewSignature(t *testing.T) {
	s := NewSignature("hex123")
	if s.PubKey != "hex123" {
		t.Errorf("PubKey = %q, want hex123", s.PubKey)
	}
}

func TestDataMessages_RoundTrip(t *testing.T) {
	priv := mustKey(t)
	storeReq := NewSignedStoreRequest(priv, []byte("hello"))
	envelope := NewPeerStoreRequestMessage("alice", storeReq)
	raw, _ := envelope.Serialize()
	got, _ := DeserializeMessage(raw)
	parsed, err := got.GetStoreRequest()
	if err != nil {
		t.Fatalf("GetStoreRequest: %v", err)
	}
	if err := parsed.Verify(); err != nil {
		t.Errorf("Verify after envelope round-trip: %v", err)
	}

	ack := NewSignedStoreAck(priv, storeReq.Hash)
	ackEnv := NewPeerStoreAckMessage("alice", ack)
	raw, _ = ackEnv.Serialize()
	got, _ = DeserializeMessage(raw)
	parsedAck, err := got.GetStoreAck()
	if err != nil {
		t.Fatalf("GetStoreAck: %v", err)
	}
	if err := parsedAck.Verify(); err != nil {
		t.Errorf("Verify ack: %v", err)
	}

	getReq := &GetRequest{Hash: storeReq.Hash}
	getEnv := NewPeerGetRequestMessage("alice", getReq)
	raw, _ = getEnv.Serialize()
	got, _ = DeserializeMessage(raw)
	parsedGet, err := got.GetGetRequest()
	if err != nil {
		t.Fatalf("GetGetRequest: %v", err)
	}
	if string(parsedGet.Hash) != string(storeReq.Hash) {
		t.Errorf("hash round-trip mismatch")
	}

	getResp := &GetResponse{Hash: storeReq.Hash, Data: []byte("data")}
	getRespEnv := NewPeerGetResponseMessage("alice", getResp)
	raw, _ = getRespEnv.Serialize()
	got, _ = DeserializeMessage(raw)
	parsedResp, err := got.GetGetResponse()
	if err != nil {
		t.Fatalf("GetGetResponse: %v", err)
	}
	if string(parsedResp.Data) != "data" {
		t.Errorf("data round-trip: %q", parsedResp.Data)
	}

	delReq := NewSignedDeleteRequest(priv, storeReq.Hash)
	delEnv := NewPeerDeleteRequestMessage("alice", delReq)
	raw, _ = delEnv.Serialize()
	got, _ = DeserializeMessage(raw)
	parsedDel, err := got.GetDeleteRequest()
	if err != nil {
		t.Fatalf("GetDeleteRequest: %v", err)
	}
	if err := parsedDel.Verify(); err != nil {
		t.Errorf("Verify delete: %v", err)
	}

	mu := PeerManifestUpdateData{}
	muEnv := NewPeerManifestUpdateMessage("alice", mu)
	raw, _ = muEnv.Serialize()
	got, _ = DeserializeMessage(raw)
	if _, err := got.GetManifestUpdate(); err != nil {
		t.Errorf("GetManifestUpdate: %v", err)
	}

	gmEnv := NewPeerGetMembersMessage("alice")
	raw, _ = gmEnv.Serialize()
	got, _ = DeserializeMessage(raw)
	if got.Type != PeerGetMembers {
		t.Errorf("type = %s, want PeerGetMembers", got.Type)
	}
}
