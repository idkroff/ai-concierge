package events

import (
	"encoding/json"
	"time"
)

// Типы событий — добавляй новые константы и payload-структуры по необходимости.
const (
	// Подключение
	WSConnected = "ws.connected"

	// Жизненный цикл звонка
	CallStarted    = "call.started"
	CallConnecting = "call.connecting"
	CallEnded = "call.ended"
	CallError = "call.error"

	// Asterisk
	AsteriskOriginateSent     = "asterisk.originate_sent"
	AsteriskOriginateResponse = "asterisk.originate_response"
	AsteriskAudiosocketReady  = "asterisk.audiosocket_ready"
	AsteriskHangup           = "asterisk.hangup"

	// Yandex Realtime
	YandexConnecting    = "yandex.connecting"
	YandexConnected     = "yandex.connected"
	YandexSessionReady  = "yandex.session_ready"
	YandexEvent         = "yandex.event"
	YandexTextDelta     = "yandex.text_delta"
	YandexAudioChunk    = "yandex.audio_chunk"
	YandexSpeechStarted   = "yandex.speech_started"
	YandexSpeechStopped   = "yandex.speech_stopped"
	YandexResponseDone    = "yandex.response_done"
	YandexInputTranscript = "yandex.input_transcript"
)

// Event — универсальный envelope для любого события.
type Event struct {
	Type      string          `json:"type"`
	CallID    string          `json:"call_id,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// Emitter отправляет события наружу (WS, лог, метрики).
type Emitter interface {
	Emit(ev Event)
}

// NoopEmitter — заглушка, события никуда не уходят.
type NoopEmitter struct{}

func (NoopEmitter) Emit(Event) {}

// Конструкторы событий (удобно добавлять новые: константа + функция).

func NewCallStarted(callID, phoneNumber string) Event {
	return newEvent(CallStarted, callID, map[string]string{"phone_number": phoneNumber})
}

func NewCallEnded(callID, reason string) Event {
	return newEvent(CallEnded, callID, map[string]string{"reason": reason})
}

func NewCallError(callID, message, source string) Event {
	return newEvent(CallError, callID, map[string]string{"message": message, "source": source})
}

func NewWSConnected() Event {
	return newEvent(WSConnected, "", map[string]string{"status": "ready"})
}

func NewCallConnecting(callID, target string) Event {
	return newEvent(CallConnecting, callID, map[string]string{"target": target})
}

func NewAsteriskOriginateSent(callID, phoneNumber string) Event {
	return newEvent(AsteriskOriginateSent, callID, map[string]string{"phone_number": phoneNumber})
}

func NewAsteriskOriginateResponse(callID string, success bool, reason, channel string) Event {
	return newEvent(AsteriskOriginateResponse, callID, map[string]any{
		"success": success, "reason": reason, "channel": channel,
	})
}

func NewAsteriskAudiosocketReady(callID string) Event {
	return newEvent(AsteriskAudiosocketReady, callID, nil)
}

func NewAsteriskHangup(callID string) Event {
	return newEvent(AsteriskHangup, callID, nil)
}

func NewYandexConnecting(callID string) Event {
	return newEvent(YandexConnecting, callID, nil)
}

func NewYandexConnected(callID string) Event {
	return newEvent(YandexConnected, callID, nil)
}

func NewYandexSessionReady(callID string) Event {
	return newEvent(YandexSessionReady, callID, nil)
}

func NewYandexEvent(callID, yandexEventType string, detail any) Event {
	return newEvent(YandexEvent, callID, map[string]any{"event_type": yandexEventType, "detail": detail})
}

func NewYandexTextDelta(callID, text string) Event {
	return newEvent(YandexTextDelta, callID, map[string]string{"text": text})
}

func NewYandexAudioChunk(callID string, sizeBytes int) Event {
	return newEvent(YandexAudioChunk, callID, map[string]any{"size_bytes": sizeBytes})
}

func NewYandexSpeechStarted(callID string) Event {
	return newEvent(YandexSpeechStarted, callID, nil)
}

func NewYandexSpeechStopped(callID string) Event {
	return newEvent(YandexSpeechStopped, callID, nil)
}

func NewYandexResponseDone(callID string) Event {
	return newEvent(YandexResponseDone, callID, nil)
}

func NewYandexInputTranscript(callID, text string) Event {
	return newEvent(YandexInputTranscript, callID, map[string]string{"text": text})
}

func newEvent(typ, callID string, payload any) Event {
	var raw json.RawMessage
	if payload != nil {
		raw, _ = json.Marshal(payload)
	}
	return Event{Type: typ, CallID: callID, Timestamp: time.Now().UTC(), Payload: raw}
}
