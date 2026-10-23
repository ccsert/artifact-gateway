package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/ocigraph"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func discoveryRequest(t *testing.T, handler http.Handler, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		authorize(r, token)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func catalogProxy(t *testing.T, store repository.HostedRepositoryStore, name string) repository.HostedRepository {
	t.Helper()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: name, Format: repository.FormatOCI, Type: repository.RepositoryTypeProxy, Endpoint: "https://" + redirectOrigin, AllowedHosts: []string{redirectOrigin, redirectCDN}})
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func catalogReader(t *testing.T, store repository.RepositoryGrantStore, repo repository.HostedRepository, actor, prefix string) {
	t.Helper()
	set, err := store.GetRepositoryGrants(context.Background(), repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(context.Background(), repo.ID, []repository.RepositoryGrant{{Principal: actor, Scopes: []string{"repositories:read"}, ResourcePrefix: prefix}}, set.Version); err != nil {
		t.Fatal(err)
	}
}

func catalogGateway(t *testing.T, store *repository.MemoryStore, upstream OCIClient, enabled bool) http.Handler {
	t.Helper()
	objects := objectstore.NewMemoryStore()
	options := GatewayOptions{Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatOCI: objects}, Caches: GatewayCaches{OCI: NewDefaultOCICache(objects, []string{redirectOrigin, redirectCDN})}, Upstream: upstream}
	if enabled {
		options.OCIDiscoveryStore = store
	}
	return newTestGateway(t, options)
}

func TestOCICatalogProxyGroupSnapshotAndAuthorization(t *testing.T) {
	var requests atomic.Int32
	fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/v2/_catalog" {
			t.Errorf("unexpected discovery route %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("last") == "" {
			w.Header().Set("Link", `</v2/_catalog?n=2&last=team%2Fa>; rel="next"`)
			_, _ = io.WriteString(w, `{"repositories":["team/a","team/a","private/secret"]}`)
		} else {
			_, _ = io.WriteString(w, `{"repositories":["team/b"]}`)
		}
	})
	store := repository.NewMemoryStore()
	repo := catalogProxy(t, store, "proxy-catalog")
	catalogReader(t, store, repo, "catalog-reader", "team/")
	createV2Group(t, store, "group-catalog", repository.FormatOCI, repository.GroupMember{RepositoryID: repo.ID})
	handler := catalogGateway(t, store, fixture.client, true)
	before := discoveryRequest(t, handler, "/v2/_catalog", "")
	if before.Code != 401 || requests.Load() != 0 {
		t.Fatal("unauthorized catalog contacted source")
	}
	token := testAuthenticator().IssueToken("catalog-reader")
	first := discoveryRequest(t, handler, "/v2/_catalog?n=1", token)
	if first.Code != 200 || first.Header().Get("OCI-Gateway-Discovery-Coverage") != "complete" || strings.Contains(first.Body.String(), "private/secret") {
		t.Fatalf("catalog=%d %s", first.Code, first.Body.String())
	}
	var page struct {
		Repositories []string `json:"repositories"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Repositories) != 1 || page.Repositories[0] != "group-catalog/team/a" {
		t.Fatalf("unexpected canonical catalog names=%v", page.Repositories)
	}
	link, err := ociDiscoveryNext(first.Header().Values("Link"))
	if err != nil || !strings.Contains(link, "snapshot=") {
		t.Fatalf("snapshot link=%q %v", link, err)
	}
	calls := requests.Load()
	next := discoveryRequest(t, handler, link, token)
	if next.Code != 200 || !strings.Contains(next.Body.String(), "group-catalog/team/b") || requests.Load() != calls {
		t.Fatalf("next=%d %s calls=%d", next.Code, next.Body.String(), requests.Load())
	}
	other := discoveryRequest(t, handler, link, testAuthenticator().IssueToken("other-reader"))
	if other.Code != 404 || strings.Contains(other.Body.String(), "team/") {
		t.Fatalf("cross principal cursor=%d %s", other.Code, other.Body.String())
	}
	catalogReader(t, store, repo, "other-reader", "")
	stale := discoveryRequest(t, handler, link, token)
	if stale.Code != 409 || strings.Contains(stale.Body.String(), "team/") {
		t.Fatalf("revoked cursor=%d %s", stale.Code, stale.Body.String())
	}
}

// A source update can linearize between the repository page and the Group's
// fresh member read. The old page must never be sealed under the new guard.
type catalogVersionSwitchStore struct {
	*repository.MemoryStore
	repositoryID        string
	afterRepositoryPage func()
}

func (s *catalogVersionSwitchStore) ListHostedRepositories(ctx context.Context, size int, after string) ([]repository.HostedRepository, string, error) {
	items, next, err := s.MemoryStore.ListHostedRepositories(ctx, size, after)
	if err == nil && s.afterRepositoryPage != nil && slices.ContainsFunc(items, func(repo repository.HostedRepository) bool {
		return repo.ID == s.repositoryID
	}) {
		action := s.afterRepositoryPage
		s.afterRepositoryPage = nil
		action()
	}
	return items, next, err
}

func TestOCICatalogRejectsConflictingSourceVersionsAcrossViews(t *testing.T) {
	for _, preceding := range []int{0, 200} {
		t.Run(fmt.Sprintf("preceding=%d", preceding), func(t *testing.T) {
			testOCICatalogConflictingVersions(t, preceding)
		})
	}
}

func testOCICatalogConflictingVersions(t *testing.T, preceding int) {
	t.Helper()
	var calls atomic.Int32
	fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		name := "team/old-source"
		if strings.HasPrefix(r.URL.Path, "/new/") {
			name = "team/new-source"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"repositories": []string{name}})
	})
	store := repository.NewMemoryStore()
	// Fixed IDs force the source onto a later page without consuming OCI's
	// discovery budget. The update must wait for the page containing this source.
	for i := range preceding {
		_, err := store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: fmt.Sprintf("%08x-0000-4000-8000-000000000000", i+1), Name: fmt.Sprintf("filler-%03d", i), Format: repository.FormatRaw, Type: repository.RepositoryTypeHosted})
		if err != nil {
			t.Fatal(err)
		}
	}
	repo, err := store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: "ffffffff-ffff-4fff-8fff-ffffffffffff", Name: "a-source-version", Format: repository.FormatOCI, Type: repository.RepositoryTypeProxy, Endpoint: "https://" + redirectOrigin, AllowedHosts: []string{redirectOrigin, redirectCDN}})
	if err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "z-group-version", repository.FormatOCI, repository.GroupMember{RepositoryID: repo.ID})
	controlled := &catalogVersionSwitchStore{MemoryStore: store, repositoryID: repo.ID, afterRepositoryPage: func() {
		repo.Endpoint += "/new"
		var err error
		repo, err = store.UpdateHostedRepository(t.Context(), repo, repo.Version)
		if err != nil {
			t.Fatal(err)
		}
	}}
	objects := objectstore.NewMemoryStore()
	handler := newTestGateway(t, GatewayOptions{Store: controlled, OCIDiscoveryStore: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatOCI: objects}, Caches: GatewayCaches{OCI: NewDefaultOCICache(objects, []string{redirectOrigin, redirectCDN})}, Upstream: fixture.client})
	changed := discoveryRequest(t, handler, "/v2/_catalog", "admin-secret")
	if controlled.afterRepositoryPage != nil {
		t.Fatal("source version switch did not observe the owned repository page")
	}
	if changed.Code != 409 || !strings.Contains(changed.Body.String(), "DISCOVERY_CHANGED") || strings.Contains(changed.Body.String(), "team/") {
		t.Fatalf("mixed source versions became a successful Catalog: %d %s", changed.Code, changed.Body.String())
	}
	for _, audit := range store.Audits {
		if audit.Operation == "oci.catalog.snapshot" {
			t.Fatal("conflicting source versions published a snapshot")
		}
	}
	before := calls.Load()
	fresh := discoveryRequest(t, handler, "/v2/_catalog", "admin-secret")
	if fresh.Code != 200 || strings.Contains(fresh.Body.String(), "team/old-source") || !strings.Contains(fresh.Body.String(), "team/new-source") || calls.Load()-before != 1 {
		t.Fatalf("fresh Catalog must enumerate the actual source once across views: %d %s calls=%d", fresh.Code, fresh.Body.String(), calls.Load()-before)
	}
}

func TestOCICatalogMixedSourcesReturnReadableCanonicalNames(t *testing.T) {
	f := newDiscoveryArtifactFixture(t, "mixed source fixture")
	var upperMisses atomic.Int32
	fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/upper/") {
			if strings.HasSuffix(r.URL.Path, "/v2/_catalog") {
				_, _ = io.WriteString(w, `{"repositories":[]}`)
				return
			}
			upperMisses.Add(1)
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/v2/_catalog") {
			_, _ = io.WriteString(w, `{"repositories":["team/proxy","team/shared"]}`)
			return
		}
		if !strings.Contains(r.URL.Path, "/team/proxy/") && !strings.Contains(r.URL.Path, "/team/shared/") {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/manifests/latest") {
			r.URL.Path = strings.TrimSuffix(r.URL.Path, "latest") + f.subject.Digest
		}
		f.serve(w, r)
	})
	store := repository.NewMemoryStore()
	hosted, err := store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "catalog-hosted", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	upper, lower := catalogProxy(t, store, "catalog-upper"), catalogProxy(t, store, "catalog-lower")
	for _, entry := range []struct {
		repo repository.HostedRepository
		path string
	}{{upper, "/upper"}, {lower, "/lower"}} {
		entry.repo.Endpoint += entry.path
		if _, err := store.UpdateHostedRepository(t.Context(), entry.repo, entry.repo.Version); err != nil {
			t.Fatal(err)
		}
	}
	group := createV2Group(t, store, "catalog-mixed", repository.FormatOCI,
		repository.GroupMember{RepositoryID: hosted.ID, Position: 0},
		repository.GroupMember{RepositoryID: upper.ID, Position: 1},
		repository.GroupMember{RepositoryID: lower.ID, Position: 2})
	handler := catalogGateway(t, store, fixture.client, true)
	hostedBody := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","annotations":{"example.source":"hosted"}}`)
	for _, name := range []string{"team/local", "team/shared"} {
		r := httptest.NewRequest(http.MethodPut, "/v2/"+hosted.Name+"/"+name+"/manifests/latest", strings.NewReader(string(hostedBody)))
		authorize(r, "resolver-secret")
		r.Header.Set("Content-Type", ocigraph.OCIManifest)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 201 {
			t.Fatalf("publish mixed Hosted fixture=%d %s", w.Code, w.Body.String())
		}
	}
	response := discoveryRequest(t, handler, "/v2/_catalog", "admin-secret")
	var result struct {
		Repositories []string `json:"repositories"`
		Gateway      struct {
			Coverage string `json:"coverage"`
			Entries  []struct {
				Name    string   `json:"name"`
				Sources []string `json:"sources"`
			} `json:"entries"`
		} `json:"gateway"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Gateway.Coverage != "complete" || len(result.Repositories) != 7 {
		t.Fatalf("mixed Catalog=%d %s", response.Code, response.Body.String())
	}
	for _, entry := range result.Gateway.Entries {
		if entry.Name == group.Name+"/team/shared" && strings.Join(entry.Sources, ",") != hosted.Name+","+lower.Name {
			t.Fatalf("mixed conflict must report actual source precedence: %+v", entry)
		}
	}
	for _, name := range result.Repositories {
		got := discoveryRequest(t, handler, "/v2/"+name+"/manifests/latest", "admin-secret")
		want := f.objects[f.subject.Digest].body
		if strings.HasPrefix(name, hosted.Name+"/") || name == group.Name+"/team/local" || name == group.Name+"/team/shared" {
			want = hostedBody
		}
		if got.Code != 200 || got.Body.String() != string(want) {
			t.Fatalf("advertised name %q is not directly readable from its authority: %d %s", name, got.Code, got.Body.String())
		}
	}
	if upperMisses.Load() == 0 {
		t.Fatal("lower Proxy read did not traverse the higher Proxy's actual 404")
	}
}

func TestOCICatalogUnsupportedPartialAndPaginationSafety(t *testing.T) {
	for _, tc := range []struct {
		name              string
		status            int
		body, link, state string
		want              int
	}{
		{"unsupported", 404, `{"errors":[]}`, "", "unsupported", 501},
		{"forbidden", 403, `{"errors":[]}`, "", "partial", 502},
		{"unauthorized", 401, `{"errors":[]}`, "", "partial", 502},
		{"missing names", 200, `{}`, "", "partial", 502},
		{"invalid name", 200, `{"repositories":["../private"]}`, "", "partial", 502},
		{"loop", 200, `{"repositories":["team/a"]}`, `</v2/_catalog?last=team%2Fa>; rel="next"`, "partial", 200},
		{"foreign link", 200, `{"repositories":["team/a"]}`, `<https://` + redirectCDN + `/v2/_catalog?last=team%2Fa>; rel="next"`, "partial", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var foreign atomic.Int32
			fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Host == redirectCDN {
					foreign.Add(1)
				}
				if tc.link != "" {
					w.Header().Set("Link", tc.link)
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			store := repository.NewMemoryStore()
			catalogProxy(t, store, "proxy-catalog")
			response := discoveryRequest(t, catalogGateway(t, store, fixture.client, true), "/v2/_catalog", "admin-secret")
			if response.Code != tc.want || response.Header().Get("OCI-Gateway-Discovery-Coverage") != tc.state || foreign.Load() != 0 {
				t.Fatalf("catalog=%d coverage=%q body=%s foreign=%d", response.Code, response.Header().Get("OCI-Gateway-Discovery-Coverage"), response.Body.String(), foreign.Load())
			}
		})
	}
}

func TestOCICatalogSourceVersionAndMissingPort(t *testing.T) {
	fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"repositories":["team/a","team/b"]}`)
	})
	store := repository.NewMemoryStore()
	repo := catalogProxy(t, store, "proxy-catalog")
	missing := discoveryRequest(t, catalogGateway(t, store, fixture.client, false), "/v2/_catalog", "admin-secret")
	if missing.Code != 503 {
		t.Fatalf("nil discovery port=%d %s", missing.Code, missing.Body.String())
	}
	missing = discoveryRequest(t, catalogGateway(t, store, fixture.client, false), "/v2/"+repo.Name+"/team/app/referrers/sha256:"+strings.Repeat("a", 64), "admin-secret")
	if missing.Code != 503 {
		t.Fatalf("nil Referrers discovery port=%d %s", missing.Code, missing.Body.String())
	}
	options := explicitGatewayOptions()
	options.OCIDiscoveryStore = (*repository.PostgresStore)(nil)
	if _, err := NewGatewayHandler(options); err == nil || !strings.Contains(err.Error(), "OCI discovery store is nil") {
		t.Fatalf("typed nil discovery capability was accepted: %v", err)
	}
	handler := catalogGateway(t, store, fixture.client, true)
	head := httptest.NewRequest(http.MethodHead, "/v2/_catalog", nil)
	authorize(head, "admin-secret")
	headed := httptest.NewRecorder()
	handler.ServeHTTP(headed, head)
	if headed.Code != 200 || headed.Body.Len() != 0 || headed.Header().Get("OCI-Gateway-Discovery-Coverage") != "complete" {
		t.Fatalf("Catalog HEAD=%d %s", headed.Code, headed.Body.String())
	}
	first := discoveryRequest(t, handler, "/v2/_catalog?n=1", "admin-secret")
	link, err := ociDiscoveryNext(first.Header().Values("Link"))
	if err != nil || link == "" {
		t.Fatalf("first=%d %s %v", first.Code, first.Body.String(), err)
	}
	if _, err := store.UpdateHostedRepository(context.Background(), repo, repo.Version); err != nil {
		t.Fatal(err)
	}
	stale := discoveryRequest(t, handler, link, "admin-secret")
	if stale.Code != 409 {
		t.Fatalf("stale source=%d %s", stale.Code, stale.Body.String())
	}
	bad := discoveryRequest(t, handler, "/v2/_catalog?n=1&n=2", "admin-secret")
	if bad.Code != 400 {
		t.Fatalf("ambiguous pagination=%d", bad.Code)
	}
	if _, err := url.Parse(link); err != nil {
		t.Fatal(err)
	}
}

func TestOCICatalogBearerUsesRegistryScope(t *testing.T) {
	var catalogTokens, repositoryTokens atomic.Int32
	fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			switch r.URL.Query().Get("scope") {
			case "registry:catalog:*":
				catalogTokens.Add(1)
			case "repository:team/a:pull":
				repositoryTokens.Add(1)
			default:
				t.Error("unexpected bearer scope")
			}
			_, _ = io.WriteString(w, `{"token":"owned-discovery-token","expires_in":60}`)
			return
		}
		scope := "registry:catalog:*"
		if r.URL.Path != "/v2/_catalog" {
			scope = "repository:team/a:pull"
		}
		if r.Header.Get("Authorization") != "Bearer owned-discovery-token" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="https://`+redirectOrigin+`/token",service="owned",scope="`+scope+`"`)
			w.WriteHeader(401)
			return
		}
		_, _ = io.WriteString(w, `{"repositories":["team/a"]}`)
	})
	member := proxyRedirectMember(nil)
	member.OCIBearer = &repository.OCIBearer{Realm: "https://" + redirectOrigin + "/token", Service: "owned"}
	response, err := fixture.client.FetchOCIDiscovery(t.Context(), member, OCIDiscoveryRequest{Kind: "catalog"})
	if err != nil {
		t.Fatal(err)
	}
	closeProxyRedirectResponse(response)
	response, err = fixture.client.FetchOCIDiscovery(t.Context(), member, OCIDiscoveryRequest{Kind: "referrers", Image: "team/a", Subject: "sha256:" + strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	closeProxyRedirectResponse(response)
	if catalogTokens.Load() != 1 || repositoryTokens.Load() != 1 {
		t.Fatalf("scope isolation catalog=%d repository=%d", catalogTokens.Load(), repositoryTokens.Load())
	}
}
