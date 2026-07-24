package app

import (
	"context"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// NativeRawMaintenance deletes only objects that have been unreferenced for
// the retention window. The metadata row remains as a collection trace.
type NativeRawMaintenance struct {
	Store   repository.NativeRawStore
	Objects OCIObjectStore
	Now     func() time.Time
}

func (m NativeRawMaintenance) Collect(ctx context.Context) error {
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}
	objects, err := m.Store.ListUnreferencedRawObjects(ctx, now().UTC().Add(-24*time.Hour), 100)
	if err != nil {
		return err
	}
	for _, object := range objects {
		release, err := m.Store.LockRawObject(ctx, object.Digest)
		if err != nil {
			return err
		}
		unreferenced, err := m.Store.RawObjectIsUnreferenced(ctx, object.Digest)
		if err != nil {
			release()
			return err
		}
		if !unreferenced {
			release()
			continue
		}
		if err = m.Objects.Delete(ctx, object.ObjectKey); err != nil {
			release()
			return err
		}
		err = m.Store.MarkRawObjectCollected(ctx, object.Digest)
		release()
		if err != nil && err != repository.ErrNotFound {
			return err
		}
	}
	return nil
}

func (m NativeRawMaintenance) Start(ctx context.Context, interval time.Duration) {
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
