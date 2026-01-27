package p2p

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
