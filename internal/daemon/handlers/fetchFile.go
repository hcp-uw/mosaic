package handlers

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
)

// FetchFileBytes reconstructs a file's raw bytes from its distributed shards
// across the peer network using Reed-Solomon erasure coding.

// TODO: implement shard discovery, peer collection, and Reed-Solomon decode.
func FetchFileBytes(filename string) ([]byte, error) {
	var testing bool = true

	// this works for videos as well, but is just a placeholder until we implement real shard collection and reconstruction logic.
	if testing {
		fileID := "1OdJSC5rt1dirWtAFr-o6aa5LIRonZQ8c"
		resp, err := http.Get("https://drive.google.com/uc?export=download&id=" + fileID + "&confirm=t")
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		// Read the response body to check for UUID
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}

		// Extract UUID from the HTML
		re := regexp.MustCompile(`uuid["\']?\s*:\s*["\']([a-f0-9\-]+)`)
		matches := re.FindStringSubmatch(string(body))

		var downloadURL string
		if len(matches) > 1 {
			// Large file - use the UUID endpoint
			uuid := matches[1]
			downloadURL = "https://drive.usercontent.google.com/download?id=" + fileID + "&export=download&confirm=t&uuid=" + uuid
		} else {
			// Regular file
			downloadURL = "https://drive.google.com/uc?export=download&id=" + fileID + "&confirm=t"
		}

		// Second request for actual file
		resp2, err := http.Get(downloadURL)
		if err != nil {
			return nil, err
		}
		defer resp2.Body.Close()

		return io.ReadAll(resp2.Body)

	}

	// Placeholder — replace with real shard collection + reconstruction.
	placeholder := fmt.Sprintf("mosaic-placeholder: %s\n", filename)
	return []byte(placeholder), nil
}
