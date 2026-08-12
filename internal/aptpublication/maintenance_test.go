package aptpublication

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type failingAPTDeleteStore struct {
	*objectstore.MemoryStore
	fail bool
}

func (s *failingAPTDeleteStore) Delete(ctx context.Context, key string) error {
	if s.fail {
		return errors.New("object store unavailable")
	}
	return s.MemoryStore.Delete(ctx, key)
}

func TestMaintenanceRetriesFailedAbandonedUploadCollection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "apt-repo", Name: "apt-repo", Format: repository.FormatAPT, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	objectKey := "native/apt/sha256/" + strings.Repeat("a", 64)
	expiresAt := time.Now().UTC().Add(time.Minute)
	session, _, err := store.CreateAPTPublicationSessionIdempotently(ctx, repository.APTPublicationSession{
		ID: "session-one", RepositoryID: repo.ID, Suite: "stable", Component: "main", Publisher: "ci",
		ObjectName: "widget_1.0_amd64.deb", DeclaredDigest: digest, DeclaredSize: 7,
		State: repository.APTPublicationSessionOpen, ExpiresAt: expiresAt,
	}, "ci", "repositories/apt-repo/apt-publication-sessions", "build-42", "payload")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.BeginAPTPackageUpload(ctx, session.ID, objectKey); err != nil {
		t.Fatal(err)
	}
	objects := &failingAPTDeleteStore{MemoryStore: objectstore.NewMemoryStore(), fail: true}
	if err = objects.Put(ctx, objectKey, []byte("partial")); err != nil {
		t.Fatal(err)
	}
	maintenance := Maintenance{Store: store, Objects: objects, Now: func() time.Time { return expiresAt.Add(time.Second) }}
	if err = maintenance.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	if scheduled, listErr := store.ListUnscheduledAPTPublicationObjects(ctx, 10); listErr != nil || len(scheduled) != 0 {
		t.Fatalf("scheduled candidates=%#v err=%v", scheduled, listErr)
	}
	if err = maintenance.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	queued, err := store.ListLifecycleJobs(ctx, repo.ID, 10)
	if err != nil || len(queued) != 1 || queued[0].State != repository.LifecycleJobPending {
		t.Fatalf("idempotent schedule jobs=%#v err=%v", queued, err)
	}
	if candidates, listErr := store.ListUncollectedAPTPublicationObjects(ctx, 10); listErr != nil || len(candidates) != 1 || candidates[0].SessionID != session.ID {
		t.Fatalf("uncollected=%#v err=%v", candidates, listErr)
	}
	if err = maintenance.RunReclaimJobs(ctx, 10); err == nil {
		t.Fatal("first reclaim must fail")
	}
	jobs, err := store.ListLifecycleJobs(ctx, repo.ID, 10)
	if err != nil || len(jobs) != 1 || jobs[0].State != repository.LifecycleJobRetrying {
		t.Fatalf("retrying jobs=%#v err=%v", jobs, err)
	}
	if candidates, listErr := store.ListUncollectedAPTPublicationObjects(ctx, 10); listErr != nil || len(candidates) != 1 {
		t.Fatalf("failed deletion lost durable candidate=%#v err=%v", candidates, listErr)
	}
	if _, err = store.RunLifecycleJobNow(ctx, repo.ID, jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	objects.fail = false
	if err = maintenance.RunReclaimJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if _, err = objects.Get(ctx, objectKey); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("object remains after retry: %v", err)
	}
	if candidates, listErr := store.ListUncollectedAPTPublicationObjects(ctx, 10); listErr != nil || len(candidates) != 0 {
		t.Fatalf("collected candidates=%#v err=%v", candidates, listErr)
	}
	stored, err := store.GetAPTPublicationSession(ctx, session.ID)
	if err != nil || stored.State != repository.APTPublicationSessionAborted || stored.CollectedAt.IsZero() {
		t.Fatalf("stored=%#v err=%v", stored, err)
	}
}
