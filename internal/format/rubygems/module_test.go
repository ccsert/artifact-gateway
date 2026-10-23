package rubygems

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	domain "github.com/ccsert/artifact-gateway/internal/rubygemspublication"
	f "github.com/ccsert/artifact-gateway/internal/testsupport/rubygemsfixture"
	"github.com/google/uuid"
)

type fixture struct {
	module  *Module
	store   *domain.Memory
	repos   *repository.MemoryStore
	objects *objectstore.MemoryStore
	repo    repository.HostedRepository
}

func options(repos *repository.MemoryStore, store domain.Store, objects objectstore.Store, audit domain.Auditor) Options {
	auth := authorization.Authenticator{AdminToken: "owned-admin", AdminActor: "operator", ResolverToken: "owned-reader", ResolverActor: "reader", RepositoryReaders: map[string][]string{"reader": {"owned-gems"}}}
	return Options{Store: store, Repositories: repos, Objects: objects, Authenticator: auth, Authorizer: authorization.RepositoryAuthorizer{Grants: repos, Legacy: auth}, Audit: audit, Anonymous: repos}
}
func setup(t *testing.T) fixture {
	t.Helper()
	repos := repository.NewMemoryStore()
	objects := objectstore.NewMemoryStore()
	store := domain.NewMemory(repos, repos)
	repo, e := repos.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "owned-gems", Format: repository.FormatRubyGems, Type: repository.RepositoryTypeHosted})
	if e != nil {
		t.Fatal(e)
	}
	module, e := New(options(repos, store, objects, repos))
	if e != nil {
		t.Fatal(e)
	}
	return fixture{module, store, repos, objects, repo}
}
func request(m http.Handler, method, path string, body []byte, auth string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	m.ServeHTTP(w, r)
	return w
}
func push(fixture fixture, gem []byte) *httptest.ResponseRecorder {
	return request(fixture.module, "POST", "/rubygems/owned-gems/api/v1/gems", gem, "owned-admin")
}
func gem(t *testing.T, name, version, platform, summary string) []byte {
	return f.Build(t, f.YAML(name, version, platform, summary, f.Dependency("leaf", "runtime")), "lib/owned.rb")
}
func TestRubyGemsHostedPublishesImmutableBytesAndMatchingIndexes(t *testing.T) {
	f := setup(t)
	body := gem(t, "owned", "1.2.3.pre", "x86_64-linux", "fixture")
	for i := 0; i < 2; i++ {
		w := push(f, body)
		if w.Code != 201 {
			t.Fatalf("push %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	records, e := f.store.List(t.Context(), f.repo.ID, 100)
	if e != nil || len(records) != 1 {
		t.Fatalf("publications %v %v", records, e)
	}
	p := records[0]
	if p.Size != int64(len(body)) || p.Platform != "x86_64-linux" {
		t.Fatal(p)
	}
	w := request(f.module, "GET", "/rubygems/owned-gems/gems/"+p.FullName()+".gem", nil, "Bearer owned-reader")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), body) {
		t.Fatalf("download %d", w.Code)
	}
	for _, path := range []string{"names", "versions", "info/owned", "prerelease_specs.4.8.gz", "quick/Marshal.4.8/" + p.FullName() + ".gemspec.rz"} {
		w := request(f.module, "GET", "/rubygems/owned-gems/"+path, nil, "Bearer owned-reader")
		if w.Code != 200 || w.Body.Len() == 0 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if w := push(f, gem(t, "owned", "1.2.3.pre", "x86_64-linux", "different")); w.Code != 409 {
		t.Fatalf("conflict %d", w.Code)
	}
	audits, e := f.repos.ListAudits(t.Context(), repository.AuditQuery{Operation: "rubygems.publish", Limit: 100})
	if e != nil || len(audits) != 1 || audits[0].Bytes != int64(len(body)) || audits[0].Evidence["platform"] != "x86_64-linux" || audits[0].Evidence["digest"] != p.Digest {
		t.Fatalf("audit %+v %v", audits, e)
	}
}
func TestRubyGemsAuthorizationScopesFilterEveryIndexAndProtectWrites(t *testing.T) {
	f := setup(t)
	if w := push(f, gem(t, "owned", "1.0.0", "ruby", "fixture")); w.Code != 201 {
		t.Fatal(w.Code)
	}
	if w := push(f, gem(t, "secret", "1.0.0", "ruby", "fixture")); w.Code != 201 {
		t.Fatal(w.Code)
	}
	if _, e := f.repos.ReplaceRepositoryGrants(t.Context(), f.repo.ID, []repository.RepositoryGrant{{Principal: "reader", Scopes: []string{"repositories:read"}, ResourcePrefix: "owned"}}, "1"); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"names", "versions"} {
		w := request(f.module, "GET", "/rubygems/owned-gems/"+path, nil, "Bearer owned-reader")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "owned") || strings.Contains(w.Body.String(), "secret") {
			t.Fatalf("filtered %s %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"info/secret", "gems/secret-1.0.0.gem", "quick/Marshal.4.8/secret-1.0.0.gemspec.rz"} {
		w := request(f.module, "GET", "/rubygems/owned-gems/"+path, nil, "Bearer owned-reader")
		if w.Code != 403 {
			t.Fatalf("denied %s %d", path, w.Code)
		}
	}
	if w := request(f.module, "POST", "/rubygems/owned-gems/api/v1/gems", gem(t, "owned", "2.0.0", "ruby", "fixture"), "Bearer owned-reader"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := request(f.module, "GET", "/rubygems/owned-gems/versions", nil, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	r := httptest.NewRequest("GET", "/rubygems/owned-gems/info/owned", nil)
	r.SetBasicAuth("gateway", "owned-reader")
	w := httptest.NewRecorder()
	f.module.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
}
func TestRubyGemsQuotaAndConcurrentPushKeepOneAtomicIdentity(t *testing.T) {
	f := setup(t)
	body := gem(t, "owned", "1.0.0", "ruby", "fixture")
	if _, e := f.store.SetQuota(t.Context(), f.repo.ID, "operator", int64(len(body))); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	codes := make(chan int, 16)
	for i := 0; i < 16; i++ {
		wg.Go(func() { codes <- push(f, body).Code })
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != 201 {
			t.Fatalf("concurrent push %d", code)
		}
	}
	if w := push(f, gem(t, "another", "1.0.0", "ruby", "fixture")); w.Code != 409 {
		t.Fatal(w.Code)
	}
	c, e := f.store.Capacity(t.Context(), f.repo.ID)
	if e != nil || c.UsedBytes != int64(len(body)) || c.ObjectCount != 1 || c.PendingBytes != 0 {
		t.Fatalf("capacity %+v %v", c, e)
	}
	objects, e := f.objects.List(t.Context(), "native/rubygems/")
	if e != nil || len(objects) != 1 {
		t.Fatalf("objects %v %v", objects, e)
	}
}

type interrupted struct {
	objectstore.Store
	fail atomic.Bool
}

func (s *interrupted) PutVerifiedReader(ctx context.Context, key string, r io.Reader, size int64, digest string) error {
	if s.fail.Load() {
		data, _ := io.ReadAll(io.LimitReader(r, 100))
		_ = s.Store.Put(ctx, key, data)
		return errors.New("owned upload interruption")
	}
	return s.Store.PutVerifiedReader(ctx, key, r, size, digest)
}
func TestRubyGemsInterruptedUploadIsInvisibleAndExpiredObjectsAreCollected(t *testing.T) {
	f := setup(t)
	broken := &interrupted{Store: f.objects}
	broken.fail.Store(true)
	f.module, _ = New(options(f.repos, f.store, broken, f.repos))
	if w := push(f, gem(t, "owned", "1.0.0", "ruby", "fixture")); w.Code != 500 {
		t.Fatal(w.Code)
	}
	rows, _ := f.store.List(t.Context(), f.repo.ID, 10)
	if len(rows) != 0 {
		t.Fatal("partial upload became visible")
	}
	keys, _ := f.objects.List(t.Context(), "native/rubygems/")
	if len(keys) != 1 {
		t.Fatal(keys)
	}
	maintenance, _ := NewMaintenance(f.store, broken)
	if e := maintenance.Collect(t.Context(), time.Now().Add(2*time.Hour)); e != nil {
		t.Fatal(e)
	}
	keys, _ = f.objects.List(t.Context(), "native/rubygems/")
	if len(keys) != 0 {
		t.Fatal(keys)
	}
	broken.fail.Store(false)
	if w := push(f, gem(t, "owned", "1.0.0", "ruby", "fixture")); w.Code != 201 {
		t.Fatalf("republish %d %s", w.Code, w.Body.String())
	}
}

type failingAudit struct {
	domain.Auditor
	fail bool
}

func (a *failingAudit) RecordAudit(ctx context.Context, r repository.AuditRecord) error {
	if a.fail && r.Operation == "rubygems.publish" {
		return errors.New("owned audit failure")
	}
	return a.Auditor.RecordAudit(ctx, r)
}
func TestRubyGemsAuditFailureRollsBackPublicationAndCorruptionFailsClosed(t *testing.T) {
	f := setup(t)
	audit := &failingAudit{Auditor: f.repos, fail: true}
	f.store = domain.NewMemory(f.repos, audit)
	f.module, _ = New(options(f.repos, f.store, f.objects, audit))
	body := gem(t, "owned", "1.0.0", "ruby", "fixture")
	if w := push(f, body); w.Code != 500 {
		t.Fatal(w.Code)
	}
	rows, _ := f.store.List(t.Context(), f.repo.ID, 10)
	if len(rows) != 0 {
		t.Fatal("audit failure became visible")
	}
	audit.fail = false
	if w := push(f, body); w.Code != 201 {
		t.Fatal(w.Code)
	}
	rows, _ = f.store.List(t.Context(), f.repo.ID, 10)
	bad := append([]byte{}, body...)
	bad[len(bad)/2] ^= 1
	if e := f.objects.Put(t.Context(), rows[0].ObjectKey, bad); e != nil {
		t.Fatal(e)
	}
	if w := request(f.module, "GET", "/rubygems/owned-gems/gems/owned-1.0.0.gem", nil, "owned-admin"); w.Code != 500 || bytes.Equal(w.Body.Bytes(), bad) {
		t.Fatal(w.Code)
	}
	if w := push(f, body); w.Code != 500 {
		t.Fatal(w.Code)
	}
}
func TestRubyGemsPreviewRequiresExplicitPortsAndNeverAdmitsFormalFormat(t *testing.T) {
	f := setup(t)
	if repository.IsSupportedFormat(repository.FormatRubyGems) || repository.FormatSupportsRepositoryType(repository.FormatRubyGems, repository.RepositoryTypeHosted) {
		t.Fatal("preview admitted")
	}
	o := options(f.repos, f.store, f.objects, f.repos)
	o.Store = nil
	if _, e := New(o); e == nil {
		t.Fatal("missing explicit publication port")
	}
	o.Store = (*domain.Memory)(nil)
	if _, e := New(o); e == nil {
		t.Fatal("typed nil accepted")
	}
	w := request(f.module.Preview(), "POST", "/api/v2/previews/rubygems/repositories", []byte(`{"name":"created-gems"}`), "Bearer owned-admin")
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"preview":true`) {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	w = request(f.module.Preview(), "POST", "/api/v2/previews/rubygems/repositories", []byte(`{"name":"proxy-gems","type":"proxy"}`), "Bearer owned-admin")
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
}
