package main

import (
	"net/http"
	"log"
	"github.com/hcp-uw/mosaic/internal/stun"

)



// when connection manager is made a go rountine starts listening to 
// check for incoming leaders
func main() {
	cm := stun.NewManager()
	mux := http.NewServeMux()

	mux.HandleFunc("/newLeader", func(w http.ResponseWriter, r *http.Request) {
		stun.NewLeader(w, r, cm)
	})

	mux.HandleFunc("/join", func(w http.ResponseWriter, r *http.Request) {
		stun.JoinNode(w, r, cm)
	})

	err := http.ListenAndServe(":6767", mux)
	if err != nil {
		log.Fatal(err)
	}
}



