package aptpublication

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type lifecycleTestStore interface {
	publisherStore
	repository.LifecycleJobStore
	repository.RepositoryCapacityStore
}

func TestLifecycleMemory(t *testing.T) {
	exerciseAPTLifecycle(t, repository.NewMemoryStore(), objectstore.NewMemoryStore())
}

// The same acceptance runs against Memory and PostgreSQL/RustFS, including the
// real transaction boundary and durable GC queue after retention expires.
func exerciseAPTLifecycle(t *testing.T, store lifecycleTestStore, objects objectstore.Store) {
	t.Helper()
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "apt-lifecycle-" + uuid.NewString(), Format: repository.FormatAPT, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(store, objects)
	var ids []string
	var firstDeb []byte
	for _, version := range []string{"1.0-1", "2.0-1"} {
		deb := testDebianPackage(t, "Package: widget\nVersion: "+version+"\nArchitecture: amd64\nMaintainer: Team <team@example.test>\nDescription: lifecycle fixture\n")
		if firstDeb == nil {
			firstDeb = bytes.Clone(deb)
		}
		session, _, e := manager.CreateSession(ctx, CreateSessionInput{RepositoryID: repo.ID, Suite: "stable", Component: "main", Publisher: "ci", ObjectName: "widget_" + version + "_amd64.deb", DeclaredDigest: digestBytes(deb), DeclaredSize: int64(len(deb)), IdempotencyKey: uuid.NewString()})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = manager.UploadPackage(ctx, session.ID, session.ObjectName, bytes.NewReader(deb), int64(len(deb))); e != nil {
			t.Fatal(e)
		}
		ids = append(ids, session.ID)
	}
	p := NewPublisher(store, objects, deterministicAPTSigner{})
	first, err := p.Publish(ctx, PublishSnapshotInput{ID: uuid.NewString(), RepositoryID: repo.ID, Suite: "stable", Sequence: 1, SessionIDs: ids, Actor: "operator", CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	shared, _, err := manager.CreateSession(ctx, CreateSessionInput{RepositoryID: repo.ID, Suite: "testing", Component: "main", Publisher: "ci", ObjectName: "widget_1.0-1_amd64.deb", DeclaredDigest: digestBytes(firstDeb), DeclaredSize: int64(len(firstDeb)), IdempotencyKey: "shared-other-suite"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = manager.UploadPackage(ctx, shared.ID, shared.ObjectName, bytes.NewReader(firstDeb), int64(len(firstDeb))); err != nil {
		t.Fatal(err)
	}
	otherSuite, err := p.Publish(ctx, PublishSnapshotInput{ID: uuid.NewString(), RepositoryID: repo.ID, Suite: "testing", Sequence: 1, SessionIDs: []string{shared.ID}, Actor: "operator", CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	revision, err := store.GetAPTPackageRevisionForSession(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	oldIndex, err := store.GetVisibleAPTSnapshotAsset(ctx, repo.ID, "dists/stable/main/binary-amd64/Packages")
	if err != nil {
		t.Fatal(err)
	}
	req := LifecycleRequest{Suite: "stable", ExpectedSnapshotID: first.ID, Action: "delete", PublicationSessionIDs: []string{ids[0]}}
	failing := Lifecycle{Publisher: NewPublisher(store, objects, deterministicAPTSigner{err: errors.New("signer down")})}
	if _, err = failing.Apply(ctx, repo.ID, "operator", "delete-one", req); !errors.Is(err, ErrSignerUnavailable) {
		t.Fatalf("signer failure: %v", err)
	}
	current, err := store.GetVisibleAPTRepositorySnapshot(ctx, repo.ID, "stable")
	if err != nil || current.ID != first.ID {
		t.Fatalf("failure changed current: %+v %v", current, err)
	}
	l := Lifecycle{Publisher: p}
	second, err := l.Apply(ctx, repo.ID, "operator", "delete-one", req)
	if err != nil {
		t.Fatal(err)
	}
	if second.Sequence <= first.Sequence+1 {
		t.Fatal("retry reused a failed build sequence")
	}
	replay, err := l.Apply(ctx, repo.ID, "operator", "delete-one", req)
	if err != nil || replay.ID != second.ID {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	conflicting := req
	conflicting.PublicationSessionIDs = []string{ids[1]}
	if _, err = l.Apply(ctx, repo.ID, "operator", "delete-one", conflicting); !errors.Is(err, repository.ErrIdempotencyConflict) {
		t.Fatalf("conflicting replay: %v", err)
	}
	if _, err = l.Apply(ctx, repo.ID, "operator", "stale", req); !errors.Is(err, repository.ErrVersionConflict) {
		t.Fatalf("stale base: %v", err)
	}
	for _, path := range []string{repository.APTPoolPath("main", revision.Package, revision.ObjectName), "dists/stable/main/binary-amd64/by-hash/SHA256/" + oldIndex.Digest[7:]} {
		if _, err = store.GetVisibleAPTSnapshotAsset(ctx, repo.ID, path); err != nil {
			t.Fatalf("old client lost %s: %v", path, err)
		}
	}
	if err = store.PruneAPTSnapshots(ctx, repo.ID, []string{first.ID}, time.Now().UTC(), repository.AuditRecord{}); !errors.Is(err, repository.ErrVersionConflict) {
		t.Fatalf("grace bypass: %v", err)
	}
	history, err := store.ListAPTRepositorySnapshots(ctx, repo.ID, "stable")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Publish(ctx, PublishSnapshotInput{ID: uuid.NewString(), RepositoryID: repo.ID, Suite: "stable", Sequence: history[len(history)-1].Snapshot.Sequence + 1, SessionIDs: ids, Actor: "operator", CreatedAt: time.Now().UTC()}); !errors.Is(err, repository.ErrVersionConflict) {
		t.Fatalf("ordinary publish bypassed deletion: %v", err)
	}
	deletions, err := store.ListAPTPackageDeletions(ctx, repo.ID, "stable")
	if err != nil || len(deletions) != 1 {
		t.Fatalf("deletions: %+v %v", deletions, err)
	}
	third, err := l.Apply(ctx, repo.ID, "operator", "restore-one", LifecycleRequest{Suite: "stable", ExpectedSnapshotID: second.ID, Action: "restore", DeletionIDs: []string{deletions[0].ID}})
	if err != nil {
		t.Fatal(err)
	}
	_, members, err := store.GetAPTRepositorySnapshot(ctx, third.ID)
	if err != nil || len(members) != 2 {
		t.Fatalf("restore membership: %+v %v", members, err)
	}
	empty, err := l.Apply(ctx, repo.ID, "operator", "delete-all", LifecycleRequest{Suite: "stable", ExpectedSnapshotID: third.ID, Action: "delete", PublicationSessionIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	index, err := store.GetVisibleAPTSnapshotAsset(ctx, repo.ID, "dists/stable/main/binary-amd64/Packages")
	if err != nil {
		t.Fatal(err)
	}
	reader, _, err := objects.Open(ctx, index.ObjectKey)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || len(body) != 0 {
		t.Fatalf("empty Packages: %q %v", body, err)
	}
	if err = store.PruneAPTSnapshots(ctx, repo.ID, []string{empty.ID}, time.Now().Add(10*24*time.Hour), repository.AuditRecord{}); !errors.Is(err, repository.ErrVersionConflict) {
		t.Fatalf("pruned visible: %v", err)
	}
	// Fail the rejected ordinary build, then release all historical views. Test
	// time advances only at the store seam; production APIs use the actual clock.
	history, err = store.ListAPTRepositorySnapshots(ctx, repo.ID, "stable")
	if err != nil {
		t.Fatal(err)
	}
	retired := []string{}
	for _, h := range history {
		if h.Snapshot.State == repository.APTRepositorySnapshotBuilding {
			_ = store.FailAPTRepositorySnapshot(ctx, h.Snapshot.ID)
		}
		if h.Snapshot.State == repository.APTRepositorySnapshotRetired {
			retired = append(retired, h.Snapshot.ID)
		}
	}
	future := time.Now().Add(8 * 24 * time.Hour)
	if err = store.PruneAPTSnapshots(ctx, repo.ID, retired, future, repository.AuditRecord{Actor: "operator", Operation: "apt.snapshot.prune", OccurredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.GetAPTPackageRevisionForSession(ctx, ids[0]); err != nil {
		t.Fatalf("prune removed revision referenced by testing suite: %v", err)
	}
	if _, err = objects.Stat(ctx, revision.ObjectKey); err != nil {
		t.Fatalf("shared package lost: %v", err)
	}
	if _, err = l.Apply(ctx, repo.ID, "operator", "delete-testing", LifecycleRequest{Suite: "testing", ExpectedSnapshotID: otherSuite.ID, Action: "delete", PublicationSessionIDs: []string{shared.ID}}); err != nil {
		t.Fatal(err)
	}
	beforePurge, err := store.GetRepositoryCapacity(ctx, repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.PruneAPTSnapshots(ctx, repo.ID, []string{otherSuite.ID}, future, repository.AuditRecord{Actor: "operator", Operation: "apt.snapshot.prune", OccurredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	afterPurge, err := store.GetRepositoryCapacity(ctx, repo.ID)
	if err != nil || beforePurge.UsedBytes-afterPurge.UsedBytes != revision.Size {
		t.Fatalf("capacity release before=%+v after=%+v err=%v", beforePurge, afterPurge, err)
	}
	if _, err = store.GetAPTPackageRevisionForSession(ctx, ids[0]); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("revision still retained: %v", err)
	}
	maintenance := Maintenance{Store: store, Objects: objects}
	if err = maintenance.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if err = maintenance.RunReclaimJobs(ctx, 100); err != nil {
			t.Fatal(err)
		}
		jobs, e := store.ListLifecycleJobs(ctx, repo.ID, 100)
		if e != nil {
			t.Fatal(e)
		}
		pending := false
		for _, job := range jobs {
			if job.State != repository.LifecycleJobCompleted {
				pending = true
			}
		}
		if !pending {
			break
		}
	}
	if _, err = objects.Stat(ctx, revision.ObjectKey); !errors.Is(err, objectstore.ErrNotFound) {
		jobs, _ := store.ListLifecycleJobs(ctx, repo.ID, 100)
		referenced, _ := store.APTObjectHasDurableReference(ctx, revision.ObjectKey)
		t.Fatalf("expired unreferenced package not collected: %v referenced=%v jobs=%+v", err, referenced, jobs)
	}
	if _, err = objects.Stat(ctx, index.ObjectKey); err != nil {
		t.Fatalf("GC deleted current empty index: %v", err)
	}
	replay, err = l.Apply(ctx, repo.ID, "operator", "delete-one", req)
	if err != nil || replay.ID != second.ID || replay.State != repository.APTRepositorySnapshotPruned {
		t.Fatalf("pruned operation replay: %+v %v", replay, err)
	}
}

type testTrustedLifecycleSigner struct {
	t     *testing.T
	trust *TrustedSnapshotArchiveVerifier
}

func (s *testTrustedLifecycleSigner) SignRelease(ctx context.Context, request SignReleaseRequest) (SignReleaseResult, error) {
	release, err := io.ReadAll(request.Release)
	if err != nil {
		return SignReleaseResult{}, err
	}
	fixture := aptSignerTestFixtureForRelease(s.t, release)
	signer, err := NewHTTPSigner(HTTPSignerOptions{Endpoint: "http://127.0.0.1:18083/v1/sign-release", Token: "test-lifecycle-token-0000000000000000", Timeout: time.Second, Client: aptSignerResponseClient(fixture.response), TrustedFingerprints: []string{fixture.fingerprint}, TrustedPublicKeys: fixture.publicKey})
	if err != nil {
		return SignReleaseResult{}, err
	}
	s.trust, err = NewTrustedSnapshotArchiveVerifier([]string{fixture.fingerprint}, fixture.publicKey)
	if err != nil {
		return SignReleaseResult{}, err
	}
	request.Release = bytes.NewReader(release)
	return signer.SignRelease(ctx, request)
}
func TestLifecycleEmptyArchiveTrustedRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := objectstore.NewMemoryStore()
	repo := createAPTHostedRepository(t, ctx, store)
	session := stageAPTPackage(t, ctx, store, objects, repo.ID, uuid.NewString(), "widget", "1.0-1")
	signer := &testTrustedLifecycleSigner{t: t}
	p := NewPublisher(store, objects, signer)
	first, err := p.Publish(ctx, PublishSnapshotInput{ID: uuid.NewString(), RepositoryID: repo.ID, Suite: "stable", Sequence: 1, SessionIDs: []string{session.ID}, Actor: "ci", CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	empty, err := (Lifecycle{Publisher: p}).Apply(ctx, repo.ID, "operator", "delete", LifecycleRequest{Suite: "stable", ExpectedSnapshotID: first.ID, Action: "delete", PublicationSessionIDs: []string{session.ID}})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	manifest, err := (SnapshotArchiveExporter{Store: store, Objects: objects}).Export(ctx, empty.ID, &output)
	if err != nil || len(manifest.Packages) != 0 {
		t.Fatalf("empty archive: %v %+v", err, manifest)
	}
	target := repository.NewMemoryStore()
	createAPTHostedRepository(t, ctx, target)
	targetObjects := objectstore.NewMemoryStore()
	importer := NewSnapshotArchiveImporter(target, targetObjects, signer.trust)
	restored, err := importer.Import(ctx, repo.ID, digestBytes(output.Bytes()), bytes.NewReader(output.Bytes()), "operator")
	if err != nil {
		t.Fatal(err)
	}
	var replay bytes.Buffer
	if _, err = (SnapshotArchiveExporter{Store: target, Objects: targetObjects}).Export(ctx, restored.ID, &replay); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output.Bytes(), replay.Bytes()) {
		t.Fatal("empty archive re-export changed bytes")
	}
}

func TestLifecycleRetentionKeepsRecentUploadsAndRejectsStalePlan(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := objectstore.NewMemoryStore()
	repo := createAPTHostedRepository(t, ctx, store)
	old := stageAPTPackage(t, ctx, store, objects, repo.ID, uuid.NewString(), "widget", "10.0-1")
	newest := stageAPTPackage(t, ctx, store, objects, repo.ID, uuid.NewString(), "widget", "2.0-1")
	other := stageAPTPackage(t, ctx, store, objects, repo.ID, uuid.NewString(), "another", "1.0-1")
	p := NewPublisher(store, objects, deterministicAPTSigner{})
	first, err := p.Publish(ctx, PublishSnapshotInput{ID: uuid.NewString(), RepositoryID: repo.ID, Suite: "stable", Sequence: 1, SessionIDs: []string{old.ID, newest.ID, other.ID}, Actor: "ci", CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	request := LifecycleRequest{Suite: "stable", ExpectedSnapshotID: first.ID, Action: "retention", KeepLatest: 1, OlderThanDays: 1}
	plan, err := PreviewLifecycle(ctx, store, repo.ID, request)
	if err != nil || len(plan.RemoveSessionIDs) != 0 {
		t.Fatalf("recent uploads selected: %+v %v", plan, err)
	}
	plan, sessions, err := planAPTLifecycle(ctx, store, repo.ID, request, time.Now().Add(48*time.Hour))
	if err != nil || len(plan.RemoveSessionIDs) != 1 || plan.RemoveSessionIDs[0] != old.ID || len(sessions) != 2 {
		t.Fatalf("retention ordering/grouping: %+v %v", plan, err)
	}
}

type blockedLifecycleSigner struct{ entered, proceed chan struct{} }

func (s blockedLifecycleSigner) SignRelease(ctx context.Context, r SignReleaseRequest) (SignReleaseResult, error) {
	close(s.entered)
	select {
	case <-s.proceed:
	case <-ctx.Done():
		return SignReleaseResult{}, ctx.Err()
	}
	return (deterministicAPTSigner{}).SignRelease(ctx, r)
}
func TestLifecycleConcurrentPublishCannotBeOverwritten(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	store := repository.NewMemoryStore()
	objects := objectstore.NewMemoryStore()
	repo := createAPTHostedRepository(t, ctx, store)
	session := stageAPTPackage(t, ctx, store, objects, repo.ID, uuid.NewString(), "widget", "1.0-1")
	p := NewPublisher(store, objects, deterministicAPTSigner{})
	first, err := p.Publish(ctx, PublishSnapshotInput{ID: uuid.NewString(), RepositoryID: repo.ID, Suite: "stable", Sequence: 1, SessionIDs: []string{session.ID}, Actor: "ci", CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	signer := blockedLifecycleSigner{entered: make(chan struct{}), proceed: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, e := (Lifecycle{Publisher: NewPublisher(store, objects, signer)}).Apply(ctx, repo.ID, "operator", "delete", LifecycleRequest{Suite: "stable", ExpectedSnapshotID: first.ID, Action: "delete", PublicationSessionIDs: []string{session.ID}})
		done <- e
	}()
	select {
	case <-signer.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	concurrent, err := p.Publish(ctx, PublishSnapshotInput{ID: uuid.NewString(), RepositoryID: repo.ID, Suite: "stable", Sequence: 3, SessionIDs: []string{session.ID}, Actor: "ci", CreatedAt: time.Now().UTC()})
	close(signer.proceed)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if !errors.Is(err, repository.ErrVersionConflict) {
			t.Fatalf("stale delete committed: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	current, err := store.GetVisibleAPTRepositorySnapshot(ctx, repo.ID, "stable")
	if err != nil || current.ID != concurrent.ID {
		t.Fatalf("concurrent publisher overwritten: %+v %v", current, err)
	}
	deletions, err := store.ListAPTPackageDeletions(ctx, repo.ID, "stable")
	if err != nil || len(deletions) != 0 {
		t.Fatalf("failed delete left barriers: %+v %v", deletions, err)
	}
}
