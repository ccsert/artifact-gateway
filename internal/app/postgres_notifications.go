package app

import "context"

type postgresNotificationSource interface {
	Listen(context.Context, string) <-chan struct{}
}

func notificationWake(ctx context.Context, store any, channel string) <-chan struct{} {
	if source, ok := store.(postgresNotificationSource); ok {
		return source.Listen(ctx, channel)
	}
	return nil
}
