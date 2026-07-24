package app

import (
	"context"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// NativeOCIMaintenance retains expired-upload rows after byte deletion so
// operators can distinguish pending, failed, and completed cleanup work.
type NativeOCIMaintenance struct {
	Store   repository.NativeOCIStore
	Objects OCIObjectStore
	Now     func() time.Time
}

func (m NativeOCIMaintenance) Collect(ctx context.Context) error {
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}
	if _, err := m.Store.ExpireOCIUploads(ctx, now().UTC(), 100); err != nil {
		return err
	}
	uploads, err := m.Store.ListUncollectedOCIUploads(ctx, 100)
	if err != nil {
		return err
	}
	for _, upload := range uploads {
		if err := m.Objects.Delete(ctx, upload.ObjectKey); err != nil {
			return err
		}
		if err := m.Store.MarkOCIUploadCollected(ctx, upload.ID); err != nil {
			return err
		}
	}
	intents, err := m.Store.ListUnclaimedOCIObjectIntents(ctx, now().UTC().Add(-24*time.Hour), 100)
	if err != nil {
		return err
	}
	for _, intent := range intents {
		release, err := m.Store.LockOCIObject(ctx, intent.ObjectKey)
		if err != nil {
			return err
		}
		unclaimed, err := m.Store.OCIObjectIntentIsUnclaimed(ctx, intent.ObjectKey)
		if err != nil {
			release()
			return err
		}
		if !unclaimed {
			release()
			continue
		}
		if err := m.Objects.Delete(ctx, intent.ObjectKey); err != nil {
			release()
			return err
		}
		if err := m.Store.MarkOCIObjectIntentCollected(ctx, intent.ObjectKey); err != nil {
			release()
			return err
		}
		release()
	}
	return nil
}

func (m NativeOCIMaintenance) Start(ctx context.Context, interval time.Duration) {
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
