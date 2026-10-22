package helm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	helmprotocol "github.com/ccsert/artifact-gateway/internal/protocol/helm"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/secrets"
	"github.com/ccsert/artifact-gateway/internal/upstreamauth"
	"go.yaml.in/yaml/v3"
)

type helmSourceClient struct{ client *http.Client }

func (c helmSourceClient) FetchHelm(ctx context.Context, repo repository.HostedRepository, target string, headers http.Header) (*http.Response, error) {
	r, e := http.NewRequestWithContext(ctx, "GET", target, nil)
	if e != nil {
		return nil, e
	}
	r.Header = headers.Clone()
	if r.Header == nil {
		r.Header = http.Header{}
	}
	if e = upstreamauth.ApplyOrigin(r, repo.Endpoint, repo.UpstreamAuth); e != nil {
		return nil, e
	}
	client := *c.client
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client.Do(r)
}

type helmFeed struct {
	server             *httptest.Server
	mu                 sync.Mutex
	chart, prov, index []byte
	status             int
	require            string
	requests           map[string]int
	conditionals       int
}

func newHelmFeed(t *testing.T, chart, prov []byte) *helmFeed {
	t.Helper()
	in, e := helmprotocol.Inspect(bytes.NewReader(chart))
	if e != nil {
		t.Fatal(e)
	}
	f := &helmFeed{chart: chart, prov: prov, requests: map[string]int{}}
	body, e := yaml.Marshal(struct {
		APIVersion string                               `yaml:"apiVersion"`
		Entries    map[string][]helmprotocol.IndexEntry `yaml:"entries"`
	}{"v1", map[string][]helmprotocol.IndexEntry{in.Metadata.Name: {{Metadata: in.Metadata, Digest: strings.TrimPrefix(digestFor(chart), "sha256:"), URLs: []string{"assets/" + helmprotocol.Filename(in.Metadata.Name, in.Metadata.Version)}}}}})
	if e != nil {
		t.Fatal(e)
	}
	f.index = body
	f.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests[r.URL.Path]++
		if f.require != "" && r.Header.Get("Authorization") != f.require {
			w.WriteHeader(401)
			return
		}
		if f.status != 0 {
			w.WriteHeader(f.status)
			return
		}
		switch {
		case r.URL.Path == "/index.yaml":
			w.Header().Set("ETag", `"owned-index"`)
			if r.Header.Get("If-None-Match") == `"owned-index"` {
				f.conditionals++
				w.WriteHeader(304)
				return
			}
			_, _ = w.Write(f.index)
		case strings.HasSuffix(r.URL.Path, ".tgz.prov"):
			if f.prov == nil {
				w.WriteHeader(404)
				return
			}
			_, _ = w.Write(f.prov)
		case strings.HasSuffix(r.URL.Path, ".tgz"):
			if f.chart == nil {
				w.WriteHeader(404)
				return
			}
			_, _ = w.Write(f.chart)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}
func proxyModule(t *testing.T, feed *helmFeed) (fixture, repository.HostedRepository) {
	t.Helper()
	s := repository.NewMemoryStore()
	objects := objectstore.NewMemoryStore()
	o := moduleOptions(s, objects)
	o.Sources = s
	o.Configuration = s
	o.Groups = s
	o.Upstream = helmSourceClient{feed.server.Client()}
	m, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	body, _ := json.Marshal(map[string]any{"name": "helm-proxy", "type": "proxy", "endpoint": feed.server.URL + "/index.yaml"})
	w := request(t, m.Preview(), "POST", "/api/v2/previews/helm/repositories", body, admin)
	var created struct{ ID string }
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &created) != nil {
		t.Fatalf("proxy=%d %s", w.Code, w.Body.String())
	}
	repo, e := s.GetHostedRepository(t.Context(), created.ID)
	if e != nil {
		t.Fatal(e)
	}
	return fixture{module: m, store: s, objects: objects, repo: repo}, repo
}
func indexChartURL(t *testing.T, w *httptest.ResponseRecorder, name string) string {
	t.Helper()
	var index struct {
		Entries map[string][]struct {
			URLs []string `yaml:"urls"`
		} `yaml:"entries"`
	}
	if w.Code != 200 || yaml.Unmarshal(w.Body.Bytes(), &index) != nil || len(index.Entries[name]) != 1 || len(index.Entries[name][0].URLs) != 1 {
		t.Fatalf("index=%d %s", w.Code, w.Body.String())
	}
	return index.Entries[name][0].URLs[0]
}
func expireHelmDocument(t *testing.T, store repository.HelmSourceStore, repo repository.HostedRepository) {
	t.Helper()
	doc, e := store.GetHelmSourceDocument(t.Context(), repo.ID, repo.Version)
	if e != nil {
		t.Fatal(e)
	}
	doc.FetchedAt = time.Now().Add(-time.Minute)
	doc.ExpiresAt = time.Now().Add(-time.Second)
	if e = store.PutHelmSourceDocument(t.Context(), doc); e != nil {
		t.Fatal(e)
	}
}
func TestHelmProxySnapshotCacheConditionalOfflineAndProvenance(t *testing.T) {
	chart := chartFixture(t, "owned", "1.2.3", "fixed closure")
	prov := provenanceFixture(t, chart)
	feed := newHelmFeed(t, chart, prov)
	f, repo := proxyModule(t, feed)
	index := request(t, f.module, "GET", "/helm/helm-proxy/index.yaml", nil, admin)
	path := indexChartURL(t, index, "owned")
	if !strings.HasPrefix(path, "/helm/helm-proxy/snapshots/") || strings.Contains(index.Body.String(), feed.server.URL) {
		t.Fatal("index exposed upstream")
	}
	if w := request(t, f.module, "GET", path, nil, nil); w.Code != 401 {
		t.Fatalf("private=%d", w.Code)
	}
	for _, test := range []struct {
		path string
		body []byte
	}{{path, chart}, {path + ".prov", prov}} {
		w := request(t, f.module, "GET", test.path, nil, admin)
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), test.body) {
			t.Fatalf("asset=%d %s", w.Code, w.Body.String())
		}
	}
	if w := request(t, f.module.Preview(), "PUT", "/api/v2/previews/helm/repositories/"+repo.ID+"/charts/owned/1.2.3", nil, admin); w.Code != 405 {
		t.Fatalf("proxy write=%d", w.Code)
	}
	metadata := request(t, f.module.Preview(), "GET", "/api/v2/previews/helm/repositories/"+repo.ID+"/charts/owned/1.2.3", nil, admin)
	var receipt struct {
		Size, ProvenanceSize                              int
		Digest, ProvenanceDigest, ChartURL, ProvenanceURL string
	}
	if metadata.Code != 200 || json.Unmarshal(metadata.Body.Bytes(), &receipt) != nil || receipt.Size != len(chart) || receipt.ProvenanceSize != len(prov) || receipt.Digest != digestFor(chart) || receipt.ProvenanceDigest != digestFor(prov) || receipt.ChartURL != path || receipt.ProvenanceURL != path+".prov" {
		t.Fatalf("complete metadata=%d %s", metadata.Code, metadata.Body.String())
	}
	expireHelmDocument(t, f.store, repo)
	updated := request(t, f.module, "GET", "/helm/helm-proxy/index.yaml", nil, admin)
	if updated.Code != 200 || !bytes.Equal(index.Body.Bytes(), updated.Body.Bytes()) {
		t.Fatalf("conditional=%d", updated.Code)
	}
	feed.mu.Lock()
	conditionals := feed.conditionals
	feed.mu.Unlock()
	if conditionals != 1 {
		t.Fatalf("conditional count=%d", conditionals)
	}
	feed.server.Close()
	expireHelmDocument(t, f.store, repo)
	offline := request(t, f.module, "GET", "/helm/helm-proxy/index.yaml", nil, admin)
	if offline.Code != 200 || !bytes.Equal(index.Body.Bytes(), offline.Body.Bytes()) {
		t.Fatalf("offline=%d %s", offline.Code, offline.Body.String())
	}
	for _, test := range []struct {
		path string
		body []byte
	}{{path, chart}, {path + ".prov", prov}} {
		w := request(t, f.module, "GET", test.path, nil, admin)
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), test.body) {
			t.Fatalf("offline asset=%d", w.Code)
		}
	}
	capacity, e := f.store.GetRepositoryCapacity(t.Context(), repo.ID)
	if e != nil || capacity.UsedBytes != int64(len(chart)+len(prov)) || capacity.ObjectCount != 2 {
		t.Fatalf("capacity=%+v %v", capacity, e)
	}
}
func TestHelmProxyMissingCorruptOrCrossIdentityClosureNeverBecomesVisible(t *testing.T) {
	for _, kind := range []string{"missing", "digest", "identity", "provenance", "denial"} {
		t.Run(kind, func(t *testing.T) {
			chart := chartFixture(t, "owned", "1.2.3", "fixed")
			feed := newHelmFeed(t, chart, provenanceFixture(t, chart))
			f, repo := proxyModule(t, feed)
			path := indexChartURL(t, request(t, f.module, "GET", "/helm/helm-proxy/index.yaml", nil, admin), "owned")
			feed.mu.Lock()
			switch kind {
			case "missing":
				feed.chart = nil
			case "digest":
				feed.chart = chartFixture(t, "owned", "1.2.3", "other bytes")
			case "identity":
				feed.chart = chartFixture(t, "other", "1.2.3", "wrong")
			case "provenance":
				feed.prov = []byte("broken signature")
			case "denial":
				feed.status = 403
			}
			feed.mu.Unlock()
			w := request(t, f.module, "GET", path, nil, admin)
			if w.Code != 502 {
				t.Fatalf("bad source=%d %s", w.Code, w.Body.String())
			}
			if all, e := f.store.ListHelmPublications(t.Context(), repo.ID, 10); e != nil || len(all) != 0 {
				t.Fatalf("visible incomplete=%+v %v", all, e)
			}
		})
	}
}
func TestHelmProxyRejectsAmbiguousBoundlessAndForbiddenIndex(t *testing.T) {
	for _, kind := range []string{"alias", "duplicate", "overlimit", "cross-host", "query", "multiple-urls", "parent-path", "userinfo", "fragment", "escaped-path"} {
		t.Run(kind, func(t *testing.T) {
			chart := chartFixture(t, "owned", "1.2.3", "fixed")
			feed := newHelmFeed(t, chart, nil)
			f, _ := proxyModule(t, feed)
			switch kind {
			case "alias":
				feed.index = []byte("apiVersion: v1\nentries: &bad {owned: [*bad]}\n")
			case "duplicate":
				feed.index = append(feed.index, []byte("entries: {}\n")...)
			case "overlimit":
				feed.index = bytes.Repeat([]byte("a"), helmprotocol.MaxIndexBytes+1)
			case "cross-host":
				feed.index = bytes.ReplaceAll(feed.index, []byte("assets/owned-1.2.3.tgz"), []byte("https://unapproved.example.invalid/owned.tgz"))
			case "query":
				feed.index = bytes.ReplaceAll(feed.index, []byte("assets/owned-1.2.3.tgz"), []byte("assets/owned-1.2.3.tgz?private=secret"))
			case "parent-path":
				feed.index = bytes.ReplaceAll(feed.index, []byte("assets/owned-1.2.3.tgz"), []byte("../owned-1.2.3.tgz"))
			case "userinfo":
				feed.index = bytes.ReplaceAll(feed.index, []byte("assets/owned-1.2.3.tgz"), []byte("https://user:secret@unapproved.example.invalid/owned.tgz"))
			case "fragment":
				feed.index = bytes.ReplaceAll(feed.index, []byte("assets/owned-1.2.3.tgz"), []byte("assets/owned-1.2.3.tgz#ignored"))
			case "escaped-path":
				feed.index = bytes.ReplaceAll(feed.index, []byte("assets/owned-1.2.3.tgz"), []byte("assets/%6fwned-1.2.3.tgz"))
			case "multiple-urls":
				feed.index = bytes.ReplaceAll(feed.index, []byte("- assets/owned-1.2.3.tgz"), []byte("- assets/owned-1.2.3.tgz\n          - other.tgz"))
			}
			if w := request(t, f.module, "GET", "/helm/helm-proxy/index.yaml", nil, admin); w.Code != 502 {
				t.Fatalf("index=%d %s", w.Code, w.Body.String())
			}
		})
	}
}
func TestHelmProxyConcurrentReadCreatesOneImmutableClosureAndQuotaRejects(t *testing.T) {
	chart := chartFixture(t, "owned", "1.2.3", "fixed")
	feed := newHelmFeed(t, chart, nil)
	f, repo := proxyModule(t, feed)
	path := indexChartURL(t, request(t, f.module, "GET", "/helm/helm-proxy/index.yaml", nil, admin), "owned")
	if _, e := f.store.ReplaceRepositoryCapacityQuota(t.Context(), repo.ID, 1); e != nil {
		t.Fatal(e)
	}
	if w := request(t, f.module, "GET", path, nil, admin); w.Code != 409 {
		t.Fatalf("quota=%d %s", w.Code, w.Body.String())
	}
	if _, e := f.store.ReplaceRepositoryCapacityQuota(t.Context(), repo.ID, 0); e != nil {
		t.Fatal(e)
	}
	feed.mu.Lock()
	feed.requests = map[string]int{}
	feed.mu.Unlock()
	var wg sync.WaitGroup
	results := make(chan string, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := request(t, f.module, "GET", path, nil, admin)
			if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), chart) {
				results <- fmt.Sprint(w.Code)
			}
		}()
	}
	wg.Wait()
	close(results)
	for e := range results {
		t.Error(e)
	}
	feed.mu.Lock()
	defer feed.mu.Unlock()
	if feed.requests["/assets/owned-1.2.3.tgz"] != 1 || feed.requests["/assets/owned-1.2.3.tgz.prov"] != 1 {
		t.Fatalf("duplicate transfers=%v", feed.requests)
	}
}
func TestHelmProxyCredentialRotationInvalidatesSnapshotsAndDenialNeverUsesStale(t *testing.T) {
	t.Setenv(secrets.KeyEnv, strings.Repeat("3", 64))
	chart := chartFixture(t, "owned", "1.2.3", "fixed")
	feed := newHelmFeed(t, chart, nil)
	f, repo := proxyModule(t, feed)
	body, _ := json.Marshal(map[string]any{"name": repo.Name, "type": "proxy", "endpoint": repo.Endpoint, "version": repo.Version, "upstreamAuth": map[string]any{"scheme": "bearer", "secret": "private-upstream-only"}})
	w := request(t, f.module.Preview(), "PUT", "/api/v2/previews/helm/repositories/"+repo.ID, body, admin)
	if w.Code != 200 || strings.Contains(w.Body.String(), "private-upstream-only") {
		t.Fatalf("configure=%d %s", w.Code, w.Body.String())
	}
	repo, _ = f.store.GetHostedRepository(t.Context(), repo.ID)
	feed.require = "Bearer private-upstream-only"
	path := indexChartURL(t, request(t, f.module, "GET", "/helm/helm-proxy/index.yaml", nil, admin), "owned")
	if w = request(t, f.module, "GET", path, nil, admin); w.Code != 200 {
		t.Fatalf("private chart=%d %s", w.Code, w.Body.String())
	}
	expireHelmDocument(t, f.store, repo)
	feed.require = "Bearer revoked"
	if w = request(t, f.module, "GET", "/helm/helm-proxy/index.yaml", nil, admin); w.Code != 502 {
		t.Fatalf("denial stale=%d", w.Code)
	}
	body, _ = json.Marshal(map[string]any{"name": repo.Name, "type": "proxy", "endpoint": repo.Endpoint, "version": repo.Version, "upstreamAuth": map[string]any{"scheme": "bearer", "secret": "rotated"}})
	w = request(t, f.module.Preview(), "PUT", "/api/v2/previews/helm/repositories/"+repo.ID, body, admin)
	if w.Code != 200 {
		t.Fatalf("rotation=%d %s", w.Code, w.Body.String())
	}
	if w = request(t, f.module, "GET", path, nil, admin); w.Code != 409 {
		t.Fatalf("old snapshot after rotation=%d %s", w.Code, w.Body.String())
	}
	feed.require = "Bearer rotated"
	newPath := indexChartURL(t, request(t, f.module, "GET", "/helm/helm-proxy/index.yaml", nil, admin), "owned")
	if w = request(t, f.module, "GET", newPath, nil, admin); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), chart) {
		t.Fatalf("new configured origin=%d", w.Code)
	}
}

func TestHelmProxyInterruptedOldSourceCannotBlockRotatedClosureOrCollectReplacement(t *testing.T) {
	chart := chartFixture(t, "owned", "1.2.3", "rotated recovery")
	prov := provenanceFixture(t, chart)
	feed := newHelmFeed(t, chart, prov)
	f, repo := proxyModule(t, feed)
	interrupted := &interruptedObjects{Store: f.objects}
	interrupted.fail.Store(true)
	options := moduleOptions(f.store, interrupted)
	options.Sources = f.store
	options.Groups = f.store
	options.Configuration = f.store
	options.Upstream = helmSourceClient{feed.server.Client()}
	var e error
	f.module, e = New(options)
	if e != nil {
		t.Fatal(e)
	}
	path := indexChartURL(t, request(t, f.module, "GET", "/helm/helm-proxy/index.yaml", nil, admin), "owned")
	if w := request(t, f.module, "GET", path, nil, admin); w.Code != 503 {
		t.Fatalf("interrupted=%d", w.Code)
	}
	body, _ := json.Marshal(map[string]any{"name": repo.Name, "type": "proxy", "endpoint": repo.Endpoint, "version": repo.Version})
	if w := request(t, f.module.Preview(), "PUT", "/api/v2/previews/helm/repositories/"+repo.ID, body, admin); w.Code != 200 {
		t.Fatalf("source version change=%d %s", w.Code, w.Body.String())
	}
	nextPath := indexChartURL(t, request(t, f.module, "GET", "/helm/helm-proxy/index.yaml", nil, admin), "owned")
	if w := request(t, f.module, "GET", nextPath, nil, admin); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), chart) {
		t.Fatalf("rotated source recovery=%d %s", w.Code, w.Body.String())
	}
	maintenance, e := NewMaintenance(f.store, f.objects)
	if e != nil {
		t.Fatal(e)
	}
	if e = maintenance.Collect(t.Context(), time.Now().Add(2*time.Hour)); e != nil {
		t.Fatal(e)
	}
	if w := request(t, f.module, "GET", nextPath+".prov", nil, admin); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), prov) {
		t.Fatalf("replacement was collected=%d", w.Code)
	}
}
