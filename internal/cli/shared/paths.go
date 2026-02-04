// internal/daemon/shared/config.go (or wherever your SocketPath is defined)
package shared

import (
	"os"
	"path/filepath"
	"runtime"
)

var SocketPath string
var portFile string

func init() {
	if runtime.GOOS == "windows" {
		// Windows doesn't use socket path
		SocketPath = ""
	} else {
		// Unix systems use socket file
		SocketPath = "/tmp/mosaicd.sock"
	}
}

// GetPortFile returns the path to the port file for Windows TCP connections
func GetPortFile() string {
	if portFile == "" {
		if runtime.GOOS == "windows" {
			tempDir := os.Getenv("TEMP")
			if tempDir == "" {
				tempDir = os.TempDir()
			}
			portFile = filepath.Join(tempDir, "mosaicd.port")
		} else {
			// Not used on Unix systems
			portFile = "/tmp/mosaicd.port"
		}
	}
	return portFile
}