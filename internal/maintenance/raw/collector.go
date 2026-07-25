package raw

import (
	"context"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// Store is the lifecycle data required to safely collect a Raw object.
type Store interface {
	ListUnreferencedRawObjects(context.Context, time.Time, int) ([]repository.RawObject, error)
	LockRawObject(context.Context, string) (func(), error)
	RawObjectIsUnreferenced(context.Context, string) (bool, error)
	MarkRawObjectCollected(context.Context, string) error
}

// ObjectStore deletes content-addressed bytes after the Store has confirmed
// that no Raw asset still references them.
type ObjectStore interface {
	Delete(context.Context, string) error
}

// Collector removes Raw objects that have remained unreferenced throughout the
// retention window. It leaves the metadata collection trace intact.
type Collector struct {
	Store   Store
	Objects ObjectStore
	Now     func() time.Time
}

func (c Collector) Collect(ctx context.Context) error {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	objects, err := c.Store.ListUnreferencedRawObjects(ctx, now().UTC().Add(-24*time.Hour), 100)
	if err != nil {
		return err
	}
	for _, object := range objects {
		release, err := c.Store.LockRawObject(ctx, object.Digest)
		if err != nil {
			return err
		}
		unreferenced, err := c.Store.RawObjectIsUnreferenced(ctx, object.Digest)
		if err != nil {
			release()
			return err
		}
		if !unreferenced {
			release()
			continue
		}
		if err = c.Objects.Delete(ctx, object.ObjectKey); err != nil {
			release()
			return err
		}
		err = c.Store.MarkRawObjectCollected(ctx, object.Digest)
		release()
		if err != nil && err != repository.ErrNotFound {
			return err
		}
	}
	return nil
}

func (c Collector) Start(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = c.Collect(ctx)
			}
		}
	}()
}
