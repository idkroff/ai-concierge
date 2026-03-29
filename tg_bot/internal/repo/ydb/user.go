package ydb

import (
	"context"
	"fmt"

	"tg_bot/internal/domain/entity"
	"tg_bot/internal/domain/repo"

	"github.com/ydb-platform/ydb-go-sdk/v3/table"
	"github.com/ydb-platform/ydb-go-sdk/v3/table/result/named"
	"github.com/ydb-platform/ydb-go-sdk/v3/table/types"
)

type UserRepository struct {
	c *Client
}

func NewUserRepository(c *Client) *UserRepository {
	return &UserRepository{c: c}
}

func (r *UserRepository) Get(ctx context.Context, userID int64) (entity.User, error) {
	query := fmt.Sprintf(`
		DECLARE $user_id AS Int64;
		SELECT name, phone
		FROM %s
		WHERE user_id = $user_id;
	`, "users")

	var user entity.User
	found := false

	err := r.c.db.Table().Do(ctx, func(ctx context.Context, s table.Session) error {
		_, res, err := s.Execute(ctx, table.DefaultTxControl(), query,
			table.NewQueryParameters(
				table.ValueParam("$user_id", types.Int64Value(userID)),
			),
		)
		if err != nil {
			return err
		}
		defer res.Close()

		if res.NextResultSet(ctx) && res.NextRow() {
			found = true
			var name, phone *string
			if err := res.ScanNamed(
				named.Optional("name", &name),
				named.Optional("phone", &phone),
			); err != nil {
				return err
			}
			user.UserID = userID
			if name != nil {
				user.Name = *name
			}
			if phone != nil {
				user.Phone = *phone
			}
		}
		return res.Err()
	})
	if err != nil {
		return entity.User{}, fmt.Errorf("ydb get user: %w", err)
	}
	if !found {
		return entity.User{}, repo.ErrUserNotFound
	}
	return user, nil
}

func (r *UserRepository) Save(ctx context.Context, user entity.User) error {
	query := fmt.Sprintf(`
		DECLARE $user_id AS Int64;
		DECLARE $name AS Utf8;
		DECLARE $phone AS Utf8;
		UPSERT INTO %s (user_id, name, phone)
		VALUES ($user_id, $name, $phone);
	`, "users")

	err := r.c.db.Table().Do(ctx, func(ctx context.Context, s table.Session) error {
		_, _, err := s.Execute(ctx, table.DefaultTxControl(), query,
			table.NewQueryParameters(
				table.ValueParam("$user_id", types.Int64Value(user.UserID)),
				table.ValueParam("$name", types.UTF8Value(user.Name)),
				table.ValueParam("$phone", types.UTF8Value(user.Phone)),
			),
		)
		return err
	})
	if err != nil {
		return fmt.Errorf("ydb save user: %w", err)
	}
	return nil
}
