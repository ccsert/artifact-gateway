package app

import (
	"context"
	"time"
)

// acquireCacheRequestLock serializes an upstream cache miss across Gateway
// instances. Callers must re-read their cache after acquiring the lock: the
// previous holder may have populated either a positive or negative entry.
func acquireCacheRequestLock(ctx context.Context, coordinator OCICacheCoordinator, key string) (func(), error) {
	if coordinator == nil {
		return func() {}, nil
	}
	for {
		owner, acquired, err := coordinator.Acquire(ctx, "cache-request:"+key, cacheDistributedLockLease)
		if err != nil {
			return nil, err
		}
		if acquired {
			return func() {
				releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer cancel()
				_ = coordinator.Release(releaseCtx, "cache-request:"+key, owner)
			}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}
