package events

import (
	"log"
	"sync"

	"github.com/gorilla/websocket"
)

// WSEmitter отправляет события в WebSocket (один JSON-объект на сообщение).
type WSEmitter struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func NewWSEmitter(conn *websocket.Conn) *WSEmitter {
	return &WSEmitter{conn: conn}
}

func (e *WSEmitter) Emit(ev Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.conn == nil {
		return
	}
	if err := e.conn.WriteJSON(ev); err != nil {
		log.Printf("[events] WS write: %v", err)
		return
	}
}
