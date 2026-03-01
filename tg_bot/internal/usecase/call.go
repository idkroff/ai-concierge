package usecase

import (
	"context"
	"fmt"

	"tg_bot/internal/domain/entity"
	"tg_bot/internal/domain/repo"
)

type CallerClient interface {
	StartCall(ctx context.Context, message string) (callID string, err error)
}

type ConfirmationRequest struct {
	Message string
}

type CallResult struct {
	CallID string
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

	callID, err := u.caller.StartCall(ctx, session.PendingMessage)
	if err != nil {
		return nil, fmt.Errorf("start call: %w", err)
	}

	session.State = entity.StateIdle
	session.PendingMessage = ""
	if err := u.sessions.Save(ctx, session); err != nil {
		return nil, fmt.Errorf("save session: %w", err)
	}

	return &CallResult{CallID: callID}, nil
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
