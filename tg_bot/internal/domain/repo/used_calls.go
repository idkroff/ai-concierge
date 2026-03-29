package repo

import "context"

type UsedCallsRepository interface {
	GetTodayCount(ctx context.Context, userID int64) (uint32, error)
	Increment(ctx context.Context, userID int64) error
}
