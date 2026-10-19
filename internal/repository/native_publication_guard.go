package repository

import (
	"context"
	"database/sql"
)

type nativePublicationGuardKey struct{}

// NativePublicationGuard validates an external durable owner inside the actual
// native publication transaction. The owner supplies its own SQL knowledge;
// repository only guarantees that its visibility changes share that transaction.
type NativePublicationGuard func(context.Context, *sql.Tx) error

// WithNativePublicationGuard adds a guard without discarding any existing owner
// constraint. A failed guard prevents all native reference changes.
func WithNativePublicationGuard(ctx context.Context, guard NativePublicationGuard) context.Context {
	if guard == nil {
		return ctx
	}
	if previous, ok := ctx.Value(nativePublicationGuardKey{}).(NativePublicationGuard); ok {
		next := guard
		guard = func(ctx context.Context, tx *sql.Tx) error {
			if err := previous(ctx, tx); err != nil {
				return err
			}
			return next(ctx, tx)
		}
	}
	return context.WithValue(ctx, nativePublicationGuardKey{}, guard)
}

func checkNativePublicationGuard(ctx context.Context, tx *sql.Tx) error {
	if guard, ok := ctx.Value(nativePublicationGuardKey{}).(NativePublicationGuard); ok {
		return guard(ctx, tx)
	}
	return nil
}
