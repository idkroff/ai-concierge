package caller

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

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

type event struct {
	Type    string          `json:"type"`
	CallID  string          `json:"call_id"`
	Payload json.RawMessage `json:"payload"`
}

type errorPayload struct {
	Message string `json:"message"`
}

func (c *Client) StartCall(ctx context.Context, message string) (string, error) {
	dialer := websocket.DefaultDialer
	conn, _, err := dialer.DialContext(ctx, c.wsURL, nil)
	if err != nil {
		return "", fmt.Errorf("ws dial: %w", err)
	}
	defer conn.Close()

	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	// Читаем ws.connected
	if _, _, err := conn.ReadMessage(); err != nil {
		return "", fmt.Errorf("ws read connected: %w", err)
	}

	// Отправляем команду старта звонка
	cmd := startCallMsg{Action: "start_call", Text: message}
	if err := conn.WriteJSON(cmd); err != nil {
		return "", fmt.Errorf("ws write: %w", err)
	}

	// Читаем события до call.started или call.error
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return "", fmt.Errorf("ws read: %w", err)
		}

		var ev event
		if err := json.Unmarshal(data, &ev); err != nil {
			log.Printf("[caller-ws] unknown message: %s", string(data))
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
