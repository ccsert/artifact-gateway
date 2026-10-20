package nuget

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

// Maintenance only collects expired, never-visible upload objects. Visible
// publication retention and other governance remain outside this preview.
type Maintenance struct {
	store   repository.NativeNuGetStore
	objects objectstore.Store
}

func NewMaintenance(store repository.NativeNuGetStore, objects objectstore.Store) (*Maintenance, error) {
	if nilPort(store) {
		return nil, errors.New("NuGet maintenance publication store is required")
	}
	if err := objectstore.Validate(objects); err != nil {
		return nil, err
	}
	return &Maintenance{store: store, objects: objects}, nil
}

func (m *Maintenance) Collect(ctx context.Context, now time.Time) error {
	candidates, err := m.store.ExpireNuGetSessions(ctx, now, 100)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		if err = m.collectSession(ctx, candidate); err != nil {
			return err
		}
	}
	return nil
}

func (m *Maintenance) collectSession(ctx context.Context, candidate repository.NuGetSession) error {
	ctx, release, err := m.store.LockNuGetPublication(ctx, candidate.RepositoryID, candidate.PackageID, candidate.Version)
	if err != nil {
		return err
	}
	defer release()
	current, err := m.store.GetNuGetSession(ctx, candidate.ID)
	if err != nil {
		return err
	}
	if current.State != "expired" || !current.CollectedAt.IsZero() || current.ObjectKey != candidate.ObjectKey {
		return nil
	}
	if err = m.objects.Delete(ctx, current.ObjectKey); err != nil && !errors.Is(err, objectstore.ErrNotFound) {
		return err
	}
	// A failed marker leaves the expired row eligible for the next scan. The
	// session-specific key can never be reused by a replacement publication.
	return m.store.MarkNuGetSessionCollected(ctx, current.ID, current.ObjectKey)
}

func (m *Maintenance) Start(ctx context.Context, interval time.Duration) {
	go func() {
		timer := time.NewTicker(interval)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-timer.C:
				if err := m.Collect(ctx, now); err != nil && ctx.Err() == nil {
					slog.Error("NuGet expired upload collection failed")
				}
			}
		}
	}()
}
