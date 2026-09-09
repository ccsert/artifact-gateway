package aptpublication

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type governanceTestStore interface {
	publisherStore
	repository.LifecycleJobStore
	repository.APTArtifactStore
	repository.ArtifactIdentityStore
	repository.RepositoryQuarantineReadPolicyStore
}

type governanceSigner struct{ hook func() }

func (s governanceSigner) SignRelease(ctx context.Context, r SignReleaseRequest) (SignReleaseResult, error) {
	s.hook()
	return (deterministicAPTSigner{}).SignRelease(ctx, r)
}

func TestAPTGovernanceMemory(t *testing.T) {
	exerciseAPTGovernance(t, repository.NewMemoryStore(), objectstore.NewMemoryStore())
}

// Run the identical contract against Memory and PostgreSQL/RustFS. Neither old
// snapshot references nor a signature already in flight may bypass quarantine.
func exerciseAPTGovernance(t *testing.T, store governanceTestStore, objects objectstore.Store) {
	t.Helper()
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "apt-governance-" + uuid.NewString(), Format: repository.FormatAPT, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	// Failed publication attempts own generated-object intents. Drain them here
	// so later integration tests using the shared database do not claim our jobs.
	defer func() {
		history, e := store.ListAPTRepositorySnapshots(ctx, repo.ID, "stable")
		if e != nil {
			t.Error(e)
			return
		}
		for _, h := range history {
			if h.Snapshot.State == repository.APTRepositorySnapshotBuilding {
				if e = store.FailAPTRepositorySnapshot(ctx, h.Snapshot.ID); e != nil {
					t.Error(e)
					return
				}
			}
		}
		m := Maintenance{Store: store, Objects: objects}
		if e = m.Schedule(ctx); e != nil {
			t.Error(e)
			return
		}
		for i := 0; i < 30; i++ {
			if e = m.RunReclaimJobs(ctx, 100); e != nil {
				t.Error(e)
				return
			}
			jobs, e := store.ListLifecycleJobs(ctx, repo.ID, 100)
			if e != nil {
				t.Error(e)
				return
			}
			pending := false
			for _, job := range jobs {
				if job.State == repository.LifecycleJobPending || job.State == repository.LifecycleJobRetrying || job.State == repository.LifecycleJobRunning {
					pending = true
				}
			}
			if !pending {
				return
			}
		}
		t.Error("governance test left pending reclaim work")
	}()
	manager := NewManager(store, objects)
	stage := func(suite, name string) repository.APTPublicationSession {
		deb := testDebianPackage(t, "Package: "+name+"\nVersion: 1.0-1\nArchitecture: amd64\n")
		session, _, err := manager.CreateSession(ctx, CreateSessionInput{RepositoryID: repo.ID, Suite: suite, Component: "main", Publisher: "ci", ObjectName: name + ".deb", DeclaredDigest: digestBytes(deb), DeclaredSize: int64(len(deb)), IdempotencyKey: uuid.NewString()})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = manager.UploadPackage(ctx, session.ID, session.ObjectName, bytes.NewReader(deb), int64(len(deb))); err != nil {
			t.Fatal(err)
		}
		return session
	}
	widget, safe := stage("stable", "widget"), stage("stable", "safe")
	coordinate := "pool/main/w/widget/widget.deb"
	if _, err = store.GetAPTScanAsset(ctx, repo.ID, coordinate, widget.DeclaredDigest); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("staged scan: %v", err)
	}
	publish := func(p *Publisher, suite string, sequence int64, ids ...string) (repository.APTRepositorySnapshot, error) {
		return p.Publish(ctx, PublishSnapshotInput{ID: uuid.NewString(), RepositoryID: repo.ID, Suite: suite, Sequence: sequence, SessionIDs: ids, Actor: "ci", CreatedAt: time.Now().UTC()})
	}
	p := NewPublisher(store, objects, deterministicAPTSigner{})
	first, err := publish(p, "stable", 1, widget.ID, safe.ID)
	if err != nil {
		t.Fatal(err)
	}
	shared := stage("testing", "widget")
	if _, err = publish(p, "testing", 1, shared.ID); err != nil {
		t.Fatal(err)
	}
	identities, err := store.ListArtifactIdentities(ctx, repo.ID, repository.FormatAPT, repository.ArtifactIdentityScan, "", 100)
	if err != nil || len(identities) != 2 {
		t.Fatalf("identities=%+v err=%v", identities, err)
	}
	before, err := store.ListAPTSnapshotAssets(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	value := repository.ArtifactQuarantine{RepositoryID: repo.ID, Format: repository.FormatAPT, Coordinate: coordinate, Digest: widget.DeclaredDigest, State: repository.ArtifactQuarantineStateQuarantined, Reason: "review confirmed", UpdatedBy: "security"}
	q, err := store.ReplaceArtifactQuarantine(ctx, value, "0")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range before {
		if _, err = store.GetVisibleAPTSnapshotAsset(ctx, repo.ID, a.Path); err != nil {
			t.Fatalf("default changed %s: %v", a.Path, err)
		}
	}
	if _, err = store.ReplaceRepositoryQuarantineReadPolicy(ctx, repo.ID, repository.RepositoryQuarantineReadPolicy{Enabled: true}, "1"); err != nil {
		t.Fatal(err)
	}
	for _, a := range before {
		_, err = store.GetVisibleAPTSnapshotAsset(ctx, repo.ID, a.Path)
		if a.Path == "pool/main/s/safe/safe.deb" {
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		if !errors.Is(err, repository.ErrArtifactQuarantined) {
			t.Fatalf("unblocked %s: %v", a.Path, err)
		}
	}
	if _, err = store.GetVisibleAPTSnapshotAsset(ctx, repo.ID, "dists/testing/InRelease"); !errors.Is(err, repository.ErrArtifactQuarantined) {
		t.Fatalf("suite alias bypass: %v", err)
	}
	if _, err = store.GetAPTScanAsset(ctx, repo.ID, coordinate, widget.DeclaredDigest); err != nil {
		t.Fatalf("quarantined packages must remain scannable: %v", err)
	}
	for _, path := range []string{"dists/stable/InRelease", "stable/main/widget@1.0-1#amd64", "pool/main/w/widget/other.deb"} {
		if _, err = store.GetAPTScanAsset(ctx, repo.ID, path, widget.DeclaredDigest); !errors.Is(err, repository.ErrNotFound) {
			t.Fatalf("scan alias %s: %v", path, err)
		}
	}
	shouldNotSign := NewPublisher(store, objects, governanceSigner{hook: func() { t.Error("quarantined package reached signer") }})
	if _, err = publish(shouldNotSign, "stable", 2, widget.ID); !errors.Is(err, repository.ErrArtifactQuarantined) {
		t.Fatalf("sign admission: %v", err)
	}
	// Deleting the quarantined member produces a usable, correctly signed view.
	// Old by-hash indexes and the shared pool URL still fail closed.
	l := Lifecycle{Publisher: p}
	clean, err := l.Apply(ctx, repo.ID, "operator", "remove-quarantined", LifecycleRequest{Suite: "stable", ExpectedSnapshotID: first.ID, Action: "delete", PublicationSessionIDs: []string{widget.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.GetVisibleAPTSnapshotAsset(ctx, repo.ID, "dists/stable/InRelease"); err != nil {
		t.Fatal(err)
	}
	for _, a := range before {
		if a.Path == coordinate || bytes.Contains([]byte(a.Path), []byte("/by-hash/")) {
			if _, err = store.GetVisibleAPTSnapshotAsset(ctx, repo.ID, a.Path); !errors.Is(err, repository.ErrArtifactQuarantined) {
				t.Fatalf("retired bypass %s: %v", a.Path, err)
			}
		}
	}
	deletions, err := store.ListAPTPackageDeletions(ctx, repo.ID, "stable")
	if err != nil || len(deletions) != 1 {
		t.Fatalf("deletions=%+v %v", deletions, err)
	}
	restore := LifecycleRequest{Suite: "stable", ExpectedSnapshotID: clean.ID, Action: "restore", DeletionIDs: []string{deletions[0].ID}}
	if _, err = l.Apply(ctx, repo.ID, "operator", "restore-quarantined", restore); !errors.Is(err, repository.ErrArtifactQuarantined) {
		t.Fatalf("restore bypass: %v", err)
	}
	q.State = repository.ArtifactQuarantineStateReleased
	q, err = store.ReplaceArtifactQuarantine(ctx, q, q.Version)
	if err != nil {
		t.Fatal(err)
	}
	current, err := store.GetVisibleAPTRepositorySnapshot(ctx, repo.ID, "stable")
	if err != nil || current.ID != clean.ID {
		t.Fatalf("release resurrected: %+v %v", current, err)
	}
	if _, err = l.Apply(ctx, repo.ID, "operator", "restore-quarantined", restore); err != nil {
		t.Fatal(err)
	}
	// Mutate governance through the store while a signature is being produced.
	// The visibility transaction must reject it even when pre-sign checks passed.
	racePublisher := NewPublisher(store, objects, governanceSigner{hook: func() {
		q.State = repository.ArtifactQuarantineStateQuarantined
		q, err = store.ReplaceArtifactQuarantine(ctx, q, q.Version)
		if err != nil {
			t.Fatal(err)
		}
	}})
	current, err = store.GetVisibleAPTRepositorySnapshot(ctx, repo.ID, "stable")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = publish(racePublisher, "stable", current.Sequence+1, widget.ID, safe.ID); !errors.Is(err, repository.ErrArtifactQuarantined) {
		t.Fatalf("inflight quarantine bypass: %v", err)
	}
	after, err := store.GetVisibleAPTRepositorySnapshot(ctx, repo.ID, "stable")
	if err != nil || after.ID != current.ID {
		t.Fatalf("failed admission changed visibility: %+v %v", after, err)
	}
	// Import has an independent pure-public-key path. It must honor destination
	// governance even when no Publisher/signer is configured there.
	source := repository.NewMemoryStore()
	if _, err = source.CreateHostedRepository(ctx, repo); err != nil {
		t.Fatal(err)
	}
	sourceObjects := objectstore.NewMemoryStore()
	importedSession := stageAPTPackageInScope(t, ctx, source, sourceObjects, repo.ID, uuid.NewString(), "incoming", "archived", "1.0-1", "archived.deb")
	trustedSigner := &testTrustedLifecycleSigner{t: t}
	sourceSnapshot, err := publish(NewPublisher(source, sourceObjects, trustedSigner), "incoming", 1, importedSession.ID)
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if _, err = (SnapshotArchiveExporter{Store: source, Objects: sourceObjects}).Export(ctx, sourceSnapshot.ID, &archive); err != nil {
		t.Fatal(err)
	}
	// Construct a production PostgreSQL archive receipt: its timestamps have
	// microsecond precision, unlike the in-memory fixture's wall-clock values.
	archiveBytes := rewriteArchiveObjects(t, archive.Bytes(), func(m *SnapshotArchiveManifest, _ map[string][]byte) {
		normalize := func(raw string) string {
			v, e := time.Parse(time.RFC3339Nano, raw)
			if e != nil {
				t.Fatal(e)
			}
			return v.Truncate(time.Microsecond).Format(time.RFC3339Nano)
		}
		m.Snapshot.CreatedAt = normalize(m.Snapshot.CreatedAt)
		m.Snapshot.PublishedAt = normalize(m.Snapshot.PublishedAt)
		for i := range m.Packages {
			m.Packages[i].CreatedAt = normalize(m.Packages[i].CreatedAt)
		}
	})
	importQuarantine, err := store.ReplaceArtifactQuarantine(ctx, repository.ArtifactQuarantine{RepositoryID: repo.ID, Format: repository.FormatAPT, Coordinate: "pool/main/a/archived/archived.deb", Digest: importedSession.DeclaredDigest, State: repository.ArtifactQuarantineStateQuarantined, Reason: "destination evidence", UpdatedBy: "security"}, "0")
	if err != nil {
		t.Fatal(err)
	}
	importer := NewSnapshotArchiveImporter(store, objects, trustedSigner.trust)
	if _, err = importer.Import(ctx, repo.ID, digestBytes(archiveBytes), bytes.NewReader(archiveBytes), "operator"); !errors.Is(err, repository.ErrArtifactQuarantined) {
		t.Fatalf("archive bypass: %v", err)
	}
	if _, err = store.GetVisibleAPTRepositorySnapshot(ctx, repo.ID, "incoming"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("failed import exposed metadata: %v", err)
	}
	importQuarantine.State = repository.ArtifactQuarantineStateReleased
	if _, err = store.ReplaceArtifactQuarantine(ctx, importQuarantine, importQuarantine.Version); err != nil {
		t.Fatal(err)
	}
	imported, err := importer.Import(ctx, repo.ID, digestBytes(archiveBytes), bytes.NewReader(archiveBytes), "operator")
	if err != nil {
		t.Fatalf("retry archive after release: %v", err)
	}
	_, members, err := store.GetAPTRepositorySnapshot(ctx, imported.ID)
	if err != nil || len(members) != 1 {
		t.Fatalf("imported members: %+v %v", members, err)
	}
	if _, err = l.Apply(ctx, repo.ID, "operator", "remove-imported", LifecycleRequest{Suite: "incoming", ExpectedSnapshotID: imported.ID, Action: "delete", PublicationSessionIDs: []string{members[0].PublicationSessionID}}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.GetAPTScanAsset(ctx, repo.ID, "pool/main/a/archived/archived.deb", importedSession.DeclaredDigest); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("retired package became scan identity: %v", err)
	}
	if _, err = store.GetVisibleAPTSnapshotAsset(ctx, repo.ID, "pool/main/a/archived/archived.deb"); err != nil {
		t.Fatalf("retired download should remain recoverable: %v", err)
	}

}
