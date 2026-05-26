// Command client connects to a relay and participates in the Mosaic network.
//
// On top of the relay's plaintext transport it speaks the RPC layer defined in
// package proto: store_shard and retrieve_shard let clients persist and fetch
// shard data across the network.
//
// Every client serves store_shard/retrieve_shard for the network as long as it
// is connected; that is always on regardless of mode. The modes only choose what
// the process does in the foreground:
//
//	-node               run as a network node: stay connected (serving shards)
//	                    and shard any file dropped into the Mosaic dir into a
//	                    .mosaic stub stored across the network.
//	-rehydrate <stub>   reconstruct a .mosaic stub from the network and exit.
//	(default)           interactive: type commands or chat lines.
//	-msg / -wait        scripted single-message test mode.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hcp-uw/mosaic/proto"
	"github.com/hcp-uw/mosaic/shardstore"
)

// client wraps a relay connection with the RPC bookkeeping: serialized writes, a
// request-ID counter, response channels keyed by request ID, and the local
// shard store this node serves to the network.
type client struct {
	conn  net.Conn
	store *shardstore.Store
	id    string
	key   []byte

	writeMu sync.Mutex
	seq     uint64

	mu      sync.Mutex
	waiters map[string]chan *proto.Message
}

func newClient(conn net.Conn, store *shardstore.Store, id string, key []byte) *client {
	return &client{conn: conn, store: store, id: id, key: key, waiters: make(map[string]chan *proto.Message)}
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

// receive reads forwarded lines and dispatches them: RPC requests are handled
// and answered, responses are routed to any waiting caller, and anything that
// isn't an RPC envelope is printed as plain text.
func (c *client) receive() {
	sc := bufio.NewScanner(c.conn)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024) // shards can be large
	for sc.Scan() {
		raw := sc.Text()
		sender, payload := splitPrefix(raw)
		msg, ok := proto.Decode(payload)
		if !ok {
			fmt.Println(raw) // plain chat text
			continue
		}
		msg.From = sender
		switch msg.Type {
		case proto.TypeRequest:
			c.handleRequest(msg)
		case proto.TypeResponse:
			c.routeResponse(sender, msg)
		}
	}
}

// routeResponse delivers a response to the caller waiting on its request ID, if
// any. Responses to other clients' calls (also forwarded to us) are dropped.
func (c *client) routeResponse(sender string, msg *proto.Message) {
	c.mu.Lock()
	ch := c.waiters[msg.ID]
	c.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- msg:
	default: // caller's buffer full; drop extra responses
	}
}

// call sends a request and collects responses until stop returns true for one
// of them or the timeout elapses. stop may be nil to always wait the full
// timeout (useful when every node is expected to answer). If to is non-empty the
// request is routed to that single node; otherwise it is broadcast.
func (c *client) call(to, method string, params any, timeout time.Duration, stop func(*proto.Message) bool) ([]*proto.Message, error) {
	pb, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	id := c.nextID()
	ch := make(chan *proto.Message, 32)

	c.mu.Lock()
	c.waiters[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.waiters, id)
		c.mu.Unlock()
	}()

	line, err := proto.Encode(&proto.Message{Type: proto.TypeRequest, ID: id, Method: method, Params: pb})
	if err != nil {
		return nil, err
	}
	if to != "" {
		line = proto.Route(to, line)
	}
	if err := c.send(line); err != nil {
		return nil, err
	}

	var out []*proto.Message
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case m := <-ch:
			out = append(out, m)
			if stop != nil && stop(m) {
				return out, nil
			}
		case <-timer.C:
			return out, nil
		}
	}
}

// handleRequest runs the requested method against the local store and sends back
// a response.
func (c *client) handleRequest(req *proto.Message) {
	switch req.Method {
	case proto.MethodStoreShard:
		var p proto.StoreShardParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			c.respond(req, proto.StoreShardResult{Success: false, Error: "bad params: " + err.Error()})
			return
		}
		c.respond(req, c.storeShard(p))
	case proto.MethodRetrieveShard:
		var p proto.RetrieveShardParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			c.respond(req, proto.RetrieveShardResult{Found: false, Error: "bad params: " + err.Error()})
			return
		}
		r := c.retrieveShard(p)
		// Stay silent when we don't hold the shard: with shards distributed across
		// nodes, only the one holder should answer.
		if r.Found || r.Error != "" {
			c.respond(req, r)
		}
	case proto.MethodPing:
		c.respond(req, proto.PingResult{OK: true})
	case proto.MethodListShards:
		c.respond(req, c.listShards())
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
	line, err := proto.Encode(&proto.Message{Type: proto.TypeResponse, ID: req.ID, Method: req.Method, Result: rb})
	if err != nil {
		log.Printf("client: encode response: %v", err)
		return
	}
	// Direct the reply back to the caller when we know who asked, so responses
	// don't fan out to every node.
	if req.From != "" {
		line = proto.Route(req.From, line)
	}
	if err := c.send(line); err != nil {
		log.Printf("client: send response: %v", err)
	}
}

// storeShard is the local handler for store_shard: it persists the shard.
func (c *client) storeShard(p proto.StoreShardParams) proto.StoreShardResult {
	if err := c.store.Put(p.Address, p.Data); err != nil {
		log.Printf("client: store_shard(%q): %v", p.Address, err)
		return proto.StoreShardResult{Success: false, Error: err.Error()}
	}
	log.Printf("client: stored shard %q (%d bytes)", p.Address, len(p.Data))
	return proto.StoreShardResult{Success: true}
}

// retrieveShard is the local handler for retrieve_shard: it reads the shard back.
func (c *client) retrieveShard(p proto.RetrieveShardParams) proto.RetrieveShardResult {
	data, found, err := c.store.Get(p.Address)
	if err != nil {
		log.Printf("client: retrieve_shard(%q): %v", p.Address, err)
		return proto.RetrieveShardResult{Found: false, Error: err.Error()}
	}
	if !found {
		return proto.RetrieveShardResult{Found: false}
	}
	log.Printf("client: served shard %q (%d bytes)", p.Address, len(data))
	return proto.RetrieveShardResult{Found: true, Data: data}
}

func (c *client) listShards() proto.ListShardsResult {
	addresses, err := c.store.ListAddresses()
	if err != nil {
		return proto.ListShardsResult{Error: err.Error()}
	}
	return proto.ListShardsResult{Addresses: addresses}
}

// storeShardNet stores one shard on a specific node (by address) and reports
// whether that node confirmed success.
func (c *client) storeShardNet(node, address string, data []byte, timeout time.Duration) (bool, error) {
	ok := func(m *proto.Message) bool {
		var r proto.StoreShardResult
		return json.Unmarshal(m.Result, &r) == nil && r.Success
	}
	resps, err := c.call(node, proto.MethodStoreShard, proto.StoreShardParams{Address: address, Data: data}, timeout, ok)
	if err != nil {
		return false, err
	}
	for _, m := range resps {
		var r proto.StoreShardResult
		if json.Unmarshal(m.Result, &r) == nil && r.Success {
			return true, nil
		}
	}
	return false, nil
}

// retrieveShardNet fetches one shard from the network; the node that holds it
// answers (others stay silent).
func (c *client) retrieveShardNet(address string, timeout time.Duration) ([]byte, error) {
	found := func(m *proto.Message) bool {
		var r proto.RetrieveShardResult
		return json.Unmarshal(m.Result, &r) == nil && r.Found
	}
	resps, err := c.call("", proto.MethodRetrieveShard, proto.RetrieveShardParams{Address: address}, timeout, found)
	if err != nil {
		return nil, err
	}
	for _, m := range resps {
		var r proto.RetrieveShardResult
		if json.Unmarshal(m.Result, &r) == nil && r.Found {
			return r.Data, nil
		}
	}
	return nil, fmt.Errorf("shard %q not found on the network", address)
}

// discoverNodes pings the network and returns the addresses of the other nodes
// that answered within timeout.
func (c *client) discoverNodes(timeout time.Duration) ([]string, error) {
	resps, err := c.call("", proto.MethodPing, struct{}{}, timeout, nil)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var nodes []string
	for _, m := range resps {
		if m.From != "" && !seen[m.From] {
			seen[m.From] = true
			nodes = append(nodes, m.From)
		}
	}
	sort.Strings(nodes)
	return nodes, nil
}

func (c *client) listShardsNet(timeout time.Duration) ([]string, error) {
	resps, err := c.call("", proto.MethodListShards, struct{}{}, timeout, nil)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var all []string
	for _, m := range resps {
		var r proto.ListShardsResult
		if json.Unmarshal(m.Result, &r) != nil || r.Error != "" {
			continue
		}
		for _, a := range r.Addresses {
			if a == "" || seen[a] {
				continue
			}
			seen[a] = true
			all = append(all, a)
		}
	}
	sort.Strings(all)
	return all, nil
}

// handleInput interprets one line of interactive input.
func (c *client) handleInput(line string) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return
	}
	switch fields[0] {
	case "nodes":
		go func() {
			nodes, err := c.discoverNodes(discoveryTimeout)
			if err != nil {
				log.Printf("client: nodes: %v", err)
				return
			}
			fmt.Printf("[nodes] %d connected (this one + %d other):\n", len(nodes)+1, len(nodes))
			for _, n := range nodes {
				fmt.Printf("  - %s\n", n)
			}
		}()
	case "store_shard":
		if len(fields) != 3 {
			fmt.Println("usage: store_shard <address> <data>")
			return
		}
		go func() {
			nodes, err := c.discoverNodes(discoveryTimeout)
			if err != nil || len(nodes) == 0 {
				fmt.Printf("[store_shard] no other nodes to store on\n")
				return
			}
			target := nodes[0]
			ok, err := c.storeShardNet(target, fields[1], []byte(fields[2]), rpcTimeout)
			if err != nil {
				log.Printf("client: store_shard: %v", err)
				return
			}
			fmt.Printf("[store_shard] %q stored on %s: %v\n", fields[1], target, ok)
		}()
	case "retrieve_shard":
		if len(fields) != 2 {
			fmt.Println("usage: retrieve_shard <address>")
			return
		}
		go func() {
			data, err := c.retrieveShardNet(fields[1], rpcTimeout)
			if err != nil {
				fmt.Printf("[retrieve_shard] %v\n", err)
				return
			}
			fmt.Printf("[retrieve_shard] %q: %d bytes: %s\n", fields[1], len(data), string(data))
		}()
	case "list_shards":
		go func() {
			addrs, err := c.listShardsNet(discoveryTimeout)
			if err != nil {
				fmt.Printf("[list_shards] %v\n", err)
				return
			}
			fmt.Printf("[list_shards] %d shard address(es)\n", len(addrs))
			for _, a := range addrs {
				fmt.Printf("  - %s\n", a)
			}
		}()
	default:
		if err := c.send(line); err != nil {
			log.Fatalf("client: send: %v", err)
		}
	}
}

// splitPrefix separates the relay's "<sender>: <payload>" framing. If the line
// has no such prefix, the whole line is returned as the payload.
func splitPrefix(line string) (sender, payload string) {
	if i := strings.Index(line, ": "); i >= 0 {
		return line[:i], line[i+2:]
	}
	return "", line
}

// rpcTimeout is how long calls wait for responses from the network.
var rpcTimeout = 4 * time.Second

// discoveryTimeout is the (shorter) window for collecting ping responses. We
// can't know how many nodes to expect, so discovery always waits this long;
// pings are cheap and answered fast, so it stays short.
var discoveryTimeout = 1500 * time.Millisecond

func main() {
	relay := flag.String("relay", "127.0.0.1:9000", "relay address host:port")
	home := flag.String("home", "", "Mosaic base directory (default ~/Mosaic)")
	setKey := flag.String("set-key", "", "set/update the user key used for identity+encryption and exit")
	node := flag.Bool("node", false, "run as a network node: serve shards to the network and shard files dropped into the Mosaic dir")
	rehydrate := flag.String("rehydrate", "", "reconstruct the given .mosaic stub from the network, then exit")
	openAfter := flag.Bool("open", false, "with -rehydrate, open the reconstructed file afterward")
	timeout := flag.Duration("timeout", rpcTimeout, "RPC timeout waiting for network responses")
	msg := flag.String("msg", "", "scripted: command/message to send on connect")
	wait := flag.Duration("wait", 0, "scripted: listen this long then exit instead of reading stdin")
	flag.Parse()
	rpcTimeout = *timeout

	base := *home
	if base == "" {
		b, err := shardstore.DefaultBase()
		if err != nil {
			log.Fatalf("client: resolve home: %v", err)
		}
		base = b
	}
	if *setKey != "" {
		if err := writeUserKey(*setKey); err != nil {
			log.Fatalf("client: set key: %v", err)
		}
		log.Printf("client: wrote key to %s", mosaicKeyPath())
		return
	}
	id, key, err := loadIdentityAndKey()
	if err != nil {
		log.Fatalf("client: %v", err)
	}
	store, err := shardstore.New(base)
	if err != nil {
		log.Fatalf("client: shard store: %v", err)
	}

	conn, err := net.Dial("tcp", *relay)
	if err != nil {
		log.Fatalf("client: dial %s: %v", *relay, err)
	}
	defer conn.Close()
	log.Printf("client: connected to relay %s (home %s)", *relay, base)

	c := newClient(conn, store, id, key)
	go c.receive()

	switch {
	case *rehydrate != "":
		if err := c.rehydrate(*rehydrate, *openAfter); err != nil {
			log.Fatalf("client: rehydrate: %v", err)
		}
		return
	case *node:
		c.watch(base)
		return
	case *msg != "":
		c.handleInput(*msg)
		log.Printf("client: sent: %s", *msg)
		if *wait > 0 {
			time.Sleep(*wait)
		}
		return
	case *wait > 0:
		time.Sleep(*wait)
		return
	}

	// Interactive mode.
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		c.handleInput(sc.Text())
	}
}
