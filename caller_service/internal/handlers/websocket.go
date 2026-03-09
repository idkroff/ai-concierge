package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"concierge/internal/events"
	"concierge/internal/parser"
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
	parser      *parser.Parser
}

func NewWSHandler(callService *service.CallService, p *parser.Parser) *WSHandler {
	return &WSHandler{callService: callService, parser: p}
}

// ClientMessage — команда от клиента.
type ClientMessage struct {
	Action      string `json:"action"`
	PhoneNumber string `json:"phone_number"`
	Text        string `json:"text"` // сырое сообщение или контекст задачи
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
			phoneNumber := msg.PhoneNumber
			callContext := msg.Text

			// Если phone_number не передан — парсим из text
			if phoneNumber == "" {
				if msg.Text == "" {
					emitter.Emit(events.NewCallError("", "phone_number or text required", "ws"))
					continue
				}
				ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
				parsed, err := h.parser.Parse(ctx, msg.Text)
				cancel()
				if err != nil {
					log.Printf("[ws] parse error: %v", err)
					emitter.Emit(events.NewCallError("", "failed to parse text: "+err.Error(), "ws"))
					continue
				}
				phoneNumber = parsed.PhoneNumber
				callContext = parsed.Context
			}

			if len(phoneNumber) != 11 {
				emitter.Emit(events.NewCallError("", "invalid phone_number (11 digits)", "ws"))
				continue
			}

			callID := uuid.New().String()
			go h.callService.HandleCall(callID, phoneNumber, callContext, emitter)
		}
	}
}
