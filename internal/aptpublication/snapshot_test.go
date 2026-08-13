package aptpublication

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type deterministicAPTSigner struct {
	err error
}

func (s deterministicAPTSigner) SignRelease(_ context.Context, request SignReleaseRequest) (SignReleaseResult, error) {
	if s.err != nil {
		return SignReleaseResult{}, s.err
	}
	release, err := io.ReadAll(request.Release)
	if err != nil {
		return SignReleaseResult{}, err
	}
	return SignReleaseResult{
		InRelease:      append([]byte("signed-cleartext\n"), release...),
		Detached:       []byte("detached:" + request.ReleaseDigest),
		SignerIdentity: "apt-release@example.test",
		KeyFingerprint: strings.Repeat("a", 40),
		Algorithm:      "fixture-sha256",
	}, nil
}

type failingSnapshotObjectStore struct {
	objectstore.Store
	remainingWrites int
}

func (s *failingSnapshotObjectStore) PutVerifiedReader(ctx context.Context, key string, body io.Reader, size int64, digest string) error {
	if s.remainingWrites == 0 {
		return errors.New("injected snapshot object failure")
	}
	s.remainingWrites--
	return s.Store.PutVerifiedReader(ctx, key, body, size, digest)
}

func TestPublisherBuildsDeterministicSignedSnapshot(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, time.August, 13, 8, 30, 0, 0, time.UTC)

	first := publishFixtureSnapshot(t, createdAt)
	second := publishFixtureSnapshot(t, createdAt)
	if !slices.Equal(first.paths, second.paths) {
		t.Fatalf("paths differ:\nfirst=%v\nsecond=%v", first.paths, second.paths)
	}
	for _, path := range first.paths {
		if !bytes.Equal(first.objects[path], second.objects[path]) {
			t.Fatalf("snapshot asset %q is not deterministic", path)
		}
	}

	packagesPath := "dists/stable/main/binary-amd64/Packages"
	packages := string(first.objects[packagesPath])
	for _, literal := range []string{
		"Package: widget\n", "Version: 1:2.0-3\n", "Architecture: amd64\n",
		"Depends: libc6 (>= 2.36)\n", "Filename: pool/main/w/widget/widget_2.0-3_amd64.deb\n",
		"Size: ", "SHA256: ",
	} {
		if !strings.Contains(packages, literal) {
			t.Fatalf("Packages missing %q:\n%s", literal, packages)
		}
	}
	packagesDigest := sha256.Sum256(first.objects[packagesPath])
	byHashPath := "dists/stable/main/binary-amd64/by-hash/SHA256/" + hex.EncodeToString(packagesDigest[:])
	if !bytes.Equal(first.objects[packagesPath], first.objects[byHashPath]) {
		t.Fatal("Acquire-By-Hash object does not match Packages")
	}
	release := string(first.objects["dists/stable/Release"])
	if !strings.Contains(release, "Acquire-By-Hash: yes\n") || !strings.Contains(release, "main/binary-amd64/Packages\n") {
		t.Fatalf("Release is incomplete:\n%s", release)
	}
	if !bytes.HasPrefix(first.objects["dists/stable/InRelease"], []byte("signed-cleartext\n")) {
		t.Fatal("InRelease was not produced by the signer")
	}
}

func TestPublisherRejectsNonUUIDSnapshotIDAtDomainBoundary(t *testing.T) {
	t.Parallel()
	store := repository.NewMemoryStore()
	_, err := NewPublisher(store, objectstore.NewMemoryStore(), deterministicAPTSigner{}).Publish(context.Background(), PublishSnapshotInput{
		ID: "snapshot-one", RepositoryID: "apt-hosted", Suite: "stable", Sequence: 1,
		SessionIDs: []string{"session-one"}, Actor: "release-operator", CreatedAt: time.Now().UTC(),
	})
	if !errors.Is(err, ErrInvalidSnapshotInput) {
		t.Fatalf("non-UUID snapshot id error=%v", err)
	}
}

func TestPublisherFailuresLeavePreviousSnapshotCompletelyVisible(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo := createAPTHostedRepository(t, ctx, store)
	objects := objectstore.NewMemoryStore()
	sessionOne := stageAPTPackage(t, ctx, store, objects, repo.ID, "session-one", "widget", "1.0-1")
	createdAt := time.Date(2026, time.August, 13, 8, 30, 0, 0, time.UTC)

	visible, err := NewPublisher(store, objects, deterministicAPTSigner{}).Publish(ctx, PublishSnapshotInput{
		ID: "00000000-0000-4000-8000-000000000001", RepositoryID: repo.ID, Suite: "stable", Sequence: 1,
		SessionIDs: []string{sessionOne.ID}, Actor: "release-operator", CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	releaseBefore, err := store.GetVisibleAPTSnapshotAsset(ctx, repo.ID, "dists/stable/Release")
	if err != nil {
		t.Fatal(err)
	}

	sessionTwo := stageAPTPackage(t, ctx, store, objects, repo.ID, "session-two", "widget", "2.0-1")
	_, err = NewPublisher(store, objects, deterministicAPTSigner{err: errors.New("signer unavailable")}).Publish(ctx, PublishSnapshotInput{
		ID: "00000000-0000-4000-8000-000000000002", RepositoryID: repo.ID, Suite: "stable", Sequence: 2,
		SessionIDs: []string{sessionTwo.ID}, Actor: "release-operator", CreatedAt: createdAt.Add(time.Minute),
	})
	if err == nil {
		t.Fatal("snapshot publication succeeded while signer was unavailable")
	}
	assertAPTSnapshotUnchanged(t, ctx, store, visible.ID, releaseBefore)

	failingObjects := &failingSnapshotObjectStore{Store: objects, remainingWrites: 1}
	_, err = NewPublisher(store, failingObjects, deterministicAPTSigner{}).Publish(ctx, PublishSnapshotInput{
		ID: "00000000-0000-4000-8000-000000000003", RepositoryID: repo.ID, Suite: "stable", Sequence: 3,
		SessionIDs: []string{sessionTwo.ID}, Actor: "release-operator", CreatedAt: createdAt.Add(2 * time.Minute),
	})
	if err == nil {
		t.Fatal("snapshot publication succeeded after generated object write failed")
	}
	assertAPTSnapshotUnchanged(t, ctx, store, visible.ID, releaseBefore)
}

func TestPublisherRejectsRepositoryGlobalPoolPathRebindingAcrossSuites(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo := createAPTHostedRepository(t, ctx, store)
	objects := objectstore.NewMemoryStore()
	createdAt := time.Date(2026, time.August, 13, 8, 30, 0, 0, time.UTC)
	first := stageAPTPackageInScope(t, ctx, store, objects, repo.ID, "session-stable", "stable", "widget", "1.0-1", "widget_current_amd64.deb")
	if _, err := NewPublisher(store, objects, deterministicAPTSigner{}).Publish(ctx, PublishSnapshotInput{
		ID: "00000000-0000-4000-8000-000000000004", RepositoryID: repo.ID, Suite: "stable", Sequence: 1,
		SessionIDs: []string{first.ID}, Actor: "release-operator", CreatedAt: createdAt,
	}); err != nil {
		t.Fatal(err)
	}
	second := stageAPTPackageInScope(t, ctx, store, objects, repo.ID, "session-testing", "testing", "widget", "2.0-1", "widget_current_amd64.deb")
	if _, err := NewPublisher(store, objects, deterministicAPTSigner{}).Publish(ctx, PublishSnapshotInput{
		ID: "00000000-0000-4000-8000-000000000005", RepositoryID: repo.ID, Suite: "testing", Sequence: 1,
		SessionIDs: []string{second.ID}, Actor: "release-operator", CreatedAt: createdAt.Add(time.Minute),
	}); !errors.Is(err, repository.ErrAPTPackageConflict) {
		t.Fatalf("pool path rebinding error=%v", err)
	}
	visible, err := store.GetVisibleAPTSnapshotAsset(ctx, repo.ID, "pool/main/w/widget/widget_current_amd64.deb")
	firstRevision, revisionErr := store.GetAPTPackageRevisionForSession(ctx, first.ID)
	if err != nil || revisionErr != nil || visible.Digest != firstRevision.Digest {
		t.Fatalf("visible pool asset=%#v revision=%#v err=%v revisionErr=%v", visible, firstRevision, err, revisionErr)
	}
}

func TestPublisherFailureIntentsReclaimOnlyUnreferencedGeneratedObjects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo := createAPTHostedRepository(t, ctx, store)
	objects := objectstore.NewMemoryStore()
	session := stageAPTPackage(t, ctx, store, objects, repo.ID, "session-failed", "widget", "1.0-1")
	revision, err := store.GetAPTPackageRevisionForSession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	failingObjects := &failingSnapshotObjectStore{Store: objects, remainingWrites: 1}
	if _, err = NewPublisher(store, failingObjects, deterministicAPTSigner{}).Publish(ctx, PublishSnapshotInput{
		ID: "00000000-0000-4000-8000-000000000006", RepositoryID: repo.ID, Suite: "stable", Sequence: 1,
		SessionIDs: []string{session.ID}, Actor: "release-operator", CreatedAt: time.Now().UTC(),
	}); err == nil {
		t.Fatal("snapshot publication unexpectedly succeeded")
	}
	keys, err := objects.List(ctx, "native/apt/sha256/")
	if err != nil || len(keys) != 2 {
		t.Fatalf("expected package plus one partial generated object, keys=%v err=%v", keys, err)
	}
	maintenance := Maintenance{Store: store, Objects: objects}
	if err = maintenance.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	if err = maintenance.RunReclaimJobs(ctx, 100); err != nil {
		t.Fatal(err)
	}
	keys, err = objects.List(ctx, "native/apt/sha256/")
	if err != nil || !slices.Equal(keys, []string{revision.ObjectKey}) {
		t.Fatalf("reclaimed keys=%v want only %q err=%v", keys, revision.ObjectKey, err)
	}
}

type publishedSnapshotFixture struct {
	paths   []string
	objects map[string][]byte
}

func publishFixtureSnapshot(t *testing.T, createdAt time.Time) publishedSnapshotFixture {
	t.Helper()
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo := createAPTHostedRepository(t, ctx, store)
	objects := objectstore.NewMemoryStore()
	session := stageAPTPackage(t, ctx, store, objects, repo.ID, "session-one", "widget", "1:2.0-3")
	_, err := NewPublisher(store, objects, deterministicAPTSigner{}).Publish(ctx, PublishSnapshotInput{
		ID: "00000000-0000-4000-8000-000000000007", RepositoryID: repo.ID, Suite: "stable", Sequence: 1,
		SessionIDs: []string{session.ID}, Actor: "release-operator", CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	assets, err := store.ListVisibleAPTSnapshotAssets(ctx, repo.ID, "stable")
	if err != nil {
		t.Fatal(err)
	}
	result := publishedSnapshotFixture{objects: make(map[string][]byte, len(assets))}
	for _, asset := range assets {
		body, getErr := objects.Get(ctx, asset.ObjectKey)
		if getErr != nil {
			t.Fatalf("get %q: %v", asset.Path, getErr)
		}
		result.paths = append(result.paths, asset.Path)
		result.objects[asset.Path] = body
	}
	slices.Sort(result.paths)
	return result
}

func createAPTHostedRepository(t *testing.T, ctx context.Context, store *repository.MemoryStore) repository.HostedRepository {
	t.Helper()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: "apt-hosted", Name: "apt-hosted", Format: repository.FormatAPT, Type: repository.RepositoryTypeHosted,
	})
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func stageAPTPackage(t *testing.T, ctx context.Context, store *repository.MemoryStore, objects objectstore.Store, repositoryID, sessionID, packageName, version string) repository.APTPublicationSession {
	return stageAPTPackageInScope(t, ctx, store, objects, repositoryID, sessionID, "stable", packageName, version, packageName+"_"+strings.TrimPrefix(version, "1:")+"_amd64.deb")
}

func stageAPTPackageInScope(t *testing.T, ctx context.Context, store *repository.MemoryStore, objects objectstore.Store, repositoryID, sessionID, suite, packageName, version, objectName string) repository.APTPublicationSession {
	t.Helper()
	manager := NewManager(store, objects)
	deb := testDebianPackage(t, "Package: "+packageName+"\nVersion: "+version+"\nArchitecture: amd64\nMaintainer: Gateway Team <gateway@example.test>\nDepends: libc6 (>= 2.36)\nDescription: snapshot fixture\n continuation line\n")
	digestBytes := sha256.Sum256(deb)
	session, _, err := manager.CreateSession(ctx, CreateSessionInput{
		ID: sessionID, RepositoryID: repositoryID, Suite: suite, Component: "main", Publisher: "ci",
		ObjectName: objectName, DeclaredDigest: "sha256:" + hex.EncodeToString(digestBytes[:]), DeclaredSize: int64(len(deb)),
		IdempotencyKey: sessionID, ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = manager.UploadPackage(ctx, session.ID, session.ObjectName, bytes.NewReader(deb), int64(len(deb))); err != nil {
		t.Fatal(err)
	}
	return session
}

func assertAPTSnapshotUnchanged(t *testing.T, ctx context.Context, store *repository.MemoryStore, snapshotID string, release repository.APTSnapshotAsset) {
	t.Helper()
	visible, err := store.GetVisibleAPTRepositorySnapshot(ctx, release.RepositoryID, "stable")
	if err != nil || visible.ID != snapshotID {
		t.Fatalf("visible snapshot=%#v err=%v", visible, err)
	}
	after, err := store.GetVisibleAPTSnapshotAsset(ctx, release.RepositoryID, release.Path)
	if err != nil || after.SnapshotID != release.SnapshotID || after.Digest != release.Digest || after.ObjectKey != release.ObjectKey {
		t.Fatalf("visible Release changed: before=%#v after=%#v err=%v", release, after, err)
	}
}
