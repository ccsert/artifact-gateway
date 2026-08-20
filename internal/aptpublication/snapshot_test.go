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
	err     error
	invalid bool
}

func (s deterministicAPTSigner) SignRelease(_ context.Context, request SignReleaseRequest) (SignReleaseResult, error) {
	if s.err != nil {
		return SignReleaseResult{}, s.err
	}
	if s.invalid {
		return SignReleaseResult{}, nil
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

type recordedSigningMetric struct {
	outcome  SigningOutcome
	duration time.Duration
}

type recordingSigningMetrics struct{ events []recordedSigningMetric }

func (m *recordingSigningMetrics) RecordAPTSigning(outcome SigningOutcome, duration time.Duration) {
	m.events = append(m.events, recordedSigningMetric{outcome: outcome, duration: duration})
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
	audits, err := first.store.ListAudits(context.Background(), repository.AuditQuery{Repository: "apt-hosted", Limit: 10})
	if err != nil {
		t.Fatalf("snapshot audit=%#v err=%v", audits, err)
	}
	var publicationAudit *repository.AuditRecord
	for index := range audits {
		if audits[index].Operation == "apt.repository_snapshot.publish" {
			publicationAudit = &audits[index]
			break
		}
	}
	if publicationAudit == nil {
		t.Fatalf("snapshot publication audit missing: %#v", audits)
		return
	}
	if publicationAudit.AuthorizationReason != "signed_snapshot_visible" ||
		publicationAudit.Evidence["signerIdentity"] != "apt-release@example.test" ||
		publicationAudit.Evidence["keyFingerprint"] != strings.Repeat("a", 40) ||
		publicationAudit.Evidence["signatureAlgorithm"] != "fixture-sha256" {
		t.Fatalf("snapshot audit=%#v", publicationAudit)
	}
}

func TestPublishedSnapshotProjectsVisibleAssetsAndGeneratedCapacity(t *testing.T) {
	t.Parallel()
	fixture := publishFixtureSnapshot(t, time.Date(2026, time.August, 13, 9, 0, 0, 0, time.UTC))
	items, err := fixture.store.SearchArtifactProjection(context.Background(), fixture.repositoryID, repository.FormatAPT, repository.ArtifactSearchQuery{
		Mode: repository.ArtifactSearchByCoordinate, Value: "pool/",
	}, 10, repository.ArtifactSearchPosition{})
	if err != nil || len(items) != 1 || items[0].Coordinate != "pool/main/w/widget/widget_2.0-3_amd64.deb" || items[0].Digest == "" || items[0].Size == nil {
		t.Fatalf("visible APT search projection=%#v err=%v", items, err)
	}
	metadata, err := fixture.store.SearchArtifactProjection(context.Background(), fixture.repositoryID, repository.FormatAPT, repository.ArtifactSearchQuery{
		Mode: repository.ArtifactSearchByCoordinate, Value: "dists/stable/",
	}, 100, repository.ArtifactSearchPosition{})
	if err != nil || len(metadata) < 5 {
		t.Fatalf("visible APT metadata projection=%#v err=%v", metadata, err)
	}
	capacity, err := fixture.store.GetRepositoryCapacity(context.Background(), fixture.repositoryID)
	if err != nil || capacity.UsedBytes <= fixture.packageSize || capacity.ObjectCount <= 1 {
		t.Fatalf("published APT capacity=%#v packageSize=%d err=%v", capacity, fixture.packageSize, err)
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

func TestPublisherExactReplayRecoversAfterTransientSignerFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo := createAPTHostedRepository(t, ctx, store)
	objects := objectstore.NewMemoryStore()
	session := stageAPTPackage(t, ctx, store, objects, repo.ID, "session-retry", "widget", "1.0-1")
	input := PublishSnapshotInput{
		ID: "00000000-0000-4000-8000-000000000008", RepositoryID: repo.ID, Suite: "stable", Sequence: 1,
		SessionIDs: []string{session.ID}, Actor: "release-operator", CreatedAt: time.Date(2026, time.August, 13, 9, 15, 0, 0, time.UTC),
	}

	metrics := &recordingSigningMetrics{}
	if _, err := NewPublisher(store, objects, deterministicAPTSigner{err: errors.New("temporary signer outage")}).WithMetrics(metrics).Publish(ctx, input); !errors.Is(err, ErrSignerUnavailable) {
		t.Fatalf("first publish error=%v", err)
	}
	if _, err := NewPublisher(store, objects, deterministicAPTSigner{err: ErrUntrustedSigner}).WithMetrics(metrics).Publish(ctx, input); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("untrusted signer publish error=%v", err)
	}
	if _, err := NewPublisher(store, objects, deterministicAPTSigner{invalid: true}).WithMetrics(metrics).Publish(ctx, input); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("invalid signature publish error=%v", err)
	}
	published, err := NewPublisher(store, objects, deterministicAPTSigner{}).WithMetrics(metrics).Publish(ctx, input)
	if err != nil || published.ID != input.ID || published.State != repository.APTRepositorySnapshotVisible {
		t.Fatalf("exact replay snapshot=%#v err=%v", published, err)
	}
	want := []SigningOutcome{SigningOutcomeUnavailable, SigningOutcomeUntrustedSigner, SigningOutcomeInvalidSignature, SigningOutcomeSuccess}
	if len(metrics.events) != len(want) {
		t.Fatalf("signing metric events=%#v", metrics.events)
	}
	for index, outcome := range want {
		if metrics.events[index].outcome != outcome || metrics.events[index].duration < 0 {
			t.Fatalf("signing metric event %d=%#v want outcome=%q", index, metrics.events[index], outcome)
		}
	}
}

func TestPublisherChangedReplayConflictsBeforeLoadingChangedSessions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo := createAPTHostedRepository(t, ctx, store)
	objects := objectstore.NewMemoryStore()
	session := stageAPTPackage(t, ctx, store, objects, repo.ID, "session-idempotency", "widget", "1.0-1")
	input := PublishSnapshotInput{
		ID: "00000000-0000-4000-8000-000000000010", RepositoryID: repo.ID, Suite: "stable", Sequence: 1,
		SessionIDs: []string{session.ID}, Actor: "release-operator", CreatedAt: time.Date(2026, time.August, 13, 9, 25, 0, 0, time.UTC),
	}
	if _, err := NewPublisher(store, objects, deterministicAPTSigner{err: errors.New("temporary signer outage")}).Publish(ctx, input); !errors.Is(err, ErrSignerUnavailable) {
		t.Fatalf("first publish error=%v", err)
	}
	changed := input
	changed.Suite = "testing"
	changed.SessionIDs = []string{"missing-session"}
	if _, err := NewPublisher(store, objects, deterministicAPTSigner{}).Publish(ctx, changed); !errors.Is(err, repository.ErrIdempotencyConflict) {
		t.Fatalf("changed replay error=%v", err)
	}
}

func TestPublisherSignerFailureWithoutObjectIntentsCanExpire(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo := createAPTHostedRepository(t, ctx, store)
	objects := objectstore.NewMemoryStore()
	session := stageAPTPackage(t, ctx, store, objects, repo.ID, "session-expire", "widget", "1.0-1")
	createdAt := time.Date(2026, time.August, 13, 9, 30, 0, 0, time.UTC)
	input := PublishSnapshotInput{
		ID: "00000000-0000-4000-8000-000000000011", RepositoryID: repo.ID, Suite: "stable", Sequence: 1,
		SessionIDs: []string{session.ID}, Actor: "release-operator", CreatedAt: createdAt,
	}
	if _, err := NewPublisher(store, objects, deterministicAPTSigner{err: errors.New("temporary signer outage")}).Publish(ctx, input); !errors.Is(err, ErrSignerUnavailable) {
		t.Fatalf("publish error=%v", err)
	}
	if err := store.ExpireAPTRepositorySnapshots(ctx, createdAt.Add(time.Minute), 10); err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := store.GetAPTRepositorySnapshot(ctx, input.ID)
	if err != nil || snapshot.State != repository.APTRepositorySnapshotFailed {
		t.Fatalf("expired snapshot=%#v err=%v", snapshot, err)
	}
}

func TestPublisherExactReplayRecoversAfterGeneratedMetadataQuotaIsRaised(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo := createAPTHostedRepository(t, ctx, store)
	objects := objectstore.NewMemoryStore()
	session := stageAPTPackage(t, ctx, store, objects, repo.ID, "session-quota", "widget", "1.0-1")
	capacity, err := store.GetRepositoryCapacity(ctx, repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryCapacityQuota(ctx, repo.ID, capacity.UsedBytes); err != nil {
		t.Fatal(err)
	}
	input := PublishSnapshotInput{
		ID: "00000000-0000-4000-8000-000000000009", RepositoryID: repo.ID, Suite: "stable", Sequence: 1,
		SessionIDs: []string{session.ID}, Actor: "release-operator", CreatedAt: time.Date(2026, time.August, 13, 9, 20, 0, 0, time.UTC),
	}
	if _, err = NewPublisher(store, objects, deterministicAPTSigner{}).Publish(ctx, input); !errors.Is(err, repository.ErrQuotaExceeded) {
		t.Fatalf("quota publish error=%v", err)
	}
	if _, err = store.ReplaceRepositoryCapacityQuota(ctx, repo.ID, 0); err != nil {
		t.Fatal(err)
	}
	published, err := NewPublisher(store, objects, deterministicAPTSigner{}).Publish(ctx, input)
	if err != nil || published.ID != input.ID || published.State != repository.APTRepositorySnapshotVisible {
		t.Fatalf("quota replay snapshot=%#v err=%v", published, err)
	}
}

func TestPublisherQuotaIncludesVisibleMetadataFromEverySuite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo := createAPTHostedRepository(t, ctx, store)
	objects := objectstore.NewMemoryStore()
	stable := stageAPTPackageInScope(t, ctx, store, objects, repo.ID, "session-stable-quota", "stable", "widget", "1.0-1", "widget_1.0-1_amd64.deb")
	testing := stageAPTPackageInScope(t, ctx, store, objects, repo.ID, "session-testing-quota", "testing", "gadget", "1.0-1", "gadget_1.0-1_amd64.deb")
	base, err := store.GetRepositoryCapacity(ctx, repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	publisher := NewPublisher(store, objects, deterministicAPTSigner{})
	if _, err = publisher.Publish(ctx, PublishSnapshotInput{
		ID: "00000000-0000-4000-8000-000000000012", RepositoryID: repo.ID, Suite: "stable", Sequence: 1,
		SessionIDs: []string{stable.ID}, Actor: "release-operator", CreatedAt: time.Date(2026, time.August, 13, 9, 35, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	afterStable, err := store.GetRepositoryCapacity(ctx, repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	testingInput := PublishSnapshotInput{
		ID: "00000000-0000-4000-8000-000000000013", RepositoryID: repo.ID, Suite: "testing", Sequence: 1,
		SessionIDs: []string{testing.ID}, Actor: "release-operator", CreatedAt: time.Date(2026, time.August, 13, 9, 36, 0, 0, time.UTC),
	}
	packages, _, err := publisher.loadPackages(ctx, testingInput)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := buildSnapshotBundle(repository.APTRepositorySnapshot{ID: testingInput.ID, RepositoryID: repo.ID, Suite: testingInput.Suite, Sequence: 1}, packages)
	if err != nil {
		t.Fatal(err)
	}
	objectsByKey := make(map[string]int64)
	for _, asset := range bundle.assets {
		if strings.HasPrefix(asset.Path, "dists/") {
			objectsByKey[asset.ObjectKey] = asset.Size
		}
	}
	var testingGenerated int64
	for _, size := range objectsByKey {
		testingGenerated += size
	}
	stableGenerated := afterStable.UsedBytes - base.UsedBytes
	quota := base.UsedBytes + max(stableGenerated, testingGenerated)
	if _, err = store.ReplaceRepositoryCapacityQuota(ctx, repo.ID, quota); err != nil {
		t.Fatal(err)
	}
	if _, err = publisher.Publish(ctx, testingInput); !errors.Is(err, repository.ErrQuotaExceeded) {
		t.Fatalf("cross-suite quota publish error=%v quota=%d stableGenerated=%d testingGenerated=%d", err, quota, stableGenerated, testingGenerated)
	}
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
	maintenance := Maintenance{Store: store, Objects: objects, Now: func() time.Time { return time.Now().UTC().Add(2 * time.Hour) }}
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
	paths        []string
	objects      map[string][]byte
	store        *repository.MemoryStore
	repositoryID string
	packageSize  int64
}

func publishFixtureSnapshot(t *testing.T, createdAt time.Time) publishedSnapshotFixture {
	t.Helper()
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo := createAPTHostedRepository(t, ctx, store)
	objects := objectstore.NewMemoryStore()
	session := stageAPTPackage(t, ctx, store, objects, repo.ID, "session-one", "widget", "1:2.0-3")
	revision, err := store.GetAPTPackageRevisionForSession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewPublisher(store, objects, deterministicAPTSigner{}).Publish(ctx, PublishSnapshotInput{
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
	result := publishedSnapshotFixture{objects: make(map[string][]byte, len(assets)), store: store, repositoryID: repo.ID, packageSize: revision.Size}
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
