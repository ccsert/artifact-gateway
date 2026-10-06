package snapshotimport_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/snapshotimport"
	testsupport "github.com/artifact-gateway/artifact-gateway/internal/testsupport/snapshotfixture"
	"github.com/google/uuid"
)

func prepared(t *testing.T) (*snapshotimport.Prepared, string, snapshotimport.Manifest, map[string][]byte) {
	t.Helper()
	dir, digest, m, files := testsupport.SnapshotBundle(t)
	p, r, err := snapshotimport.Prepare(context.Background(), dir, digest)
	if err != nil {
		t.Fatalf("prepare=%v %v", r, err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p, dir, m, files
}
func target(t *testing.T) (*repository.MemoryStore, string, *objectstore.MemoryStore) {
	t.Helper()
	s := repository.NewMemoryStore()
	id := uuid.NewString()
	if _, err := s.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: id, Name: "synthetic-" + id, Format: repository.FormatMaven}); err != nil {
		t.Fatal(err)
	}
	return s, id, objectstore.NewMemoryStore()
}
func TestBundleRejectsAnomaliesBeforeAnyWrites(t *testing.T) {
	for _, kind := range []string{"missing-jar", "wrong-pom-version", "wrong-path", "duplicate-timestamp-build", "bad-metadata-current", "wrong-bytes", "manifest-drift", "symlink", "trailing-xml", "pom-only-jar", "unsupported-packaging"} {
		t.Run(kind, func(t *testing.T) {
			dir, _, m, files := testsupport.SnapshotBundle(t)
			var digest string
			b := &m.Coordinates[0].Builds[0]
			switch kind {
			case "missing-jar":
				b.Files = append(b.Files[:1], b.Files[2:]...)
			case "wrong-pom-version", "trailing-xml", "unsupported-packaging":
				f := &b.Files[0]
				body := string(files[f.Path])
				if kind == "wrong-pom-version" {
					body = strings.ReplaceAll(body, "1.0-SNAPSHOT", "1.0")
				}
				if kind == "trailing-xml" {
					body += "<extra/>"
				}
				if kind == "unsupported-packaging" {
					body = strings.ReplaceAll(body, "</project>", "<packaging>custom-unmapped</packaging></project>")
				}
				writeFixtureFile(t, dir, f, []byte(body))
			case "wrong-path":
				b.Files[0].Path = "../escape.pom"
			case "duplicate-timestamp-build":
				m.Coordinates[0].Builds = append(m.Coordinates[0].Builds, *b)
			case "bad-metadata-current":
				f := m.Coordinates[0].Metadata
				writeFixtureFile(t, dir, f, []byte(strings.ReplaceAll(string(files[f.Path]), "1.0-20260101.000000-7", "1.0-20260101.000000-99")))
			case "wrong-bytes":
				f := b.Files[1]
				if err := os.WriteFile(filepath.Join(dir, f.Path), bytes.Repeat([]byte("X"), int(f.Size)), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				f := b.Files[1]
				if err := os.Rename(filepath.Join(dir, f.Path), filepath.Join(dir, f.Path)+".real"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Base(f.Path)+".real", filepath.Join(dir, f.Path)); err != nil {
					t.Fatal(err)
				}
			case "pom-only-jar":
				b.Files = b.Files[:1]
			}
			if kind != "manifest-drift" {
				digest = testsupport.WriteSnapshotManifest(t, dir, m)
			} else {
				digest = "sha256:" + strings.Repeat("0", 64)
			}
			p, r, err := snapshotimport.Prepare(context.Background(), dir, digest)
			if p != nil {
				defer func() { _ = p.Close() }()
			}
			if err == nil {
				t.Fatalf("accepted %s", kind)
			}
			if p != nil && len(r) == 0 {
				t.Fatal("missing explicit rejected GAV")
			}
		})
	}
}
func writeFixtureFile(t *testing.T, dir string, f *snapshotimport.File, b []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, f.Path), b, 0600); err != nil {
		t.Fatal(err)
	}
	f.Size = int64(len(b))
	f.Digest = fixtureDigest(b)
}

func TestArchetypeSnapshotImportRequiresMainJAR(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "classifier-is-not-main", true: "missing-file"}[missing], func(t *testing.T) {
			dir, _, m, _ := testsupport.SnapshotArchetypeBundle(t)
			b := &m.Coordinates[0].Builds[0]
			want := "incomplete_build"
			if missing {
				if err := os.Remove(filepath.Join(dir, b.Files[1].Path)); err != nil {
					t.Fatal(err)
				}
				want = "source_bytes_mismatch"
			} else {
				// Keep the POM and both classifier assets, but no main JAR.
				b.Files = append(b.Files[:1], b.Files[2:]...)
			}
			digest := testsupport.WriteSnapshotManifest(t, dir, m)
			var out, stderr bytes.Buffer
			code := snapshotimport.RunCLI(context.Background(), []string{"verify", "--bundle", dir, "--manifest-sha256", digest}, &out, &stderr)
			var report snapshotimport.Report
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if code != 1 || report.Status != "rejected" || len(report.Rejected) != 1 || report.Rejected[0].Coordinate != m.Coordinates[0].Coordinate || report.Rejected[0].Reason != want {
				t.Fatalf("verify exit=%d report=%+v; want explicit %s rejection", code, report, want)
			}
		})
	}
}

func TestExplicitExclusionAndAbsentMetadata(t *testing.T) {
	p, dir, m, _ := prepared(t)
	m.Coordinates[0].Metadata = nil
	m.Excluded = []snapshotimport.Exclusion{{Coordinate: "org.example:broken:1.0-SNAPSHOT", Reason: "incomplete-build"}}
	digest := testsupport.WriteSnapshotManifest(t, dir, m)
	p2, r, err := snapshotimport.Prepare(context.Background(), dir, digest)
	if err != nil {
		t.Fatalf("%v %v", r, err)
	}
	defer func() { _ = p2.Close() }()
	s, id, o := target(t)
	report, err := snapshotimport.Run(context.Background(), p2, s, o, id, "target", "operator", testsupport.SnapshotTargetBinding, true, testsupport.SnapshotCapacity(t, p2, id, "target", "operator"))
	if err != nil || report.Status != "verified" || len(report.Excluded) != 1 {
		t.Fatalf("%v %v", report, err)
	}
	checkpoint, err := s.GetMavenSnapshotImport(context.Background(), id, p.Plans[0].Coordinate)
	if err != nil || checkpoint.Metadata != nil || len(checkpoint.Aliases) != 0 {
		t.Fatalf("invented current mapping=%v %v", checkpoint, err)
	}
}
func TestImportCapacityConflictDryRunAndReplay(t *testing.T) {
	p, _, _, _ := prepared(t)
	s, id, o := target(t)
	ctx := context.Background()
	cap := testsupport.SnapshotCapacity(t, p, id, "target", "operator")
	r, err := snapshotimport.Run(ctx, p, s, o, id, "target", "operator", testsupport.SnapshotTargetBinding, false)
	if err != nil || r.Status != "ready" {
		t.Fatalf("dry=%v %v", r, err)
	}
	if _, err := s.GetMavenSnapshotImport(ctx, id, p.Plans[0].Coordinate); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("dry-run wrote checkpoint")
	}
	keys, _ := o.List(ctx, "")
	if len(keys) != 0 {
		t.Fatal("dry-run wrote bytes")
	}
	if _, err := snapshotimport.Run(ctx, p, s, o, id, "target", "operator", testsupport.SnapshotTargetBinding, true); err == nil {
		t.Fatal("apply without preflight")
	}
	wrong := cap
	wrong.References = cap.References[1:]
	if _, err := snapshotimport.Run(ctx, p, s, o, id, "target", "operator", testsupport.SnapshotTargetBinding, true, wrong); err == nil {
		t.Fatal("accepted incomplete references")
	}
	for i := 0; i < 2; i++ {
		r, err = snapshotimport.Run(ctx, p, s, o, id, "target", "operator", testsupport.SnapshotTargetBinding, true, cap)
		if err != nil || r.Status != "verified" {
			t.Fatalf("run %d=%v %v", i, r, err)
		}
	}
	artifacts, _ := s.ListMavenArtifacts(ctx, id)
	if len(artifacts) != 3 {
		t.Fatalf("replay created duplicates=%v", artifacts)
	}
	first, _ := s.SearchMavenArtifacts(ctx, id, "org.example:", 1, repository.MavenArtifactCursor{})
	second, _ := s.SearchMavenArtifacts(ctx, id, "org.example:", 1, repository.MavenArtifactCursor{Coordinate: first[0].Coordinate, BuildNumber: first[0].BuildNumber})
	if len(second) != 1 || first[0].ID == second[0].ID {
		t.Fatal("duplicate source build lost in paging")
	}
	plan, _ := p.References(id, "target", "operator", testsupport.SnapshotTargetBinding)
	changed := plan[0]
	changed.SourceID = "different-source"
	if err := s.CheckMavenSnapshotImport(ctx, changed); !errors.Is(err, repository.ErrIdempotencyConflict) {
		t.Fatalf("source conflict=%v", err)
	}
	_, err = s.CreateMavenPublishSession(ctx, repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: id, Coordinate: p.Plans[0].Coordinate, Publisher: "ordinary", State: "open"})
	if !errors.Is(err, repository.ErrNameExists) {
		t.Fatalf("ordinary overwrite=%v", err)
	}
	// Full target bytes, not its digest metadata, determine the replay result.
	a := plan[0].Assets[0]
	if err := o.Put(ctx, a.ObjectKey, bytes.Repeat([]byte("X"), int(a.Size))); err != nil {
		t.Fatal(err)
	}
	r, err = snapshotimport.Run(ctx, p, s, o, id, "target", "operator", testsupport.SnapshotTargetBinding, true, cap)
	if err == nil || r.Status != "rejected" {
		t.Fatalf("same size corruption=%v %v", r, err)
	}
}

type failObjects struct {
	*objectstore.MemoryStore
	remaining int
}

func (o *failObjects) PutVerifiedReader(ctx context.Context, key string, r io.Reader, size int64, digest string) error {
	if o.remaining == 0 {
		return errors.New("synthetic interruption")
	}
	o.remaining--
	return o.MemoryStore.PutVerifiedReader(ctx, key, r, size, digest)
}
func TestImportPartialUploadAndConcurrentRecovery(t *testing.T) {
	p, _, _, _ := prepared(t)
	s, id, base := target(t)
	ctx := context.Background()
	o := &failObjects{MemoryStore: base, remaining: 3}
	cap := testsupport.SnapshotCapacity(t, p, id, "target", "operator")
	r, err := snapshotimport.Run(ctx, p, s, o, id, "target", "operator", testsupport.SnapshotTargetBinding, true, cap)
	if err == nil || r.Status != "partial" {
		t.Fatalf("failure=%v %v", r, err)
	}
	items, _ := s.ListMavenArtifacts(ctx, id)
	if len(items) != 0 {
		t.Fatal("partial history became visible")
	}
	checkpoint, _ := s.GetMavenSnapshotImport(ctx, id, p.Plans[0].Coordinate)
	if checkpoint.State != "staged" {
		t.Fatal("missing checkpoint")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := snapshotimport.Run(ctx, p, s, base, id, "target", "operator", testsupport.SnapshotTargetBinding, true, cap)
			if err == nil && r.Status != "verified" {
				err = errors.New("not verified")
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	items, _ = s.ListMavenArtifacts(ctx, id)
	if len(items) != 3 {
		t.Fatal("concurrent duplicates")
	}
}
func TestCLIReadOnlyAndStrictSafeErrors(t *testing.T) {
	_, dir, _, _ := prepared(t)
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	digest := fixtureDigest(data)
	for _, args := range [][]string{nil, {"bad"}, {"verify", "--bundle", dir, "--manifest-sha256", digest, "--spec", "forbidden"}, {"apply", "--bundle", dir, "--manifest-sha256", digest}, {"verify", "--unknown", "synthetic-secret-marker"}} {
		var out, stderr bytes.Buffer
		if snapshotimport.RunCLI(context.Background(), args, &out, &stderr) != 2 {
			t.Fatal("bad CLI args accepted")
		}
		if strings.Contains(stderr.String(), "synthetic-secret-marker") {
			t.Fatal("echoed unknown input")
		}
	}
	var out, stderr bytes.Buffer
	if code := snapshotimport.RunCLI(context.Background(), []string{"verify", "--bundle", dir, "--manifest-sha256", digest}, &out, &stderr); code != 0 {
		t.Fatalf("verify=%d %s", code, out.String())
	}
	var report snapshotimport.Report
	if err = json.Unmarshal(out.Bytes(), &report); err != nil || report.Status != "source-verified" {
		t.Fatalf("report=%s %v", out.String(), err)
	}
	if code := snapshotimport.RunCLI(context.Background(), []string{"verify", "--help"}, io.Discard, io.Discard); code != 0 {
		t.Fatal("help failed")
	}
}

func fixtureDigest(b []byte) string {
	v := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(v[:])
}
