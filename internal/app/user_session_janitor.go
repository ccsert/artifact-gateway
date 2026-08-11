package app

import (
	"context"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

const (
	defaultUserSessionHistoryRetention = 30 * 24 * time.Hour
	defaultUserSessionPruneBatch       = 500
)

// UserSessionJanitor keeps expired session metadata bounded while preserving a
// short operational history. Store implementations provide multi-node-safe
// bounded deletion.
type UserSessionJanitor struct {
	Store     repository.UserSessionStore
	Retention time.Duration
	BatchSize int
	Now       func() time.Time
}

func (j UserSessionJanitor) Run(ctx context.Context) (int, error) {
	retention := j.Retention
	if retention <= 0 {
		retention = defaultUserSessionHistoryRetention
	}
	batchSize := j.BatchSize
	if batchSize <= 0 {
		batchSize = defaultUserSessionPruneBatch
	}
	now := time.Now().UTC()
	if j.Now != nil {
		now = j.Now().UTC()
	}
	return j.Store.PruneExpiredUserSessions(ctx, now.Add(-retention), batchSize)
}

func (j UserSessionJanitor) Start(ctx context.Context, interval time.Duration) {
	if j.Store == nil || interval <= 0 {
		return
	}
	go func() {
		_, _ = j.Run(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = j.Run(ctx)
			}
		}
	}()
}
