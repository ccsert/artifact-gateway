package app

import "context"

type blockingLifecycleDeleteStore struct {
	OCIObjectStore
	entered chan struct{}
	release chan struct{}
}

func (s blockingLifecycleDeleteStore) Delete(ctx context.Context, key string) error {
	s.entered <- struct{}{}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.release:
		return s.OCIObjectStore.Delete(ctx, key)
	}
}
