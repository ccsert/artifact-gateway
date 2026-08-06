package app

import (
	"context"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type BackgroundOperationQueueObserver struct {
	Store   repository.BackgroundOperationQueueStore
	Metrics *Metrics
}

func (o BackgroundOperationQueueObserver) Run(ctx context.Context) error {
	stats, err := o.Store.BackgroundOperationQueueStats(ctx)
	if err != nil {
		return err
	}
	o.Metrics.ReplaceBackgroundOperationQueueStats(stats)
	return nil
}

func (o BackgroundOperationQueueObserver) Start(ctx context.Context, interval time.Duration) {
	if o.Store == nil || o.Metrics == nil || interval <= 0 {
		return
	}
	go func() {
		_ = o.Run(ctx)
		lifecycleWake := notificationWake(ctx, o.Store, "artifact_gateway_lifecycle_jobs")
		replicationWake := notificationWake(ctx, o.Store, "artifact_gateway_replication_plans")
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = o.Run(ctx)
			case <-lifecycleWake:
				_ = o.Run(ctx)
			case <-replicationWake:
				_ = o.Run(ctx)
			}
		}
	}()
}
