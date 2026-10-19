//go:build integration

package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresConanObjectCollectionFence(t *testing.T) {
	store := openPostgresConanManagedContractStore(t)
	exerciseConanObjectCollectionFence(t, store, func(t *testing.T, key string) ConanObjectIntent {
		return postgresConanCollectionObject(t, store, key)
	})
}

func TestPostgresConanObjectCollectionSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"recipe", "package"} {
		for _, phase := range []string{"delete_not_acknowledged", "finalization_failure"} {
			t.Run(kind+"/"+phase, func(t *testing.T) {
				store := openPostgresConanManagedContractStore(t)
				f, target := newConanCollectionPromotion(t, store, kind)
				asset := f.assets(t)[0]
				conanCollectionTombstone(t, store, f.session, f.repo.ID)
				conanCollectionTombstone(t, store, f.session, target.ID)
				if collect, err := store.BeginConanObjectCollection(ctx, asset.ObjectKey); err != nil || !collect {
					t.Fatalf("claim before restart collect=%t err=%v", collect, err)
				}
				before := postgresConanCollectionObject(t, store, asset.ObjectKey)
				if phase == "finalization_failure" {
					remove := installConanCollectionCompletionFailure(t, store, asset.ObjectKey)
					if err := store.CompleteConanObjectCollection(ctx, asset.ObjectKey); err == nil || !strings.Contains(err.Error(), "fixture Conan collection finalization unavailable") {
						t.Fatalf("did not reach finalization failure: %v", err)
					}
					remove()
				}
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
				reopened, err := NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = reopened.Close() })
				after := postgresConanCollectionObject(t, reopened, asset.ObjectKey)
				if after.CollectingAt.IsZero() || !after.CollectingAt.Equal(before.CollectingAt) || !after.CollectedAt.IsZero() {
					t.Fatalf("worker restart released durable claim: before=%+v after=%+v", before, after)
				}
				if err := conanCollectionRestore(ctx, reopened, f.session, target.ID); !errors.Is(err, ErrNotFound) {
					t.Fatalf("shared restore crossed restart fence: %v", err)
				}
				if collect, err := reopened.BeginConanObjectCollection(ctx, asset.ObjectKey); err != nil || !collect {
					t.Fatalf("restart could not retry deletion collect=%t err=%v", collect, err)
				}
				if err := reopened.CompleteConanObjectCollection(ctx, asset.ObjectKey); err != nil {
					t.Fatal(err)
				}
				if err := reopened.CompleteConanObjectCollection(ctx, asset.ObjectKey); err != nil {
					t.Fatalf("finalization retry is not idempotent: %v", err)
				}
				if collect, err := reopened.BeginConanObjectCollection(ctx, asset.ObjectKey); err != nil || collect {
					t.Fatalf("collected object granted a new delete claim collect=%t err=%v", collect, err)
				}
				conanCollectionAssertDeleted(t, reopened, f.session, target.ID)
			})
		}
	}
}

func TestPostgresConanObjectCollectionSerializesSharedRestore(t *testing.T) {
	store := openPostgresConanManagedContractStore(t)
	for _, kind := range []string{"recipe", "package"} {
		t.Run(kind, func(t *testing.T) {
			// Separate connections race over a source-owned key and a target
			// repository. Both operations must agree on one global object boundary.
			for attempt := 0; attempt < 8; attempt++ {
				f, target := newConanCollectionPromotion(t, store, kind)
				asset := f.assets(t)[0]
				conanCollectionTombstone(t, store, f.session, f.repo.ID)
				conanCollectionTombstone(t, store, f.session, target.ID)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				start := make(chan struct{})
				type collectionResult struct {
					collect bool
					err     error
				}
				collection, restored := make(chan collectionResult, 1), make(chan error, 1)
				go func() {
					<-start
					collect, err := store.BeginConanObjectCollection(ctx, asset.ObjectKey)
					collection <- collectionResult{collect, err}
				}()
				go func() { <-start; restored <- conanCollectionRestore(ctx, store, f.session, target.ID) }()
				close(start)
				claim, restoreErr := <-collection, <-restored
				cancel()
				if claim.err != nil {
					t.Fatal(claim.err)
				}
				if claim.collect {
					if !errors.Is(restoreErr, ErrNotFound) {
						t.Fatalf("both shared restore and collector won: collect=%t restore=%v", claim.collect, restoreErr)
					}
					conanCollectionAssertDeleted(t, store, f.session, target.ID)
				} else if restoreErr != nil {
					t.Fatalf("reference winner did not restore: %v", restoreErr)
				}
			}
		})
	}
}

func postgresConanCollectionObject(t *testing.T, store *PostgresStore, key string) ConanObjectIntent {
	t.Helper()
	var object ConanObjectIntent
	var claimed, collecting, collected sql.NullTime
	err := store.db.QueryRow(`SELECT repository_id::text,object_key,digest,size,created_at,claimed_at,collecting_at,collected_at FROM native_conan_object_intents WHERE object_key=$1`, key).Scan(&object.RepositoryID, &object.ObjectKey, &object.Digest, &object.Size, &object.CreatedAt, &claimed, &collecting, &collected)
	if err != nil {
		t.Fatal(err)
	}
	object.ClaimedAt, object.CollectingAt, object.CollectedAt = claimed.Time, collecting.Time, collected.Time
	return object
}

func installConanCollectionCompletionFailure(t *testing.T, store *PostgresStore, key string) func() {
	t.Helper()
	name := "conan_collection_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	removed := false
	remove := func() {
		t.Helper()
		if removed {
			return
		}
		if _, err := store.db.Exec(fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON native_conan_object_intents`, name)); err != nil {
			t.Errorf("drop owned collection trigger: %v", err)
		}
		if _, err := store.db.Exec(fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, name)); err != nil {
			t.Errorf("drop owned collection function: %v", err)
		}
		removed = true
	}
	t.Cleanup(remove)
	_, err := store.db.Exec(fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.object_key=TG_ARGV[0] AND NEW.collected_at IS NOT NULL THEN
        RAISE EXCEPTION 'fixture Conan collection finalization unavailable';
    END IF;
    RETURN NEW;
END;
$$`, name))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(fmt.Sprintf(`CREATE TRIGGER %s BEFORE UPDATE ON native_conan_object_intents FOR EACH ROW EXECUTE FUNCTION %s('%s')`, name, name, key)); err != nil {
		t.Fatal(err)
	}
	return remove
}
