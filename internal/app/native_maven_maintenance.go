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
}

func (m NativeMavenMaintenance) Collect(ctx context.Context) error {
	intents, err := m.Store.ClaimExpiredMavenObjectIntents(ctx, time.Now().UTC().Add(-24*time.Hour), 100)
	if err != nil {
		return err
	}
	for _, intent := range intents {
		if err := m.Store.DeleteClaimedMavenObjectIntent(ctx, intent.ObjectKey); err != nil {
			continue
		}
		if err := m.Objects.Delete(ctx, intent.ObjectKey); err != nil {
			return err
		}
	}
	return nil
}
