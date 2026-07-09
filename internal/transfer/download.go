package transfer

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
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

		// Reset first-chunk timestamp to now so the "File ready" total time
		// measures the actual fetch duration rather than from whenever the
		// initial push shards arrived (which can be seconds earlier).
		fileFirstChunkNano.Store(meta.FileHash, time.Now().UnixNano())

		// Arm download-progress counters so the CLI can show a progress bar.
		SetDownloadTarget(meta.FileHash, len(missing))
		defer ClearDownloadTarget()

		// Register file-ready channel before requesting any shards so we
		// don't miss a signal that fires while we're still in the loop.
		fileCh := make(chan struct{}, 1)
		fileReadyChans.Store(meta.FileHash, fileCh)
		defer fileReadyChans.Delete(meta.FileHash)

		// Request missing shards with bounded parallelism. QUIC multiplexes
		// streams independently so parallel requests don't fight for bandwidth
		// the way parallel UDP bursts do — each stream gets its fair share of
		// the connection's congestion window.
		const (
			maxParallelShards      = 4
			shardFirstChunkTimeout = 10 * time.Second
			shardIdleTimeout       = 30 * time.Second
			shardMaxRetries        = 2
		)

		type fetchErr struct {
			idx int
			err error
		}
		fetchErrCh := make(chan fetchErr, len(missing))
		sem := make(chan struct{}, maxParallelShards)
		var wg sync.WaitGroup

		for _, idx := range missing {
			idx := idx
			shardPath := filepath.Join(ShardsDir(), meta.FileHash,
				fmt.Sprintf("shard%d_%s.dat", idx, meta.FileHash))
			if _, err := os.Stat(shardPath); err == nil {
				continue // arrived from redistribution while we were setting up
			}

			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()

				holders := getHolders(meta.FileHash, idx)
				if len(holders) == 0 {
					fmt.Printf("[Transfer] No known holders for shard %d of %s — broadcasting to all peers\n", idx, filename)
				} else {
					fmt.Printf("[Transfer] Requesting shard %d of %s from %d known holder(s)\n", idx, filename, len(holders))
				}

				received := false
				for attempt := 0; attempt <= shardMaxRetries && !received; attempt++ {
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

					activityCh := make(chan struct{}, 1)
					shardActivityChans.Store(shardKey, activityCh)

					msg := api.NewShardRequestMessage(sign, api.ShardRequestData{
						FileHash:   meta.FileHash,
						ShardIndex: idx,
					})
					// Targeted-first: on the initial attempt, ask only the known
					// reachable holders (holders holds their P2P ids). On retries, or
					// when no holder is known, broadcast to all peers as a fallback so
					// a stale or incomplete holder list can never block the fetch.
					if len(holders) > 0 && attempt == 0 {
						for _, h := range holders {
							_ = client.SendToPeer(h, msg)
						}
					} else {
						_ = client.SendToAllPeers(msg)
					}

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
					fetchErrCh <- fetchErr{idx, fmt.Errorf("shard %d of %q timed out after %d retries", idx, filename, shardMaxRetries)}
				}
			}()
		}

		wg.Wait()
		close(fetchErrCh)

		if e, ok := <-fetchErrCh; ok {
			return nil, e.err
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
