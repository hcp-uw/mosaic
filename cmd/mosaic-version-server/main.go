package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
)

var (
	versionFile = flag.String("file", "/var/run/mosaic/latest-version", "path to the version file")
	port        = flag.Int("port", 8080, "HTTP port")
)

func main() {
	flag.Parse()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(*versionFile)
		if err != nil {
			http.Error(w, "version unavailable", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, strings.TrimSpace(string(data)))
	})

	fmt.Printf("Version server listening on :%d (file: %s)\n", *port, *versionFile)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", *port), nil); err != nil {
		fmt.Fprintf(os.Stderr, "version server: %v\n", err)
		os.Exit(1)
	}
}
