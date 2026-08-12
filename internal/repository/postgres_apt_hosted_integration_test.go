//go:build integration

package repository

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newPostgresAPTHostedFixture(t *testing.T) (context.Context, *PostgresStore, HostedRepository) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	ctx := context.Background()
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := store.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "apt-hosted-" + uuid.NewString(), Format: FormatAPT, Type: RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return ctx, store, repo
}

func TestPostgresAPTHostedStagesRevisionAndBuildingSnapshotWithoutVisibility(t *testing.T) {
	ctx, store, repo := newPostgresAPTHostedFixture(t)
	digest := "sha256:" + strings.Repeat("a", 64)
	session := APTPublicationSession{
		ID: uuid.NewString(), RepositoryID: repo.ID, Suite: "stable", Component: "main", Publisher: "ci",
		ObjectName: "widget_1.0-1_amd64.deb", DeclaredDigest: digest, DeclaredSize: 8,
		ExpectedIdentity: "widget@1.0-1#amd64", State: APTPublicationSessionOpen, ExpiresAt: time.Now().Add(time.Hour),
	}
	created, replayed, err := store.CreateAPTPublicationSessionIdempotently(ctx, session, "ci", "apt", "build-1", "payload-1")
	if err != nil || replayed || created.ID != session.ID {
		t.Fatalf("created=%#v replayed=%t err=%v", created, replayed, err)
	}
	replayedSession, replayed, err := store.CreateAPTPublicationSessionIdempotently(ctx, APTPublicationSession{ID: uuid.NewString()}, "ci", "apt", "build-1", "payload-1")
	if err != nil || !replayed || replayedSession.ID != session.ID {
		t.Fatalf("replay=%#v replayed=%t err=%v", replayedSession, replayed, err)
	}
	if _, _, err = store.CreateAPTPublicationSessionIdempotently(ctx, session, "ci", "apt", "build-1", "different"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting replay error=%v", err)
	}

	objectKey := "native/apt/sha256/" + strings.Repeat("a", 64)
	if err = store.BeginAPTPackageUpload(ctx, session.ID, objectKey); err != nil {
		t.Fatal(err)
	}
	revision, err := store.CompleteAPTPackageUpload(ctx, session.ID, APTPackageRevision{
		ID: uuid.NewString(), RepositoryID: repo.ID, Package: "widget", Version: "1.0-1", Architecture: "amd64",
		CanonicalIdentity: session.ExpectedIdentity, Digest: digest, ObjectKey: objectKey,
		Size: 8, ObjectName: session.ObjectName, Publisher: "ci",
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetAPTPublicationSession(ctx, session.ID)
	if err != nil || stored.State != APTPublicationSessionStaged || stored.PackageRevisionID != revision.ID {
		t.Fatalf("stored=%#v err=%v", stored, err)
	}
	capacity, err := store.GetRepositoryCapacity(ctx, repo.ID)
	if err != nil || capacity.UsedBytes != 8 || capacity.ObjectCount != 1 {
		t.Fatalf("capacity=%#v err=%v", capacity, err)
	}
	if _, err = store.GetAPTAsset(ctx, repo.ID, "pool/main/w/widget/widget_1.0-1_amd64.deb"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("staged package leaked to protocol assets: %v", err)
	}

	snapshot, err := store.CreateAPTRepositorySnapshot(ctx, APTRepositorySnapshot{
		ID: uuid.NewString(), RepositoryID: repo.ID, Suite: "stable", Sequence: 1, State: APTRepositorySnapshotBuilding,
	}, []APTSnapshotPackage{{PublicationSessionID: session.ID, PackageRevisionID: revision.ID, Component: "main", Architecture: "amd64"}})
	if err != nil || snapshot.State != APTRepositorySnapshotBuilding {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
	if _, err = store.GetVisibleAPTRepositorySnapshot(ctx, repo.ID, "stable"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("building snapshot became visible: %v", err)
	}
}

func TestPostgresAPTHostedReservationEnforcesQuotaAndExpiredUploadIsRecoverable(t *testing.T) {
	ctx, store, repo := newPostgresAPTHostedFixture(t)
	if _, err := store.ReplaceRepositoryCapacityQuota(ctx, repo.ID, 12); err != nil {
		t.Fatal(err)
	}
	newSession := func(id, digest string) APTPublicationSession {
		return APTPublicationSession{
			ID: id, RepositoryID: repo.ID, Suite: "stable", Component: "main", Publisher: "ci",
			ObjectName: id + ".deb", DeclaredDigest: digest, DeclaredSize: 8,
			State: APTPublicationSessionOpen, ExpiresAt: time.Now().Add(time.Minute),
		}
	}
	first := newSession(uuid.NewString(), "sha256:"+strings.Repeat("b", 64))
	if _, _, err := store.CreateAPTPublicationSessionIdempotently(ctx, first, "ci", "apt", "one", "one"); err != nil {
		t.Fatal(err)
	}
	second := newSession(uuid.NewString(), "sha256:"+strings.Repeat("c", 64))
	if _, _, err := store.CreateAPTPublicationSessionIdempotently(ctx, second, "ci", "apt", "two", "two"); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("second reservation error=%v", err)
	}
	objectKey := "native/apt/sha256/" + strings.Repeat("b", 64)
	if err := store.BeginAPTPackageUpload(ctx, first.ID, objectKey); err != nil {
		t.Fatal(err)
	}
	abandoned, err := store.ExpireAPTPublicationSessions(ctx, time.Now().Add(2*time.Minute), 10)
	if err != nil || len(abandoned) != 1 || abandoned[0].ObjectKey != objectKey {
		t.Fatalf("abandoned=%#v err=%v", abandoned, err)
	}
}

func TestPostgresAPTPublicationSerializesWithFirstQuotaWrite(t *testing.T) {
	ctx, store, repo := newPostgresAPTHostedFixture(t)
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var lockedID string
	if err = tx.QueryRowContext(ctx, `SELECT id::text FROM hosted_repositories WHERE id=$1 FOR UPDATE`, repo.ID).Scan(&lockedID); err != nil {
		t.Fatal(err)
	}

	session := APTPublicationSession{
		ID: uuid.NewString(), RepositoryID: repo.ID, Suite: "stable", Component: "main", Publisher: "ci",
		ObjectName: "oversized.deb", DeclaredDigest: "sha256:" + strings.Repeat("e", 64), DeclaredSize: 8,
		State: APTPublicationSessionOpen, ExpiresAt: time.Now().Add(time.Hour),
	}
	result := make(chan error, 1)
	go func() {
		_, _, createErr := store.CreateAPTPublicationSessionIdempotently(ctx, session, "ci", "apt", "quota-race", "quota-race")
		result <- createErr
	}()
	select {
	case createErr := <-result:
		t.Fatalf("publication bypassed repository lock before quota commit: %v", createErr)
	case <-time.After(100 * time.Millisecond):
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO repository_capacity_quotas(repository_id,quota_bytes) VALUES ($1,4)`, repo.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case createErr := <-result:
		if !errors.Is(createErr, ErrQuotaExceeded) {
			t.Fatalf("publication error=%v, want quota exceeded", createErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("publication remained blocked after quota commit")
	}
}

func TestPostgresAPTConcurrentExactRetryReplaysBeforeQuotaEvaluation(t *testing.T) {
	ctx, store, repo := newPostgresAPTHostedFixture(t)
	if _, err := store.ReplaceRepositoryCapacityQuota(ctx, repo.ID, 8); err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var lockedID string
	if err = tx.QueryRowContext(ctx, `SELECT id::text FROM hosted_repositories WHERE id=$1 FOR UPDATE`, repo.ID).Scan(&lockedID); err != nil {
		t.Fatal(err)
	}
	session := APTPublicationSession{
		ID: uuid.NewString(), RepositoryID: repo.ID, Suite: "stable", Component: "main", Publisher: "ci",
		ObjectName: "concurrent.deb", DeclaredDigest: "sha256:" + strings.Repeat("f", 64), DeclaredSize: 8,
		State: APTPublicationSessionOpen, ExpiresAt: time.Now().Add(time.Hour),
	}
	type createResult struct {
		session  APTPublicationSession
		replayed bool
		err      error
	}
	results := make(chan createResult, 2)
	for range 2 {
		go func() {
			created, replayed, createErr := store.CreateAPTPublicationSessionIdempotently(ctx, session, "ci", "apt", "same-key", "same-payload")
			results <- createResult{session: created, replayed: replayed, err: createErr}
		}()
	}
	select {
	case result := <-results:
		t.Fatalf("request bypassed held repository lock: %+v", result)
	case <-time.After(100 * time.Millisecond):
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	replayCount := 0
	for range 2 {
		select {
		case result := <-results:
			if result.err != nil || result.session.ID != session.ID {
				t.Fatalf("result=%+v", result)
			}
			if result.replayed {
				replayCount++
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent publication did not finish")
		}
	}
	if replayCount != 1 {
		t.Fatalf("replayed requests=%d, want 1", replayCount)
	}
}
