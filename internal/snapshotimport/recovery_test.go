package snapshotimport_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/snapshotimport"
	fixture "github.com/artifact-gateway/artifact-gateway/internal/testsupport/snapshotfixture"
)

type mutateAtUpload struct {
	*objectstore.MemoryStore
	key, path string
	body      []byte
	mutated   bool
}

func (o *mutateAtUpload) PutVerifiedReader(ctx context.Context, key string, r io.Reader, size int64, digest string) error {
	if key == o.key && !o.mutated {
		o.mutated = true
		if err := os.WriteFile(o.path, o.body, 0600); err != nil {
			return err
		}
	}
	return o.MemoryStore.PutVerifiedReader(ctx, key, r, size, digest)
}

func TestSourceChangeDuringUploadCannotPoisonDigestObject(t *testing.T) {
	p, dir, m, files := prepared(t)
	s, id, objects := target(t)
	f := m.Coordinates[0].Builds[0].Files[1]
	key := "native/maven/sha256/" + strings.TrimPrefix(f.Digest, "sha256:")
	changed := &mutateAtUpload{MemoryStore: objects, key: key, path: filepath.Join(dir, f.Path), body: []byte(strings.Repeat("X", int(f.Size)))}
	r, err := snapshotimport.Run(context.Background(), p, s, changed, id, "target", "operator", fixture.SnapshotTargetBinding, true, fixture.SnapshotCapacity(t, p, id, "target", "operator"))
	if err != nil || r.Status != "verified" || !changed.mutated {
		t.Fatalf("fixed spool=%v %v", r, err)
	}
	got, err := objects.Get(context.Background(), key)
	if err != nil || string(got) != string(files[f.Path]) {
		t.Fatal("mutable upload poisoned valid digest key")
	}
}

type mutateAtStage struct {
	*repository.MemoryStore
	name, path string
	mutated    bool
}

func (s *mutateAtStage) MarkMavenPublishObject(ctx context.Context, id, name, key string) error {
	if name == s.name && !s.mutated {
		s.mutated = true
		if err := os.WriteFile(s.path, []byte("changed after preflight"), 0600); err != nil {
			return err
		}
	}
	return s.MemoryStore.MarkMavenPublishObject(ctx, id, name, key)
}
func TestSourceChangeBeforeSpoolRejectsWithoutPoisonAndCanRecover(t *testing.T) {
	p, dir, m, files := prepared(t)
	s, id, o := target(t)
	f := m.Coordinates[0].Builds[0].Files[1]
	key := "native/maven/sha256/" + strings.TrimPrefix(f.Digest, "sha256:")
	stage := &mutateAtStage{MemoryStore: s, name: filepath.Base(f.Path), path: filepath.Join(dir, f.Path)}
	cap := fixture.SnapshotCapacity(t, p, id, "target", "operator")
	if _, err := snapshotimport.Run(context.Background(), p, stage, o, id, "target", "operator", fixture.SnapshotTargetBinding, true, cap); err == nil {
		t.Fatal("admitted changed source")
	}
	if _, err := o.Get(context.Background(), key); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatal("invalid source left a poisoned CAS key")
	}
	if err := os.WriteFile(filepath.Join(dir, f.Path), files[f.Path], 0600); err != nil {
		t.Fatal(err)
	}
	if r, err := snapshotimport.Run(context.Background(), p, s, o, id, "target", "operator", fixture.SnapshotTargetBinding, true, cap); err != nil || r.Status != "verified" {
		t.Fatalf("restored source cannot recover=%v %v", r, err)
	}
}

type expireWhileLocked struct {
	*repository.MemoryStore
	until   time.Time
	entered bool
}

func (s *expireWhileLocked) LockMavenSnapshotImport(ctx context.Context, repo, coord string) (context.Context, func(), error) {
	ctx, release, err := s.MemoryStore.LockMavenSnapshotImport(ctx, repo, coord)
	if err != nil {
		return ctx, release, err
	}
	s.entered = true
	time.Sleep(time.Until(s.until) + 10*time.Millisecond)
	return ctx, release, nil
}
func TestCapacityExpiresDuringLockWaitBeforeFirstWrite(t *testing.T) {
	p, _, _, _ := prepared(t)
	s, id, o := target(t)
	cap := fixture.SnapshotCapacity(t, p, id, "target", "operator")
	cap.Snapshot.ValidUntil = time.Now().Add(time.Second)
	wait := &expireWhileLocked{MemoryStore: s, until: cap.Snapshot.ValidUntil}
	r, err := snapshotimport.Run(context.Background(), p, wait, o, id, "target", "operator", fixture.SnapshotTargetBinding, true, cap)
	if err == nil || !wait.entered || r.Entries[0].Reason != "capacity_preflight_expired_before_write" {
		t.Fatalf("expiry boundary=%v %v", r, err)
	}
	if _, err := s.GetMavenSnapshotImport(context.Background(), id, p.Plans[0].Coordinate); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("expired evidence reserved coordinate")
	}
	keys, _ := o.List(context.Background(), "")
	if len(keys) != 0 {
		t.Fatal("expired evidence uploaded objects")
	}
}

func TestImportSameTimestampDistinctBuildsAndOwnership(t *testing.T) {
	dir, digest, _, files := fixture.SnapshotBundleWithBuilds(t, []fixture.BuildIdentity{{Timestamp: "20260101.000000", Number: 1}, {Timestamp: "20260101.000000", Number: 10}, {Timestamp: "20260101.000000", Number: 70}})
	p, rejected, err := snapshotimport.Prepare(context.Background(), dir, digest)
	if err != nil {
		t.Fatalf("%v %v", rejected, err)
	}
	defer func() { _ = p.Close() }()
	s, id, objects := target(t)
	if _, err = snapshotimport.Run(context.Background(), p, s, objects, id, "target", "operator", fixture.SnapshotTargetBinding, true, fixture.SnapshotCapacity(t, p, id, "target", "operator")); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListMavenArtifacts(context.Background(), id)
	if err != nil || len(items) != 3 {
		t.Fatalf("%v %v", items, err)
	}
	ids := map[string]bool{}
	for _, a := range items {
		if ids[a.ID] {
			t.Fatal("equal POMs collapsed source identity")
		}
		ids[a.ID] = true
		if a.SourceBuildNumber == 1 {
			nodes, err := s.ListArtifactBrowseNodes(context.Background(), id, repository.FormatMaven, repository.ArtifactBrowseParent{Kind: repository.BrowseNodeVersion, Version: a.Coordinate, BuildNumber: a.BuildNumber}, 200, "")
			if err != nil {
				t.Fatal(err)
			}
			for _, n := range nodes {
				if strings.Contains(n.Path, "-10.") || strings.Contains(n.Path, "-70.") {
					t.Fatal("prefix captured sibling build")
				}
			}
			if _, err = s.TombstoneMavenArtifact(context.Background(), id, a.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	for path := range files {
		if strings.Contains(path, "-10.") || strings.Contains(path, "-10-") {
			if _, err := s.GetMavenAsset(context.Background(), id, path); err != nil {
				t.Fatalf("sibling hidden: %s %v", path, err)
			}
		}
	}
}

func TestManifestRequiresExplicitAbsenceAndRejectsAmbiguousMetadata(t *testing.T) {
	for _, kind := range []string{"omitted-metadata", "omitted-size", "duplicate-pom-field", "classifier-in-value", "legacy-metadata"} {
		t.Run(kind, func(t *testing.T) {
			dir, _, m, files := fixture.SnapshotBundle(t)
			if kind == "duplicate-pom-field" {
				f := &m.Coordinates[0].Builds[0].Files[0]
				writeFixtureFile(t, dir, f, []byte(strings.ReplaceAll(string(files[f.Path]), "</project>", "<version>1.0-SNAPSHOT</version></project>")))
			}
			if kind == "classifier-in-value" {
				f := m.Coordinates[0].Metadata
				writeFixtureFile(t, dir, f, []byte(strings.Replace(string(files[f.Path]), "<extension>jar</extension><classifier></classifier><value>1.0-20260101.000000-7</value>", "<extension>jar</extension><classifier></classifier><value>1.0-20260101.000000-7-sources</value>", 1)))
			}
			if kind == "legacy-metadata" {
				f := m.Coordinates[0].Metadata
				b := string(files[f.Path])
				start := strings.Index(b, "<snapshotVersions>")
				end := strings.Index(b, "</snapshotVersions>") + len("</snapshotVersions>")
				writeFixtureFile(t, dir, f, []byte(b[:start]+b[end:]))
			}
			digest := fixture.WriteSnapshotManifest(t, dir, m)
			if kind == "omitted-metadata" || kind == "omitted-size" {
				b, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
				var raw map[string]any
				if err := json.Unmarshal(b, &raw); err != nil {
					t.Fatal(err)
				}
				c := raw["coordinates"].([]any)[0].(map[string]any)
				if kind == "omitted-metadata" {
					delete(c, "metadata")
				} else {
					delete(c["builds"].([]any)[0].(map[string]any)["files"].([]any)[0].(map[string]any), "size")
				}
				b, _ = json.Marshal(raw)
				if err := os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0600); err != nil {
					t.Fatal(err)
				}
				digest = fixtureDigest(b)
			}
			p, _, err := snapshotimport.Prepare(context.Background(), dir, digest)
			if p != nil {
				defer func() { _ = p.Close() }()
			}
			if err == nil {
				t.Fatalf("accepted %s", kind)
			}
		})
	}
}

type commitFault struct {
	*repository.MemoryStore
	coordinate string
	lost       bool
	once       bool
}

func (s *commitFault) CommitMavenSnapshotImport(ctx context.Context, p repository.MavenSnapshotImportPlan) (repository.MavenSnapshotImport, error) {
	if p.Coordinate != s.coordinate || s.once {
		return s.MemoryStore.CommitMavenSnapshotImport(ctx, p)
	}
	s.once = true
	if s.lost {
		v, err := s.MemoryStore.CommitMavenSnapshotImport(ctx, p)
		if err != nil {
			return v, err
		}
		return v, errors.New("synthetic lost response")
	}
	return repository.MavenSnapshotImport{}, errors.New("synthetic failed commit")
}

func TestLostCommitResponseReplaysWithoutChangingSelection(t *testing.T) {
	p, _, _, _ := prepared(t)
	s, id, objects := target(t)
	ctx := context.Background()
	cap := fixture.SnapshotCapacity(t, p, id, "target", "operator")
	fault := &commitFault{MemoryStore: s, coordinate: p.Plans[0].Coordinate, lost: true}
	r, err := snapshotimport.Run(ctx, p, fault, objects, id, "target", "operator", fixture.SnapshotTargetBinding, true, cap)
	if err == nil || r.Status != "partial" || r.Entries[0].State != "committed" {
		t.Fatalf("ambiguous report=%v %v", r, err)
	}
	before, _ := s.GetMavenSnapshotImport(ctx, id, p.Plans[0].Coordinate)
	r, err = snapshotimport.Run(ctx, p, s, objects, id, "target", "different-operator", fixture.SnapshotTargetBinding, true, cap)
	if err != nil || r.Counts.Verified != 1 {
		t.Fatalf("%v %v", r, err)
	}
	after, _ := s.GetMavenSnapshotImport(ctx, id, p.Plans[0].Coordinate)
	b, _ := json.Marshal(before)
	a, _ := json.Marshal(after)
	if string(a) != string(b) || len(s.Audits) != 1 {
		t.Fatal("replay altered immutable checkpoint or duplicated audit")
	}
	repo, _ := s.GetHostedRepository(ctx, id)
	if s.Audits[0].Repository != repo.Name || s.Audits[0].Evidence["repositoryId"] != id {
		t.Fatal("audit not searchable by repository name")
	}
}

func TestMultiCoordinateFailureKeepsCommittedHistoryAndMetadata(t *testing.T) {
	_, dir, m, _ := prepared(t)
	var clone snapshotimport.Coordinate
	body, _ := json.Marshal(m.Coordinates[0])
	body = []byte(strings.ReplaceAll(string(body), "widget", "zother"))
	if err := json.Unmarshal(body, &clone); err != nil {
		t.Fatal(err)
	}
	write := func(f *snapshotimport.File) {
		old := strings.ReplaceAll(f.Path, "zother", "widget")
		b, err := os.ReadFile(filepath.Join(dir, old))
		if err != nil {
			t.Fatal(err)
		}
		b = []byte(strings.ReplaceAll(string(b), "widget", "zother"))
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, f.Path)), 0700); err != nil {
			t.Fatal(err)
		}
		writeFixtureFile(t, dir, f, b)
	}
	for i := range clone.Builds {
		for j := range clone.Builds[i].Files {
			write(&clone.Builds[i].Files[j])
		}
	}
	write(clone.Metadata)
	m.Coordinates = append(m.Coordinates, clone)
	digest := fixture.WriteSnapshotManifest(t, dir, m)
	p, rejected, err := snapshotimport.Prepare(context.Background(), dir, digest)
	if err != nil {
		t.Fatalf("%v %v", rejected, err)
	}
	defer func() { _ = p.Close() }()
	s, id, objects := target(t)
	ctx := context.Background()
	cap := fixture.SnapshotCapacity(t, p, id, "target", "operator")
	fault := &commitFault{MemoryStore: s, coordinate: clone.Coordinate}
	r, err := snapshotimport.Run(ctx, p, fault, objects, id, "target", "operator", fixture.SnapshotTargetBinding, true, cap)
	if err == nil || r.Counts.Verified != 1 || r.Counts.Failed != 1 {
		t.Fatalf("%v %v", r, err)
	}
	before, _ := s.GetMavenSnapshotImport(ctx, id, m.Coordinates[0].Coordinate)
	items, _ := s.ListMavenArtifacts(ctx, id)
	if len(items) != 3 {
		t.Fatal("partial second GAV visible")
	}
	r, err = snapshotimport.Run(ctx, p, s, objects, id, "target", "operator", fixture.SnapshotTargetBinding, true, cap)
	if err != nil || r.Counts.Verified != 2 {
		t.Fatalf("%v %v", r, err)
	}
	after, _ := s.GetMavenSnapshotImport(ctx, id, m.Coordinates[0].Coordinate)
	b, _ := json.Marshal(before)
	a, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatal("recovery changed first selector")
	}
}

func TestSourceDriftRetentionAndStaleCapacityRejectBeforeMutation(t *testing.T) {
	for _, kind := range []string{"source-drift", "retention", "stale-capacity", "different-target"} {
		t.Run(kind, func(t *testing.T) {
			p, dir, m, _ := prepared(t)
			s, id, o := target(t)
			ctx := context.Background()
			cap := fixture.SnapshotCapacity(t, p, id, "target", "operator")
			switch kind {
			case "source-drift":
				f := m.Coordinates[0].Builds[0].Files[1]
				if err := os.WriteFile(filepath.Join(dir, f.Path), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "retention":
				policy, _ := s.GetRepositoryRetentionPolicy(ctx, id)
				policy.Enabled = true
				if _, err := s.ReplaceRepositoryRetentionPolicy(ctx, id, policy, policy.Version); err != nil {
					t.Fatal(err)
				}
			case "stale-capacity":
				cap.Snapshot.ValidUntil = time.Now().UTC().Add(-time.Minute)
			case "different-target":
				cap.TargetID = "sha256:" + strings.Repeat("b", 64)
			}
			if _, err := snapshotimport.Run(ctx, p, s, o, id, "target", "operator", fixture.SnapshotTargetBinding, true, cap); err == nil {
				t.Fatalf("accepted %s", kind)
			}
			if _, err := s.GetMavenSnapshotImport(ctx, id, p.Plans[0].Coordinate); !errors.Is(err, repository.ErrNotFound) {
				t.Fatal("wrote checkpoint")
			}
			keys, _ := o.List(ctx, "")
			if len(keys) != 0 {
				t.Fatal("wrote objects")
			}
		})
	}
}

func TestTargetBindingPinsDatabaseEndpointAndBucket(t *testing.T) {
	a, err := snapshotimport.TargetBinding("cluster-A\x00db", "http://S3.EXAMPLE/", "bucket")
	if err != nil {
		t.Fatal(err)
	}
	b, err := snapshotimport.TargetBinding("cluster-A\x00db", "http://s3.example", "bucket")
	if err != nil || a != b {
		t.Fatal("normalization changed identity")
	}
	for _, v := range [][3]string{{"cluster-B\x00db", "http://s3.example", "bucket"}, {"cluster-A\x00other", "http://s3.example", "bucket"}, {"cluster-A\x00db", "http://other.example", "bucket"}, {"cluster-A\x00db", "http://s3.example", "other"}} {
		b, err := snapshotimport.TargetBinding(v[0], v[1], v[2])
		if err != nil || b == a {
			t.Fatal("different physical target accepted")
		}
	}
}
