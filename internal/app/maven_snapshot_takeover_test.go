package app

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/snapshotimport"
	testsupport "github.com/artifact-gateway/artifact-gateway/internal/testsupport/snapshotfixture"
	"github.com/google/uuid"
)

// This public-boundary regression compiled before a takeover operation existed.
// It must fail on behavior, rather than merely referencing a missing Go symbol.
type snapshotTakeoverOperator interface {
	TakeoverMavenSnapshotImport(context.Context, repository.MavenSnapshotImportPlan, string) (repository.MavenSnapshotImport, error)
}

func clientSnapshotMetadata(timestamp string, number int) string {
	return fmt.Sprintf(`<metadata><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.0-SNAPSHOT</version><versioning><snapshot><timestamp>%s</timestamp><buildNumber>%d</buildNumber></snapshot><snapshotVersions><snapshotVersion><extension>pom</extension><value>1.0-%s-%d</value><updated>20261006071000</updated></snapshotVersion><snapshotVersion><extension>jar</extension><value>1.0-%s-%d</value><updated>20261006071000</updated></snapshotVersion></snapshotVersions></versioning></metadata>`, timestamp, number, timestamp, number, timestamp, number)
}

func TestMavenSnapshotTakeoverPreservesHistoryAndCurrentUntilReadyPublish(t *testing.T) {
	ctx := context.Background()
	dir, digest, _, files := testsupport.SnapshotBundle(t)
	p, rejected, err := snapshotimport.Prepare(ctx, dir, digest)
	if err != nil {
		t.Fatalf("prepare=%v %v", rejected, err)
	}
	defer func() { _ = p.Close() }()
	s := repository.NewMemoryStore()
	repo, err := s.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "synthetic-takeover", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	if r, err := snapshotimport.Run(ctx, p, s, objects, repo.ID, "target", "operator", testsupport.SnapshotTargetBinding, true, testsupport.SnapshotCapacity(t, p, repo.ID, "target", "operator")); err != nil || r.Status != "verified" {
		t.Fatalf("import=%v %v", r, err)
	}
	h := newNativeMavenHandler(s, objects, Authenticator{AdminToken: "admin-secret", ResolverToken: "resolver-secret", RepositoryReaders: map[string][]string{"maven": {repo.Name}}, RepositoryWriters: map[string][]string{"maven": {repo.Name}}})
	base := "/repository/maven/" + repo.Name + "/org/example/widget/1.0-SNAPSHOT/"
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.SetBasicAuth("maven", "resolver-secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	assertSourceCurrent := func() {
		t.Helper()
		w := request(http.MethodGet, base+"maven-metadata.xml", "")
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), files["org/example/widget/1.0-SNAPSHOT/maven-metadata.xml"]) {
			t.Fatalf("source current changed: %d %q", w.Code, w.Body.String())
		}
	}
	stamp, number := "20261006.071000", 8
	clientPrefix := fmt.Sprintf("widget-1.0-%s-%d", stamp, number)
	if w := request(http.MethodPut, base+clientPrefix+".jar", "new bytecode"); w.Code != 409 {
		t.Fatalf("protected PUT=%d", w.Code)
	}
	assertSourceCurrent()
	operator, ok := any(s).(snapshotTakeoverOperator)
	if !ok {
		t.Fatal("formal explicit takeover is missing; ordinary deployment cannot continue after preserved import")
	}
	plans, err := p.References(repo.ID, "target", "operator", testsupport.SnapshotTargetBinding)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := operator.TakeoverMavenSnapshotImport(ctx, plans[0], "synthetic-takeover-key"); err != nil {
			t.Fatal(err)
		}
	}
	assertSourceCurrent()
	for _, asset := range []struct{ suffix, body string }{{".jar", "new bytecode"}, {".pom", `<project><modelVersion>4.0.0</modelVersion><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.0-SNAPSHOT</version></project>`}} {
		if w := request(http.MethodPut, base+clientPrefix+asset.suffix, asset.body); w.Code != 201 {
			t.Fatalf("ordinary PUT=%d %s", w.Code, w.Body.String())
		}
		assertSourceCurrent()
	}
	if w := request(http.MethodPut, base+"maven-metadata.xml", string(files["org/example/widget/1.0-SNAPSHOT/maven-metadata.xml"])); w.Code != 409 {
		t.Fatalf("unrelated old metadata=%d", w.Code)
	}
	assertSourceCurrent()
	if w := request(http.MethodPut, strings.TrimSuffix(base, "1.0-SNAPSHOT/")+"maven-metadata.xml", "<metadata/>"); w.Code != 201 {
		t.Fatalf("auxiliary GA metadata=%d", w.Code)
	}
	assertSourceCurrent()
	for i := 0; i < 2; i++ {
		if w := request(http.MethodPut, base+"maven-metadata.xml", clientSnapshotMetadata(stamp, number)); w.Code != 201 {
			t.Fatalf("ready completion=%d %s", w.Code, w.Body.String())
		}
	}
	if w := request(http.MethodGet, base+"widget-1.0-SNAPSHOT.jar", ""); w.Code != 200 || w.Body.String() != "new bytecode" {
		t.Fatalf("new current=%d %q", w.Code, w.Body.String())
	}
	if w := request(http.MethodGet, base+"maven-metadata.xml", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "<buildNumber>9</buildNumber>") {
		t.Fatalf("server allocation must exceed every source number: %d %q", w.Code, w.Body.String())
	}
	if w := request(http.MethodGet, base+"widget-1.0-SNAPSHOT-sources.jar", ""); w.Code != 200 || w.Body.String() != "synthetic sources 1" {
		t.Fatalf("unpublished classifier lost source choice: %d %q", w.Code, w.Body.String())
	}
	for path, body := range files {
		if strings.HasSuffix(path, "maven-metadata.xml") {
			continue
		}
		w := request(http.MethodGet, "/repository/maven/"+repo.Name+"/"+path, "")
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), body) {
			t.Fatalf("history changed: %s %d", path, w.Code)
		}
	}
	for _, suffix := range []string{".jar", ".jar.sha256", "-new.jar"} {
		if w := request(http.MethodPut, base+"widget-1.0-20260101.000000-7"+suffix, "overwrite"); w.Code != 409 {
			t.Fatalf("archived namespace accepted %s: %d", suffix, w.Code)
		}
	}
}
