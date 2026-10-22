package helm

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

// Maintenance collects only expired, never-visible upload closures. Visible
// chart governance remains outside this explicitly bounded Hosted preview.
type Maintenance struct {
	store   repository.NativeHelmStore
	objects objectstore.Store
}

func NewMaintenance(store repository.NativeHelmStore, objects objectstore.Store) (*Maintenance, error) {
	if nilPort(store) {
		return nil, errors.New("Helm maintenance publication store is required")
	}
	if err := objectstore.Validate(objects); err != nil {
		return nil, err
	}
	return &Maintenance{store: store, objects: objects}, nil
}
func (m *Maintenance) Collect(ctx context.Context, now time.Time) error {
	candidates, err := m.store.ExpireHelmSessions(ctx, now, 100)
	if err != nil {
		return err
	}
	for _, in := range candidates {
		if err = m.collect(ctx, in); err != nil {
			return err
		}
	}
	return nil
}
func (m *Maintenance) collect(ctx context.Context, candidate repository.HelmSession) error {
	ctx, release, err := m.store.LockHelmPublication(ctx, candidate.RepositoryID, candidate.Name, candidate.Version)
	if err != nil {
		return err
	}
	defer release()
	current, err := m.store.GetHelmSession(ctx, candidate.ID)
	if err != nil {
		return err
	}
	if current.State != "expired" || !current.CollectedAt.IsZero() || current.ObjectKey != candidate.ObjectKey || current.ProvenanceKey != candidate.ProvenanceKey {
		return nil
	}
	for _, key := range []string{current.ObjectKey, current.ProvenanceKey} {
		if key != "" {
			if err = m.objects.Delete(ctx, key); err != nil && !errors.Is(err, objectstore.ErrNotFound) {
				return err
			}
		}
	}
	return m.store.MarkHelmSessionCollected(ctx, current.ID, current.ObjectKey)
}
func (m *Maintenance) Start(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if err := m.Collect(ctx, now); err != nil && ctx.Err() == nil {
					slog.Error("Helm expired upload collection failed")
				}
			}
		}
	}()
}
