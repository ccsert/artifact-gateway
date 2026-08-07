package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func ListenChannels(ctx context.Context, conn *sql.Conn, channels ...string) error {
	for _, channel := range channels {
		if channel == "" {
			return fmt.Errorf("PostgreSQL notification channel is empty")
		}
		if _, err := conn.ExecContext(ctx, `LISTEN `+pgx.Identifier{channel}.Sanitize()); err != nil {
			return err
		}
	}
	return nil
}

func WaitForNotification(ctx context.Context, conn *sql.Conn) (string, error) {
	var channel string
	err := conn.Raw(func(driverConn any) error {
		provider, ok := driverConn.(interface{ Conn() *pgx.Conn })
		if !ok {
			return fmt.Errorf("PostgreSQL driver does not expose pgx connection")
		}
		notification, err := provider.Conn().WaitForNotification(ctx)
		if err != nil {
			if ctx.Err() == nil {
				return driver.ErrBadConn
			}
			return ctx.Err()
		}
		channel = notification.Channel
		return nil
	})
	return channel, err
}
