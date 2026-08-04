package app

import (
	"context"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type LifecycleJobRecovery struct {
	Store repository.LifecycleJobStore
	Now   func() time.Time
}

func (r LifecycleJobRecovery) Run(ctx context.Context) (int, error) {
	now := time.Now().UTC()
	if r.Now != nil {
		now = r.Now().UTC()
	}
	return r.Store.RecoverExpiredLifecycleJobs(ctx, now)
}

func (r LifecycleJobRecovery) Start(ctx context.Context, interval time.Duration) {
	if r.Store == nil || interval <= 0 {
		return
	}
	go func() {
		_, _ = r.Run(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = r.Run(ctx)
			}
		}
	}()
}
