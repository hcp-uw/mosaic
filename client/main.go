// Command client connects to a relay and exchanges messages with other clients.
//
// On top of the relay's plaintext transport it speaks the RPC layer defined in
// package proto: typing "store_shard <address> <data>" sends a request to every
// other client, each of which runs the method and returns a response. Lines
// that aren't commands are forwarded as plain chat text.
//
// In interactive mode it reads commands from stdin. For scripted testing, pass
// -msg to send a single line and -wait to listen for incoming messages for a
// fixed duration before exiting.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hcp-uw/mosaic/proto"
	"github.com/hcp-uw/mosaic/shardstore"
)

// client wraps a relay connection with the bookkeeping the RPC layer needs:
// serialized writes, a request-ID counter, and the set of requests we originated
// (so we only surface responses to our own calls).
type client struct {
	conn net.Conn

	writeMu sync.Mutex

	seq uint64

	pendingMu sync.Mutex
	pending   map[string]bool
}

func newClient(conn net.Conn) *client {
	return &client{conn: conn, pending: make(map[string]bool)}
}

// send writes a single line to the relay, guarding against interleaved writes
// from the main and receiver goroutines.
func (c *client) send(line string) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err := fmt.Fprintf(c.conn, "%s\n", line)
	return err
}

func (c *client) nextID() string {
	return fmt.Sprintf("%d-%d", os.Getpid(), atomic.AddUint64(&c.seq, 1))
}

func (c *client) markPending(id string) {
	c.pendingMu.Lock()
	c.pending[id] = true
	c.pendingMu.Unlock()
}

func (c *client) isPending(id string) bool {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	return c.pending[id]
}

// receive reads forwarded lines and dispatches them: RPC requests are handled
// and answered, responses to our own calls are printed, and anything that isn't
// an RPC envelope is printed as plain text.
func (c *client) receive() {
	sc := bufio.NewScanner(c.conn)
	for sc.Scan() {
		raw := sc.Text()
		sender, payload := splitPrefix(raw)
		msg, ok := proto.Decode(payload)
		if !ok {
			fmt.Println(raw) // plain chat text
			continue
		}
		switch msg.Type {
		case proto.TypeRequest:
			c.handleRequest(msg)
		case proto.TypeResponse:
			c.handleResponse(sender, msg)
		}
	}
}

// handleRequest runs the requested method and sends back a response.
func (c *client) handleRequest(req *proto.Message) {
	switch req.Method {
	case proto.MethodStoreShard:
		var p proto.StoreShardParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			c.respond(req, proto.StoreShardResult{Success: false, Error: "bad params: " + err.Error()})
			return
		}
		c.respond(req, storeShard(p))
	case proto.MethodRetrieveShard:
		var p proto.RetrieveShardParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			c.respond(req, proto.RetrieveShardResult{Found: false, Error: "bad params: " + err.Error()})
			return
		}
		c.respond(req, retrieveShard(p))
	default:
		log.Printf("client: ignoring unknown method %q", req.Method)
	}
}

// respond marshals result and sends it as a response echoing the request's ID
// and method.
func (c *client) respond(req *proto.Message, result any) {
	rb, err := json.Marshal(result)
	if err != nil {
		log.Printf("client: marshal result: %v", err)
		return
	}
	line, err := proto.Encode(&proto.Message{
		Type:   proto.TypeResponse,
		ID:     req.ID,
		Method: req.Method,
		Result: rb,
	})
	if err != nil {
		log.Printf("client: encode response: %v", err)
		return
	}
	if err := c.send(line); err != nil {
		log.Printf("client: send response: %v", err)
	}
}

// handleResponse prints responses to requests this client originated.
func (c *client) handleResponse(sender string, msg *proto.Message) {
	if !c.isPending(msg.ID) {
		return // a response to someone else's call, forwarded to us
	}
	switch msg.Method {
	case proto.MethodStoreShard:
		var r proto.StoreShardResult
		if err := json.Unmarshal(msg.Result, &r); err != nil {
			log.Printf("client: bad store_shard result from %s: %v", sender, err)
			return
		}
		if r.Success {
			fmt.Printf("[store_shard] %s: success\n", sender)
		} else {
			fmt.Printf("[store_shard] %s: failure (%s)\n", sender, r.Error)
		}
	case proto.MethodRetrieveShard:
		var r proto.RetrieveShardResult
		if err := json.Unmarshal(msg.Result, &r); err != nil {
			log.Printf("client: bad retrieve_shard result from %s: %v", sender, err)
			return
		}
		switch {
		case r.Error != "":
			fmt.Printf("[retrieve_shard] %s: error (%s)\n", sender, r.Error)
		case r.Found:
			fmt.Printf("[retrieve_shard] %s: found %d bytes: %s\n", sender, len(r.Data), string(r.Data))
		default:
			fmt.Printf("[retrieve_shard] %s: not found\n", sender)
		}
	default:
		fmt.Printf("[response] %s: %s\n", sender, string(msg.Result))
	}
}

// callStoreShard sends a store_shard request to all other clients.
func (c *client) callStoreShard(address string, data []byte) error {
	pb, err := json.Marshal(proto.StoreShardParams{Address: address, Data: data})
	if err != nil {
		return err
	}
	id := c.nextID()
	line, err := proto.Encode(&proto.Message{
		Type:   proto.TypeRequest,
		ID:     id,
		Method: proto.MethodStoreShard,
		Params: pb,
	})
	if err != nil {
		return err
	}
	c.markPending(id)
	return c.send(line)
}

// callRetrieveShard sends a retrieve_shard request to all other clients.
func (c *client) callRetrieveShard(address string) error {
	pb, err := json.Marshal(proto.RetrieveShardParams{Address: address})
	if err != nil {
		return err
	}
	id := c.nextID()
	line, err := proto.Encode(&proto.Message{
		Type:   proto.TypeRequest,
		ID:     id,
		Method: proto.MethodRetrieveShard,
		Params: pb,
	})
	if err != nil {
		return err
	}
	c.markPending(id)
	return c.send(line)
}

// handleInput interprets one line of user input: a recognized command is turned
// into an RPC call, anything else is sent as plain chat text.
func (c *client) handleInput(line string) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return
	}
	switch fields[0] {
	case "store_shard":
		if len(fields) != 3 {
			fmt.Println("usage: store_shard <address> <data>")
			return
		}
		if err := c.callStoreShard(fields[1], []byte(fields[2])); err != nil {
			log.Printf("client: store_shard: %v", err)
		}
	case "retrieve_shard":
		if len(fields) != 2 {
			fmt.Println("usage: retrieve_shard <address>")
			return
		}
		if err := c.callRetrieveShard(fields[1]); err != nil {
			log.Printf("client: retrieve_shard: %v", err)
		}
	default:
		if err := c.send(line); err != nil {
			log.Fatalf("client: send: %v", err)
		}
	}
}

// storeShard is the local handler for the store_shard method. It persists the
// shard under ~/Mosaic/.shards.
func storeShard(p proto.StoreShardParams) proto.StoreShardResult {
	if err := shardstore.Store(p.Address, p.Data); err != nil {
		log.Printf("client: store_shard(address=%q): %v", p.Address, err)
		return proto.StoreShardResult{Success: false, Error: err.Error()}
	}
	log.Printf("client: store_shard(address=%q, %d bytes) — stored", p.Address, len(p.Data))
	return proto.StoreShardResult{Success: true}
}

// retrieveShard is the local handler for the retrieve_shard method. It reads the
// shard back from ~/Mosaic/.shards.
func retrieveShard(p proto.RetrieveShardParams) proto.RetrieveShardResult {
	data, found, err := shardstore.Retrieve(p.Address)
	if err != nil {
		log.Printf("client: retrieve_shard(address=%q): %v", p.Address, err)
		return proto.RetrieveShardResult{Found: false, Error: err.Error()}
	}
	if !found {
		log.Printf("client: retrieve_shard(address=%q) — not found", p.Address)
		return proto.RetrieveShardResult{Found: false}
	}
	log.Printf("client: retrieve_shard(address=%q, %d bytes) — found", p.Address, len(data))
	return proto.RetrieveShardResult{Found: true, Data: data}
}

// splitPrefix separates the relay's "<sender>: <payload>" framing. If the line
// has no such prefix, the whole line is returned as the payload.
func splitPrefix(line string) (sender, payload string) {
	if i := strings.Index(line, ": "); i >= 0 {
		return line[:i], line[i+2:]
	}
	return "", line
}

func main() {
	relay := flag.String("relay", "127.0.0.1:9000", "relay address host:port")
	msg := flag.String("msg", "", "optional command/message to send on connect")
	wait := flag.Duration("wait", 0, "if >0, listen this long then exit instead of reading stdin")
	flag.Parse()

	conn, err := net.Dial("tcp", *relay)
	if err != nil {
		log.Fatalf("client: dial %s: %v", *relay, err)
	}
	defer conn.Close()

	log.Printf("client: connected to relay %s", *relay)

	c := newClient(conn)
	go c.receive()

	if *msg != "" {
		c.handleInput(*msg)
		log.Printf("client: sent: %s", *msg)
	}

	if *wait > 0 {
		time.Sleep(*wait)
		log.Printf("client: done listening, exiting")
		return
	}

	// Interactive mode: each stdin line is a command or chat text.
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		c.handleInput(sc.Text())
	}
}
