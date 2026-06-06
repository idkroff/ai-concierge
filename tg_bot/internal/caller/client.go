package caller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"tg_bot/internal/domain/entity"

	"github.com/gorilla/websocket"
)

type Client struct {
	wsURL    string
	httpBase string
	http     *http.Client
}

func NewClient(baseURL string) *Client {
	httpBase := baseURL
	if strings.HasPrefix(baseURL, "ws") { // ws:// -> http://, wss:// -> https://
		httpBase = "http" + strings.TrimPrefix(baseURL, "ws")
	}
	return &Client{
		wsURL:    baseURL + "/ws",
		httpBase: httpBase,
		http:     &http.Client{Timeout: 30 * time.Second},
	}
}

type parseRequest struct {
	Text string `json:"text"`
}

type parseResponse struct {
	PhoneNumber  string `json:"phone_number"`
	Organization string `json:"organization"`
	Context      string `json:"context"`
	DisplayName  string `json:"display_name"`
	IsHotline    bool   `json:"is_hotline"`
}

type apiError struct {
	Error string `json:"error"`
}

// Parse — preview через /parse caller-сервиса: резолв номера без старта звонка.
func (c *Client) Parse(ctx context.Context, message string) (*entity.ParsedCall, error) {
	body, err := json.Marshal(parseRequest{Text: message})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.httpBase+"/parse", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("parse request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var ae apiError
		_ = json.NewDecoder(resp.Body).Decode(&ae)
		if ae.Error != "" {
			return nil, fmt.Errorf("%s", ae.Error)
		}
		return nil, fmt.Errorf("parse status %d", resp.StatusCode)
	}

	var pr parseResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, fmt.Errorf("decode parse response: %w", err)
	}
	return &entity.ParsedCall{
		PhoneNumber:  pr.PhoneNumber,
		Organization: pr.Organization,
		Context:      pr.Context,
		DisplayName:  pr.DisplayName,
		IsHotline:    pr.IsHotline,
	}, nil
}

type startCallMsg struct {
	Action      string `json:"action"`
	PhoneNumber string `json:"phone_number"`
	Text        string `json:"text"`
	Interactive bool   `json:"interactive"`
}

type clarificationResponseMsg struct {
	Action          string `json:"action"`
	CallID          string `json:"call_id"`
	ClarificationID string `json:"clarification_id"`
	Response        string `json:"response"`
}

type wsEvent struct {
	Type    string          `json:"type"`
	CallID  string          `json:"call_id"`
	Payload json.RawMessage `json:"payload"`
}

type errorPayload struct {
	Message string `json:"message"`
}

// StartCall открывает ws-звонок. Возвращает функцию respond для отправки ответа
// клиента на доуточнение по тому же соединению (интерактивный режим).
func (c *Client) StartCall(ctx context.Context, phoneNumber, text string, interactive bool) (string, <-chan entity.CallEvent, func(clarificationID, answer string) error, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.wsURL, nil)
	if err != nil {
		return "", nil, nil, fmt.Errorf("ws dial: %w", err)
	}

	// Читаем ws.connected
	if _, _, err := conn.ReadMessage(); err != nil {
		conn.Close()
		return "", nil, nil, fmt.Errorf("ws read connected: %w", err)
	}

	// Отправляем команду с уже найденным номером (caller-сервис не парсит повторно)
	if err := conn.WriteJSON(startCallMsg{Action: "start_call", PhoneNumber: phoneNumber, Text: text, Interactive: interactive}); err != nil {
		conn.Close()
		return "", nil, nil, fmt.Errorf("ws write: %w", err)
	}

	// Ждём call.started или call.error (в рамках ctx с timeout)
	callID, err := waitForCallStarted(ctx, conn)
	if err != nil {
		conn.Close()
		return "", nil, nil, err
	}

	// Дальше события идут в фоне - снимаем дедлайн и читаем до конца звонка
	conn.SetReadDeadline(time.Time{})

	// writeMu сериализует писателей в conn (gorilla: 1 reader + 1 writer):
	// respond (из хендлера) и close-фрейм (из read-loop).
	var writeMu sync.Mutex
	respond := func(clarificationID, answer string) error {
		log.Printf("[caller-ws] -> clarification.response call=%s clar=%s ans=%q", callID, clarificationID, answer)
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteJSON(clarificationResponseMsg{
			Action:          "clarification.response",
			CallID:          callID,
			ClarificationID: clarificationID,
			Response:        answer,
		})
	}

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
				writeMu.Lock()
				conn.WriteMessage(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
				)
				writeMu.Unlock()
				return
			}
		}
	}()

	return callID, events, respond, nil
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
