package repository

import (
	"context"
	"reflect"
	"testing"
	"time"
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

func TestNestedObjectLocksRetainParentOwnership(t *testing.T) {
	store := NewMemoryStore()
	parent, releaseParent, err := LockObjectKeys(context.Background(), []string{"package"}, store, FormatAPT, store.LockAPTObject)
	if err != nil {
		t.Fatal(err)
	}
	childDone := make(chan struct{})
	go func() {
		_, release, err := LockObjectKeys(parent, []string{"package", "metadata"}, store, FormatAPT, store.LockAPTObject)
		if err == nil {
			release()
		}
		close(childDone)
	}()
	select {
	case <-childDone:
	case <-time.After(time.Second):
		t.Fatal("nested publisher deadlocked on worker object")
	}
	acquired := make(chan struct{})
	go func() { release, _ := store.LockAPTObject(context.Background(), "package"); close(acquired); release() }()
	select {
	case <-acquired:
		t.Fatal("child released parent lock")
	case <-time.After(10 * time.Millisecond):
	}
	releaseParent()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("parent lock was leaked")
	}
}
