// Command relay is a message relay server.
//
// Clients connect over TCP and send a single identifying name as their first
// line. After that, every line a client sends is forwarded to all other
// connected clients, prefixed with the sender's name. Clients never connect to
// each other directly — the relay is the only thing they talk to.
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

func (h *hub) add(name string, conn net.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[name] = conn
}

func (h *hub) remove(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, name)
}

// broadcast sends line to every client except the sender.
func (h *hub) broadcast(from, line string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for name, conn := range h.clients {
		if name == from {
			continue
		}
		if _, err := fmt.Fprintf(conn, "%s: %s\n", from, line); err != nil {
			log.Printf("relay: failed sending to %s: %v", name, err)
		}
	}
}

func (h *hub) handle(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)

	name, err := r.ReadString('\n')
	if err != nil {
		log.Printf("relay: client %v disconnected before registering", conn.RemoteAddr())
		return
	}
	name = trimLine(name)
	if name == "" {
		name = conn.RemoteAddr().String()
	}

	h.add(name, conn)
	log.Printf("relay: %q connected from %v", name, conn.RemoteAddr())
	defer func() {
		h.remove(name)
		log.Printf("relay: %q disconnected", name)
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
		log.Printf("relay: %q -> all: %s", name, line)
		h.broadcast(name, line)
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
