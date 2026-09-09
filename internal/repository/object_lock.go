package repository

import (
	"context"
	"reflect"
	"sort"
)

type npmProxyContextLockStore interface {
	LockNPMProxyWithContext(context.Context, string) (context.Context, func(), error)
}

// LockNPMProxyWithContext lets PostgreSQL keep later npm object and admission
// locks on the same dedicated advisory-lock session. Other stores retain their
// existing single-process lock implementation.
func LockNPMProxyWithContext(ctx context.Context, store NativeNPMStore, key string) (context.Context, func(), error) {
	if locker, ok := store.(npmProxyContextLockStore); ok {
		return locker.LockNPMProxyWithContext(ctx, key)
	}
	release, err := store.LockNPMProxy(ctx, key)
	return ctx, release, err
}

// LockObjectKeys acquires object locks in a stable order and releases any
// partial acquisition on failure. Stable ordering prevents multi-file formats
// from deadlocking when two workers coordinate the same object set.
type objectLockContextKey struct{}
type objectLockContext struct {
	store  any
	format Format
	keys   map[string]bool
}

func LockObjectKeys(ctx context.Context, objectKeys []string, store any, format Format, lock func(context.Context, string) (func(), error)) (context.Context, func(), error) {
	unique := make(map[string]struct{}, len(objectKeys))
	keys := make([]string, 0, len(objectKeys))
	for _, key := range objectKeys {
		if key == "" {
			continue
		}
		if _, exists := unique[key]; exists {
			continue
		}
		unique[key] = struct{}{}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if batch, ok := store.(ArtifactObjectKeysLockStore); ok {
		return batch.LockArtifactObjectKeys(ctx, format, keys)
	}
	// A format publisher may stage an object already held by its worker. Carry
	// ownership through the context just as PostgreSQL's advisory session does;
	// reacquiring a process mutex would deadlock. Child scopes never release
	// locks owned by their parent.
	held := make(map[string]bool)
	if parent, ok := ctx.Value(objectLockContextKey{}).(objectLockContext); ok && parent.format == format && store != nil && reflect.TypeOf(store).Comparable() && parent.store == store {
		for key := range parent.keys {
			held[key] = true
		}
	}

	releases := make([]func(), 0, len(keys))
	for _, key := range keys {
		if held[key] {
			continue
		}
		release, err := lock(ctx, key)
		if err != nil {
			for index := len(releases) - 1; index >= 0; index-- {
				releases[index]()
			}
			return ctx, nil, err
		}
		releases = append(releases, release)
		held[key] = true
	}
	return context.WithValue(ctx, objectLockContextKey{}, objectLockContext{store: store, format: format, keys: held}), func() {
		for index := len(releases) - 1; index >= 0; index-- {
			releases[index]()
		}
	}, nil
}
