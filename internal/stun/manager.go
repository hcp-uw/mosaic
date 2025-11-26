package stun

import (
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

// The stun server maintains a websocket connection with the leader node

type leaderRequest struct {
	W http.ResponseWriter
	R *http.Request
}

type connectionManager struct {
	leader   *websocket.Conn
	leaderIp string
	incomingLeader chan leaderRequest

	incomingMsg chan []byte
}

const (
	pongWait = 10 * time.Second
	pingPeriod = (pongWait * 9) /10   
	writeWait = 5 * time.Second
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

func NewManager() *connectionManager {
	manager := connectionManager{}

	manager.runConnection()

	return &manager
}


func (cm *connectionManager) runConnection() {
	for leaderRequest := range cm.incomingLeader {
		conn, err := upgrader.Upgrade(leaderRequest.W, leaderRequest.R, nil)
		if err != nil {                                   
			log.Println(err)
		}
		
		defer conn.Close()
		cm.leader = conn   
		cm.leaderIp = leaderRequest.R.RemoteAddr

		cm.reader()
		go cm.pinger()

	}
}                                 

func (cm *connectionManager) writeMessage(message []byte) {
	if cm.leader != nil && cm.leaderIp != "" {
		cm.leader.WriteMessage(websocket.BinaryMessage, message)
		return
	} 

	// TODO if no leader is found make a new one or smth
}

func (cm *connectionManager) reader() {
	defer cm.leader.Close()
	defer func(){cm.leaderIp = ""}()

	cm.leader.SetReadDeadline(time.Now().Add(pongWait))
	cm.leader.SetPongHandler(func(string) error {
		cm.leader.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		if (cm.leader == nil) {
			return
		}
		msgType, data, err := cm.leader.ReadMessage()
		if err != nil {
			log.Println("Read err: ", err)
			return
		}  
		
		switch msgType {

		case websocket.BinaryMessage:
			cm.incomingMsg <- data
		case websocket.CloseMessage:
			return
		}


	}

}

func (cm *connectionManager) pinger() {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for range ticker.C {
		cm.leader.SetWriteDeadline(time.Now().Add(writeWait))
		if err := cm.leader.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
			log.Println(err)
			cm.leader.Close()
			cm.leaderIp = ""
			return
		}
	}
}
