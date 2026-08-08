package app

import (
	"context"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// RuntimeNodeHeartbeat publishes one process session and its capabilities. The
// session ID fences graceful shutdown from later processes using the same
// deployment-owned instance ID.
type RuntimeNodeHeartbeat struct {
	Store         repository.RuntimeNodeStore
	Node          repository.RuntimeNode
	Retention     time.Duration
	PruneInterval time.Duration
	Now           func() time.Time
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
	h.Node.StoppedAt = time.Time{}
	return h.Store.UpsertRuntimeNodeHeartbeat(ctx, h.Node)
}

func (h *RuntimeNodeHeartbeat) Stop(ctx context.Context) error {
	if h.Store == nil {
		return repository.ErrInvalidRuntimeNode
	}
	stoppedAt := h.now()
	h.Node.StoppedAt = stoppedAt
	h.Node.LastSeenAt = stoppedAt
	return h.Store.MarkRuntimeNodeStopped(ctx, h.Node.InstanceID, h.Node.SessionID, stoppedAt)
}

func (h *RuntimeNodeHeartbeat) Start(ctx context.Context, interval time.Duration) <-chan struct{} {
	done := make(chan struct{})
	if h.Store == nil || interval <= 0 {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		_ = h.Record(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		var pruneTicker *time.Ticker
		var prune <-chan time.Time
		if h.Retention > 0 && h.PruneInterval > 0 {
			pruneTicker = time.NewTicker(h.PruneInterval)
			prune = pruneTicker.C
			defer pruneTicker.Stop()
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = h.Record(ctx)
			case <-prune:
				_, _ = h.Store.PruneRuntimeNodes(ctx, h.now().Add(-h.Retention))
			}
		}
	}()
	return done
}
