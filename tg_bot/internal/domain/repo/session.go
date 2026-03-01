package repo

import (
	"context"

	"tg_bot/internal/domain/entity"
)

type SessionRepository interface {
	Get(ctx context.Context, userID int64) (*entity.Session, error)
	Save(ctx context.Context, session *entity.Session) error
}
