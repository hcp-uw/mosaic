package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"os/signal"

	"github.com/hcp-uw/mosaic/internal/api"
	"github.com/hcp-uw/mosaic/internal/cli/shared"
	"github.com/hcp-uw/mosaic/internal/encoding"
	"github.com/hcp-uw/mosaic/internal/p2p"
)

const (
	dataShards   = 10
	parityShards = 4
	totalShards  = dataShards + parityShards
	chunkSize    = 4 * 1024 // 4KB raw → ~6KB JSON/base64, under macOS UDP max datagram (9216)
)

// shardAssembly buffers incoming chunks for one shard until all arrive.
type shardAssembly struct {
	mu              sync.Mutex // per-shard lock; avoids global lock contention
	chunks          map[int][]byte
	totalChunks     int
	fileName        string
	fileHash        string
	fileSize        int
	shardIndex      int
	totalDataShards int
	totalShards     int
}

var (
	assemblyMu    sync.Mutex
	assemblies    = make(map[string]*shardAssembly) // key: "fileHash:shardIndex"
	reconstructed sync.Map                          // fileHash → true; prevents duplicate reconstruction

	// sendLimiter is a shared token-bucket across all shard goroutines.
	// Tokens refill at ~20,000/sec — well below the receiver's ~42K/sec drain
	// capacity, so the socket buffer never fills and packets are never dropped.
	sendLimiter = make(chan struct{}, 200)
)

// startSendLimiter refills sendLimiter at 20,000 tokens/sec (200 every 10ms).
// Must be called once before any uploadFile goroutines start.
func startSendLimiter(ctx context.Context) {
	for i := 0; i < 200; i++ {
		sendLimiter <- struct{}{}
	}
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				for i := 0; i < 200; i++ {
					select {
					case sendLimiter <- struct{}{}:
					default: // already full — skip
					}
				}
			}
		}
	}()
}

func main() {
	serverAddr := shared.DefaultSTUNServer
	runClient(serverAddr)
}

func runClient(serverAddr string) {
	config := p2p.DefaultClientConfig(serverAddr, shared.DefaultTURNServer, shared.TURNUsername, shared.TURNPassword)
	client, err := p2p.NewClient(config)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	client.OnStateChange(func(state p2p.ClientState) {
		fmt.Printf("[State] %s\n", state)
	})

	client.OnPeerAssigned(func(peer *p2p.PeerInfo) {
		fmt.Printf("[Peer Assigned] ID: %s, Address: %s\n", peer.ID, peer.Address)
		fmt.Println("Connecting to peer...")
		if err := client.ConnectToPeer(peer); err != nil {
			fmt.Printf("[Error] Failed to connect to peer: %v\n", err)
		}
		fmt.Printf("[Peer Connected] ID: %s, Address: %s\n", peer.ID, peer.Address)
	})

	client.OnError(func(err error) {
		fmt.Printf("[Error] %v\n", err)
	})

	client.OnMessageReceived(func(data []byte) {
		msg, err := api.DeserializeMessage(data)
		if err != nil {
			return
		}
		switch msg.Type {
		case api.ShardChunk:
			go handleShardChunk(msg)
		case api.ShardRequest:
			go handleShardRequest(msg, client)
		}
	})

	fmt.Printf("Connecting to STUN server at %s...\n", serverAddr)
	if err := client.ConnectToStun(); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}

	fmt.Println("Connected! Waiting for peer...")
	fmt.Println("Commands:")
	fmt.Println("  upload <filepath>   — encode into 10 shards and stream to peer")
	fmt.Println("Press Ctrl+C to disconnect.")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startSendLimiter(ctx)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			parts := strings.Fields(line)
			if len(parts) == 0 {
				continue
			}
			switch parts[0] {
			case "upload":
				if len(parts) < 2 {
					fmt.Println("usage: upload <filepath>")
					continue
				}
				if !client.IsPeerCommunicationAvailable() {
					fmt.Println("[Info] Not connected to a peer yet")
					continue
				}
				go uploadFile(parts[1], client)
			default:
				fmt.Println("unknown command. try: upload <filepath>")
			}
		}
	}()

	<-sigChan
	fmt.Println("\nDisconnecting...")
	client.DisconnectFromStun()
}

// uploadFile streams a file to peers as 32KB chunks across 10 data + 4 parity shards.
// Never loads the full file or a full shard into memory.
func uploadFile(path string, client *p2p.Client) {
	filename := filepath.Base(path)
	nameNoExt := strings.TrimSuffix(filename, filepath.Ext(filename))

	// Stream-hash + measure the file without loading it all into memory.
	f, err := os.Open(path)
	if err != nil {
		fmt.Printf("[Upload] Cannot open file: %v\n", err)
		return
	}
	hasher := sha256.New()
	fileSize64, err := io.Copy(hasher, f)
	f.Close()
	if err != nil {
		fmt.Printf("[Upload] Cannot hash file: %v\n", err)
		return
	}
	fileHash := hex.EncodeToString(hasher.Sum(nil))
	fileSize := int(fileSize64)

	fmt.Printf("[Upload] %s  hash=%s…  size=%d bytes\n", filename, fileHash[:12], fileSize)

	outDir, err := os.MkdirTemp("", "mosaic-out-*")
	if err != nil {
		fmt.Printf("[Upload] Cannot create temp dir: %v\n", err)
		return
	}
	defer os.RemoveAll(outDir)

	// Stream-copy original file into outDir (encoder reads from dirOut).
	src, err := os.Open(path)
	if err != nil {
		fmt.Printf("[Upload] Cannot open for copy: %v\n", err)
		return
	}
	dst, err := os.Create(filepath.Join(outDir, filename))
	if err != nil {
		src.Close()
		fmt.Printf("[Upload] Cannot stage file: %v\n", err)
		return
	}
	if _, err := io.Copy(dst, src); err != nil {
		src.Close()
		dst.Close()
		fmt.Printf("[Upload] Copy failed: %v\n", err)
		return
	}
	src.Close()
	dst.Close()

	// Reed-Solomon encode. The encoder streams internally (20MB blocks).
	fmt.Printf("[Upload] Encoding into %d data + %d parity shards...\n", dataShards, parityShards)
	enc, err := encoding.NewEncoder(dataShards, parityShards, outDir, outDir)
	if err != nil {
		fmt.Printf("[Upload] Encoder init failed: %v\n", err)
		return
	}
	if err := enc.EncodeFile(filename); err != nil {
		fmt.Printf("[Upload] Encode failed: %v\n", err)
		return
	}
	fmt.Println("[Upload] Encoding done. Sending shards...")

	sign := api.NewSignature(client.GetID())

	var wg sync.WaitGroup
	var sendErr error
	var sendErrMu sync.Mutex
	// Send all shards in parallel — per-shard receiver mutex prevents assembly contention.
	sem := make(chan struct{}, 1) // sequential — one shard at a time eliminates burst overlap

	for i := 0; i < totalShards; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(shardIdx int) {
			defer func() { <-sem }()
			defer wg.Done()

			shardPath := filepath.Join(outDir, ".bin", filename, fmt.Sprintf("shard%d_%s.dat", shardIdx, nameNoExt))

			info, err := os.Stat(shardPath)
			if err != nil {
				sendErrMu.Lock()
				sendErr = fmt.Errorf("cannot stat shard %d: %w", shardIdx, err)
				sendErrMu.Unlock()
				return
			}
			shardFileSize := info.Size()
			totalChunks := int((shardFileSize + chunkSize - 1) / chunkSize)

			sf, err := os.Open(shardPath)
			if err != nil {
				sendErrMu.Lock()
				sendErr = fmt.Errorf("cannot open shard %d: %w", shardIdx, err)
				sendErrMu.Unlock()
				return
			}
			defer sf.Close()

			buf := make([]byte, chunkSize)
			chunkIndex := 0
			for {
				n, err := io.ReadFull(sf, buf)
				if n > 0 {
					chunk := make([]byte, n)
					copy(chunk, buf[:n])
					msg := api.NewShardChunkMessage(sign, api.ShardChunkData{
						FileHash:        fileHash,
						FileName:        nameNoExt,
						FileSize:        fileSize,
						ShardIndex:      shardIdx,
						ChunkIndex:      chunkIndex,
						TotalChunks:     totalChunks,
						TotalDataShards: dataShards,
						TotalShards:     totalShards,
						Data:            chunk,
					})
					<-sendLimiter // token bucket — caps total send rate at ~60K packets/sec
					if werr := client.SendToAllPeers(msg); werr != nil {
						sendErrMu.Lock()
						sendErr = fmt.Errorf("send failed shard %d chunk %d: %w", shardIdx, chunkIndex, werr)
						sendErrMu.Unlock()
						return
					}
					chunkIndex++
				}
				if err == io.EOF || err == io.ErrUnexpectedEOF {
					break
				}
				if err != nil {
					sendErrMu.Lock()
					sendErr = fmt.Errorf("read error shard %d: %w", shardIdx, err)
					sendErrMu.Unlock()
					return
				}
			}
			fmt.Printf("[Upload] Shard %d/%d sent (%d chunks, %d bytes)\n", shardIdx+1, totalShards, chunkIndex, shardFileSize)
		}(i)
	}

	wg.Wait()
	if sendErr != nil {
		fmt.Printf("[Upload] Failed: %v\n", sendErr)
		return
	}
	fmt.Println("[Upload] All shards sent.")
}

// handleShardChunk stores an incoming chunk and writes the shard to disk once
// all chunks for that shard have arrived.
func handleShardChunk(msg *api.Message) {
	d, err := msg.GetShardChunkData()
	if err != nil {
		fmt.Printf("[Recv] Failed to parse ShardChunk: %v\n", err)
		return
	}

	key := fmt.Sprintf("%s:%d", d.FileHash, d.ShardIndex)

	// Brief global lock — just to get or create the assembly entry.
	assemblyMu.Lock()
	asm, ok := assemblies[key]
	if !ok {
		asm = &shardAssembly{
			chunks:          make(map[int][]byte),
			totalChunks:     d.TotalChunks,
			fileName:        d.FileName,
			fileHash:        d.FileHash,
			fileSize:        d.FileSize,
			shardIndex:      d.ShardIndex,
			totalDataShards: d.TotalDataShards,
			totalShards:     d.TotalShards,
		}
		assemblies[key] = asm
	}
	assemblyMu.Unlock()

	// Per-shard lock — chunks for different shards don't block each other.
	asm.mu.Lock()
	asm.chunks[d.ChunkIndex] = d.Data
	received := len(asm.chunks)
	total := asm.totalChunks
	asm.mu.Unlock()

	if received%100 == 0 || received == total {
		fmt.Printf("[Recv] Shard %d: %d/%d chunks\n", d.ShardIndex, received, total)
	}

	if received == total {
		// All chunks arrived — finalize off the hot path.
		assemblyMu.Lock()
		finalAsm := assemblies[key]
		delete(assemblies, key)
		assemblyMu.Unlock()
		go finalizeShard(finalAsm)
	}
}

// finalizeShard concatenates chunks in order, writes the shard file to disk,
// then triggers reconstruction if enough data shards are present.
func finalizeShard(asm *shardAssembly) {
	shardsBase := filepath.Join(os.Getenv("HOME"), "Mosaic", ".shards")
	shardDir := filepath.Join(shardsBase, asm.fileHash)
	if err := os.MkdirAll(shardDir, 0755); err != nil {
		fmt.Printf("[Assemble] Cannot create shard dir: %v\n", err)
		return
	}

	// Name shards after the fileHash so the decoder's filepath.Base(relativePath)
	// convention matches: decoder expects shard{i}_{filepath.Base(relativePath)}.dat
	shardPath := filepath.Join(shardDir, fmt.Sprintf("shard%d_%s.dat", asm.shardIndex, asm.fileHash))
	sf, err := os.Create(shardPath)
	if err != nil {
		fmt.Printf("[Assemble] Cannot create shard file: %v\n", err)
		return
	}
	for i := 0; i < asm.totalChunks; i++ {
		chunk, ok := asm.chunks[i]
		if !ok {
			sf.Close()
			fmt.Printf("[Assemble] Missing chunk %d for shard %d — shard incomplete\n", i, asm.shardIndex)
			return
		}
		if _, err := sf.Write(chunk); err != nil {
			sf.Close()
			fmt.Printf("[Assemble] Write failed at chunk %d: %v\n", i, err)
			return
		}
	}
	sf.Close()
	fmt.Printf("[Assemble] Shard %d complete → %s\n", asm.shardIndex, shardPath)

	// Count data shards (index 0..totalDataShards-1) now on disk.
	count := 0
	for i := 0; i < asm.totalDataShards; i++ {
		p := filepath.Join(shardDir, fmt.Sprintf("shard%d_%s.dat", i, asm.fileHash))
		if _, err := os.Stat(p); err == nil {
			count++
		}
	}
	if count >= asm.totalDataShards {
		// Only reconstruct once even if more parity shards arrive later.
		if _, already := reconstructed.LoadOrStore(asm.fileHash, true); !already {
			reconstructFile(asm.fileHash, asm.fileSize, asm.totalDataShards, asm.totalShards, shardsBase)
		}
	}
}

// reconstructFile uses the Reed-Solomon decoder to recover the original file from shards.
func reconstructFile(fileHash string, fileSize, totalData, total int, shardsBase string) {
	outDir, err := os.MkdirTemp("", "mosaic-recon-*")
	if err != nil {
		fmt.Printf("[Reconstruct] Cannot create output dir: %v\n", err)
		return
	}

	enc, err := encoding.NewEncoder(totalData, total-totalData, outDir, shardsBase)
	if err != nil {
		fmt.Printf("[Reconstruct] Encoder init failed: %v\n", err)
		return
	}

	fmt.Printf("[Reconstruct] Decoding %s...\n", fileHash[:12])
	if err := enc.DecodeShards(fileHash, fileSize); err != nil {
		fmt.Printf("[Reconstruct] Decode failed: %v\n", err)
		return
	}

	// Decoder writes to outDir/<fileHash>.<ext> — relativePath is just the
	// fileHash with no subdirectory, so filepath.Dir("fileHash") == ".".
	matches, _ := filepath.Glob(filepath.Join(outDir, fileHash+"*"))
	if len(matches) > 0 {
		fmt.Printf("[Reconstruct] File ready: %s\n", matches[0])
	} else {
		fmt.Printf("[Reconstruct] Done — output in %s\n", outDir)
	}
}

// handleShardRequest responds to a peer requesting a stored shard.
func handleShardRequest(msg *api.Message, client *p2p.Client) {
	d, err := msg.GetShardRequestData()
	if err != nil {
		return
	}
	shardsBase := filepath.Join(os.Getenv("HOME"), "Mosaic", ".shards")
	pattern := filepath.Join(shardsBase, d.FileHash, fmt.Sprintf("shard%d_*.dat", d.ShardIndex))
	matches, _ := filepath.Glob(pattern)
	sign := api.NewSignature(client.GetID())

	if len(matches) == 0 {
		_ = client.SendToAllPeers(api.NewShardResponseMessage(sign, api.ShardResponseData{
			FileHash: d.FileHash, ShardIndex: d.ShardIndex, Found: false,
		}))
		return
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		return
	}
	_ = client.SendToAllPeers(api.NewShardResponseMessage(sign, api.ShardResponseData{
		FileHash: d.FileHash, ShardIndex: d.ShardIndex, Found: true, Data: data,
	}))
}
