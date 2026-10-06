package app

import (
	"bytes"
	"context"
	"github.com/artifact-gateway/artifact-gateway/internal/snapshotimport"
	testsupport "github.com/artifact-gateway/artifact-gateway/internal/testsupport/snapshotfixture"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestMavenSnapshotImportAbsentMetadataAndQuarantineReads(t *testing.T) {
	for _, absent := range []bool{false, true} {
		t.Run(map[bool]string{false: "quarantine", true: "explicit-absence"}[absent], func(t *testing.T) {
			ctx := context.Background()
			dir, digest, m, _ := testsupport.SnapshotBundle(t)
			if absent {
				m.Coordinates[0].Metadata = nil
				digest = testsupport.WriteSnapshotManifest(t, dir, m)
			}
			p, rejected, err := snapshotimport.Prepare(ctx, dir, digest)
			if err != nil {
				t.Fatalf("%v %v", rejected, err)
			}
			defer func() { _ = p.Close() }()
			s := repository.NewMemoryStore()
			repo, err := s.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "snapshot-read-" + uuid.NewString(), Format: repository.FormatMaven})
			if err != nil {
				t.Fatal(err)
			}
			objects := NewMemoryOCIObjectStore()
			if _, err = snapshotimport.Run(ctx, p, s, objects, repo.ID, "synthetic-target", "synthetic-operator", testsupport.SnapshotTargetBinding, true, testsupport.SnapshotCapacity(t, p, repo.ID, "synthetic-target", "synthetic-operator")); err != nil {
				t.Fatal(err)
			}
			group := "snapshot-group-" + uuid.NewString()
			createV2Group(t, s, group, repository.FormatMaven, repository.GroupMember{RepositoryID: repo.ID})
			if !absent {
				enableQuarantineReadPolicy(t, s, repo.ID)
				items, _ := s.ListMavenArtifacts(ctx, repo.ID)
				quarantineReadIdentity(t, s, repo, items[0].Coordinate, items[0].Digest)
			}
			h := NewGatewayHandler(Dependencies{NativeMavenObjectStore: objects}, s, TestAdapter{}, testAuthenticator())
			for _, scope := range []string{repo.Name, group} {
				for _, method := range []string{http.MethodGet, http.MethodHead} {
					for _, file := range []string{"widget-1.0-20260101.000000-7.jar", "widget-1.0-SNAPSHOT.jar", "maven-metadata.xml"} {
						r := httptest.NewRequest(method, "/maven/"+scope+"/org/example/widget/1.0-SNAPSHOT/"+file, nil)
						authorize(r, "resolver-secret")
						w := httptest.NewRecorder()
						h.ServeHTTP(w, r)
						want := http.StatusForbidden
						if file == "maven-metadata.xml" {
							want = 404
						}
						if absent {
							want = 404
							if strings.Contains(file, "20260101") {
								want = 200
							}
						}
						if w.Code != want {
							t.Fatalf("%s %s/%s=%d want %d body=%q", method, scope, file, w.Code, want, w.Body.String())
						}
					}
				}
			}
		})
	}
}

func TestMavenSnapshotImportReservationRejectsOrdinarySameActorWrites(t *testing.T) {
	ctx := context.Background()
	dir, digest, _, _ := testsupport.SnapshotBundle(t)
	p, _, err := snapshotimport.Prepare(ctx, dir, digest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	s := repository.NewMemoryStore()
	repo, err := s.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "snapshot-staged", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	plans, err := p.References(repo.ID, "synthetic-target", "maven", testsupport.SnapshotTargetBinding)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := s.BeginMavenSnapshotImport(ctx, plans[0])
	if err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	h := newNativeMavenHandler(s, objects, Authenticator{AdminToken: "admin-secret", ResolverToken: "resolver-secret", RepositoryReaders: map[string][]string{"maven": {repo.Name}}, RepositoryWriters: map[string][]string{"maven": {repo.Name}}})
	for _, path := range []string{"org/example/widget/1.0-SNAPSHOT/widget-1.0-SNAPSHOT.pom", "org/example/widget/1.0-SNAPSHOT/maven-metadata.xml", "org/example/widget/maven-metadata.xml"} {
		r := httptest.NewRequest(http.MethodPut, "/repository/maven/"+repo.Name+"/"+path, bytes.NewBufferString("synthetic upload"))
		r.SetBasicAuth("maven", "resolver-secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if !strings.HasSuffix(path, "widget/maven-metadata.xml") && w.Code != 409 {
			t.Fatalf("PUT %s=%d", path, w.Code)
		}
		session, err := s.GetMavenPublishSession(ctx, checkpoint.SessionID)
		if err != nil || session.State != "open" {
			t.Fatal("ordinary upload completed imported session")
		}
	}
	for _, request := range []struct{ method, path string }{{http.MethodPut, "/api/v2/publish-sessions/" + checkpoint.SessionID + "/objects/widget-1.0-20260101.000000-7.pom"}, {http.MethodPost, "/api/v2/publish-sessions/" + checkpoint.SessionID + ":commit"}} {
		r := httptest.NewRequest(request.method, request.path, bytes.NewBufferString("synthetic"))
		r.Header.Set("Authorization", "Bearer admin-secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 409 || !strings.Contains(w.Body.String(), "snapshot_import_reserved") {
			t.Fatalf("native session route=%d %s", w.Code, w.Body.String())
		}
	}
	if report, err := snapshotimport.Run(ctx, p, s, objects, repo.ID, "synthetic-target", "maven", testsupport.SnapshotTargetBinding, true, testsupport.SnapshotCapacity(t, p, repo.ID, "synthetic-target", "maven")); err != nil || report.Status != "verified" {
		t.Fatalf("%v %v", report, err)
	}
}

func TestMavenSnapshotImportPreservesSourceHistory(t *testing.T) {
	ctx := context.Background()
	dir, digest, _, files := testsupport.SnapshotBundle(t)
	prepared, rejected, err := snapshotimport.Prepare(ctx, dir, digest)
	if err != nil {
		t.Fatalf("prepare=%v rejected=%v", err, rejected)
	}
	defer func() { _ = prepared.Close() }()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "synthetic-history", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	auth := Authenticator{ResolverToken: "resolver-secret", RepositoryReaders: map[string][]string{"maven": {"synthetic-history"}}, RepositoryWriters: map[string][]string{"maven": {"synthetic-history"}}}
	report, err := snapshotimport.Run(ctx, prepared, store, objects, repo.ID, "synthetic-target", "synthetic-operator", testsupport.SnapshotTargetBinding, true, testsupport.SnapshotCapacity(t, prepared, repo.ID, "synthetic-target", "synthetic-operator"))
	if err != nil || report.Status != "verified" {
		t.Fatalf("import=%v %v", report, err)
	}
	h := newNativeMavenHandler(store, objects, auth)
	read := func(path string) (int, string) {
		r := httptest.NewRequest(http.MethodGet, "/repository/maven/synthetic-history/"+path, nil)
		r.SetBasicAuth("maven", "resolver-secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	for path, body := range files {
		if code, got := read(path); code != http.StatusOK || got != string(body) {
			t.Fatalf("historical GET %s=%d %q; source timestamp/build must survive", path, code, got)
		}
	}
	for canonical, want := range map[string]string{"widget-1.0-SNAPSHOT.jar": "synthetic jar 0", "widget-1.0-SNAPSHOT-sources.jar": "synthetic sources 1", "widget-1.0-SNAPSHOT-tests.zip": "synthetic zip 2"} {
		if code, got := read("org/example/widget/1.0-SNAPSHOT/" + canonical); code != 200 || got != want {
			t.Fatalf("current %s=%d %q", canonical, code, got)
		}
	}
	artifacts, err := store.ListMavenArtifacts(ctx, repo.ID)
	if err != nil || len(artifacts) != 3 {
		t.Fatalf("histories=%v %v", artifacts, err)
	}
	for _, a := range artifacts {
		if a.SourceTimestamp == "20260101.000000" {
			if _, err := store.TombstoneMavenArtifact(ctx, repo.ID, a.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if code, _ := read("org/example/widget/1.0-SNAPSHOT/maven-metadata.xml"); code != 404 {
		t.Fatalf("deleted current metadata=%d; must not select another history", code)
	}
	if code, got := read("org/example/widget/1.0-SNAPSHOT/widget-1.0-20260102.000000-7.jar"); code != 200 || got != "synthetic jar 1" {
		t.Fatalf("sibling history=%d %q", code, got)
	}
}
