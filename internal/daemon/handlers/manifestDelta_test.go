package handlers

import (
	"testing"

	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
)

func testManifest() filesystem.NetworkManifest {
	return filesystem.NetworkManifest{
		Version: 4,
		Users: map[int]*filesystem.UserState{
			7: {
				UserID:    7,
				Username:  "alice",
				PublicKey: []byte("alice-pub"),
				Records: map[string]*filesystem.FileRecord{
					"h1": {ContentHash: "h1", Seq: 1},
					"h2": {ContentHash: "h2", Seq: 2},
				},
			},
		},
		ShardMap: map[string]*filesystem.ShardLocations{
			"h1": {Holders: map[int][]string{0: {"node-x"}}},
		},
	}
}

// TestManifestDelta covers the core delta-sync logic: first send is full, a peer
// that has seen everything gets an empty record delta, a newer record produces a
// one-record delta, and the ShardMap + user public key always travel.
func TestManifestDelta(t *testing.T) {
	peer := "delta-test-peer"
	forgetManifestPeer(peer)
	defer forgetManifestPeer(peer)

	m := testManifest()

	// 1. First contact (nothing seen) → full record set, ShardMap + pubkey present.
	delta, hasNew := manifestDeltaFor(peer, m)
	if !hasNew {
		t.Fatal("first delta should contain records")
	}
	if len(delta.Users[7].Records) != 2 {
		t.Fatalf("first delta should have both records, got %d", len(delta.Users[7].Records))
	}
	if len(delta.ShardMap) != 1 {
		t.Error("delta must carry the full ShardMap")
	}
	if string(delta.Users[7].PublicKey) != "alice-pub" {
		t.Error("delta must carry the user public key for signature verification")
	}

	// 2. Mark it sent → a re-send now has no newer records (but still the ShardMap).
	noteManifestSent(peer, m)
	delta2, hasNew2 := manifestDeltaFor(peer, m)
	if hasNew2 || len(delta2.Users) != 0 {
		t.Errorf("after noteManifestSent there should be no newer records, got %d user(s)", len(delta2.Users))
	}
	if len(delta2.ShardMap) != 1 {
		t.Error("ShardMap is always sent in full, even in an empty-record delta")
	}

	// 3. A NEW file at Seq 1 (a fresh upload) — even though another file already
	//    reached Seq 2 — must still appear in the delta. This is the regression:
	//    per-file Seq tracking, not a per-user max (which would exclude Seq 1 <= 2).
	m.Users[7].Records["h3"] = &filesystem.FileRecord{ContentHash: "h3", Seq: 1}
	delta3, hasNew3 := manifestDeltaFor(peer, m)
	if !hasNew3 {
		t.Fatal("a newly-added file (Seq 1) must produce a delta even if another file is at a higher Seq")
	}
	if len(delta3.Users[7].Records) != 1 || delta3.Users[7].Records["h3"] == nil {
		t.Fatalf("delta should contain only the new file h3, got %v", delta3.Users[7].Records)
	}

	// 4. A newer version of an existing file (Seq bump) is included; unchanged ones aren't.
	noteManifestSent(peer, m)
	m.Users[7].Records["h1"] = &filesystem.FileRecord{ContentHash: "h1", Seq: 2} // h1 was Seq 1
	delta4, _ := manifestDeltaFor(peer, m)
	if len(delta4.Users[7].Records) != 1 || delta4.Users[7].Records["h1"] == nil {
		t.Fatalf("delta should contain only the bumped record h1, got %v", delta4.Users[7].Records)
	}

	// 5. Forgetting the peer (reconnect) resets to a full sync.
	noteManifestSent(peer, m)
	forgetManifestPeer(peer)
	delta5, _ := manifestDeltaFor(peer, m)
	if len(delta5.Users[7].Records) != 3 {
		t.Fatalf("after forgetting the peer the next delta must be full (3 records), got %d", len(delta5.Users[7].Records))
	}
}
