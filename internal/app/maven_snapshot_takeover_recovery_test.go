package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/snapshotimport"
	testsupport "github.com/artifact-gateway/artifact-gateway/internal/testsupport/snapshotfixture"
	"github.com/google/uuid"
)

type takeoverFixture struct {
	store   *repository.MemoryStore
	objects *MemoryOCIObjectStore
	repo    repository.HostedRepository
	p       *snapshotimport.Prepared
	plan    repository.MavenSnapshotImportPlan
	h       nativeMavenHandler
	files   map[string][]byte
}

func newTakeoverFixture(t *testing.T, bundles ...func(testing.TB) (string, string, snapshotimport.Manifest, map[string][]byte)) takeoverFixture {
	t.Helper()
	ctx := context.Background()
	bundle := testsupport.SnapshotBundle
	if len(bundles) == 1 {
		bundle = bundles[0]
	}
	dir, digest, _, files := bundle(t)
	p, _, err := snapshotimport.Prepare(ctx, dir, digest)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	s := repository.NewMemoryStore()
	repo, err := s.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "takeover-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	o := NewMemoryOCIObjectStore()
	if _, err = snapshotimport.Run(ctx, p, s, o, repo.ID, "target", "operator", testsupport.SnapshotTargetBinding, true, testsupport.SnapshotCapacity(t, p, repo.ID, "target", "operator")); err != nil {
		t.Fatal(err)
	}
	plans, err := p.References(repo.ID, "target", "operator", testsupport.SnapshotTargetBinding)
	if err != nil {
		t.Fatal(err)
	}
	h := newNativeMavenHandler(s, o, Authenticator{AdminToken: "admin-secret", ResolverToken: "resolver-secret", RepositoryReaders: map[string][]string{"maven": {repo.Name}}, RepositoryWriters: map[string][]string{"maven": {repo.Name}}})
	return takeoverFixture{s, o, repo, p, plans[0], h, files}
}
func (f takeoverFixture) request(method, name, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/repository/maven/"+f.repo.Name+"/org/example/widget/1.0-SNAPSHOT/"+name, strings.NewReader(body))
	r.SetBasicAuth("maven", "resolver-secret")
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w
}
func (f takeoverFixture) takeOver(t *testing.T) {
	t.Helper()
	r, err := snapshotimport.RunTakeover(context.Background(), f.p, f.store, f.objects, f.repo.ID, "target", "operator", testsupport.SnapshotTargetBinding, "reviewed-key", false)
	if err != nil || r.Counts.Writable != 1 {
		t.Fatalf("takeover=%+v %v", r, err)
	}
}
func (f takeoverFixture) putBuild(t *testing.T, stamp string, number int, pom bool) {
	t.Helper()
	name := fmt.Sprintf("widget-1.0-%s-%d", stamp, number)
	if w := f.request(http.MethodPut, name+".jar", "jar "+stamp); w.Code != 201 {
		t.Fatalf("jar=%d %s", w.Code, w.Body.String())
	}
	if pom {
		if w := f.request(http.MethodPut, name+".pom", `<project><modelVersion>4.0.0</modelVersion><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.0-SNAPSHOT</version></project>`); w.Code != 201 {
			t.Fatalf("pom=%d %s", w.Code, w.Body.String())
		}
	}
}

func TestSnapshotTakeoverOperatorPreflightAndSealedReplay(t *testing.T) {
	ctx := context.Background()
	f := newTakeoverFixture(t)
	r, err := snapshotimport.RunTakeover(ctx, f.p, f.store, f.objects, f.repo.ID, "target", "operator", testsupport.SnapshotTargetBinding, "reviewed-key", true)
	if err != nil || r.Counts.TakeoverReady != 1 {
		t.Fatalf("dry=%+v %v", r, err)
	}
	v, _ := f.store.GetMavenSnapshotImport(ctx, f.repo.ID, f.plan.Coordinate)
	if v.Writable() {
		t.Fatal("dry-run granted publication")
	}
	f.takeOver(t)
	f.takeOver(t)
	audits, err := f.store.ListAudits(ctx, repository.AuditQuery{Repository: f.repo.Name, Operation: "maven.snapshot.takeover", Limit: 100})
	if err != nil || len(audits) != 1 {
		t.Fatalf("audit=%v %v", audits, err)
	}
	if _, err = f.store.TakeoverMavenSnapshotImport(ctx, f.plan, "another-key"); !errors.Is(err, repository.ErrIdempotencyConflict) {
		t.Fatalf("key conflict=%v", err)
	}
	for _, apply := range []bool{false, true} {
		r, err = snapshotimport.Run(ctx, f.p, f.store, f.objects, f.repo.ID, "target", "operator", testsupport.SnapshotTargetBinding, apply, testsupport.SnapshotCapacity(t, f.p, f.repo.ID, "target", "operator"))
		if err == nil || r.Entries[0].Reason != "snapshot_import_taken_over" {
			t.Fatalf("sealed import=%+v %v", r, err)
		}
	}
	if _, err = f.store.BeginMavenSnapshotImport(ctx, f.plan); !errors.Is(err, repository.ErrMavenSnapshotTakenOver) {
		t.Fatalf("sealed begin=%v", err)
	}
	if _, err = f.store.CommitMavenSnapshotImport(ctx, f.plan); !errors.Is(err, repository.ErrMavenSnapshotTakenOver) {
		t.Fatalf("sealed commit=%v", err)
	}
	policy, _ := f.store.GetRepositoryRetentionPolicy(ctx, f.repo.ID)
	enabled := policy
	enabled.Enabled = true
	if _, err = f.store.ReplaceRepositoryRetentionPolicy(ctx, f.repo.ID, enabled, policy.Version); !errors.Is(err, repository.ErrMavenSnapshotImportRetention) {
		t.Fatalf("retention enabled=%v", err)
	}
	items, _ := f.store.ListMavenArtifacts(ctx, f.repo.ID)
	if _, err = f.store.TombstoneMavenArtifactForRetention(ctx, f.repo.ID, items[0].ID, policy.Version); err == nil {
		t.Fatal("stale retention deleted history")
	}
	if _, err = f.store.CreateMavenPublishSession(ctx, repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: f.repo.ID, Coordinate: f.plan.Coordinate}); !errors.Is(err, repository.ErrNameExists) {
		t.Fatal("native session bypassed reservation")
	}
}

func TestSnapshotTakeoverRejectsCorruptTargetWithoutGrant(t *testing.T) {
	f := newTakeoverFixture(t)
	a := f.plan.Assets[0]
	if err := f.objects.Put(context.Background(), a.ObjectKey, []byte("corrupt")); err != nil {
		t.Fatal(err)
	}
	for _, dry := range []bool{true, false} {
		r, err := snapshotimport.RunTakeover(context.Background(), f.p, f.store, f.objects, f.repo.ID, "target", "operator", testsupport.SnapshotTargetBinding, "reviewed-key", dry)
		if err == nil || r.Status != "rejected" {
			t.Fatalf("corrupt takeover=%+v %v", r, err)
		}
	}
	v, _ := f.store.GetMavenSnapshotImport(context.Background(), f.repo.ID, f.plan.Coordinate)
	if v.Writable() {
		t.Fatal("corrupt target became writable")
	}
}

func TestSnapshotDeploymentRetryAndDelayedMetadataCannotRegressCurrent(t *testing.T) {
	f := newTakeoverFixture(t)
	f.takeOver(t)
	first, second := "20261006.071100", "20261006.071200"
	f.putBuild(t, first, 8, false)
	if w := f.request(http.MethodPut, "maven-metadata.xml", clientSnapshotMetadata(first, 8)); w.Code != 409 {
		t.Fatalf("partial receipt=%d", w.Code)
	}
	f.putBuild(t, second, 8, true)
	if w := f.request(http.MethodPut, "maven-metadata.xml", clientSnapshotMetadata(first, 8)); w.Code != 409 {
		t.Fatal("old receipt completed new deployment")
	}
	if w := f.request(http.MethodPut, "maven-metadata.xml", clientSnapshotMetadata(second, 8)); w.Code != 201 {
		t.Fatalf("new ready=%d %s", w.Code, w.Body.String())
	}
	current := f.request(http.MethodGet, "maven-metadata.xml", "").Body.Bytes()
	firstPom := fmt.Sprintf("widget-1.0-%s-8.pom", first)
	if w := f.request(http.MethodPut, firstPom, `<project><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.0-SNAPSHOT</version></project>`); w.Code != 201 {
		t.Fatalf("recover old=%d", w.Code)
	}
	for i := 0; i < 2; i++ {
		if w := f.request(http.MethodPut, "maven-metadata.xml", clientSnapshotMetadata(first, 8)); w.Code != 201 {
			t.Fatalf("late completion=%d", w.Code)
		}
	}
	if w := f.request(http.MethodGet, "maven-metadata.xml", ""); !bytes.Equal(w.Body.Bytes(), current) {
		t.Fatal("older completion regressed current")
	}
	if w := f.request(http.MethodGet, "widget-1.0-SNAPSHOT.jar", ""); w.Body.String() != "jar "+second {
		t.Fatal("current mixed deployments")
	}
}

func TestSnapshotDeploymentParallelReceiptsAndPermissionRefusal(t *testing.T) {
	f := newTakeoverFixture(t)
	f.takeOver(t)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			stamp := fmt.Sprintf("20261006.07200%d", i)
			f.putBuild(t, stamp, 9, true)
			if w := f.request(http.MethodPut, "maven-metadata.xml", clientSnapshotMetadata(stamp, 9)); w.Code != 201 {
				t.Errorf("complete %s=%d %s", stamp, w.Code, w.Body.String())
			}
		}(i)
	}
	wg.Wait()
	v, _ := f.store.GetMavenSnapshotImport(context.Background(), f.repo.ID, f.plan.Coordinate)
	if v.CurrentBuildNumber != 12 {
		t.Fatalf("concurrent allocation/current=%d", v.CurrentBuildNumber)
	}
	for path, body := range f.files {
		if strings.HasSuffix(path, "maven-metadata.xml") {
			continue
		}
		name := path[strings.LastIndexByte(path, '/')+1:]
		w := f.request(http.MethodGet, name, "")
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), body) {
			t.Fatalf("history=%s %d", name, w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodPut, "/repository/maven/"+f.repo.Name+"/org/example/widget/1.0-SNAPSHOT/widget-1.0-20261006.080000-13.jar", strings.NewReader("unauthorized"))
	r.SetBasicAuth("maven", "wrong-secret")
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("unauthorized=%d", w.Code)
	}
}

func TestSnapshotDeletedDeploymentCannotReplaceCurrentWithSharedObjects(t *testing.T) {
	f := newTakeoverFixture(t)
	f.takeOver(t)
	// Both deployments share their POM object, retaining a live global CAS ref.
	f.putBuild(t, "20261006.075000", 9, true)
	if w := f.request(http.MethodPut, "maven-metadata.xml", clientSnapshotMetadata("20261006.075000", 9)); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	current := f.request(http.MethodGet, "maven-metadata.xml", "").Body.Bytes()
	for _, asset := range []struct{ suffix, body string }{{".jar", "jar 20261006.075000"}, {".pom", `<project><modelVersion>4.0.0</modelVersion><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.0-SNAPSHOT</version></project>`}} {
		if w := f.request(http.MethodPut, "widget-1.0-20261006.075100-10"+asset.suffix, asset.body); w.Code != 201 {
			t.Fatalf("shared object upload=%d", w.Code)
		}
	}
	session, err := f.store.FindMavenSnapshotDeployment(context.Background(), f.repo.ID, f.plan.Coordinate, "maven", "20261006.075100", 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.TombstoneMavenArtifact(context.Background(), f.repo.ID, session.ID); err != nil {
		t.Fatal(err)
	}
	if w := f.request(http.MethodPut, "maven-metadata.xml", clientSnapshotMetadata("20261006.075100", 10)); w.Code != 409 {
		t.Fatalf("deleted completion=%d", w.Code)
	}
	if w := f.request(http.MethodGet, "maven-metadata.xml", ""); !bytes.Equal(current, w.Body.Bytes()) {
		t.Fatal("deleted deployment replaced current")
	}
}

func TestSnapshotTakeoverPreservesMultipartExtensionsAndRejectsInvalidPairs(t *testing.T) {
	f := newTakeoverFixture(t, testsupport.SnapshotSignedBundle)
	f.takeOver(t)
	for _, suffix := range []string{"-bad&name.jar", "evil", "-sources.bad&ext"} {
		if w := f.request(http.MethodPut, "widget-1.0-20261006.080000-9"+suffix, "bad pair"); w.Code != 400 && w.Code != 409 {
			t.Fatalf("invalid pair %s=%d", suffix, w.Code)
		}
	}
	f.putBuild(t, "20261006.080000", 9, true)
	if w := f.request(http.MethodPut, "maven-metadata.xml", clientSnapshotMetadata("20261006.080000", 9)); w.Code != 201 {
		t.Fatalf("complete=%d %s", w.Code, w.Body.String())
	}
	w := f.request(http.MethodGet, "maven-metadata.xml", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<extension>jar.asc</extension><classifier>sources</classifier>") || !strings.Contains(w.Body.String(), "<extension>tar.gz</extension>") {
		t.Fatalf("multipart metadata=%d %s", w.Code, w.Body.String())
	}
	for _, pair := range []struct{ canonical, target string }{{"widget-1.0-SNAPSHOT.jar.asc", "widget-1.0-20260101.000000-7.jar.asc"}, {"widget-1.0-SNAPSHOT-sources.jar.asc", "widget-1.0-20260102.000000-7-sources.jar.asc"}, {"widget-1.0-SNAPSHOT.tar.gz", "widget-1.0-20260101.000000-7.tar.gz"}} {
		body := f.files["org/example/widget/1.0-SNAPSHOT/"+pair.target]
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			w := f.request(method, pair.canonical, "")
			if w.Code != 200 || method == http.MethodGet && !bytes.Equal(w.Body.Bytes(), body) {
				t.Fatalf("pair=%s %s %d", method, pair.canonical, w.Code)
			}
		}
	}
	for _, suffix := range []string{".md5", ".sha1", ".sha256", ".sha512"} {
		if w := f.request(http.MethodGet, "widget-1.0-SNAPSHOT.jar"+suffix, ""); w.Code != 200 {
			t.Fatalf("new checksum %s=%d", suffix, w.Code)
		}
	}
}

type plannedTakeoverRetentionStore struct {
	*repository.MemoryStore
	onPlan func()
	once   sync.Once
}

func (s *plannedTakeoverRetentionStore) ListMavenArtifacts(ctx context.Context, repo string) ([]repository.MavenArtifact, error) {
	items, err := s.MemoryStore.ListMavenArtifacts(ctx, repo)
	if err == nil {
		s.once.Do(s.onPlan)
	}
	return items, err
}

func TestSnapshotTakeoverStopsAlreadyPlannedRetentionWorkers(t *testing.T) {
	for _, worker := range []string{"maven", "repository"} {
		t.Run(worker, func(t *testing.T) {
			f := newTakeoverFixture(t)
			ctx := context.Background()
			policy, err := f.store.ReplaceRepositoryRetentionPolicy(ctx, f.repo.ID, repository.RepositoryRetentionPolicy{Enabled: true, KeepDays: 1, SnapshotKeepDays: 1, MinimumVersions: 1, MaximumVersions: 1}, "1")
			if err != nil {
				t.Fatal(err)
			}
			planned := false
			wrapped := &plannedTakeoverRetentionStore{MemoryStore: f.store, onPlan: func() {
				planned = true
				disabled := policy
				disabled.Enabled = false
				if _, err := f.store.ReplaceRepositoryRetentionPolicy(ctx, f.repo.ID, disabled, policy.Version); err != nil {
					t.Fatal(err)
				}
				f.takeOver(t)
			}}
			if worker == "maven" {
				_ = (NativeMavenRetention{Store: wrapped, Now: func() time.Time { return time.Now().Add(48 * time.Hour) }}).Collect(ctx)
			} else {
				_ = (NativeRepositoryRetention{Store: wrapped, Now: func() time.Time { return time.Now().Add(48 * time.Hour) }}).Collect(ctx)
			}
			if !planned {
				t.Fatal("worker never reached its retained old plan")
			}
			items, err := f.store.ListMavenArtifacts(ctx, f.repo.ID)
			if err != nil || len(items) != 3 {
				t.Fatalf("planned retention deleted history: %v %v", items, err)
			}
			if w := f.request(http.MethodGet, "maven-metadata.xml", ""); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), f.files["org/example/widget/1.0-SNAPSHOT/maven-metadata.xml"]) {
				t.Fatal("planned retention changed source resolution")
			}
		})
	}
}

func TestSnapshotTakeoverRejectsQuarantineAndSequenceExhaustion(t *testing.T) {
	t.Run("quarantine", func(t *testing.T) {
		f := newTakeoverFixture(t)
		ctx := context.Background()
		if _, err := f.store.ReplaceArtifactQuarantine(ctx, repository.ArtifactQuarantine{RepositoryID: f.repo.ID, Format: repository.FormatMaven, Coordinate: f.plan.Coordinate, Digest: f.plan.Artifacts[0].Digest, State: repository.ArtifactQuarantineStateQuarantined, Reason: "synthetic review", UpdatedBy: "operator"}, "0"); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.ReplaceRepositoryQuarantineReadPolicy(ctx, f.repo.ID, repository.RepositoryQuarantineReadPolicy{Enabled: true}, "1"); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.TakeoverMavenSnapshotImport(ctx, f.plan, "reviewed-key"); !errors.Is(err, repository.ErrArtifactQuarantined) {
			t.Fatalf("quarantine takeover=%v", err)
		}
	})
	t.Run("exhaustion", func(t *testing.T) {
		f := newTakeoverFixture(t, func(t testing.TB) (string, string, snapshotimport.Manifest, map[string][]byte) {
			return testsupport.SnapshotBundleWithBuilds(t, []testsupport.BuildIdentity{{Timestamp: "20260101.000000", Number: repository.MaxMavenSnapshotBuildNumber - 1}, {Timestamp: "20260102.000000", Number: repository.MaxMavenSnapshotBuildNumber - 1}, {Timestamp: "20260103.000000", Number: repository.MaxMavenSnapshotBuildNumber}})
		})
		if _, err := f.store.TakeoverMavenSnapshotImport(context.Background(), f.plan, "reviewed-key"); !errors.Is(err, repository.ErrMavenSnapshotBuildExhausted) {
			t.Fatalf("exhaustion=%v", err)
		}
	})
}

func TestSnapshotTakeoverWithAbsentMetadataAndCorruptNewDeployment(t *testing.T) {
	f := newTakeoverFixture(t, func(t testing.TB) (string, string, snapshotimport.Manifest, map[string][]byte) {
		dir, _, m, files := testsupport.SnapshotBundle(t)
		m.Coordinates[0].Metadata = nil
		return dir, testsupport.WriteSnapshotManifest(t, dir, m), m, files
	})
	f.takeOver(t)
	if w := f.request(http.MethodGet, "maven-metadata.xml", ""); w.Code != 404 {
		t.Fatal("takeover synthesized absent source metadata")
	}
	f.putBuild(t, "20261006.081000", 1, true)
	session, err := f.store.FindMavenSnapshotDeployment(context.Background(), f.repo.ID, f.plan.Coordinate, "maven", "20261006.081000", 1)
	if err != nil {
		t.Fatal(err)
	}
	var key string
	for _, object := range session.Objects {
		if strings.HasSuffix(object.Name, ".jar") {
			key = "native/maven/sha256/" + strings.TrimPrefix(object.Digest, "sha256:")
		}
	}
	if err = f.objects.Put(context.Background(), key, []byte("changed new bytes")); err != nil {
		t.Fatal(err)
	}
	if w := f.request(http.MethodPut, "maven-metadata.xml", clientSnapshotMetadata("20261006.081000", 1)); w.Code != 409 {
		t.Fatalf("corrupt ready completion=%d", w.Code)
	}
	if w := f.request(http.MethodGet, "maven-metadata.xml", ""); w.Code != 404 {
		t.Fatal("corrupt deployment changed absent current")
	}
	if err = f.objects.Put(context.Background(), key, []byte("jar 20261006.081000")); err != nil {
		t.Fatal(err)
	}
	if w := f.request(http.MethodPut, "maven-metadata.xml", clientSnapshotMetadata("20261006.081000", 1)); w.Code != 201 {
		t.Fatalf("corrected same bytes retry=%d %s", w.Code, w.Body.String())
	}
	if w := f.request(http.MethodGet, "maven-metadata.xml", ""); w.Code != 200 {
		t.Fatal("complete deployment did not establish first live selector")
	}
}

type concurrentSnapshotCompletionObjects struct {
	OCIObjectStore
	key    string
	once   sync.Once
	mutate func()
}

func (o *concurrentSnapshotCompletionObjects) Open(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	if key == o.key {
		o.once.Do(o.mutate)
	}
	return o.OCIObjectStore.Open(ctx, key)
}

func TestSnapshotConcurrentAppendFencesValidatedCompletion(t *testing.T) {
	f := newTakeoverFixture(t)
	f.takeOver(t)
	stamp := "20261006.082000"
	f.putBuild(t, stamp, 9, true)
	s, err := f.store.FindMavenSnapshotDeployment(context.Background(), f.repo.ID, f.plan.Coordinate, "maven", stamp, 9)
	if err != nil {
		t.Fatal(err)
	}
	var key string
	for _, o := range s.Objects {
		if strings.HasSuffix(o.Name, ".jar") {
			key = "native/maven/sha256/" + strings.TrimPrefix(o.Digest, "sha256:")
		}
	}
	h := f.h
	h.objects = &concurrentSnapshotCompletionObjects{OCIObjectStore: f.objects, key: key, mutate: func() {
		if w := f.request(http.MethodPut, "widget-1.0-"+stamp+"-9-tests.zip", "new tests"); w.Code != 201 {
			t.Fatalf("concurrent append=%d", w.Code)
		}
	}}
	r := httptest.NewRequest(http.MethodPut, "/repository/maven/"+f.repo.Name+"/org/example/widget/1.0-SNAPSHOT/maven-metadata.xml", strings.NewReader(clientSnapshotMetadata(stamp, 9)))
	r.SetBasicAuth("maven", "resolver-secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 409 {
		t.Fatalf("stale validation completed changed facts=%d", w.Code)
	}
	if w := f.request(http.MethodGet, "maven-metadata.xml", ""); !bytes.Equal(w.Body.Bytes(), f.files["org/example/widget/1.0-SNAPSHOT/maven-metadata.xml"]) {
		t.Fatal("stale fingerprint changed current")
	}
	body := strings.Replace(clientSnapshotMetadata(stamp, 9), "</snapshotVersions>", `<snapshotVersion><extension>zip</extension><classifier>tests</classifier><value>1.0-`+stamp+`-9</value><updated>20261006082000</updated></snapshotVersion></snapshotVersions>`, 1)
	if w := f.request(http.MethodPut, "maven-metadata.xml", body); w.Code != 201 {
		t.Fatalf("fresh complete facts=%d %s", w.Code, w.Body.String())
	}
}
