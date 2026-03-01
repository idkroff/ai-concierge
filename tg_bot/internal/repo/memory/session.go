package memory

import (
	"context"
	"sync"

	"tg_bot/internal/domain/entity"
)

type SessionRepository struct {
	mu       sync.RWMutex
	sessions map[int64]*entity.Session
}

func NewSessionRepository() *SessionRepository {
	return &SessionRepository{
		sessions: make(map[int64]*entity.Session),
	}
}

func (r *SessionRepository) Get(_ context.Context, userID int64) (*entity.Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if s, ok := r.sessions[userID]; ok {
		copy := *s
		return &copy, nil
	}

	return &entity.Session{
		UserID: userID,
		State:  entity.StateIdle,
	}, nil
}

func (r *SessionRepository) Save(_ context.Context, session *entity.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	copy := *session
	r.sessions[session.UserID] = &copy
	return nil
}
