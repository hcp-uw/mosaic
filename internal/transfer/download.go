package transfer

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
	"github.com/hcp-uw/mosaic/internal/cli/shared"
	"github.com/hcp-uw/mosaic/internal/encoding"
	"github.com/hcp-uw/mosaic/internal/p2p"
)

// FetchFileBytes reconstructs a file from locally stored shards. If shards
// are missing and a P2P client + shard-holder lookup are provided, it requests
// the missing shards from peers and waits up to 60 s for reconstruction.
func FetchFileBytes(filename string, client *p2p.Client, getHolders func(contentHash string, shardIndex int) []string) ([]byte, error) {
	meta := FindShardMeta(filename)
	if meta == nil {
		return nil, fmt.Errorf("shards for %q not found — file may not have been received yet", filename)
	}

	missing := missingDataShards(meta.FileHash, meta.TotalDataShards)

	if len(missing) > 0 && client != nil && getHolders != nil {
		if !client.IsPeerCommunicationAvailable() {
			return nil, fmt.Errorf("no peers connected to request shards for %q", filename)
		}

		sign := api.NewSignature(client.GetID())

		// Arm download-progress counters so the CLI can show a progress bar.
		SetDownloadTarget(meta.FileHash, len(missing))
		defer ClearDownloadTarget()

		// Register file-ready channel before requesting any shards so we
		// don't miss a signal that fires while we're still in the loop.
		fileCh := make(chan struct{}, 1)
		fileReadyChans.Store(meta.FileHash, fileCh)
		defer fileReadyChans.Delete(meta.FileHash)

		// Request missing shards one at a time, mirroring the upload path's
		// sequential-per-peer delivery (semaphore = 1 for 1 peer). Requesting
		// all shards simultaneously causes the holder to launch N concurrent
		// StreamShardToPeer goroutines that fight each other for bandwidth.
		const (
			// How long to wait before the first chunk of a shard arrives.
			// A lost request or unreachable peer shows up quickly — 10 s is
			// plenty for any connection faster than ~8 Kbps.
			shardFirstChunkTimeout = 10 * time.Second
			// How long to wait between consecutive chunks once a shard is
			// actively streaming. Resets on every incoming chunk of that shard.
			shardIdleTimeout = 30 * time.Second
			shardMaxRetries  = 2
		)

		for _, idx := range missing {
			if downloadCancelled.Load() {
				return nil, fmt.Errorf("download cancelled")
			}
			// The shard may have arrived from join redistribution while we were
			// waiting for earlier shards — skip if it's already on disk.
			shardPath := filepath.Join(ShardsDir(), meta.FileHash,
				fmt.Sprintf("shard%d_%s.dat", idx, meta.FileHash))
			if _, err := os.Stat(shardPath); err == nil {
				continue
			}

			holders := getHolders(meta.FileHash, idx)
			if len(holders) == 0 {
				fmt.Printf("[Transfer] No known holders for shard %d of %s — broadcasting to all peers\n", idx, filename)
			} else {
				fmt.Printf("[Transfer] Requesting shard %d of %s from %d known holder(s)\n", idx, filename, len(holders))
			}

			received := false
			for attempt := 0; attempt <= shardMaxRetries && !received; attempt++ {
				// Shard may have arrived via join redistribution or a concurrent
				// assembly between attempts — skip re-requesting if it's on disk.
				if _, err := os.Stat(shardPath); err == nil {
					received = true
					break
				}
				if attempt > 0 {
					fmt.Printf("[Transfer] Shard %d of %s — retry %d/%d\n", idx, filename, attempt, shardMaxRetries)
				}

				shardKey := fmt.Sprintf("%s:%d", meta.FileHash, idx)
				shardCh := make(chan struct{}, 1)
				shardReadyChans.Store(shardKey, shardCh)

				// Register an activity channel so HandleBinaryShardChunk can
				// reset the idle timer immediately on each chunk arrival.
				activityCh := make(chan struct{}, 1)
				shardActivityChans.Store(shardKey, activityCh)

				msg := api.NewShardRequestMessage(sign, api.ShardRequestData{
					FileHash:   meta.FileHash,
					ShardIndex: idx,
				})
				_ = client.SendToAllPeers(msg)

				// Start with the short first-chunk timeout; reset to the longer
				// idle timeout on every incoming chunk of this shard.
				idleTimer := time.NewTimer(shardFirstChunkTimeout)

			waitShard:
				for {
					select {
					case <-shardCh:
						received = true
						break waitShard
					case <-activityCh:
						idleTimer.Reset(shardIdleTimeout)
					case <-idleTimer.C:
						shardReadyChans.Delete(shardKey)
						break waitShard
					}
				}
				idleTimer.Stop()
				shardActivityChans.Delete(shardKey)
			}

			if !received {
				return nil, fmt.Errorf("shard %d of %q timed out after %d retries", idx, filename, shardMaxRetries)
			}
		}

		// All data shards are on disk — wait for autoReconstruct to write the file.
		// Check first in case reconstruction already completed during the loop.
		destPath := filepath.Join(shared.MosaicDir(), filename)
		if _, err := os.Stat(destPath); err == nil {
			return os.ReadFile(destPath)
		}
		select {
		case <-fileCh:
			return os.ReadFile(destPath)
		case <-time.After(30 * time.Second):
			return nil, fmt.Errorf("reconstruction of %q timed out", filename)
		}
	}

	// Guard against reaching the local-decode path when shards are still missing.
	// This happens when the caller has no P2P client (e.g. after logout without rejoining).
	// Falling through would produce a confusing "too few shards given" error.
	if len(missing) > 0 {
		return nil, fmt.Errorf("%d/%d data shards missing for %q — run 'mos join' to connect to the network and fetch them from peers",
			len(missing), meta.TotalDataShards, filename)
	}

	// All shards present locally — decrypt to a temp dir then reconstruct.
	key, err := shardEncryptionKey()
	if err != nil {
		return nil, fmt.Errorf("cannot derive shard key: %w", err)
	}

	plainDir, err := os.MkdirTemp("", "mosaic-plain-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(plainDir)
	if _, err := decryptShardsToDir(meta.FileHash, meta.TotalShards, key, plainDir); err != nil {
		return nil, fmt.Errorf("decrypt shards: %w", err)
	}

	outDir, err := os.MkdirTemp("", "mosaic-fetch-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(outDir)

	enc, err := encoding.NewEncoder(meta.TotalDataShards, meta.TotalShards-meta.TotalDataShards, outDir, plainDir)
	if err != nil {
		return nil, fmt.Errorf("encoder init: %w", err)
	}
	// Use stored block size if available; fall back to computing from file size.
	blockSize := encoding.ComputeBlockSize(meta.FileSize, meta.TotalDataShards)
	if meta.BlockSize > 0 {
		blockSize = meta.BlockSize
	}
	enc.SetBlockSize(blockSize)
	if err := enc.DecodeShards(meta.FileHash, meta.FileSize); err != nil {
		return nil, fmt.Errorf("decode failed: %w", err)
	}

	matches, _ := filepath.Glob(filepath.Join(outDir, meta.FileHash+"*"))
	if len(matches) == 0 {
		return nil, fmt.Errorf("reconstructed file not found in %s", outDir)
	}
	return os.ReadFile(matches[0])
}

// DeleteLocalShards removes the shard directory for contentHash from the local
// shard store. Called when the file owner broadcasts a ShardDelete message.
func DeleteLocalShards(contentHash string) {
	shardDir := filepath.Join(ShardsDir(), contentHash)
	if err := os.RemoveAll(shardDir); err != nil {
		fmt.Printf("[Transfer] DeleteLocalShards: could not remove shards for %s: %v\n", contentHash, err)
		return
	}
	fmt.Printf("[Transfer] Deleted local shards for %s\n", contentHash)
}
