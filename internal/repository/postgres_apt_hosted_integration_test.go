//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresAPTSignedSnapshotVisibilityAndAuditCommitAtomically(t *testing.T) {
	ctx, store, repo := newPostgresAPTHostedFixture(t)
	packageDigest := "sha256:" + strings.Repeat("a", 64)
	session := APTPublicationSession{
		ID: uuid.NewString(), RepositoryID: repo.ID, Suite: "stable", Component: "main", Publisher: "ci",
		ObjectName: "widget_1.0-1_amd64.deb", DeclaredDigest: packageDigest, DeclaredSize: 8,
		State: APTPublicationSessionOpen, ExpiresAt: time.Now().Add(time.Hour),
	}
	if _, _, err := store.CreateAPTPublicationSessionIdempotently(ctx, session, "ci", "apt/"+repo.ID, "snapshot-atomic-"+repo.ID, "snapshot-atomic"); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginAPTPackageUpload(ctx, session.ID, "native/apt/sha256/"+strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	revision, err := store.CompleteAPTPackageUpload(ctx, session.ID, APTPackageRevision{
		ID: uuid.NewString(), RepositoryID: repo.ID, Package: "widget", Version: "1.0-1", Architecture: "amd64",
		CanonicalIdentity: "widget@1.0-1#amd64", Digest: packageDigest,
		ObjectKey: "native/apt/sha256/" + strings.Repeat("a", 64), Size: 8, ObjectName: session.ObjectName, Publisher: "ci",
	})
	if err != nil {
		t.Fatal(err)
	}
	create := func(sequence int64) APTRepositorySnapshot {
		t.Helper()
		snapshot, createErr := store.CreateAPTRepositorySnapshot(ctx, APTRepositorySnapshot{
			ID: uuid.NewString(), RepositoryID: repo.ID, Suite: "stable", Sequence: sequence, State: APTRepositorySnapshotBuilding,
		}, []APTSnapshotPackage{{PublicationSessionID: session.ID, PackageRevisionID: revision.ID, Component: "main", Architecture: "amd64"}})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return snapshot
	}
	assets := func(snapshot APTRepositorySnapshot, marker string) ([]APTSnapshotAsset, []byte) {
		t.Helper()
		indexPath := "dists/stable/main/binary-amd64/Packages"
		indexBody := []byte(marker + ":Packages")
		indexSum := sha256.Sum256(indexBody)
		indexDigest := "sha256:" + hex.EncodeToString(indexSum[:])
		releaseBody := []byte(fmt.Sprintf("Suite: stable\nAcquire-By-Hash: yes\nSHA256:\n %s %16d main/binary-amd64/Packages\n", strings.TrimPrefix(indexDigest, "sha256:"), len(indexBody)))
		bodies := map[string][]byte{
			"dists/stable/Release": releaseBody, "dists/stable/InRelease": []byte(marker + ":InRelease"),
			"dists/stable/Release.gpg": []byte(marker + ":Release.gpg"), indexPath: indexBody,
			"dists/stable/main/binary-amd64/by-hash/SHA256/" + strings.TrimPrefix(indexDigest, "sha256:"): indexBody,
		}
		result := make([]APTSnapshotAsset, 0, len(bodies)+1)
		for path, body := range bodies {
			sum := sha256.Sum256(body)
			digest := "sha256:" + hex.EncodeToString(sum[:])
			result = append(result, APTSnapshotAsset{
				SnapshotID: snapshot.ID, RepositoryID: repo.ID, Path: path,
				Digest: digest, ObjectKey: "native/apt/sha256/" + strings.TrimPrefix(digest, "sha256:"),
				Size: int64(len(body)), ContentType: "text/plain",
			})
		}
		result = append(result, APTSnapshotAsset{
			SnapshotID: snapshot.ID, RepositoryID: repo.ID, Path: "pool/main/w/widget/widget_1.0-1_amd64.deb",
			Digest: revision.Digest, ObjectKey: revision.ObjectKey, Size: revision.Size, ContentType: "application/vnd.debian.binary-package",
		})
		return result, releaseBody
	}
	complete := func(snapshot APTRepositorySnapshot, snapshotAssets []APTSnapshotAsset) APTRepositorySnapshot {
		snapshot.State = APTRepositorySnapshotVisible
		for _, asset := range snapshotAssets {
			if asset.Path == "dists/stable/Release" {
				snapshot.ReleaseDigest = asset.Digest
			}
			if asset.Path == "dists/stable/InRelease" {
				snapshot.InReleaseDigest = asset.Digest
			}
		}
		snapshot.SignerIdentity = "integration-signer"
		snapshot.KeyFingerprint = strings.Repeat("b", 40)
		snapshot.SignatureAlgorithm = "integration-fixture"
		return snapshot
	}

	first := create(1)
	firstAssets, firstRelease := assets(first, "first")
	first, err = store.PublishAPTRepositorySnapshotWithAudit(ctx, complete(first, firstAssets), firstAssets, firstRelease, AuditRecord{
		Repository: repo.Name, Actor: "release-operator", Outcome: AuditResolved, Operation: "apt.repository_snapshot.publish", Status: 200,
	})
	if err != nil || first.State != APTRepositorySnapshotVisible || first.PublishedAt.IsZero() {
		t.Fatalf("first snapshot=%#v err=%v", first, err)
	}
	latest, err := store.GetLatestVisibleAPTRepositorySnapshot(ctx, repo.ID)
	if err != nil || latest.ID != first.ID {
		t.Fatalf("latest visible snapshot=%#v err=%v", latest, err)
	}

	second := create(2)
	secondAssets, secondRelease := assets(second, "second")
	_, err = store.PublishAPTRepositorySnapshotWithAudit(ctx, complete(second, secondAssets), secondAssets, secondRelease, AuditRecord{
		Repository: repo.Name, Actor: string([]byte{0xff}), Outcome: AuditResolved, Operation: "apt.repository_snapshot.publish", Status: 200,
	})
	if err == nil {
		t.Fatal("snapshot visibility committed without durable audit")
	}
	visible, err := store.GetVisibleAPTRepositorySnapshot(ctx, repo.ID, "stable")
	if err != nil || visible.ID != first.ID {
		t.Fatalf("visible snapshot changed after rollback: %#v err=%v", visible, err)
	}
	asset, err := store.GetVisibleAPTSnapshotAsset(ctx, repo.ID, "dists/stable/InRelease")
	var firstInReleaseDigest string
	for _, firstAsset := range firstAssets {
		if firstAsset.Path == "dists/stable/InRelease" {
			firstInReleaseDigest = firstAsset.Digest
		}
	}
	if err != nil || asset.SnapshotID != first.ID || asset.Digest != firstInReleaseDigest {
		t.Fatalf("visible asset changed after rollback: %#v err=%v", asset, err)
	}
	listed, err := store.ListVisibleAPTSnapshotAssets(ctx, repo.ID, "stable")
	if err != nil || len(listed) != 6 {
		t.Fatalf("visible assets=%#v err=%v", listed, err)
	}
}

func TestPostgresLatestVisibleAPTSnapshotIsRepositoryWide(t *testing.T) {
	ctx, store, repo := newPostgresAPTHostedFixture(t)
	otherRepo, err := store.CreateHostedRepository(ctx, HostedRepository{
		ID: uuid.NewString(), Name: "apt-hosted-other-" + uuid.NewString(), Format: FormatAPT, Type: RepositoryTypeHosted,
	})
	if err != nil {
		t.Fatal(err)
	}
	baseTime := time.Now().Add(-time.Hour)
	insertSnapshot := func(repositoryID, suite string, state APTRepositorySnapshotState, publishedAt time.Time) string {
		t.Helper()
		id := uuid.NewString()
		_, insertErr := store.db.ExecContext(ctx, `INSERT INTO native_apt_repository_snapshots
			(id,repository_id,suite,sequence,state,release_digest,inrelease_digest,signer_identity,key_fingerprint,signature_algorithm,created_at,published_at)
			VALUES ($1,$2,$3,1,$4,$5,$6,'integration-signer',$7,'integration-fixture',$8,$8)`,
			id, repositoryID, suite, string(state), "sha256:"+strings.Repeat("c", 64), "sha256:"+strings.Repeat("d", 64), strings.Repeat("e", 40), publishedAt)
		if insertErr != nil {
			t.Fatal(insertErr)
		}
		return id
	}
	_ = insertSnapshot(repo.ID, "stable", APTRepositorySnapshotVisible, baseTime)
	latestAcrossSuitesID := insertSnapshot(repo.ID, "testing", APTRepositorySnapshotVisible, baseTime.Add(time.Minute))
	_ = insertSnapshot(repo.ID, "unstable", APTRepositorySnapshotRetired, baseTime.Add(2*time.Minute))
	_ = insertSnapshot(otherRepo.ID, "stable", APTRepositorySnapshotVisible, baseTime.Add(3*time.Minute))

	latest, err := store.GetLatestVisibleAPTRepositorySnapshot(ctx, repo.ID)
	if err != nil || latest.ID != latestAcrossSuitesID || latest.Suite != "testing" {
		t.Fatalf("repository-wide latest visible snapshot=%#v err=%v", latest, err)
	}
}

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
	uncollected, err := store.ListUncollectedAPTPublicationObjects(ctx, 10)
	if err != nil || len(uncollected) != 1 || uncollected[0].SessionID != first.ID || uncollected[0].RepositoryID != repo.ID {
		t.Fatalf("uncollected=%#v err=%v", uncollected, err)
	}
	unscheduled, err := store.ListUnscheduledAPTPublicationObjects(ctx, 10)
	if err != nil || len(unscheduled) != 1 || unscheduled[0].SessionID != first.ID {
		t.Fatalf("unscheduled=%#v err=%v", unscheduled, err)
	}
	if err = store.MarkAPTPublicationObjectScheduled(ctx, first.ID, objectKey); err != nil {
		t.Fatal(err)
	}
	unscheduled, err = store.ListUnscheduledAPTPublicationObjects(ctx, 10)
	if err != nil || len(unscheduled) != 0 {
		t.Fatalf("scheduled objects remain=%#v err=%v", unscheduled, err)
	}
	if err = store.MarkAPTPublicationObjectCollected(ctx, first.ID, objectKey); err != nil {
		t.Fatal(err)
	}
	uncollected, err = store.ListUncollectedAPTPublicationObjects(ctx, 10)
	if err != nil || len(uncollected) != 0 {
		t.Fatalf("collected objects remain=%#v err=%v", uncollected, err)
	}
	collected, err := store.GetAPTPublicationSession(ctx, first.ID)
	if err != nil || collected.CollectedAt.IsZero() {
		t.Fatalf("collected session=%#v err=%v", collected, err)
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
