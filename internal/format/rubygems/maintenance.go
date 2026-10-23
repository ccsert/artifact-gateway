package rubygems

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	domain "github.com/ccsert/artifact-gateway/internal/rubygemspublication"
)

type Maintenance struct {
	store   domain.Store
	objects objectstore.Store
}

func NewMaintenance(store domain.Store, objects objectstore.Store) (*Maintenance, error) {
	if nilPort(store) {
		return nil, errors.New("RubyGems publication store is required")
	}
	if e := objectstore.Validate(objects); e != nil {
		return nil, e
	}
	return &Maintenance{store, objects}, nil
}
func (m *Maintenance) Collect(ctx context.Context, now time.Time) error {
	items, e := m.store.Expire(ctx, now, 100)
	if e != nil {
		return e
	}
	for _, candidate := range items {
		if e = m.collect(ctx, candidate); e != nil {
			return e
		}
	}
	return nil
}
func (m *Maintenance) collect(ctx context.Context, s domain.Session) error {
	ctx, release, e := m.store.Lock(ctx, s.RepositoryID, s.Identity())
	if e != nil {
		return e
	}
	defer release()
	current, e := m.store.GetSession(ctx, s.ID)
	if e != nil {
		return e
	}
	if current.State != "expired" || !current.CollectedAt.IsZero() || current.ObjectKey != s.ObjectKey {
		return nil
	}
	if e = m.objects.Delete(ctx, current.ObjectKey); e != nil && !errors.Is(e, objectstore.ErrNotFound) {
		return e
	}
	return m.store.MarkCollected(ctx, current.ID, current.ObjectKey)
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
				if e := m.Collect(ctx, now); e != nil && ctx.Err() == nil {
					slog.Error("RubyGems expired-upload collection failed")
				}
			}
		}
	}()
}
