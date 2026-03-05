package handlers

import (
	"encoding/json"
	"log"
	"net/http"

	"concierge/internal/events"
	"concierge/internal/service"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(*http.Request) bool { return true },
}

// WSHandler обрабатывает WebSocket: приём команд и поток событий.
type WSHandler struct {
	callService *service.CallService
}

func NewWSHandler(callService *service.CallService) *WSHandler {
	return &WSHandler{callService: callService}
}

// ClientMessage — команда от клиента.
type ClientMessage struct {
	Action      string `json:"action"`
	PhoneNumber string `json:"phone_number"`
}

func (h *WSHandler) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[ws] upgrade: %v", err)
		return
	}
	defer conn.Close()

	emitter := events.NewWSEmitter(conn)
	emitter.Emit(events.NewWSConnected())

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return
			}
			log.Printf("[ws] read: %v", err)
			return
		}

		var msg ClientMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			emitter.Emit(events.NewCallError("", "invalid JSON", "ws"))
			continue
		}

		if msg.Action == "start_call" {
			if msg.PhoneNumber == "" {
				emitter.Emit(events.NewCallError("", "phone_number required", "ws"))
				continue
			}
			if !isValidPhoneNumber(msg.PhoneNumber) {
				emitter.Emit(events.NewCallError("", "invalid phone_number (11 digits)", "ws"))
				continue
			}
			callID := uuid.New().String()
			go h.callService.HandleCall(callID, msg.PhoneNumber, emitter)
		}
	}
}
