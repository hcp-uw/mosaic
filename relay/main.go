// Command relay is a message relay server.
//
// Clients connect over TCP. Every line a client sends is forwarded to all other
// connected clients, prefixed with the sender's address. Clients never connect
// to each other directly — the relay is the only thing they talk to.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"net"
	"sync"
)

// hub tracks connected clients and fans out messages between them.
type hub struct {
	mu      sync.Mutex
	clients map[string]net.Conn
}

func newHub() *hub {
	return &hub{clients: make(map[string]net.Conn)}
}

func (h *hub) add(addr string, conn net.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[addr] = conn
}

func (h *hub) remove(addr string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, addr)
}

// broadcast sends line to every client except the sender.
func (h *hub) broadcast(from, line string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for addr, conn := range h.clients {
		if addr == from {
			continue
		}
		if _, err := fmt.Fprintf(conn, "%s: %s\n", from, line); err != nil {
			log.Printf("relay: failed sending to %s: %v", addr, err)
		}
	}
}

func (h *hub) handle(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)

	addr := conn.RemoteAddr().String()

	h.add(addr, conn)
	log.Printf("relay: %q connected", addr)
	defer func() {
		h.remove(addr)
		log.Printf("relay: %q disconnected", addr)
	}()

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = trimLine(line)
		if line == "" {
			continue
		}
		log.Printf("relay: %q -> all: %s", addr, line)
		h.broadcast(addr, line)
	}
}

// trimLine strips a trailing \n and \r from a line.
func trimLine(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

func main() {
	addr := flag.String("addr", ":9000", "address to listen on")
	flag.Parse()

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("relay: listen on %s: %v", *addr, err)
	}
	log.Printf("relay: listening on %s", *addr)

	h := newHub()
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("relay: accept: %v", err)
			continue
		}
		go h.handle(conn)
	}
}
