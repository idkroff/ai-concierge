package repo

import (
	"context"
	"errors"

	"tg_bot/internal/domain/entity"
)

var ErrUserNotFound = errors.New("user not found")

type UserRepository interface {
	Get(ctx context.Context, userID int64) (entity.User, error)
	Save(ctx context.Context, user entity.User) error
}
