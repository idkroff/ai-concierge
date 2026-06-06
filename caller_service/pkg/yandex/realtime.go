package yandex

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// debugEvents=1 включает сырой дамп всех событий Yandex Realtime в лог.
var debugEvents = os.Getenv("DEBUG_EVENTS") == "1"

// Event types
type Event struct {
	Type       string          `json:"type"`
	Session    json.RawMessage `json:"session,omitempty"`
	Audio      string          `json:"audio,omitempty"`
	Delta      string          `json:"delta,omitempty"`
	Response   json.RawMessage `json:"response,omitempty"`
	Error      json.RawMessage `json:"error,omitempty"`
	Message    string          `json:"message,omitempty"`
	Transcript string          `json:"transcript,omitempty"`

	// Поля function-call (OpenAI Realtime GA): приходят на
	// response.function_call_arguments.done и в output-item-ах
	CallID    string          `json:"call_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments string          `json:"arguments,omitempty"`
	Item      json.RawMessage `json:"item,omitempty"`
}

// FunctionCall — нормализованный вызов инструмента моделью.
type FunctionCall struct {
	CallID    string
	Name      string
	Arguments string
}

type SessionUpdate struct {
	Type    string        `json:"type"`
	Session SessionConfig `json:"session"`
}

type SessionConfig struct {
	Type             string           `json:"type"`
	OutputModalities []string         `json:"output_modalities"`
	Audio            AudioConfig      `json:"audio"`
	Instructions     string           `json:"instructions"`
	Tools            []ToolDefinition `json:"tools,omitempty"`
	ToolChoice       string           `json:"tool_choice,omitempty"`
}

// ToolDefinition — описание инструмента для session.update (формат OpenAI GA).
type ToolDefinition struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// askPrincipalTool — инструмент «спросить клиента». Объявляется только в
// интерактивном режиме. Политика: дёргать клиента на любую неуверенность.
var askPrincipalTool = ToolDefinition{
	Type:        "function",
	Name:        "ask_principal",
	Description: "Спроси клиента (того, по чьему поручению ты звонишь), когда собеседник запрашивает информацию, которой ты НЕ можешь знать и которая известна только клиенту: число гостей, на чьё имя, дата и время, предпочтения, любые детали брони или заказа. НИКОГДА не выдумывай такие данные — сразу вызывай этот инструмент. Вызывай его молча, не зачитывая вслух. В поле question задай короткий конкретный вопрос клиенту на русском.",
	Parameters:  json.RawMessage(`{"type":"object","properties":{"question":{"type":"string","description":"Короткий конкретный вопрос клиенту, напр. 'На сколько человек бронировать?'"}},"required":["question"]}`),
}

// ConversationItemCreate — добавление элемента в диалог (ответ на тул / текст).
type ConversationItemCreate struct {
	Type string          `json:"type"`
	Item json.RawMessage `json:"item"`
}

type AudioConfig struct {
	Input  InputAudioConfig  `json:"input"`
	Output OutputAudioConfig `json:"output"`
}

type InputAudioConfig struct {
	Format                  AudioFormat                   `json:"format"`
	TurnDetection           TurnDetection                 `json:"turn_detection"`
	InputAudioTranscription InputAudioTranscriptionConfig `json:"input_audio_transcription"`
}

type InputAudioTranscriptionConfig struct {
	Model string `json:"model"`
}

type OutputAudioConfig struct {
	Format AudioFormat `json:"format"`
	Voice  string      `json:"voice"`
}

type AudioFormat struct {
	Type string `json:"type"`
	Rate int    `json:"rate"`
}

type TurnDetection struct {
	Type              string  `json:"type"`
	Threshold         float64 `json:"threshold"`
	SilenceDurationMs int     `json:"silence_duration_ms"`
}

type ResponseCreate struct {
	Type     string                     `json:"type"`
	Response ResponseCreateInstructions `json:"response"`
}

type ResponseCreateInstructions struct {
	Instructions string `json:"instructions"`
}

type InputAudioBufferAppend struct {
	Type  string `json:"type"`
	Audio string `json:"audio"`
}

// Client represents Yandex Realtime API client
type Client struct {
	conn         *websocket.Conn
	apiKey       string
	folder       string
	instructions string
	interactive  bool // объявлять ли инструмент ask_principal

	// Каналы для коммуникации
	audioOutput   chan []byte       // Аудио от Yandex (для воспроизведения)
	textOutput    chan string       // Текстовый ответ от Yandex
	events        chan Event        // Все события
	functionCalls chan FunctionCall // Вызовы инструментов моделью
	fcArgs        map[string]string // накопление аргументов тула по call_id (только eventLoop)

	// Управление
	stopChan chan struct{}
	wg       sync.WaitGroup
	mu       sync.Mutex
	writeMu  sync.Mutex // сериализует все записи в conn (gorilla: 1 writer)

	// Состояние
	connected    bool
	sessionReady bool
}

// NewClient создает новый клиент Yandex Realtime API
func NewClient(apiKey, folder, instructions string, interactive bool) *Client {
	return &Client{
		apiKey:        apiKey,
		folder:        folder,
		instructions:  instructions,
		interactive:   interactive,
		audioOutput:   make(chan []byte, 100),
		textOutput:    make(chan string, 10),
		events:        make(chan Event, 50),
		functionCalls: make(chan FunctionCall, 8),
		fcArgs:        make(map[string]string),
		stopChan:      make(chan struct{}),
	}
}

// writeJSON сериализует запись в WebSocket (gorilla допускает один writer).
func (c *Client) writeJSON(v interface{}) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteJSON(v)
}

// Connect подключается к Yandex Realtime API
func (c *Client) Connect() error {
	realtimeURL := fmt.Sprintf("wss://rest-assistant.api.cloud.yandex.net/v1/realtime/openai?model=gpt://%s/speech-realtime-250923", c.folder)

	headers := http.Header{}
	headers.Add("Authorization", fmt.Sprintf("api-key %s", c.apiKey))

	conn, _, err := websocket.DefaultDialer.Dial(realtimeURL, headers)
	if err != nil {
		return fmt.Errorf("ошибка подключения к WebSocket: %w", err)
	}

	c.conn = conn
	c.connected = true

	// Ждём событие session.created
	var created Event
	if err := c.conn.ReadJSON(&created); err != nil {
		return fmt.Errorf("ошибка чтения session.created: %w", err)
	}

	// Логируем полученное событие для отладки
	fmt.Printf("🔍 Получено событие от Yandex: type=%s\n", created.Type)
	if created.Type == "error" || created.Message != "" {
		errorDetails, _ := json.Marshal(created)
		fmt.Printf("🔍 Детали события: %s\n", string(errorDetails))
	}

	if created.Type != "session.created" {
		// Если пришла ошибка, выводим детали
		if created.Type == "error" {
			errorMsg := "неизвестная ошибка"
			if created.Message != "" {
				errorMsg = created.Message
			} else if len(created.Error) > 0 {
				errorMsg = string(created.Error)
			}
			// Пытаемся распарсить полный JSON ошибки
			errorJSON, _ := json.MarshalIndent(created, "", "  ")
			return fmt.Errorf("ошибка от Yandex API: %s\nДетали: %s", errorMsg, string(errorJSON))
		}
		return fmt.Errorf("неожиданный тип события: %s (ожидался session.created). Полное событие: %+v", created.Type, created)
	}

	// Запускаем обработчик событий ДО отправки session.update
	c.wg.Add(1)
	go c.eventLoop()

	// Обновляем сессию (событие session.updated придет в eventLoop)
	if err := c.updateSession(); err != nil {
		return err
	}

	return nil
}

// updateSession обновляет настройки сессии
func (c *Client) updateSession() error {
	session := SessionConfig{
		Type:             "realtime",
		OutputModalities: []string{"audio"},
		Audio: AudioConfig{
			Input: InputAudioConfig{
				Format: AudioFormat{
					Type: "audio/pcm",
					Rate: 24000,
				},
				TurnDetection: TurnDetection{
					Type:              "server_vad",
					Threshold:         0.5,
					SilenceDurationMs: 2200, // Увеличено с 400 до 1200мс - ждем дольше перед ответом
				},
				InputAudioTranscription: InputAudioTranscriptionConfig{
					Model: "whisper-1",
				},
			},
			Output: OutputAudioConfig{
				Format: AudioFormat{
					Type: "audio/pcm",
					Rate: 44100, // 44.1kHz (стандартная частота, будем конвертировать в 8kHz)
				},
				Voice: "marina",
			},
		},
		Instructions: c.instructions,
	}

	// В интерактивном режиме объявляем инструмент ask_principal.
	// Без него session.update байт-в-байт совпадает с прежним поведением.
	if c.interactive {
		session.Tools = []ToolDefinition{askPrincipalTool}
		session.ToolChoice = "auto"
	}

	return c.writeJSON(SessionUpdate{Type: "session.update", Session: session})
}

// SendAudio отправляет аудио данные в API (должно быть PCM 24kHz)
func (c *Client) SendAudio(audioData []byte) error {
	msg := InputAudioBufferAppend{
		Type:  "input_audio_buffer.append",
		Audio: base64.StdEncoding.EncodeToString(audioData),
	}

	return c.writeJSON(msg)
}

// SendAudioChunked отправляет аудио чанками
func (c *Client) SendAudioChunked(audioData []byte, chunkSize int) error {
	for i := 0; i < len(audioData); i += chunkSize {
		end := i + chunkSize
		if end > len(audioData) {
			end = len(audioData)
		}
		chunk := audioData[i:end]

		if err := c.SendAudio(chunk); err != nil {
			return err
		}
	}

	return nil
}

// SendSilence отправляет тишину для активации VAD
func (c *Client) SendSilence(durationMs int) error {
	// 24kHz, 16-bit mono = 48 байт/мс
	samplesPerMs := 48
	silenceChunk := make([]byte, samplesPerMs*durationMs)

	return c.SendAudio(silenceChunk)
}

// TriggerResponse запрашивает генерацию ответа
func (c *Client) TriggerResponse(instructions string) error {
	responseCreate := ResponseCreate{
		Type: "response.create",
		Response: ResponseCreateInstructions{
			Instructions: instructions,
		},
	}

	return c.writeJSON(responseCreate)
}

// SubmitFunctionOutput отдаёт результат вызова инструмента модели.
// После него нужно вызвать TriggerResponse, чтобы модель продолжила разговор.
func (c *Client) SubmitFunctionOutput(callID, output string) error {
	item, err := json.Marshal(map[string]string{
		"type":    "function_call_output",
		"call_id": callID,
		"output":  output,
	})
	if err != nil {
		return err
	}
	return c.writeJSON(ConversationItemCreate{Type: "conversation.item.create", Item: item})
}

// InjectText добавляет в диалог текстовое сообщение (страховка R8:
// чтобы модель гарантированно увидела ответ клиента, даже если
// function_call_output будет проигнорирован).
func (c *Client) InjectText(role, text string) error {
	item, err := json.Marshal(map[string]interface{}{
		"type": "message",
		"role": role,
		"content": []map[string]string{
			{"type": "input_text", "text": text},
		},
	})
	if err != nil {
		return err
	}
	return c.writeJSON(ConversationItemCreate{Type: "conversation.item.create", Item: item})
}

// FunctionCalls возвращает канал вызовов инструментов моделью.
func (c *Client) FunctionCalls() <-chan FunctionCall {
	return c.functionCalls
}

// eventLoop обрабатывает входящие события
func (c *Client) eventLoop() {
	defer c.wg.Done()
	defer close(c.audioOutput)
	defer close(c.textOutput)
	defer close(c.events)
	defer close(c.functionCalls)

	c.conn.SetReadDeadline(time.Now().Add(300 * time.Second))

	for {
		select {
		case <-c.stopChan:
			return
		default:
		}

		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			// Игнорируем ошибки при закрытии соединения
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return
			}
			// Проверяем, не закрыли ли мы соединение сами
			select {
			case <-c.stopChan:
				return
			default:
			}
			// Игнорируем ошибки "use of closed network connection"
			if c.isConnectionClosed() {
				return
			}
			fmt.Printf("⚠️  Ошибка чтения события: %v\n", err)
			return
		}

		// Обновляем таймаут
		c.conn.SetReadDeadline(time.Now().Add(300 * time.Second))

		var event Event
		if err := json.Unmarshal(raw, &event); err != nil {
			fmt.Printf("⚠️  Yandex: не разобрал событие: %v | raw=%s\n", err, string(raw))
			continue
		}

		// Сырой дамп события (кроме аудио-дельт — они огромные). DEBUG_EVENTS=1.
		if debugEvents && event.Type != "response.output_audio.delta" {
			fmt.Printf("🔽 RAW %s\n", string(raw))
		}

		// Отправляем событие в канал
		select {
		case c.events <- event:
		default:
		}

		if event.Type == "error" {
			fmt.Printf("❌ Yandex error event: %s\n", string(raw))
		} else if !debugEvents && event.Type != "response.output_audio.delta" && event.Type != "response.output_text.delta" {
			fmt.Printf("🔔 Yandex event: %s\n", event.Type)
		}

		// Обрабатываем специфичные события
		switch event.Type {
		case "session.updated":
			fmt.Println("✅ Yandex session готова")
			c.sessionReady = true

		case "response.output_audio.delta":
			if event.Delta != "" {
				audioData, err := base64.StdEncoding.DecodeString(event.Delta)
				if err == nil {
					select {
					case c.audioOutput <- audioData:
					default:
						// Буфер полон
					}
				}
			}

		case "response.output_text.delta":
			if event.Delta != "" {
				select {
				case c.textOutput <- event.Delta:
				default:
				}
			}

		case "response.function_call_arguments.delta":
			// Аргументы тула приходят чанками — копим по call_id.
			if event.CallID != "" && event.Delta != "" {
				c.fcArgs[event.CallID] += event.Delta
			}

		case "response.function_call_arguments.done":
			// Готовый вызов инструмента (OpenAI GA). Доставляем блокирующе
			// (с оглядкой на stopChan) — терять его нельзя (R5).
			if event.CallID != "" {
				args := event.Arguments
				if args == "" {
					args = c.fcArgs[event.CallID]
				}
				delete(c.fcArgs, event.CallID)
				fmt.Printf("🛠️  Yandex function_call done: call_id=%s name=%q args=%q\n", event.CallID, event.Name, args)
				c.emitFunctionCall(FunctionCall{CallID: event.CallID, Name: event.Name, Arguments: args})
			}
		}
	}
}

// emitFunctionCall блокирующе доставляет вызов инструмента (R5).
func (c *Client) emitFunctionCall(fc FunctionCall) {
	select {
	case c.functionCalls <- fc:
	case <-c.stopChan:
	}
}

// AudioOutput возвращает канал с аудио данными от API
func (c *Client) AudioOutput() <-chan []byte {
	return c.audioOutput
}

// TextOutput возвращает канал с текстовыми ответами
func (c *Client) TextOutput() <-chan string {
	return c.textOutput
}

// Events возвращает канал со всеми событиями
func (c *Client) Events() <-chan Event {
	return c.events
}

// Close закрывает соединение
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected {
		return nil
	}

	close(c.stopChan)
	c.connected = false

	if c.conn != nil {
		// Отправляем нормальный close-фрейм чтобы избежать 1006 abnormal closure на стороне eventLoop
		c.writeMu.Lock()
		_ = c.conn.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		)
		c.writeMu.Unlock()
		c.conn.Close()
	}

	c.wg.Wait()
	return nil
}

// IsConnected проверяет, подключен ли клиент
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// IsSessionReady проверяет, готова ли сессия
func (c *Client) IsSessionReady() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessionReady
}

// isConnectionClosed проверяет, закрыто ли соединение
func (c *Client) isConnectionClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.connected
}
