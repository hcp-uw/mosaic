package transfer

import (
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hcp-uw/mosaic/internal/cli/shared"
)

const (
	DataShards   = 10
	ParityShards = 4
	TotalShards  = DataShards + ParityShards

	// chunkSize is the UDP send unit. Keeping it at 8 KB limits IP fragmentation
	// on lossy paths (WiFi, TURN relay) where one dropped fragment kills the chunk.
	// QUIC handles reliability internally, so chunkSizeQUIC can be much larger —
	// 256 KB = 4 chunks per 1 MB shard vs 128, cutting per-shard overhead 32×.
	chunkSize     = 8 * 1024
	chunkSizeQUIC = 256 * 1024

	// binaryMagic is the first byte of every binary shard frame.
	// JSON messages always start with '{' (0x7B) so 0x01 is unambiguous.
	binaryMagic byte = 0x01
)

// ShardMeta is stored alongside each shard set so FetchFileBytes can look up
// fileHash and fileSize from just a filename, without consulting the manifest.
type ShardMeta struct {
	// FileName and FileSize are set only by the file owner (written during upload
	// or by EnsureShardMeta). Nodes storing shards for other users leave these
	// empty — they only need the RS parameters to serve the shard.
	FileName string `json:"fileName,omitempty"`
	FileSize int    `json:"fileSize,omitempty"`

	FileHash        string `json:"fileHash"`
	TotalDataShards int    `json:"totalDataShards"`
	TotalShards     int    `json:"totalShards"`
	BlockSize       int    `json:"blockSize"` // shard block size used during encoding
}

type shardAssembly struct {
	mu              sync.Mutex
	chunks          map[int][]byte
	totalChunks     int
	fileName        string
	fileHash        string
	fileSize        int
	shardIndex      int
	totalDataShards int
	totalShards     int
	firstChunkAt    time.Time // when the first chunk arrived; used for per-shard timing logs
}

var (
	assemblyMu    sync.Mutex
	assemblies    = make(map[string]*shardAssembly) // key: "fileHash:shardIndex"
	reconstructed sync.Map // fileHash → true; prevents duplicate reconstruction

	// shardStoredCb is called after a shard is successfully written to disk.
	// The daemon registers this to update the network manifest and broadcast.
	shardStoredCb   func(contentHash string, shardIndex int)
	shardStoredCbMu sync.Mutex

	// shardSentCb is called after a shard is successfully sent to a peer.
	// The daemon registers this to record the peer as a shard holder immediately,
	// without waiting for the receiver to assemble and report back.
	shardSentCb   func(contentHash string, shardIndex int, peerID string)
	shardSentCbMu sync.Mutex

	// fileReadyChans allows FetchFileBytes to wait for autoReconstruct to finish.
	fileReadyChans sync.Map // fileHash → chan struct{}

	// shardReadyChans allows FetchFileBytes to wait for a specific shard.
	// key: "fileHash:shardIndex" → chan struct{}
	shardReadyChans sync.Map

	// shardRelayCallback is called when a shard arrives that was originally
	// requested by a different peer (relay scenario). The daemon registers this
	// so it can forward the shard to the original requester via binary streaming.
	shardRelayCallback   func(fileHash string, shardIndex int, requesterIDs []string)
	shardRelayCallbackMu sync.Mutex

	// pendingShardRequests tracks shards we are relaying on behalf of other peers.
	// Key: "fileHash:shardIndex" → list of requester P2P IDs awaiting that shard.
	pendingShardRequestsMu sync.Mutex
	pendingShardRequests   = make(map[string][]string)

	// uploadProgress tracks shard distribution progress for the current upload.
	// shardsDispatched is incremented each time a shard is stored or sent.
	// shardsTotal is set at the start of UploadFile.
	uploadShardsDispatched atomic.Int32
	uploadShardsTotal      atomic.Int32

	// joinSync tracks shards received from peers after a network join.
	// Used by the CLI to block until redistribution settles.
	joinShardsReceived        atomic.Int32
	joinLastShardActivityNano atomic.Int64 // Unix nanoseconds; 0 = no activity yet

	// downloadProgress tracks shard fetching during a FetchFileBytes call.
	downloadShardsNeeded   atomic.Int32
	downloadShardsReceived atomic.Int32
	downloadTargetHash     string
	downloadTargetMu       sync.Mutex

	// lastChunkReceivedNano is updated on every incoming shard chunk so that
	// FetchFileBytes can use an idle timeout instead of a fixed deadline.
	lastChunkReceivedNano atomic.Int64

	// shardLastChunkNano tracks the last chunk arrival time per shard so the
	// per-shard idle timer in FetchFileBytes resets only when chunks for THAT
	// specific shard arrive, not when unrelated shards of other files arrive.
	// key: "fileHash:shardIndex" → Unix nanoseconds (int64)
	shardLastChunkNano sync.Map

	// shardActivityChans lets FetchFileBytes reset its idle timer immediately
	// on each chunk arrival instead of polling every 5 seconds.
	// key: "fileHash:shardIndex" → chan struct{}
	shardActivityChans sync.Map

	// shardAckChans allows the sender to block after ShardStreamDone until the
	// receiver's ShardStreamAck arrives with the list of missing chunk indices.
	// key: "fileHash:shardIndex:peerID" → chan []int
	shardAckChans sync.Map

	// finalizingShards tracks shards whose last chunk has arrived but whose shard
	// file is still being written to disk by finalizeShard. HandleShardStreamDone
	// checks this so it doesn't falsely report "all chunks missing" during the
	// window between assembly deletion and file write completion.
	// key: "fileHash:shardIndex" → struct{}
	finalizingShards sync.Map

	// inProgressServes prevents duplicate concurrent StreamShardToPeer goroutines
	// for the same (fileHash, shardIndex, peerID) triple. Without this, a slow ack
	// timeout on the receiver causes it to re-request while the original serve
	// goroutine is still running, producing cascading duplicate streams.
	// key: "fileHash:shardIndex:peerID" → struct{}
	inProgressServes sync.Map

	// uploadCancelled is set by CancelUpload to stop the shard dispatch loop.
	uploadCancelled atomic.Bool

	// downloadCancelled is set by CancelDownload to break out of FetchFileBytes.
	downloadCancelled atomic.Bool
)

// CancelUpload signals the active upload to stop at its next shard boundary.
func CancelUpload() { uploadCancelled.Store(true) }

// CancelDownload signals the active download to stop at its next shard boundary.
func CancelDownload() { downloadCancelled.Store(true) }

// ResetCancelFlags clears both cancel flags so the next op starts clean.
func ResetCancelFlags() {
	uploadCancelled.Store(false)
	downloadCancelled.Store(false)
}

// SetShardRelayCallback registers a callback invoked when a shard arrives that
// had pending relay requesters. The daemon uses this to forward the shard to
// the original requester(s) via binary streaming.
func SetShardRelayCallback(fn func(fileHash string, shardIndex int, requesterIDs []string)) {
	shardRelayCallbackMu.Lock()
	shardRelayCallback = fn
	shardRelayCallbackMu.Unlock()
}

// ShortPeer extracts just the IP address from a peer ID that is in "ip:port"
// form (as assigned by the STUN server). Falls back to the full ID when it
// can't be parsed, so logs are never empty but also never misleadingly
// truncated (the old peerID[:8] cut "205.175.106.5:51005" to "205.175.").
func ShortPeer(peerID string) string {
	if host, _, err := net.SplitHostPort(peerID); err == nil {
		return host
	}
	return peerID
}

// shortPeer is the package-local alias.
func shortPeer(peerID string) string { return ShortPeer(peerID) }

func registerPendingShardRequest(fileHash string, shardIndex int, requesterID string) {
	key := fmt.Sprintf("%s:%d", fileHash, shardIndex)
	pendingShardRequestsMu.Lock()
	pendingShardRequests[key] = append(pendingShardRequests[key], requesterID)
	pendingShardRequestsMu.Unlock()
}

func takePendingShardRequesters(fileHash string, shardIndex int) []string {
	key := fmt.Sprintf("%s:%d", fileHash, shardIndex)
	pendingShardRequestsMu.Lock()
	ids := pendingShardRequests[key]
	delete(pendingShardRequests, key)
	pendingShardRequestsMu.Unlock()
	return ids
}

// SetShardStoredCallback registers a function that is called (in a goroutine)
// each time a shard is fully assembled and written to disk. The daemon uses
// this to record shard ownership in the network manifest and broadcast.
func SetShardStoredCallback(fn func(contentHash string, shardIndex int)) {
	shardStoredCbMu.Lock()
	shardStoredCb = fn
	shardStoredCbMu.Unlock()
}

// SetShardSentCallback registers a function that is called after a shard is
// successfully sent to a peer. The daemon uses this to record the peer as a
// holder immediately, without waiting for the peer's assembly to complete.
func SetShardSentCallback(fn func(contentHash string, shardIndex int, peerID string)) {
	shardSentCbMu.Lock()
	shardSentCb = fn
	shardSentCbMu.Unlock()
}

// GetUploadProgress returns the number of shards dispatched so far and the
// total shards for the current upload. Both are 0 when no upload is running.
func GetUploadProgress() (dispatched, total int) {
	return int(uploadShardsDispatched.Load()), int(uploadShardsTotal.Load())
}

// resetUploadProgress initialises the counters for a new upload.
func resetUploadProgress(total int) {
	uploadShardsDispatched.Store(0)
	uploadShardsTotal.Store(int32(total))
}

// ResetJoinSync clears the join-sync counters at the start of a new join.
func ResetJoinSync() {
	joinShardsReceived.Store(0)
	joinLastShardActivityNano.Store(0)
}

// GetJoinShardActivity returns how many shards have been received from peers
// since the last join, and the Unix-nanosecond timestamp of the last one.
func GetJoinShardActivity() (received int, lastNano int64) {
	return int(joinShardsReceived.Load()), joinLastShardActivityNano.Load()
}

// SetDownloadTarget arms the download-progress counters for a FetchFileBytes call.
func SetDownloadTarget(fileHash string, needed int) {
	downloadTargetMu.Lock()
	downloadTargetHash = fileHash
	downloadTargetMu.Unlock()
	downloadShardsNeeded.Store(int32(needed))
	downloadShardsReceived.Store(0)
}

// ClearDownloadTarget disarms the download-progress counters after a fetch.
func ClearDownloadTarget() {
	downloadTargetMu.Lock()
	downloadTargetHash = ""
	downloadTargetMu.Unlock()
	downloadShardsNeeded.Store(0)
	downloadShardsReceived.Store(0)
}

// GetDownloadProgress returns (received, needed) for the active FetchFileBytes call.
func GetDownloadProgress() (received, needed int) {
	return int(downloadShardsReceived.Load()), int(downloadShardsNeeded.Load())
}

// TransferDiagnostics is a point-in-time snapshot of transfer-layer state,
// used by the mos debug transfer command to produce per-tick log lines.
type TransferDiagnostics struct {
	ActiveAssemblies int           // shards currently being assembled from incoming chunks
	PendingAcks      int           // shard sends currently blocked waiting for ShardStreamAck
	UDPPacerInterval time.Duration // current inter-chunk sleep on the UDP fallback path
	UploadDispatched int           // shards dispatched in the active upload (0 if idle)
	UploadTotal      int           // total shards in the active upload (0 if idle)
}

// GetTransferDiagnostics returns a snapshot of current transfer-layer state.
func GetTransferDiagnostics() TransferDiagnostics {
	assemblyMu.Lock()
	activeAsm := len(assemblies)
	assemblyMu.Unlock()

	pendingAcks := 0
	shardAckChans.Range(func(_, _ any) bool { pendingAcks++; return true })

	udpPacer.mu.Lock()
	pacerInterval := udpPacer.interval
	udpPacer.mu.Unlock()

	dispatched, total := GetUploadProgress()
	return TransferDiagnostics{
		ActiveAssemblies: activeAsm,
		PendingAcks:      pendingAcks,
		UDPPacerInterval: pacerInterval,
		UploadDispatched: dispatched,
		UploadTotal:      total,
	}
}

// testShardsDir is overridden in tests to redirect shard I/O to a temp dir.
var testShardsDir string

// ShardsDir returns ~/Mosaic/.shards — the base directory for all stored shards.
func ShardsDir() string {
	if testShardsDir != "" {
		return testShardsDir
	}
	return filepath.Join(shared.MosaicDir(), ".shards")
}
