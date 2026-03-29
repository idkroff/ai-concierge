package ydb

import (
	"context"
	"fmt"
	"time"

	"github.com/ydb-platform/ydb-go-sdk/v3/table"
	"github.com/ydb-platform/ydb-go-sdk/v3/table/result/named"
	"github.com/ydb-platform/ydb-go-sdk/v3/table/types"
)

var moscowLoc = time.FixedZone("UTC+3", 3*60*60)

type UsedCallsRepository struct {
	c *Client
}

func NewUsedCallsRepository(c *Client) *UsedCallsRepository {
	return &UsedCallsRepository{c: c}
}

func (r *UsedCallsRepository) GetTodayCount(ctx context.Context, userID int64) (uint32, error) {
	count, dropTS, found, err := r.read(ctx, userID)
	if err != nil {
		return 0, err
	}
	if !found || dayChanged(dropTS, time.Now()) {
		return 0, nil
	}
	return count, nil
}

func (r *UsedCallsRepository) Increment(ctx context.Context, userID int64) error {
	count, dropTS, found, err := r.read(ctx, userID)
	if err != nil {
		return err
	}

	now := time.Now()
	if !found || dayChanged(dropTS, now) {
		return r.upsert(ctx, userID, 1, now)
	}
	return r.upsert(ctx, userID, count+1, dropTS)
}

func dayChanged(dropTS, now time.Time) bool {
	return dropTS.In(moscowLoc).Format("2006-01-02") != now.In(moscowLoc).Format("2006-01-02")
}

func (r *UsedCallsRepository) read(ctx context.Context, userID int64) (count uint32, dropTS time.Time, found bool, err error) {
	query := fmt.Sprintf(`
		DECLARE $user_id AS Int64;
		SELECT count, drop_timestamp
		FROM %s
		WHERE user_id = $user_id;
	`, "used_calls")

	err = r.c.db.Table().Do(ctx, func(ctx context.Context, s table.Session) error {
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
			var c *uint32
			var ts *time.Time
			if err := res.ScanNamed(
				named.Optional("count", &c),
				named.Optional("drop_timestamp", &ts),
			); err != nil {
				return err
			}
			if c != nil && ts != nil {
				count = *c
				dropTS = *ts
				found = true
			}
		}
		return res.Err()
	})
	if err != nil {
		err = fmt.Errorf("ydb read used_calls: %w", err)
	}
	return
}

func (r *UsedCallsRepository) upsert(ctx context.Context, userID int64, count uint32, dropTS time.Time) error {
	query := fmt.Sprintf(`
		DECLARE $user_id AS Int64;
		DECLARE $count AS Uint32;
		DECLARE $drop_timestamp AS Timestamp;
		UPSERT INTO %s (user_id, count, drop_timestamp)
		VALUES ($user_id, $count, $drop_timestamp);
	`, "used_calls")

	err := r.c.db.Table().Do(ctx, func(ctx context.Context, s table.Session) error {
		_, _, err := s.Execute(ctx, table.DefaultTxControl(), query,
			table.NewQueryParameters(
				table.ValueParam("$user_id", types.Int64Value(userID)),
				table.ValueParam("$count", types.Uint32Value(count)),
				table.ValueParam("$drop_timestamp", types.TimestampValueFromTime(dropTS)),
			),
		)
		return err
	})
	if err != nil {
		return fmt.Errorf("ydb upsert used_calls: %w", err)
	}
	return nil
}
