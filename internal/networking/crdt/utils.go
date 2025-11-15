package crdt

import (
	"github.com/hcp-uw/mosaic/internal/networking/protocol"
)

// compareSignatures provides deterministic ordering of signatures for conflict resolution
func compareSignatures(sig1, sig2 []byte) int {
	minLen := len(sig1)
	if len(sig2) < minLen {
		minLen = len(sig2)
	}

	for i := 0; i < minLen; i++ {
		if sig1[i] < sig2[i] {
			return -1
		}
		if sig1[i] > sig2[i] {
			return 1
		}
	}

	if len(sig1) < len(sig2) {
		return -1
	}
	if len(sig1) > len(sig2) {
		return 1
	}
	return 0
}

// joinMessagesEqual compares two signed join messages for equality
func joinMessagesEqual(a, b *protocol.SignedJoinMessage) bool {
	if a == nil || b == nil {
		return a == b
	}

	return a.Data.NodeID == b.Data.NodeID &&
		string(a.Data.PublicKey) == string(b.Data.PublicKey) &&
		string(a.PublicKey) == string(b.PublicKey) &&
		string(a.Signature) == string(b.Signature) &&
		a.Timestamp.Equal(b.Timestamp)
}

// storeOperationsEqual compares two store operations for equality
func storeOperationsEqual(a, b *protocol.StoreOperationMessage) bool {
	if a == nil || b == nil {
		return a == b
	}

	return signedStoreRequestsEqual(a.SignedStoreRequest, b.SignedStoreRequest) &&
		signedStoreAcknowledgesEqual(a.SignedStoreAcknowledge, b.SignedStoreAcknowledge)
}

// signedStoreRequestsEqual compares two signed store requests
func signedStoreRequestsEqual(a, b *protocol.SignedStoreRequest) bool {
	if a == nil || b == nil {
		return a == b
	}

	return string(a.Data.DataHash) == string(b.Data.DataHash) &&
		a.Data.Size == b.Data.Size &&
		a.Data.Timestamp.Equal(b.Data.Timestamp) &&
		string(a.PublicKey) == string(b.PublicKey) &&
		string(a.Signature) == string(b.Signature) &&
		a.Timestamp.Equal(b.Timestamp)
}

// signedStoreAcknowledgesEqual compares two signed store acknowledges
func signedStoreAcknowledgesEqual(a, b *protocol.SignedStoreAcknowledge) bool {
	if a == nil || b == nil {
		return a == b
	}

	return string(a.Data.RequestHash) == string(b.Data.RequestHash) &&
		a.Data.Status == b.Data.Status &&
		a.Data.Timestamp.Equal(b.Data.Timestamp) &&
		string(a.PublicKey) == string(b.PublicKey) &&
		string(a.Signature) == string(b.Signature) &&
		a.Timestamp.Equal(b.Timestamp)
}