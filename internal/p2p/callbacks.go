package p2p

import "github.com/hcp-uw/mosaic/internal/api"

// OnStateChange registers a callback for state changes
func (c *Client) OnStateChange(callback func(ClientState)) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.stateCallbacks = append(c.stateCallbacks, callback)
}

// OnPeerAssigned registers a callback for peer assignment
func (c *Client) OnPeerAssigned(callback func(*PeerInfo)) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.peerCallbacks = append(c.peerCallbacks, callback)
}

// OnPeerLeft registers a callback invoked with the peer ID whenever a peer
// is evicted after a pong timeout. Use this to trigger shard redistribution.
func (c *Client) OnPeerLeft(callback func(peerID string)) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.peerLeftCallbacks = append(c.peerLeftCallbacks, callback)
}

// OnHandshakeDone registers a callback invoked with the peer ID once the
// X25519 session key exchange completes for that peer. Use this to trigger
// work that requires an encrypted channel (e.g. manifest sync), because
// the peer's HandshakeDone flag is guaranteed true when this fires.
func (c *Client) OnHandshakeDone(callback func(peerID string)) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.handshakeCallbacks = append(c.handshakeCallbacks, callback)
}

// OnError registers a callback for errors
func (c *Client) OnError(callback func(error)) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.errorCallbacks = append(c.errorCallbacks, callback)
}

// OnMessageReceived registers a callback for received peer messages
func (c *Client) OnMessageReceived(callback func([]byte)) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.messageCallbacks = append(c.messageCallbacks, callback)
}

// setState updates the client state and notifies callbacks
func (c *Client) setState(newState ClientState) {

    oldState := c.state
    c.state = newState

	if oldState != newState {
		for _, callback := range c.stateCallbacks {
			go callback(newState)
		}
	}
}

// notifyPeerAssigned notifies callbacks about peer assignment
func (c *Client) notifyPeerAssigned(peerInfo *PeerInfo) {
	for _, callback := range c.peerCallbacks {
		go callback(peerInfo)
	}
}

// notifyPeerLeft notifies callbacks that a peer has been evicted.
func (c *Client) notifyPeerLeft(peerID string) {
	for _, callback := range c.peerLeftCallbacks {
		go callback(peerID)
	}
}

// notifyHandshakeDone notifies callbacks that a session key was established.
func (c *Client) notifyHandshakeDone(peerID string) {
	c.mutex.RLock()
	cbs := make([]func(string), len(c.handshakeCallbacks))
	copy(cbs, c.handshakeCallbacks)
	c.mutex.RUnlock()
	for _, cb := range cbs {
		go cb(peerID)
	}
}

// notifyError notifies callbacks about errors
func (c *Client) notifyError(err error) {
	for _, callback := range c.errorCallbacks {
		go callback(err)
	}
}

// notifyMessageReceived notifies callbacks about received messages
func (c *Client) notifyMessageReceived(data []byte) {
	for _, callback := range c.messageCallbacks {
		go callback(data)
	}
}

// SetShardProbeHandler registers a callback invoked (in a goroutine) when a
// ShardProbe message arrives. Registered by the transfer package to avoid an
// import cycle (transfer imports p2p, so p2p cannot import transfer).
func (c *Client) SetShardProbeHandler(fn func(msg *api.Message)) {
	c.probeMu.Lock()
	c.onShardProbe = fn
	c.probeMu.Unlock()
}

// SetShardProbeAckHandler registers a callback invoked synchronously when a
// ShardProbeAck arrives, so it can deliver to the waiting ProbeShardAtPeer channel
// before the goroutine that called it returns.
func (c *Client) SetShardProbeAckHandler(fn func(msg *api.Message)) {
	c.probeMu.Lock()
	c.onShardProbeAck = fn
	c.probeMu.Unlock()
}

// SetQUICBinaryFrameHandler registers a handler called SYNCHRONOUSLY from the
// QUIC stream-reader goroutine for every 0x01 binary shard frame. Calling it
// synchronously ensures the assembly map is fully updated before the stream-done
// notification fires, eliminating the race where ShardStreamAck was computed
// before the last chunk was stored.
func (c *Client) SetQUICBinaryFrameHandler(fn func(peerID string, data []byte)) {
	c.quicCallbackMu.Lock()
	c.quicBinaryFrameHandler = fn
	c.quicCallbackMu.Unlock()
}

func (c *Client) callQUICBinaryFrameHandler(peerID string, data []byte) {
	c.quicCallbackMu.Lock()
	fn := c.quicBinaryFrameHandler
	c.quicCallbackMu.Unlock()
	if fn != nil {
		fn(peerID, data) // synchronous — must be fast (in-memory only)
	}
}

// SetQUICStreamDoneCallback registers a callback invoked (in a goroutine) when
// a QUIC receive stream reaches EOF. peerID is the sender; lastFrame is the last
// binary chunk frame read from the stream, used to identify the shard.
func (c *Client) SetQUICStreamDoneCallback(fn func(peerID string, lastFrame []byte)) {
	c.quicCallbackMu.Lock()
	c.quicStreamDoneFn = fn
	c.quicCallbackMu.Unlock()
}

func (c *Client) notifyQUICStreamDone(peerID string, lastFrame []byte) {
	c.quicCallbackMu.Lock()
	fn := c.quicStreamDoneFn
	c.quicCallbackMu.Unlock()
	if fn != nil {
		go fn(peerID, lastFrame)
	}
}
