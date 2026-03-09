package caller

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"tg_bot/internal/domain/entity"

	"github.com/gorilla/websocket"
)

type Client struct {
	wsURL string
}

func NewClient(baseURL string) *Client {
	return &Client{wsURL: baseURL + "/ws"}
}

type startCallMsg struct {
	Action string `json:"action"`
	Text   string `json:"text"`
}

type wsEvent struct {
	Type    string          `json:"type"`
	CallID  string          `json:"call_id"`
	Payload json.RawMessage `json:"payload"`
}

type errorPayload struct {
	Message string `json:"message"`
}

func (c *Client) StartCall(ctx context.Context, message string) (string, <-chan entity.CallEvent, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.wsURL, nil)
	if err != nil {
		return "", nil, fmt.Errorf("ws dial: %w", err)
	}

	// Читаем ws.connected
	if _, _, err := conn.ReadMessage(); err != nil {
		conn.Close()
		return "", nil, fmt.Errorf("ws read connected: %w", err)
	}

	// Отправляем команду
	if err := conn.WriteJSON(startCallMsg{Action: "start_call", Text: message}); err != nil {
		conn.Close()
		return "", nil, fmt.Errorf("ws write: %w", err)
	}

	// Ждём call.started или call.error (в рамках ctx с timeout)
	callID, err := waitForCallStarted(ctx, conn)
	if err != nil {
		conn.Close()
		return "", nil, err
	}

	// Дальше события идут в фоне - снимаем дедлайн и читаем до конца звонка
	conn.SetReadDeadline(time.Time{})

	events := make(chan entity.CallEvent, 32)
	go func() {
		defer close(events)
		defer conn.Close()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var ev wsEvent
			if err := json.Unmarshal(data, &ev); err != nil {
				log.Printf("[caller-ws] unknown message: %s", string(data))
				continue
			}
			events <- entity.CallEvent{Type: ev.Type, CallID: ev.CallID, Payload: ev.Payload}
			if ev.Type == "call.ended" || ev.Type == "call.error" {
				return
			}
		}
	}()

	return callID, events, nil
}

func waitForCallStarted(ctx context.Context, conn *websocket.Conn) (string, error) {
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetReadDeadline(deadline)
	}
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return "", fmt.Errorf("ws read: %w", err)
		}
		var ev wsEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "call.started":
			return ev.CallID, nil
		case "call.error":
			var p errorPayload
			_ = json.Unmarshal(ev.Payload, &p)
			return "", fmt.Errorf("caller-service error: %s", p.Message)
		}
	}
}
