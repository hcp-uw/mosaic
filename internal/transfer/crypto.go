package transfer

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/hcp-uw/mosaic/internal/cli/shared"
)

// shardEncryptionKey loads the 32-byte AES-256 shard key cached at login time.
// The key is derived from the login key (HKDF-SHA256, info="mosaic-shard-key")
// and written to ~/.mosaic-shard.key during login so the raw login key is never
// stored on disk.
func shardEncryptionKey() ([32]byte, error) {
	var key [32]byte
	data, err := os.ReadFile(shared.ShardKeyPath())
	if err != nil {
		return key, fmt.Errorf("not logged in — run 'mos login <key>'")
	}
	if len(data) != 32 {
		return key, fmt.Errorf("shard key file is corrupt (expected 32 bytes, got %d)", len(data))
	}
	copy(key[:], data)
	return key, nil
}

func encryptChunk(key [32]byte, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// shardChunkNonce derives a deterministic 12-byte AES-GCM nonce for a specific
// shard chunk from its coordinates. Every (fileHash, shardIndex, chunkIndex)
// triple identifies a unique plaintext (fileHash fixes the file bytes, hence the
// shard bytes, hence the chunk bytes) under the user's single shard key, so a
// coordinate-derived nonce is never reused across two different plaintexts — the
// only condition AES-GCM requires. Determinism is what makes every node's copy
// of a shard byte-identical, which in turn lets storage proofs (probe.go) compare
// SHA-256 hashes across holders and lets repair (repair.go) regenerate a missing
// shard with exactly the same bytes the original had.
func shardChunkNonce(fileHash string, shardIndex, chunkIndex int) []byte {
	h := sha256.New()
	h.Write([]byte("mosaic-shard-nonce\x00"))
	h.Write([]byte(fileHash))
	var coords [8]byte
	binary.LittleEndian.PutUint32(coords[0:4], uint32(shardIndex))
	binary.LittleEndian.PutUint32(coords[4:8], uint32(chunkIndex))
	h.Write(coords[:])
	return h.Sum(nil)[:12] // AES-GCM standard nonce size
}

// encryptChunkDeterministic encrypts one shard chunk with a coordinate-derived
// nonce (see shardChunkNonce) so the ciphertext is identical on every node that
// encrypts the same chunk. Output format matches encryptChunk: [nonce][ct+tag].
func encryptChunkDeterministic(key [32]byte, fileHash string, shardIndex, chunkIndex int, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := shardChunkNonce(fileHash, shardIndex, chunkIndex)
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func decryptChunk(key [32]byte, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(data) < gcm.NonceSize() {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ct := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ct, nil)
}

// ──────────────────────────────────────────────────────────
// Encrypted shard file I/O
//
// On-disk format (all integers little-endian):
//
//	[4 bytes] totalChunks
//	for each chunk:
//	  [4 bytes] chunk length
//	  [N bytes] AES-GCM encrypted chunk data
//
// ──────────────────────────────────────────────────────────

// writeEncryptedShardFile writes pre-encrypted chunks to a shard file.
func writeEncryptedShardFile(path string, chunks [][]byte) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(len(chunks)))
	if _, err := f.Write(hdr[:]); err != nil {
		return err
	}
	for _, chunk := range chunks {
		var lenBuf [4]byte
		binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(chunk)))
		if _, err := f.Write(lenBuf[:]); err != nil {
			return err
		}
		if _, err := f.Write(chunk); err != nil {
			return err
		}
	}
	return nil
}

// decryptShardToPlaintext reads a length-prefixed encrypted shard file and
// returns the concatenated plaintext.
func decryptShardToPlaintext(path string, key [32]byte) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var hdr [4]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return nil, fmt.Errorf("read totalChunks: %w", err)
	}
	totalChunks := int(binary.LittleEndian.Uint32(hdr[:]))

	var plain []byte
	for i := 0; i < totalChunks; i++ {
		var lenBuf [4]byte
		if _, err := io.ReadFull(f, lenBuf[:]); err != nil {
			return nil, fmt.Errorf("read chunk %d len: %w", i, err)
		}
		n := int(binary.LittleEndian.Uint32(lenBuf[:]))
		encrypted := make([]byte, n)
		if _, err := io.ReadFull(f, encrypted); err != nil {
			return nil, fmt.Errorf("read chunk %d data: %w", i, err)
		}
		dec, err := decryptChunk(key, encrypted)
		if err != nil {
			return nil, fmt.Errorf("decrypt chunk %d: %w", i, err)
		}
		plain = append(plain, dec...)
	}
	return plain, nil
}

// encryptAndStoreShardFile reads srcPath in chunkSizeOnDisk windows, encrypts each
// chunk with a deterministic per-chunk nonce (see shardChunkNonce), and writes the
// length-prefixed encrypted chunks to dstPath in the same on-disk format as
// writeEncryptedShardFile. 8 KB chunks reduce the per-shard frame count ~7× vs the
// 1200-byte UDP wire size while remaining safely below the 65507-byte UDP datagram
// limit for the serve fallback path.
//
// fileHash and shardIndex are the shard's coordinates: with chunkIndex they select
// the deterministic nonce, so this file is byte-identical to what any other node
// produces (or forwards over the wire) for the same shard — the invariant storage
// proofs and repair depend on.
func encryptAndStoreShardFile(srcPath, dstPath string, key [32]byte, fileHash string, shardIndex int) error {
	info, err := os.Stat(srcPath)
	if err != nil {
		return err
	}
	totalChunks := int((info.Size() + chunkSizeOnDisk - 1) / chunkSizeOnDisk)

	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(totalChunks))
	if _, err := dst.Write(hdr[:]); err != nil {
		return err
	}

	buf := make([]byte, chunkSizeOnDisk)
	for chunkIndex := 0; ; chunkIndex++ {
		n, err := io.ReadFull(src, buf)
		if n > 0 {
			encrypted, eerr := encryptChunkDeterministic(key, fileHash, shardIndex, chunkIndex, buf[:n])
			if eerr != nil {
				return eerr
			}
			var lenBuf [4]byte
			binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(encrypted)))
			if _, err := dst.Write(lenBuf[:]); err != nil {
				return err
			}
			if _, err := dst.Write(encrypted); err != nil {
				return err
			}
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// encryptShardFileToChunks reads a plaintext shard file and returns AES-GCM
// encrypted slices, one per chunkSize window.
func encryptShardFileToChunks(path string, key [32]byte) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var chunks [][]byte
	buf := make([]byte, chunkSize)
	for {
		n, err := io.ReadFull(f, buf)
		if n > 0 {
			enc, eerr := encryptChunk(key, buf[:n])
			if eerr != nil {
				return nil, eerr
			}
			chunks = append(chunks, enc)
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return chunks, nil
}

// decryptShardsToDir decrypts all locally stored shards for fileHash into
// destDir/fileHash/ as flat plaintext files ready for the RS decoder.
// Missing shards are skipped — RS handles them as erasures.
// Returns the number of shards successfully decrypted.
func decryptShardsToDir(fileHash string, totalShards int, key [32]byte, destDir string) (int, error) {
	shardDir := filepath.Join(ShardsDir(), fileHash)
	outDir := filepath.Join(destDir, fileHash)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return 0, err
	}
	decrypted := 0
	for i := 0; i < totalShards; i++ {
		src := filepath.Join(shardDir, fmt.Sprintf("shard%d_%s.dat", i, fileHash))
		if _, err := os.Stat(src); err != nil {
			continue
		}
		plain, err := decryptShardToPlaintext(src, key)
		if err != nil {
			continue // wrong key or corrupt — skip silently
		}
		dst := filepath.Join(outDir, fmt.Sprintf("shard%d_%s.dat", i, fileHash))
		if err := os.WriteFile(dst, plain, 0644); err != nil {
			return decrypted, err
		}
		decrypted++
	}
	return decrypted, nil
}
