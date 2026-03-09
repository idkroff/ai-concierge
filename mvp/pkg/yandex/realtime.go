package yandex

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Event types
type Event struct {
	Type     string          `json:"type"`
	Session  json.RawMessage `json:"session,omitempty"`
	Audio    string          `json:"audio,omitempty"`
	Delta    string          `json:"delta,omitempty"`
	Response json.RawMessage `json:"response,omitempty"`
	Error    json.RawMessage `json:"error,omitempty"`
	Message  string          `json:"message,omitempty"`
}

type SessionUpdate struct {
	Type    string        `json:"type"`
	Session SessionConfig `json:"session"`
}

type SessionConfig struct {
	Type             string      `json:"type"`
	OutputModalities []string    `json:"output_modalities"`
	Audio            AudioConfig `json:"audio"`
	Instructions     string      `json:"instructions"`
}

type AudioConfig struct {
	Input  InputAudioConfig  `json:"input"`
	Output OutputAudioConfig `json:"output"`
}

type InputAudioConfig struct {
	Format        AudioFormat   `json:"format"`
	TurnDetection TurnDetection `json:"turn_detection"`
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

	// Каналы для коммуникации
	audioOutput chan []byte // Аудио от Yandex (для воспроизведения)
	textOutput  chan string // Текстовый ответ от Yandex
	events      chan Event  // Все события

	// Управление
	stopChan chan struct{}
	wg       sync.WaitGroup
	mu       sync.Mutex

	// Состояние
	connected    bool
	sessionReady bool
}

// NewClient создает новый клиент Yandex Realtime API
func NewClient(apiKey, folder, instructions string) *Client {
	return &Client{
		apiKey:       apiKey,
		folder:       folder,
		instructions: instructions,
		audioOutput:  make(chan []byte, 100),
		textOutput:   make(chan string, 10),
		events:       make(chan Event, 50),
		stopChan:     make(chan struct{}),
	}
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
	sessionUpdate := SessionUpdate{
		Type: "session.update",
		Session: SessionConfig{
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
		},
	}

	return c.conn.WriteJSON(sessionUpdate)
}

// SendAudio отправляет аудио данные в API (должно быть PCM 24kHz)
func (c *Client) SendAudio(audioData []byte) error {
	msg := InputAudioBufferAppend{
		Type:  "input_audio_buffer.append",
		Audio: base64.StdEncoding.EncodeToString(audioData),
	}

	return c.conn.WriteJSON(msg)
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

	return c.conn.WriteJSON(responseCreate)
}

// eventLoop обрабатывает входящие события
func (c *Client) eventLoop() {
	defer c.wg.Done()
	defer close(c.audioOutput)
	defer close(c.textOutput)
	defer close(c.events)

	c.conn.SetReadDeadline(time.Now().Add(300 * time.Second))

	for {
		select {
		case <-c.stopChan:
			return
		default:
		}

		var event Event
		if err := c.conn.ReadJSON(&event); err != nil {
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

		// Отправляем событие в канал
		select {
		case c.events <- event:
		default:
		}

		// Логируем все события для отладки
		if event.Type != "response.output_audio.delta" && event.Type != "response.output_text.delta" {
			if event.Type == "error" {
				errorJSON, _ := json.MarshalIndent(event, "", "  ")
				fmt.Printf("🔔 Yandex event: %s\n%s\n", event.Type, string(errorJSON))
			} else {
				fmt.Printf("🔔 Yandex event: %s\n", event.Type)
			}
		}
		if event.Type == "error" {
			details, _ := json.MarshalIndent(event, "", "  ")
			fmt.Printf("❌ Yandex error event: %s\n", string(details))
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
		}
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
