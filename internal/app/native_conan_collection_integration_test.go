//go:build integration

package app

import (
	"context"
	"database/sql"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/database"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresRustFSConanCollectionFencesSharedRestoreAndRetries(t *testing.T) {
	databaseURL, endpoint := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_RUSTFS_ENDPOINT")
	accessKey, secretKey := os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY")
	if databaseURL == "" || endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("owned PostgreSQL and RustFS integration environment is required")
	}
	exerciseConanCollectionWorker(t, func(t *testing.T) (conanCollectionWorkerStore, ObjectStore, func(*testing.T, conanCollectionWorkerStore, string) conanCollectionWorkerStore) {
		store, err := repository.NewPostgresStore(databaseURL)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		db, err := database.OpenPostgres(databaseURL, database.DefaultPoolConfig())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		objects, err := objectstore.NewRustFSStore(endpoint, accessKey, secretKey, "conan-collection-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:20])
		if err != nil {
			t.Fatal(err)
		}
		if err := objects.EnsureBucket(context.Background()); err != nil {
			t.Fatal(err)
		}
		owned := &ownedPostgresConanCollectionWorker{PostgresStore: store, db: db, state: &ownedConanCollectionState{keys: make(map[string]bool), jobs: make(map[string]string)}}
		t.Cleanup(func() {
			owned.state.mu.Lock()
			defer owned.state.mu.Unlock()
			for key := range owned.state.keys {
				if err := objects.Delete(context.Background(), key); err != nil {
					t.Errorf("delete owned collection fixture object: %v", err)
				}
			}
		})
		reopen := func(t *testing.T, previous conanCollectionWorkerStore, _ string) conanCollectionWorkerStore {
			t.Helper()
			prior := previous.(*ownedPostgresConanCollectionWorker)
			if err := prior.PostgresStore.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := repository.NewPostgresStore(databaseURL)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reopened.Close() })
			return &ownedPostgresConanCollectionWorker{PostgresStore: reopened, db: db, state: prior.state}
		}
		return owned, objects, reopen
	})
}

// The worker uses real PostgreSQL jobs, leases, object intents and a real
// RustFS bucket. Only candidate selection is restricted to this test's UUIDs,
// so running it beside other integration tests cannot claim their jobs.
type ownedPostgresConanCollectionWorker struct {
	*repository.PostgresStore
	db    *sql.DB
	state *ownedConanCollectionState
}

type ownedConanCollectionState struct {
	mu   sync.Mutex
	keys map[string]bool
	jobs map[string]string
}

func (s *ownedPostgresConanCollectionWorker) StageConanObject(ctx context.Context, object repository.ConanObjectIntent) error {
	if err := s.PostgresStore.StageConanObject(ctx, object); err != nil {
		return err
	}
	s.state.mu.Lock()
	s.state.keys[object.ObjectKey] = true
	s.state.mu.Unlock()
	return nil
}

func (s *ownedPostgresConanCollectionWorker) EnqueueLifecycleJob(ctx context.Context, job repository.LifecycleJob) (repository.LifecycleJob, bool, error) {
	stored, created, err := s.PostgresStore.EnqueueLifecycleJob(ctx, job)
	if err == nil {
		s.state.mu.Lock()
		s.state.jobs[stored.ID] = stored.RepositoryID
		s.state.mu.Unlock()
	}
	return stored, created, err
}

func (s *ownedPostgresConanCollectionWorker) ListReclaimableConanObjects(ctx context.Context, before time.Time, limit int) ([]repository.ConanObjectIntent, error) {
	objects, err := s.PostgresStore.ListReclaimableConanObjects(ctx, before, 1000000)
	if err != nil {
		return nil, err
	}
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	var owned []repository.ConanObjectIntent
	for _, object := range objects {
		if s.state.keys[object.ObjectKey] {
			owned = append(owned, object)
			if limit > 0 && len(owned) >= limit {
				break
			}
		}
	}
	return owned, nil
}

func (s *ownedPostgresConanCollectionWorker) ClaimLifecycleJobsByKindAndFormat(ctx context.Context, kind repository.LifecycleJobKind, format repository.Format, limit int) ([]repository.LifecycleJob, error) {
	s.state.mu.Lock()
	ids := make([]string, 0, len(s.state.jobs))
	repositories := make(map[string]string, len(s.state.jobs))
	for id, repo := range s.state.jobs {
		ids = append(ids, id)
		repositories[id] = repo
	}
	s.state.mu.Unlock()
	sort.Strings(ids)
	var jobs []repository.LifecycleJob
	for _, id := range ids {
		if limit > 0 && len(jobs) >= limit {
			break
		}
		result, err := s.db.ExecContext(ctx, `UPDATE lifecycle_jobs jobs SET state='running',started_at=now(),completed_at=NULL,next_attempt_at=NULL,lease_expires_at=now()+interval '10 minutes',lease_token=gen_random_uuid()::text,attempts=attempts+1 WHERE id=$1 AND repository_id=$2 AND kind=$3 AND payload->>'format'=$4 AND state IN ('pending','retrying') AND COALESCE(next_attempt_at,created_at)<=now() AND attempts<max_attempts AND NOT EXISTS(SELECT 1 FROM lifecycle_jobs running WHERE running.repository_id=jobs.repository_id AND running.state='running')`, id, repositories[id], kind, format)
		if err != nil {
			return nil, err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if count == 1 {
			job, err := s.PostgresStore.GetLifecycleJob(ctx, repositories[id], id)
			if err != nil {
				return nil, err
			}
			jobs = append(jobs, job)
		}
	}
	return jobs, nil
}
