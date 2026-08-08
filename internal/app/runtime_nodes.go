package app

import (
	"context"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// RuntimeNodeHeartbeat publishes process capabilities to PostgreSQL. The
// initial write is immediate; the ticker is only a liveness refresh and can be
// missed safely because the next process start overwrites the same identity.
type RuntimeNodeHeartbeat struct {
	Store repository.RuntimeNodeStore
	Node  repository.RuntimeNode
	Now   func() time.Time
}

func (h *RuntimeNodeHeartbeat) now() time.Time {
	if h.Now != nil {
		return h.Now().UTC()
	}
	return time.Now().UTC()
}

func (h *RuntimeNodeHeartbeat) Record(ctx context.Context) error {
	if h.Store == nil {
		return repository.ErrInvalidRuntimeNode
	}
	now := h.now()
	if h.Node.StartedAt.IsZero() {
		h.Node.StartedAt = now
	}
	h.Node.LastSeenAt = now
	return h.Store.UpsertRuntimeNodeHeartbeat(ctx, h.Node)
}

func (h *RuntimeNodeHeartbeat) Start(ctx context.Context, interval time.Duration) {
	if h.Store == nil || interval <= 0 {
		return
	}
	go func() {
		_ = h.Record(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = h.Record(ctx)
			}
		}
	}()
}
