package stun

import (
	"encoding/json"
	"net"
	"net/http"
)

func NewLeader(w http.ResponseWriter, r *http.Request, cm *connectionManager) {
	newLeader := leaderRequest{
		W: w,
		R: r,
	}

	cm.incomingLeader <- newLeader
}

func JoinNode(w http.ResponseWriter, r *http.Request, cm *connectionManager) {
	IpAdd := net.ParseIP(r.RemoteAddr)
	publicKey := r.URL.Query().Get("public_key")

	joinMessage := joinMessage{
		IP:        IpAdd,
		PublicKey: []byte(publicKey),
	}

	jsonMsgBytes, err := json.Marshal(joinMessage)
	if err != nil {
		http.Error(w, "failed to marshal join message", http.StatusInternalServerError)
		return
	}

	message := message{
		messageType: JoinMessage,
		contents:    jsonMsgBytes,
	}

}
