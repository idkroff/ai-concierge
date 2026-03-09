package usecase

import (
	"context"
	"encoding/json"
	"fmt"

	"tg_bot/internal/domain/entity"
	"tg_bot/internal/domain/repo"
)

type CallerClient interface {
	StartCall(ctx context.Context, message string) (callID string, events <-chan entity.CallEvent, err error)
}

type TranscriptRole string

const (
	RoleSystem TranscriptRole = "system"
	RoleAgent  TranscriptRole = "agent"
	RoleCallee TranscriptRole = "callee"
)

// TranscriptEntry — одна реплика в диалоге.
type TranscriptEntry struct {
	Role TranscriptRole
	Text string
}

// CallUpdate — текущее состояние звонка для отображения.
type CallUpdate struct {
	Transcript      []TranscriptEntry
	AgentStreaming  string // текст агента в процессе генерации
	AbonentSpeaking bool   // абонент сейчас говорит
	Ended           bool
	EndReason       string
	Error           string
}

type ConfirmationRequest struct {
	Message string
}

type CallResult struct {
	CallID  string
	Updates <-chan CallUpdate
}

type CallUsecase struct {
	sessions repo.SessionRepository
	caller   CallerClient
}

func NewCallUsecase(sessions repo.SessionRepository, caller CallerClient) *CallUsecase {
	return &CallUsecase{
		sessions: sessions,
		caller:   caller,
	}
}

func (u *CallUsecase) HandleMessage(ctx context.Context, userID int64, message string) (*ConfirmationRequest, error) {
	session, err := u.sessions.Get(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	session.State = entity.StateAwaitingConfirmation
	session.PendingMessage = message

	if err := u.sessions.Save(ctx, session); err != nil {
		return nil, fmt.Errorf("save session: %w", err)
	}

	return &ConfirmationRequest{Message: message}, nil
}

func (u *CallUsecase) ConfirmCall(ctx context.Context, userID int64) (*CallResult, error) {
	session, err := u.sessions.Get(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	if session.State != entity.StateAwaitingConfirmation {
		return nil, fmt.Errorf("unexpected session state: %s", session.State)
	}

	callID, events, err := u.caller.StartCall(ctx, session.PendingMessage)
	if err != nil {
		return nil, fmt.Errorf("start call: %w", err)
	}

	session.State = entity.StateIdle
	session.PendingMessage = ""
	if err := u.sessions.Save(ctx, session); err != nil {
		return nil, fmt.Errorf("save session: %w", err)
	}

	return &CallResult{CallID: callID, Updates: watchEvents(events)}, nil
}

func (u *CallUsecase) CancelCall(ctx context.Context, userID int64) error {
	session, err := u.sessions.Get(ctx, userID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	session.State = entity.StateIdle
	session.PendingMessage = ""

	if err := u.sessions.Save(ctx, session); err != nil {
		return fmt.Errorf("save session: %w", err)
	}

	return nil
}

// watchEvents преобразует поток сырых событий в поток обновлений состояния звонка.
func watchEvents(events <-chan entity.CallEvent) <-chan CallUpdate {
	ch := make(chan CallUpdate, 8)
	go func() {
		defer close(ch)

		var transcript []TranscriptEntry
		var agentText string
		abonentSpeaking := false

		send := func(ended bool, endReason, errMsg string) {
			snap := make([]TranscriptEntry, len(transcript))
			copy(snap, transcript)
			ch <- CallUpdate{
				Transcript:      snap,
				AgentStreaming:  agentText,
				AbonentSpeaking: abonentSpeaking,
				Ended:           ended,
				EndReason:       endReason,
				Error:           errMsg,
			}
		}

		for ev := range events {
			switch ev.Type {
			case "asterisk.audiosocket_ready":
				transcript = append(transcript, TranscriptEntry{Role: RoleSystem, Text: "абонент ответил"})
				send(false, "", "")

			case "yandex.input_transcript":
				var p struct {
					Text string `json:"text"`
				}
				_ = json.Unmarshal(ev.Payload, &p)
				if p.Text != "" {
					transcript = append(transcript, TranscriptEntry{Role: RoleCallee, Text: p.Text})
					abonentSpeaking = false
					send(false, "", "")
				}

			case "yandex.speech_started":
				abonentSpeaking = true
				send(false, "", "")

			case "yandex.speech_stopped":
				abonentSpeaking = false

			case "yandex.text_delta":
				var p struct {
					Text string `json:"text"`
				}
				_ = json.Unmarshal(ev.Payload, &p)
				agentText += p.Text

			case "yandex.response_done":
				if agentText != "" {
					transcript = append(transcript, TranscriptEntry{Role: RoleAgent, Text: agentText})
					agentText = ""
					abonentSpeaking = false
					send(false, "", "")
				}

			case "call.ended":
				var p struct {
					Reason string `json:"reason"`
				}
				_ = json.Unmarshal(ev.Payload, &p)
				if agentText != "" {
					transcript = append(transcript, TranscriptEntry{Role: RoleAgent, Text: agentText})
				}
				send(true, p.Reason, "")

			case "call.error":
				var p struct {
					Message string `json:"message"`
				}
				_ = json.Unmarshal(ev.Payload, &p)
				send(true, "", p.Message)
			}
		}
	}()
	return ch
}
