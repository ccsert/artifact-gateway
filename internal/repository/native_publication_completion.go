package repository

import (
	"context"
	"database/sql"
)

type nativePublicationCompletionKey struct{}

// WithNativePublicationCompletion lets a durable owner atomically commit its
// checkpoint after native references and audit have been written. No separate
// transaction or post-commit callback can substitute for this boundary.
func WithNativePublicationCompletion(ctx context.Context, completion func(context.Context, *sql.Tx) error) context.Context {
	return context.WithValue(ctx, nativePublicationCompletionKey{}, completion)
}
func completeNativePublication(ctx context.Context, tx *sql.Tx) error {
	completion, ok := ctx.Value(nativePublicationCompletionKey{}).(func(context.Context, *sql.Tx) error)
	if !ok || completion == nil {
		return ErrDisabled
	}
	return completion(ctx, tx)
}
