package app

import (
	"context"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// NativeMavenMaintenance collects only unreferenced intents older than the
// publication recovery window. The store rechecks references while deleting.
type NativeMavenMaintenance struct {
	Store   repository.NativeMavenStore
	Objects OCIObjectStore
	Now     func() time.Time
}

func (m NativeMavenMaintenance) Collect(ctx context.Context) error {
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}
	intents, err := m.Store.ClaimExpiredMavenObjectIntents(ctx, now().UTC().Add(-24*time.Hour), 100)
	if err != nil {
		return err
	}
	for _, intent := range intents {
		referenced, err := m.Store.MavenObjectIntentHasReference(ctx, intent.ObjectKey)
		if err != nil {
			return err
		}
		if referenced {
			// A committed reference wins over this collector claim. The commit path
			// fences claimed intents, so no promotion can pass this point after the
			// recheck and before object deletion.
			_ = m.Store.ReleaseClaimedMavenObjectIntent(ctx, intent.ObjectKey, intent.ClaimToken)
			continue
		}
		if err := m.Objects.Delete(ctx, intent.ObjectKey); err != nil {
			_ = m.Store.ReleaseClaimedMavenObjectIntent(ctx, intent.ObjectKey, intent.ClaimToken)
			return err
		}
		if err := m.Store.DeleteClaimedMavenObjectIntent(ctx, intent.ObjectKey, intent.ClaimToken); err != nil {
			_ = m.Store.ReleaseClaimedMavenObjectIntent(ctx, intent.ObjectKey, intent.ClaimToken)
			return err
		}
	}
	return nil
}

func (m NativeMavenMaintenance) Start(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = m.Collect(ctx)
			}
		}
	}()
}
