//go:build integration

package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/database"
	"github.com/google/uuid"
)

func newOCILockTestStore(t *testing.T, primarySize, lockSize int) (*PostgresStore, *sql.DB, *sql.DB) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	open := func(size int) *sql.DB {
		config := database.DefaultPoolConfig()
		config.MaxOpenConns, config.MaxIdleConns = size, size
		pool, err := database.OpenPostgres(url, config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = pool.Close() })
		return pool
	}
	primary, notifications, locks := open(primarySize), open(1), open(lockSize)
	store, err := NewPostgresStoreWithPools(primary, notifications, locks)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, primary, locks
}

func TestPostgresOCINestedLocksShareOneConnection(t *testing.T) {
	store, primary, locks := newOCILockTestStore(t, 1, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	uploadCtx, releaseUpload, err := store.LockOCIUpload(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	defer releaseUpload()
	releaseObject, err := store.LockOCIObject(uploadCtx, "nested-object-"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if locks.Stats().InUse != 1 {
		t.Fatalf("nested locks used %d connections, want 1", locks.Stats().InUse)
	}
	if err := primary.PingContext(ctx); err != nil {
		t.Fatalf("metadata unavailable inside nested locks: %v", err)
	}
	releaseObject()
	releaseObject() // Release is idempotent, including nested references.
	if locks.Stats().InUse != 1 {
		t.Fatal("releasing object lock also released the outer upload session")
	}
	releaseUpload()
	if locks.Stats().InUse != 0 {
		t.Fatal("upload release left a lock connection checked out")
	}
}

func TestPostgresOCINestedCancellationDropsSessionAndPreservesOtherOwner(t *testing.T) {
	first, primary, locks := newOCILockTestStore(t, 1, 1)
	second, _, secondLocks := newOCILockTestStore(t, 1, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, key := uuid.NewString(), "contended-object-"+uuid.NewString()
	releaseHeld, err := second.LockOCIObject(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseHeld()
	uploadCtx, releaseUpload, err := first.LockOCIUpload(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseUpload()
	waitCtx, waitCancel := context.WithTimeout(uploadCtx, 100*time.Millisecond)
	defer waitCancel()
	if release, err := first.LockOCIObject(waitCtx, key); err == nil {
		release()
		t.Fatal("acquired another instance's object lock")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("nested lock cancellation: %v", err)
	}
	if locks.Stats().InUse != 0 || secondLocks.Stats().InUse != 1 {
		t.Fatalf("cancelled borrower changed lock ownership: first=%+v second=%+v", locks.Stats(), secondLocks.Stats())
	}
	if err := primary.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	releaseHeld()
	// The failed nested acquisition aborts its outer session as well. A different
	// Store must acquire that upload key, not just another reentrant session.
	_, releaseRecovered, err := second.LockOCIUpload(ctx, id)
	if err != nil {
		t.Fatalf("outer upload lock leaked after cancellation: %v", err)
	}
	releaseRecovered()
	// Reusing the now-closed carried context starts a fresh owner/session.
	_, releaseFresh, err := first.LockOCIUpload(uploadCtx, id)
	if err != nil {
		t.Fatal(err)
	}
	releaseFresh()
}

func TestPostgresOCIBrokenSessionIsNotReturnedToPool(t *testing.T) {
	store, primary, locks := newOCILockTestStore(t, 1, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	key := "terminated-object-" + uuid.NewString()
	release, err := store.LockOCIObject(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	// Target only the session holding this unique synthetic lock, never an
	// arbitrary backend or an application-wide connection set.
	var pid int
	if err = primary.QueryRowContext(ctx, `SELECT pid FROM pg_locks
		WHERE locktype='advisory' AND granted AND objsubid=1
		AND classid=((hashtextextended($1,0)>>32)&4294967295)::oid
		AND objid=(hashtextextended($1,0)&4294967295)::oid`, "native-oci-object:"+key).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err = primary.ExecContext(ctx, "SELECT pg_terminate_backend($1)", pid); err != nil {
		t.Fatal(err)
	}
	release()
	if locks.Stats().InUse != 0 {
		t.Fatal("broken session remains checked out")
	}
	recovered, err := store.LockOCIObject(ctx, key)
	if err != nil {
		t.Fatalf("could not reacquire after backend failure: %v", err)
	}
	recovered()
}

func TestPostgresOCILocksLeavePrimaryPoolAvailable(t *testing.T) {
	for _, kind := range []string{"upload", "object"} {
		t.Run(kind, func(t *testing.T) {
			store, primary, locks := newOCILockTestStore(t, 2, 4)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for i := range 2 {
				key := fmt.Sprintf("oci-pool-%s-%d-%s", kind, i, uuid.NewString())
				var release func()
				var err error
				if kind == "upload" {
					_, release, err = store.LockOCIUpload(ctx, key)
				} else {
					release, err = store.LockOCIObject(ctx, key)
				}
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(release)
			}
			queryCtx, queryCancel := context.WithTimeout(ctx, 250*time.Millisecond)
			defer queryCancel()
			var value int
			if err := primary.QueryRowContext(queryCtx, "SELECT 1").Scan(&value); err != nil {
				t.Fatalf("metadata query blocked by OCI %s locks: %v; primary=%+v locks=%+v", kind, err, primary.Stats(), locks.Stats())
			}
			if value != 1 || locks.Stats().InUse != 2 || primary.Stats().InUse != 0 {
				t.Fatalf("unexpected pool isolation: value=%d primary=%+v locks=%+v", value, primary.Stats(), locks.Stats())
			}
		})
	}
}
