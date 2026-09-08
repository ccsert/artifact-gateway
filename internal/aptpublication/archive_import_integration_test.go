//go:build integration

package aptpublication

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type restoreCommitHookStore struct {
	managerStore
	beforeCommit func()
}

func (s restoreCommitHookStore) CommitAPTArchiveRestore(ctx context.Context, p repository.APTArchiveRestorePlan, release []byte, a repository.AuditRecord) (repository.APTRepositorySnapshot, error) {
	s.beforeCommit()
	return s.managerStore.CommitAPTArchiveRestore(ctx, p, release, a)
}

func TestPostgresRustFSArchiveRestoreAcrossInstancesAndFailedAttemptReclamation(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" || os.Getenv("TEST_RUSTFS_ENDPOINT") == "" {
		t.Skip("PostgreSQL/RustFS required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	a, err := repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	b, err := repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()
	repo, err := a.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "apt-restore-" + uuid.NewString(), Format: repository.FormatAPT, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	objects, err := objectstore.NewRustFSStore(os.Getenv("TEST_RUSTFS_ENDPOINT"), os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY"), "apt-restore-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:20])
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	body, trust := trustedArchiveFixture(t)
	// The backup receipt is created after assigning the original repository UUID.
	body = rewriteArchiveObjects(t, body, func(m *SnapshotArchiveManifest, _ map[string][]byte) { m.Snapshot.RepositoryID = repo.ID })
	receipt := digestBytes(body)
	manifest, err := VerifySnapshotArchive(ctx, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	// Force a final-transaction quota failure after every object was written.
	hook := restoreCommitHookStore{managerStore: a, beforeCommit: func() {
		if _, e := b.ReplaceRepositoryCapacityQuota(ctx, repo.ID, 1); e != nil {
			t.Fatal(e)
		}
	}}
	if _, err = NewSnapshotArchiveImporter(hook, objects, trust).Import(ctx, repo.ID, receipt, bytes.NewReader(body), "restore-admin"); !errors.Is(err, repository.ErrQuotaExceeded) {
		t.Fatalf("commit quota race=%v", err)
	}
	if capacity, e := b.GetRepositoryCapacity(ctx, repo.ID); e != nil || capacity.UsedBytes != 0 {
		t.Fatalf("failed commit leaked metadata: %#v %v", capacity, e)
	}
	if _, _, e := b.GetAPTRepositorySnapshot(ctx, manifest.Snapshot.ID); !errors.Is(e, repository.ErrNotFound) {
		t.Fatalf("failed commit leaked snapshot: %v", e)
	}
	if _, err = b.ReplaceRepositoryCapacityQuota(ctx, repo.ID, 0); err != nil {
		t.Fatal(err)
	}
	recorder := &restoreRecordingObjects{Store: objects, failAt: 3}
	recorder.beforeWrite = func(key string) {
		durable, e := b.APTObjectHasDurableReference(ctx, key)
		if e != nil || !durable {
			t.Fatalf("missing durable intent: %v %v", durable, e)
		}
		if _, e = b.GetVisibleAPTRepositorySnapshot(ctx, repo.ID, "stable"); !errors.Is(e, repository.ErrNotFound) {
			t.Fatalf("partial visibility: %v", e)
		}
		capacity, e := b.GetRepositoryCapacity(ctx, repo.ID)
		if e != nil || capacity.UsedBytes != 0 {
			t.Fatalf("partial package metadata: %#v %v", capacity, e)
		}
	}
	if _, err = NewSnapshotArchiveImporter(a, recorder, trust).Import(ctx, repo.ID, receipt, bytes.NewReader(body), "restore-admin"); err == nil {
		t.Fatal("interrupted import succeeded")
	}
	abandoned, err := b.ListUnscheduledAPTArchiveObjects(ctx, 100)
	if err != nil || len(abandoned) == 0 {
		t.Fatalf("missing cleanup intents: %v %v", abandoned, err)
	}
	maintenance := Maintenance{Store: b, Objects: objects}
	if err = maintenance.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	// PostgreSQL leases at most one lifecycle job per repository per tick.
	for range len(abandoned) {
		if err = maintenance.RunReclaimJobs(ctx, 100); err != nil {
			t.Fatal(err)
		}
	}
	for _, intent := range abandoned {
		if intent.RepositoryID != repo.ID {
			continue
		}
		reader, _, e := objects.Open(ctx, intent.ObjectKey)
		if e == nil {
			_ = reader.Close()
			referenced, refErr := b.APTObjectHasDurableReference(ctx, intent.ObjectKey)
			t.Fatalf("failed import object survived collection key=%s durable=%v refErr=%v", intent.ObjectKey, referenced, refErr)
		}
	}
	results := make(chan error, 2)
	for _, store := range []*repository.PostgresStore{a, b} {
		go func() {
			_, e := NewSnapshotArchiveImporter(store, objects, trust).Import(ctx, repo.ID, receipt, bytes.NewReader(body), "restore-admin")
			results <- e
		}()
	}
	for range 2 {
		if e := <-results; e != nil {
			t.Fatal(e)
		}
	}
	snapshot, members, err := b.GetAPTRepositorySnapshot(ctx, manifest.Snapshot.ID)
	if err != nil || snapshot.State != repository.APTRepositorySnapshotVisible || len(members) != len(manifest.Packages) {
		t.Fatalf("restored snapshot=%#v members=%d err=%v", snapshot, len(members), err)
	}
	var exported bytes.Buffer
	if _, err = (SnapshotArchiveExporter{Store: b, Objects: objects}).Export(ctx, snapshot.ID, &exported); err != nil || !bytes.Equal(exported.Bytes(), body) {
		t.Fatalf("restored archive mismatch: %v", err)
	}
	// Replay repairs a missing object while preserving the immutable snapshot.
	assets, err := b.ListAPTSnapshotAssets(ctx, snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.Delete(ctx, assets[0].ObjectKey); err != nil {
		t.Fatal(err)
	}
	if _, err = NewSnapshotArchiveImporter(a, objects, trust).Import(ctx, repo.ID, receipt, bytes.NewReader(body), "restore-admin"); err != nil {
		t.Fatal(err)
	}
	exported.Reset()
	if _, err = (SnapshotArchiveExporter{Store: b, Objects: objects}).Export(ctx, snapshot.ID, &exported); err != nil || !bytes.Equal(exported.Bytes(), body) {
		t.Fatalf("repaired archive mismatch: %v", err)
	}
	audits, err := b.ListAudits(ctx, repository.AuditQuery{Repository: repo.Name, Operation: "apt.repository_snapshot.restore", Limit: 10})
	if err != nil || len(audits) != 3 || audits[0].Evidence["archiveDigest"] != receipt {
		t.Fatalf("restore audits=%#v err=%v", audits, err)
	}
}
