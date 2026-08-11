package repository

import (
	"context"
	"reflect"
	"testing"
)

type objectLockTestContextKey struct{}

type recordingObjectKeysLockStore struct {
	format Format
	keys   []string
}

func (s *recordingObjectKeysLockStore) LockArtifactObjectKeys(ctx context.Context, format Format, keys []string) (context.Context, func(), error) {
	s.format = format
	s.keys = append([]string(nil), keys...)
	return context.WithValue(ctx, objectLockTestContextKey{}, "locked"), func() {}, nil
}

func TestLockObjectKeysUsesBatchStoreAndReturnsDerivedContext(t *testing.T) {
	store := &recordingObjectKeysLockStore{}
	lockedCtx, release, err := LockObjectKeys(context.Background(), []string{"z", "a", "z", ""}, store, FormatPyPI, nil)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if store.format != FormatPyPI || !reflect.DeepEqual(store.keys, []string{"a", "z"}) {
		t.Fatalf("batch lock format=%q keys=%v", store.format, store.keys)
	}
	if value := lockedCtx.Value(objectLockTestContextKey{}); value != "locked" {
		t.Fatalf("derived context value=%v", value)
	}
}
