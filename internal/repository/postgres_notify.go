package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Listen returns a reconnecting wake-up channel for a PostgreSQL NOTIFY
// channel. Notifications are hints only; callers must re-check their durable
// task table before doing work.
func (s *PostgresStore) Listen(ctx context.Context, channel string) <-chan struct{} {
	wake := make(chan struct{}, 1)
	go func() {
		defer close(wake)
		for ctx.Err() == nil {
			conn, err := pgx.Connect(ctx, s.databaseURL)
			if err == nil {
				_, err = conn.Exec(ctx, `LISTEN `+pgx.Identifier{channel}.Sanitize())
			}
			if err != nil {
				if conn != nil {
					_ = conn.Close(context.Background())
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second):
				}
				continue
			}
			for ctx.Err() == nil {
				if _, err = conn.WaitForNotification(ctx); err != nil {
					break
				}
				select {
				case wake <- struct{}{}:
				default:
				}
			}
			_ = conn.Close(context.Background())
		}
	}()
	return wake
}
