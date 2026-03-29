package usecase

import (
	"context"
	"errors"
	"fmt"

	"tg_bot/internal/domain/entity"
	"tg_bot/internal/domain/repo"
)

type UserUsecase struct {
	users repo.UserRepository
}

func NewUserUsecase(users repo.UserRepository) *UserUsecase {
	return &UserUsecase{users: users}
}

func (u *UserUsecase) EnsureUser(ctx context.Context, userID int64) (entity.User, error) {
	user, err := u.users.Get(ctx, userID)
	if err == nil {
		return user, nil
	}
	if !errors.Is(err, repo.ErrUserNotFound) {
		return entity.User{}, fmt.Errorf("get user: %w", err)
	}
	user = entity.User{UserID: userID}
	if err := u.users.Save(ctx, user); err != nil {
		return entity.User{}, fmt.Errorf("save user: %w", err)
	}
	return user, nil
}

func (u *UserUsecase) SaveName(ctx context.Context, userID int64, name string) error {
	user, err := u.users.Get(ctx, userID)
	if errors.Is(err, repo.ErrUserNotFound) {
		user = entity.User{UserID: userID}
	} else if err != nil {
		return fmt.Errorf("get user: %w", err)
	}
	user.Name = name
	return u.users.Save(ctx, user)
}

func (u *UserUsecase) SavePhone(ctx context.Context, userID int64, phone string) error {
	user, err := u.users.Get(ctx, userID)
	if errors.Is(err, repo.ErrUserNotFound) {
		user = entity.User{UserID: userID}
	} else if err != nil {
		return fmt.Errorf("get user: %w", err)
	}
	user.Phone = phone
	return u.users.Save(ctx, user)
}
