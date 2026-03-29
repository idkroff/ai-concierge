package ydb

import (
	"context"
	"fmt"
	"path"

	ydbsdk "github.com/ydb-platform/ydb-go-sdk/v3"
	"github.com/ydb-platform/ydb-go-sdk/v3/table"
	yc "github.com/ydb-platform/ydb-go-yc"
)

type Client struct {
	db     *ydbsdk.Driver
	prefix string
}

func NewClient(ctx context.Context, dsn string, saKeyFile string) (*Client, error) {
	db, err := ydbsdk.Open(ctx, dsn,
		yc.WithServiceAccountKeyFileCredentials(saKeyFile),
		yc.WithInternalCA(),
	)
	if err != nil {
		return nil, fmt.Errorf("ydb open: %w", err)
	}
	return &Client{db: db, prefix: db.Name()}, nil
}

func (c *Client) Close(ctx context.Context) error {
	return c.db.Close(ctx)
}

func (c *Client) EnsureTables(ctx context.Context) error {
	tables := []struct {
		name string
		ddl  string
	}{
		{
			name: "users",
			ddl: `CREATE TABLE users (
				user_id Int64,
				name Utf8,
				phone Utf8,
				PRIMARY KEY (user_id)
			)`,
		},
		{
			name: "used_calls",
			ddl: `CREATE TABLE used_calls (
				user_id Int64,
				count Uint32,
				drop_timestamp Timestamp,
				PRIMARY KEY (user_id)
			)`,
		},
	}

	for _, t := range tables {
		fullPath := path.Join(c.prefix, t.name)
		if _, err := c.db.Scheme().DescribePath(ctx, fullPath); err == nil {
			continue
		}
		err := c.db.Table().Do(ctx, func(ctx context.Context, s table.Session) error {
			return s.ExecuteSchemeQuery(ctx, t.ddl)
		})
		if err != nil {
			return fmt.Errorf("create table %s: %w", t.name, err)
		}
	}
	return nil
}
