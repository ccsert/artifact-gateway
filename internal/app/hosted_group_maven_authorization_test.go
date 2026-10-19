package app

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

const groupMavenAuthorizationPOM = `<project><modelVersion>4.0.0</modelVersion><groupId>org.example</groupId><artifactId>demo-parent</artifactId><version>1.0.0</version></project>`

type groupMavenAuthorizationFixture struct {
	store    *repository.MemoryStore
	objects  *groupMavenAuthorizationObjects
	hosted   repository.HostedRepository
	proxy    repository.HostedRepository
	group    repository.HostedGroup
	upstream *proxyUpstream
	handler  http.Handler
}

type groupMavenAuthorizationObjects struct {
	*objectstore.MemoryStore
	reads atomic.Int32
}

func (s *groupMavenAuthorizationObjects) Get(ctx context.Context, key string) ([]byte, error) {
	s.reads.Add(1)
	return s.MemoryStore.Get(ctx, key)
}

func newGroupMavenAuthorizationFixture(t *testing.T) *groupMavenAuthorizationFixture {
	t.Helper()
	f := &groupMavenAuthorizationFixture{store: repository.NewMemoryStore(), objects: &groupMavenAuthorizationObjects{MemoryStore: objectstore.NewMemoryStore()}}
	var err error
	f.hosted, err = f.store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "sample-releases", Name: "sample-releases", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	f.upstream = &proxyUpstream{bodies: map[string][]byte{}, calls: map[string]int{}}
	upstream := httptest.NewServer(f.upstream)
	t.Cleanup(upstream.Close)
	host := strings.TrimPrefix(upstream.URL, "http://")
	f.proxy, err = f.store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "sample-proxy", Name: "sample-proxy", Format: repository.FormatMaven,
		Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL, AllowedHosts: []string{host},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.group = createV2Group(t, f.store, "sample-public", repository.FormatMaven,
		repository.GroupMember{RepositoryID: f.hosted.ID, Position: 0},
		repository.GroupMember{RepositoryID: f.proxy.ID, Position: 1},
	)
	f.grants(t, f.hosted, repository.RepositoryGrant{Principal: "fixture-reader", Scopes: []string{"repositories:read"}, ResourcePrefix: "org.example:"})
	f.grants(t, f.proxy, repository.RepositoryGrant{Principal: "fixture-reader", Scopes: []string{"repositories:read"}})
	cache := NewMavenCache(objectstore.NewMemoryStore(), time.Hour, time.Hour, time.Minute, time.Minute, []string{host})
	auth := Authenticator{AdminToken: "fixture-admin", ResolverToken: "fixture-secret", ResolverActor: "fixture-reader"}
	f.handler = newTestGateway(t, GatewayOptions{Dependencies: Dependencies{}, Store: f.store, Adapter: TestAdapter{}, Authenticator: auth, Storage: ObjectStores{repository.FormatMaven: f.objects}, Caches: GatewayCaches{OCI: NewDefaultOCICache(objectstore.NewMemoryStore(), nil), Maven: cache}, Upstream: UpstreamClient{HTTPClient: upstream.Client(), AllowHTTPForTesting: true}})
	return f
}

func (f *groupMavenAuthorizationFixture) grants(t *testing.T, repo repository.HostedRepository, grants ...repository.RepositoryGrant) {
	t.Helper()
	set, err := f.store.GetRepositoryGrants(context.Background(), repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ReplaceRepositoryGrants(context.Background(), repo.ID, grants, set.Version); err != nil {
		t.Fatal(err)
	}
}

func (f *groupMavenAuthorizationFixture) publish(t *testing.T, repo, path, body string) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPut, "/repository/maven/"+repo+"/"+path, strings.NewReader(body))
	authorize(r, "fixture-admin")
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("publish %s: status=%d body=%q", path, w.Code, w.Body.String())
	}
}

func (f *groupMavenAuthorizationFixture) request(method, repo, path, actor string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/maven/"+repo+"/"+path, nil)
	r.SetBasicAuth(actor, "fixture-secret")
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

func TestV2GroupMavenCoordinateGrantReadsHostedPOM(t *testing.T) {
	f := newGroupMavenAuthorizationFixture(t)
	path := "org/example/demo-parent/1.0.0/demo-parent-1.0.0.pom"
	f.publish(t, f.hosted.Name, path, groupMavenAuthorizationPOM)
	for _, repo := range []string{f.hosted.Name, f.group.Name} {
		w := f.request(http.MethodGet, repo, path, "fixture-reader")
		if w.Code != http.StatusOK || w.Body.String() != groupMavenAuthorizationPOM {
			t.Fatalf("%s: status=%d body=%q, want complete hosted POM", repo, w.Code, w.Body.String())
		}
	}
	if calls := f.upstream.callCount("/" + path); calls != 0 {
		t.Fatalf("hosted hit reached proxy: calls=%d", calls)
	}
}

func (f *groupMavenAuthorizationFixture) expectRead(t *testing.T, path, body string) {
	t.Helper()
	before := f.upstream.callCount("/" + path)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, repo := range []string{f.hosted.Name, f.group.Name} {
			w := f.request(method, repo, path, "fixture-reader")
			wantBody := body
			if method == http.MethodHead {
				wantBody = ""
			}
			if w.Code != http.StatusOK || w.Body.String() != wantBody || w.Header().Get("Content-Length") != strconv.Itoa(len(body)) {
				t.Fatalf("%s %s/%s: status=%d length=%s body=%q", method, repo, path, w.Code, w.Header().Get("Content-Length"), w.Body.String())
			}
			if !strings.Contains(path, "maven-metadata.xml") {
				sum := sha256.Sum256([]byte(body))
				if etag := w.Header().Get("ETag"); etag != `"`+hex.EncodeToString(sum[:])+`"` {
					t.Fatalf("%s %s/%s: ETag=%q", method, repo, path, etag)
				}
			}
		}
	}
	if calls := f.upstream.callCount("/" + path); calls != before {
		t.Fatalf("hosted read reached proxy: %s calls=%d, previously %d", path, calls, before)
	}
}

func groupMavenAuthorizationChecksums(body string) map[string]string {
	md5Sum, sha1Sum := md5.Sum([]byte(body)), sha1.Sum([]byte(body))
	sha256Sum, sha512Sum := sha256.Sum256([]byte(body)), sha512.Sum512([]byte(body))
	return map[string]string{
		".md5": hex.EncodeToString(md5Sum[:]) + "\n", ".sha1": hex.EncodeToString(sha1Sum[:]) + "\n",
		".sha256": hex.EncodeToString(sha256Sum[:]) + "\n", ".sha512": hex.EncodeToString(sha512Sum[:]) + "\n",
	}
}

func TestV2GroupMavenCoordinateGrantReadVariants(t *testing.T) {
	f := newGroupMavenAuthorizationFixture(t)
	base := "org/example/demo-parent/1.0.0/demo-parent-1.0.0"
	for _, asset := range []struct{ suffix, body string }{
		{".pom", groupMavenAuthorizationPOM}, {".jar", "synthetic bytecode"},
		{"-sources.jar", "synthetic sources"}, {"-javadoc.zip", "synthetic documentation"},
	} {
		f.publish(t, f.hosted.Name, base+asset.suffix, asset.body)
		f.expectRead(t, base+asset.suffix, asset.body)
		for suffix, body := range groupMavenAuthorizationChecksums(asset.body) {
			if suffix == ".sha512" {
				// Ordinary publications generate the existing three sidecars.
				for _, repo := range []string{f.hosted.Name, f.group.Name} {
					if w := f.request(http.MethodGet, repo, base+asset.suffix+suffix, "fixture-reader"); w.Code != http.StatusNotFound {
						t.Fatalf("unsupported sidecar %s: status=%d", repo, w.Code)
					}
				}
				continue
			}
			f.expectRead(t, base+asset.suffix+suffix, body)
		}
	}
	metadata := `<metadata><groupId>org.example</groupId><artifactId>demo-parent</artifactId><versioning><latest>1.0.0</latest><release>1.0.0</release><versions><version>1.0.0</version></versions></versioning></metadata>`
	path := "org/example/demo-parent/maven-metadata.xml"
	f.expectRead(t, path, metadata)
	for suffix, body := range groupMavenAuthorizationChecksums(metadata) {
		f.expectRead(t, path+suffix, body)
	}
}

func TestV2GroupMavenCoordinateGrantMetadataIsolation(t *testing.T) {
	f := newGroupMavenAuthorizationFixture(t)
	f.publish(t, f.hosted.Name, "org/example/demo-parent/1.0.0/demo-parent-1.0.0.pom", groupMavenAuthorizationPOM)
	for _, coordinate := range []struct{ group, artifact, version string }{
		{"org.example.demo-parent", "hidden", "2.0.0"},
		{"org.example", "demo-parent-extra", "3.0.0"},
	} {
		path := strings.ReplaceAll(coordinate.group, ".", "/") + "/" + coordinate.artifact + "/" + coordinate.version + "/" + coordinate.artifact + "-" + coordinate.version + ".pom"
		pom := fmt.Sprintf(`<project><groupId>%s</groupId><artifactId>%s</artifactId><version>%s</version></project>`, coordinate.group, coordinate.artifact, coordinate.version)
		f.publish(t, f.hosted.Name, path, pom)
	}
	metadata := `<metadata><groupId>org.example</groupId><artifactId>demo-parent</artifactId><versioning><latest>1.0.0</latest><release>1.0.0</release><versions><version>1.0.0</version></versions></versioning></metadata>`
	f.expectRead(t, "org/example/demo-parent/maven-metadata.xml", metadata)
	for suffix, body := range groupMavenAuthorizationChecksums(metadata) {
		f.expectRead(t, "org/example/demo-parent/maven-metadata.xml"+suffix, body)
	}
}

func TestV2GroupMavenCoordinateGrantMetadataMissFallsBack(t *testing.T) {
	f := newGroupMavenAuthorizationFixture(t)
	f.publish(t, f.hosted.Name, "org/example/demo-parent/hidden/2.0.0/hidden-2.0.0.pom", `<project><groupId>org.example.demo-parent</groupId><artifactId>hidden</artifactId><version>2.0.0</version></project>`)
	path := "org/example/demo-parent/maven-metadata.xml"
	metadata := `<metadata><groupId>org.example</groupId><artifactId>demo-parent</artifactId><versioning><versions><version>1.0.0</version></versions></versioning></metadata>`
	f.upstream.bodies["/"+path] = []byte(metadata)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		if w := f.request(method, f.hosted.Name, path, "fixture-reader"); w.Code != http.StatusNotFound {
			t.Fatalf("unrelated hosted coordinate answered metadata: status=%d body=%q", w.Code, w.Body.String())
		}
		w := f.request(method, f.group.Name, path, "fixture-reader")
		wantBody := metadata
		if method == http.MethodHead {
			wantBody = ""
		}
		if w.Code != http.StatusOK || w.Body.String() != wantBody {
			t.Fatalf("metadata fallback %s: status=%d body=%q", method, w.Code, w.Body.String())
		}
	}
}

func TestV2GroupMavenCoordinateGrantSnapshotMetadata(t *testing.T) {
	f := newGroupMavenAuthorizationFixture(t)
	base := "org/example/demo-parent/1.0-SNAPSHOT/"
	pom := strings.ReplaceAll(groupMavenAuthorizationPOM, "1.0.0", "1.0-SNAPSHOT")
	for _, asset := range []struct{ suffix, body string }{{".pom", pom}, {".jar", "snapshot bytecode"}, {"-sources.jar", "snapshot sources"}} {
		f.publish(t, f.hosted.Name, base+"demo-parent-1.0-SNAPSHOT"+asset.suffix, asset.body)
	}
	f.publish(t, f.hosted.Name, base+"maven-metadata.xml", "<metadata/>")
	// A version-scoped grant can read version metadata, but cannot read the
	// artifact-level version list or a different version.
	grant := repository.RepositoryGrant{Principal: "fixture-reader", Scopes: []string{"repositories:read"}, ResourcePrefix: "org.example:demo-parent:1.0-SNAPSHOT"}
	f.grants(t, f.hosted, grant)
	f.grants(t, f.proxy, grant)
	path := base + "maven-metadata.xml"
	w := f.request(http.MethodGet, f.hosted.Name, path, "fixture-reader")
	for _, fragment := range []string{"<groupId>org.example</groupId>", "<artifactId>demo-parent</artifactId>", "<version>1.0-SNAPSHOT</version>", "<buildNumber>1</buildNumber>", "<extension>pom</extension>", "<extension>jar</extension>", "<classifier>sources</classifier>"} {
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), fragment) {
			t.Fatalf("snapshot metadata: status=%d body=%q, missing %q", w.Code, w.Body.String(), fragment)
		}
	}
	f.expectRead(t, path, w.Body.String())
	for suffix, body := range groupMavenAuthorizationChecksums(w.Body.String()) {
		f.expectRead(t, path+suffix, body)
	}
	var metadata struct {
		Value string `xml:"versioning>snapshotVersions>snapshotVersion>value"`
	}
	if err := xml.Unmarshal(w.Body.Bytes(), &metadata); err != nil || metadata.Value == "" {
		t.Fatalf("snapshot version value=%q err=%v", metadata.Value, err)
	}
	f.expectRead(t, base+"demo-parent-"+metadata.Value+".pom", pom)
	f.expectRead(t, base+"demo-parent-"+metadata.Value+".jar", "snapshot bytecode")
	for _, deniedPath := range []string{"org/example/demo-parent/maven-metadata.xml", "org/example/demo-parent/2.0-SNAPSHOT/maven-metadata.xml"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			for _, repo := range []string{f.hosted.Name, f.group.Name} {
				if got := f.request(method, repo, deniedPath, "fixture-reader"); got.Code != http.StatusForbidden {
					t.Fatalf("%s %s/%s: status=%d, want 403", method, repo, deniedPath, got.Code)
				}
			}
		}
	}
}

func TestV2GroupMavenCoordinateGrantBoundaries(t *testing.T) {
	for _, test := range []struct{ name, groupID, prefix, actor, scope string }{
		{"child-group", "org.example.child", "org.example:", "fixture-reader", "repositories:read"},
		{"adjacent-group", "org.examples", "org.example:", "fixture-reader", "repositories:read"},
		{"other-group", "org.other", "org.example:", "fixture-reader", "repositories:read"},
		{"other-artifact", "org.example", "org.example:other:", "fixture-reader", "repositories:read"},
		{"other-version", "org.example", "org.example:demo-parent:2.0.0", "fixture-reader", "repositories:read"},
		{"other-principal", "org.example", "org.example:", "other-reader", "repositories:read"},
		{"no-read-scope", "org.example", "org.example:", "fixture-reader", "repositories:intelligence"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newGroupMavenAuthorizationFixture(t)
			path := strings.ReplaceAll(test.groupID, ".", "/") + "/demo-parent/1.0.0/demo-parent-1.0.0.pom"
			pom := strings.ReplaceAll(groupMavenAuthorizationPOM, "org.example", test.groupID)
			f.publish(t, f.hosted.Name, path, pom)
			grant := repository.RepositoryGrant{Principal: "fixture-reader", Scopes: []string{test.scope}, ResourcePrefix: test.prefix}
			f.grants(t, f.hosted, grant)
			f.grants(t, f.proxy, grant)
			f.objects.reads.Store(0)
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				for _, repo := range []string{f.hosted.Name, f.group.Name} {
					w := f.request(method, repo, path, test.actor)
					if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), pom) || strings.Contains(fmt.Sprint(w.Header()), f.hosted.Name) || strings.Contains(w.Body.String(), f.hosted.Name) || strings.Contains(w.Body.String(), f.proxy.Name) {
						t.Fatalf("%s %s: status=%d headers=%v body=%q", method, repo, w.Code, w.Header(), w.Body.String())
					}
				}
			}
			if f.objects.reads.Load() != 0 || f.upstream.callCount("/"+path) != 0 {
				t.Fatal("denied member payload was accessed")
			}
			denials := 0
			for _, audit := range f.store.Audits {
				if audit.Actor == test.actor && audit.Outcome == repository.AuditAccessDenied {
					denials++
					if audit.Status != http.StatusForbidden || audit.AuthorizationSource != "repository_grants" || audit.AuthorizationReason != "scope_not_granted" {
						t.Fatalf("authorization audit=%#v", audit)
					}
					if audit.MemberName != "" && audit.Resource != test.groupID+":demo-parent:1.0.0" {
						t.Fatalf("group member authorization resource=%q", audit.Resource)
					}
				}
			}
			if denials != 6 {
				t.Fatalf("authorization denials=%d, want two native and four group member records", denials)
			}
		})
	}
}

func TestV2GroupMavenCoordinateGrantMissingAndUnavailable(t *testing.T) {
	f := newGroupMavenAuthorizationFixture(t)
	path := "org/example/demo-parent/1.0.0/demo-parent-1.0.0.pom"
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, repo := range []string{f.hosted.Name, f.group.Name} {
			if w := f.request(method, repo, path, "fixture-reader"); w.Code != http.StatusNotFound {
				t.Fatalf("missing %s %s: status=%d body=%q", method, repo, w.Code, w.Body.String())
			}
		}
	}
	missingAudits := 0
	for _, audit := range f.store.Audits {
		if audit.GroupName == f.group.Name && audit.Outcome == repository.AuditNotFound {
			missingAudits++
			if audit.AuthorizationSource != "" {
				t.Fatalf("missing payload audit=%#v", audit)
			}
		}
	}
	if missingAudits == 0 {
		t.Fatal("missing group payload did not record not-found")
	}
	// The proxy's negative cache must not hide a subsequently published hosted
	// payload. Metadata existence alone must not make a missing object readable.
	f.publish(t, f.hosted.Name, path, groupMavenAuthorizationPOM)
	f.expectRead(t, path, groupMavenAuthorizationPOM)
	asset, err := f.store.GetMavenAsset(context.Background(), f.hosted.ID, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.objects.Delete(context.Background(), asset.ObjectKey); err != nil {
		t.Fatal(err)
	}
	before := f.upstream.callCount("/" + path)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, repo := range []string{f.hosted.Name, f.group.Name} {
			if w := f.request(method, repo, path, "fixture-reader"); w.Code != http.StatusServiceUnavailable {
				t.Fatalf("unavailable %s %s: status=%d body=%q", method, repo, w.Code, w.Body.String())
			}
		}
	}
	if calls := f.upstream.callCount("/" + path); calls != before {
		t.Fatal("unavailable hosted object fell through to the proxy")
	}
	metadata := `<metadata><groupId>org.example</groupId><artifactId>demo-parent</artifactId><versioning><latest>1.0.0</latest><release>1.0.0</release><versions><version>1.0.0</version></versions></versioning></metadata>`
	f.expectRead(t, "org/example/demo-parent/maven-metadata.xml", metadata)
	for _, audit := range f.store.Audits {
		if audit.Actor == "fixture-reader" && audit.Outcome == repository.AuditAccessDenied {
			t.Fatalf("authorized missing/unavailable payload recorded as denial: %#v", audit)
		}
	}
}

func TestV2GroupMavenCoordinateGrantSkipsDeniedHostedMember(t *testing.T) {
	f := newGroupMavenAuthorizationFixture(t)
	path := "org/example/demo-parent/1.0.0/demo-parent-1.0.0.pom"
	f.publish(t, f.hosted.Name, path, groupMavenAuthorizationPOM)
	f.grants(t, f.hosted, repository.RepositoryGrant{Principal: "other-reader", Scopes: []string{"repositories:read"}, ResourcePrefix: "org.example:"})
	f.grants(t, f.proxy, repository.RepositoryGrant{Principal: "fixture-reader", Scopes: []string{"repositories:read"}, ResourcePrefix: "org.example:"})
	proxyPOM := strings.ReplaceAll(groupMavenAuthorizationPOM, "</project>", "<description>authorized proxy</description></project>")
	metadataPath := "org/example/demo-parent/maven-metadata.xml"
	f.upstream.bodies["/"+path] = []byte(proxyPOM)
	f.upstream.bodies["/"+metadataPath] = []byte("<metadata><versioning><versions><version>2.0.0</version></versions></versioning></metadata>")
	f.objects.reads.Store(0)
	for _, requestedPath := range []string{path, metadataPath} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			if w := f.request(method, f.hosted.Name, requestedPath, "fixture-reader"); w.Code != http.StatusForbidden {
				t.Fatalf("grant crossed repository boundary: status=%d", w.Code)
			}
			w := f.request(method, f.group.Name, requestedPath, "fixture-reader")
			want := string(f.upstream.bodies["/"+requestedPath])
			if method == http.MethodHead {
				want = ""
			}
			if w.Code != http.StatusOK || w.Body.String() != want || strings.Contains(fmt.Sprint(w.Header()), f.hosted.Name) {
				t.Fatalf("authorized fallback: status=%d headers=%v body=%q", w.Code, w.Header(), w.Body.String())
			}
		}
	}
	if f.objects.reads.Load() != 0 {
		t.Fatal("denied hosted payload was accessed")
	}
	if calls := f.upstream.callCount("/" + path); calls != 1 {
		t.Fatalf("authorized proxy payload did not use its cache: calls=%d", calls)
	}
}
