package handlers

import "github.com/hcp-uw/mosaic/internal/transfer"

// FetchFileBytes reconstructs a file's raw bytes from its distributed shards
// using Reed-Solomon erasure coding.
func FetchFileBytes(filename string) ([]byte, error) {
	return transfer.FetchFileBytes(filename)
}
