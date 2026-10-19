package nexussource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/egress"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/secrets"
)

type fixtureStore struct{ values map[string]Source }

func newFixtureStore() *fixtureStore { return &fixtureStore{map[string]Source{}} }
func (s *fixtureStore) Create(_ context.Context, v Source) (Source, error) {
	v.Version = "1"
	v.CreatedAt = time.Now().UTC()
	v.UpdatedAt = v.CreatedAt
	s.values[v.ID] = v
	return v, nil
}
func (s *fixtureStore) Get(_ context.Context, id string) (Source, error) {
	v, ok := s.values[id]
	if !ok {
		return Source{}, ErrNotFound
	}
	return v, nil
}
func (s *fixtureStore) List(_ context.Context, limit int, cursor string) ([]Source, string, error) {
	ids := make([]string, 0, len(s.values))
	for id := range s.values {
		if id > cursor {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	next := ""
	if len(ids) > limit {
		ids = ids[:limit]
		next = ids[len(ids)-1]
	}
	out := []Source{}
	for _, id := range ids {
		out = append(out, s.values[id])
	}
	return out, next, nil
}
func (s *fixtureStore) Update(ctx context.Context, v Source, expected string) (Source, error) {
	old, err := s.Get(ctx, v.ID)
	if err != nil {
		return Source{}, err
	}
	if old.Version != expected {
		return Source{}, ErrVersionConflict
	}
	n, _ := strconv.Atoi(old.Version)
	v.Version = strconv.Itoa(n + 1)
	v.UpdatedAt = time.Now().UTC()
	s.values[v.ID] = v
	return v, nil
}
func (s *fixtureStore) Delete(ctx context.Context, id, expected string) error {
	v, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if v.Version != expected {
		return ErrVersionConflict
	}
	delete(s.values, id)
	return nil
}

type emptyTargets struct{}

func (emptyTargets) GetHostedGroupByName(context.Context, string) (repository.HostedGroup, error) {
	return repository.HostedGroup{}, repository.ErrNotFound
}
func (emptyTargets) GetHostedRepositoryByName(context.Context, string) (repository.HostedRepository, error) {
	return repository.HostedRepository{}, repository.ErrNotFound
}
func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	var failure *Failure
	if !errors.As(err, &failure) || failure.Code != code {
		t.Fatalf("error=%v, want bounded code %s", err, code)
	}
}
func sourceInput() Input {
	return Input{Name: "synthetic-nexus", BaseURL: "https://example.com", Credentials: &Credentials{"synthetic-reader", "synthetic-password-do-not-echo"}}
}

func TestSourceCredentialLifecycleAndOriginBinding(t *testing.T) {
	t.Setenv(secrets.KeyEnv, "0123456789abcdef0123456789abcdef")
	store := newFixtureStore()
	service := New(store, emptyTargets{}, Options{})
	ctx := context.Background()
	created, err := service.Create(ctx, sourceInput())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(created.CredentialCiphertext, "synthetic") || created.CredentialCiphertext == "" {
		t.Fatal("credentials must be sealed")
	}
	plain, err := secrets.Open("nexus-source:"+created.ID, created.CredentialCiphertext)
	if err != nil || !strings.Contains(plain, "synthetic-reader") {
		t.Fatal("ciphertext must be source-bound and readable")
	}
	if _, err := secrets.Open("nexus-source:another", created.CredentialCiphertext); err == nil {
		t.Fatal("ciphertext must not work for another source")
	}
	updated, err := service.Update(ctx, created.ID, Input{Name: "renamed", BaseURL: created.BaseURL}, "1")
	if err != nil || updated.Version != "2" || updated.CredentialCiphertext != created.CredentialCiphertext {
		t.Fatalf("update=%#v err=%v", updated, err)
	}
	_, err = service.Update(ctx, created.ID, sourceInput(), "1")
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale update=%v", err)
	}
	_, err = service.Update(ctx, created.ID, Input{Name: "renamed", BaseURL: "https://other.example"}, "2")
	requireCode(t, err, "credential_replacement_required")
	updated, err = service.Update(ctx, created.ID, Input{Name: "renamed", BaseURL: "https://other.example", ClearCredentials: true}, "2")
	if err != nil || updated.CredentialCiphertext != "" {
		t.Fatalf("clear=%#v err=%v", updated, err)
	}
	if err := service.Delete(ctx, created.ID, "2"); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale delete=%v", err)
	}
	if err := service.Delete(ctx, created.ID, "3"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted source=%v", err)
	}
}
func TestSourceRejectsUnsafeConfigurationAndMissingEncryptionKey(t *testing.T) {
	service := New(newFixtureStore(), emptyTargets{}, Options{})
	ctx := context.Background()
	for _, base := range []string{"http://example.com", "https://user:secret@example.com", "https://example.com?token=secret", "https://example.com/#secret", "https://example.com/a/../b", "https://example.com/%2e%2e/a"} {
		_, err := service.Create(ctx, Input{Name: "source", BaseURL: base})
		requireCode(t, err, "invalid_source")
	}
	t.Setenv(secrets.KeyEnv, "")
	_, err := service.Create(ctx, sourceInput())
	requireCode(t, err, "encryption_key_unavailable")
	if _, err := service.Create(ctx, Input{Name: "public", BaseURL: "https://example.com"}); err != nil {
		t.Fatalf("anonymous source=%v", err)
	}
}

type nexusFixture struct {
	mu       sync.Mutex
	t        *testing.T
	handler  func(http.ResponseWriter, *http.Request) bool
	requests []string
}

func (f *nexusFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	f.mu.Unlock()
	if r.Method != "GET" {
		f.t.Errorf("source write attempted: %s", r.Method)
		w.WriteHeader(500)
		return
	}
	user, password, ok := r.BasicAuth()
	if !ok || user != "synthetic-reader" || password != "synthetic-password-do-not-echo" {
		f.t.Error("source read did not receive intended synthetic credential")
	}
	w.Header().Set("Content-Type", "application/json")
	if f.handler != nil && f.handler(w, r) {
		return
	}
	switch r.URL.Path {
	case "/service/rest/v1/repositories":
		_ = json.NewEncoder(w).Encode([]map[string]string{{"name": "releases", "format": "maven2", "type": "hosted"}, {"name": "snapshots", "format": "maven2", "type": "hosted"}, {"name": "mixed", "format": "maven2", "type": "hosted"}, {"name": "raw-a", "format": "raw", "type": "hosted"}, {"name": "raw-b", "format": "raw", "type": "hosted"}, {"name": "central", "format": "maven2", "type": "proxy"}, {"name": "public", "format": "maven2", "type": "group"}, {"name": "unknown", "format": "custom", "type": "hosted"}})
	case "/service/rest/v1/repositories/maven/hosted/releases":
		_, _ = w.Write([]byte(`{"name":"releases","maven":{"versionPolicy":"RELEASE","layoutPolicy":"STRICT"},"storage":{"blobStoreName":"default","writePolicy":"ALLOW_ONCE"},"online":true}`))
	case "/service/rest/v1/repositories/maven/hosted/snapshots":
		_, _ = w.Write([]byte(`{"name":"snapshots","maven":{"versionPolicy":"SNAPSHOT"}}`))
	case "/service/rest/v1/repositories/maven/hosted/mixed":
		_, _ = w.Write([]byte(`{"name":"mixed","maven":{"versionPolicy":"MIXED"}}`))
	case "/service/rest/v1/repositories/maven/proxy/central":
		_, _ = w.Write([]byte(`{"name":"central","maven":{"versionPolicy":"RELEASE"},"proxy":{"remoteUrl":"https://repo.maven.apache.org/maven2"},"httpClient":{"authentication":{"password":"upstream-secret-do-not-echo"}}}`))
	case "/service/rest/v1/repositories/maven/group/public":
		_, _ = w.Write([]byte(`{"name":"public","group":{"memberNames":["releases","central"]}}`))
	case "/service/rest/v1/repositories/raw/hosted/raw-a":
		_, _ = w.Write([]byte(`{"name":"raw-a"}`))
	case "/service/rest/v1/repositories/raw/hosted/raw-b":
		_, _ = w.Write([]byte(`{"name":"raw-b"}`))
	case "/service/rest/v1/assets":
		repo := r.URL.Query().Get("repository")
		token := r.URL.Query().Get("continuationToken")
		if repo != "raw-a" && repo != "raw-b" && repo != "releases" && repo != "mixed" {
			f.t.Errorf("unsupported asset discovery %s", repo)
		}
		format := "raw"
		if repo == "releases" || repo == "mixed" {
			format = "maven2"
		}
		p := "first.txt"
		var next any = "next-page"
		if token != "" {
			p = "second.txt"
			next = nil
		}
		if repo == "mixed" {
			if token == "" {
				p = "com/acme/lib/1.0/lib-1.0.jar"
			} else {
				p = "com/acme/lib/1.1-SNAPSHOT/lib-1.1-20260101.123456-1.jar"
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"repository": repo, "format": format, "path": p, "downloadUrl": "https://example.com/repository/" + repo + "/" + p, "fileSize": 4, "checksum": map[string]string{"sha256": strings.Repeat("a", 64)}}}, "continuationToken": next})
	default:
		f.t.Errorf("request outside read-only allowlist: %s", r.URL.Path)
		w.WriteHeader(500)
	}
}
func fixtureService(t *testing.T, custom func(http.ResponseWriter, *http.Request) bool) (*Service, string, *nexusFixture) {
	t.Helper()
	t.Setenv(secrets.KeyEnv, "0123456789abcdef0123456789abcdef")
	fixture := &nexusFixture{t: t, handler: custom}
	server := httptest.NewTLSServer(fixture)
	t.Cleanup(server.Close)
	hooks := egress.DefaultHooks()
	hooks.ProxyFromEnvironment = func(*http.Request) (*url.URL, error) { return nil, nil }
	hooks.LookupIP = func(context.Context, string, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.10")}, nil
	}
	hooks.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	service := New(newFixtureStore(), emptyTargets{}, Options{Client: server.Client(), EgressHooks: &hooks})
	v, err := service.Create(context.Background(), sourceInput())
	if err != nil {
		t.Fatal(err)
	}
	return service, v.ID, fixture
}
func TestNexusDiscoveryReadsMultipleRepositoriesAndExhaustsPagination(t *testing.T) {
	service, id, fixture := fixtureService(t, nil)
	result, err := service.Discover(context.Background(), id, DiscoveryInput{Repositories: []string{"raw-a", "releases", "snapshots", "mixed", "unknown", "central", "public", "raw-b"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Assets) != 7 || len(result.UnsupportedAssets) != 1 || result.Stats.Pages != 8 || result.Stats.Requests != 16 || len(result.Repositories) != 8 {
		t.Fatalf("discovery=%#v", result)
	}
	want := map[string]string{"raw-a": "supported", "raw-b": "supported", "releases": "supported", "snapshots": "unsupported", "mixed": "supported", "unknown": "unsupported", "central": "preview_only", "public": "preview_only"}
	for _, repo := range result.Repositories {
		if repo.Status != want[repo.Name] {
			t.Fatalf("repository=%#v", repo)
		}
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "do-not-echo") {
		t.Fatal("source secret leaked")
	}
	for _, request := range fixture.recordedRequests() {
		if strings.Contains(request, "security") || strings.Contains(request, "script") || strings.Contains(request, "/repository/") {
			t.Fatalf("forbidden source access: %s", request)
		}
	}
}
func TestNexusPreflightReportsUnsupportedAndCredentialVisibleScope(t *testing.T) {
	service, id, _ := fixtureService(t, nil)
	out, err := service.Preflight(context.Background(), id)
	if err != nil || len(out.Repositories) != 8 || out.Stats.Requests != 8 {
		t.Fatalf("preflight=%#v err=%v", out, err)
	}
	for _, r := range out.Repositories {
		if r.Name == "central" {
			found := false
			for _, f := range r.Fields {
				if f.Field == "httpClient.authentication.password" && f.Disposition == "unsupported" {
					found = true
				}
			}
			if !found {
				t.Fatal("source credential field disposition missing")
			}
		}
	}
}
func TestNexusFailuresAreExplicitAndBounded(t *testing.T) {
	cases := []struct {
		name, code string
		handler    func(http.ResponseWriter, *http.Request) bool
		input      DiscoveryInput
		timeout    time.Duration
	}{
		{name: "unauthorized", code: "source_unauthorized", handler: func(w http.ResponseWriter, r *http.Request) bool {
			w.WriteHeader(401)
			_, _ = w.Write([]byte("synthetic-password-do-not-echo"))
			return true
		}},
		{name: "forbidden", code: "source_forbidden", handler: func(w http.ResponseWriter, r *http.Request) bool { w.WriteHeader(403); return true }},
		{name: "redirect", code: "source_redirect_denied", handler: func(w http.ResponseWriter, r *http.Request) bool {
			w.Header().Set("Location", "https://other.example/security/users")
			w.WriteHeader(302)
			return true
		}},
		{name: "invalid-json", code: "source_invalid_response", handler: func(w http.ResponseWriter, r *http.Request) bool { _, _ = w.Write([]byte(`{secret`)); return true }},
		{name: "page-too-large", code: "source_limit_exceeded", handler: func(w http.ResponseWriter, r *http.Request) bool {
			_, _ = w.Write([]byte(strings.Repeat(" ", maxResponseBytes+1)))
			return true
		}},
		{name: "repeat-token", code: "source_pagination_cycle", handler: func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path != "/service/rest/v1/assets" {
				return false
			}
			_, _ = w.Write([]byte(`{"items":[],"continuationToken":"repeat"}`))
			return true
		}},
		{name: "cycle-token", code: "source_pagination_cycle", handler: func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path != "/service/rest/v1/assets" {
				return false
			}
			next := "a"
			if r.URL.Query().Get("continuationToken") == "a" {
				next = "b"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}, "continuationToken": next})
			return true
		}},
		{name: "interrupted-page", code: "source_forbidden", handler: func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Query().Get("continuationToken") == "" {
				return false
			}
			w.WriteHeader(403)
			return true
		}},
		{name: "max-pages", code: "source_limit_exceeded", input: DiscoveryInput{Repositories: []string{"raw-a"}, MaxPages: 1}},
		{name: "max-assets", code: "source_limit_exceeded", input: DiscoveryInput{Repositories: []string{"raw-a"}, MaxAssets: 1}},
		{name: "missing-repository", code: "source_repository_not_visible", input: DiscoveryInput{Repositories: []string{"not-visible"}}},
		{name: "timeout", code: "source_timeout", timeout: 25 * time.Millisecond, handler: func(w http.ResponseWriter, r *http.Request) bool { <-r.Context().Done(); return true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, id, fixture := fixtureService(t, tc.handler)
			service.options.OperationTimeout = tc.timeout
			input := tc.input
			if len(input.Repositories) == 0 {
				input.Repositories = []string{"raw-a"}
			}
			out, err := service.Discover(context.Background(), id, input)
			requireCode(t, err, tc.code)
			if len(out.Assets) != 0 {
				t.Fatal("failed pagination must not become partial success")
			}
			if len(fixture.recordedRequests()) > 6 {
				t.Fatalf("unbounded request count %d", len(fixture.recordedRequests()))
			}
		})
	}
}
func TestNexusRejectsUntrustedDownloadAddressesWithoutContactingThem(t *testing.T) {
	for _, download := range []string{"https://evil.example/repository/raw-a/a.txt", "https://user:secret@example.com/repository/raw-a/a.txt", "http://example.com/repository/raw-a/a.txt", "https://example.com/repository/raw-a/a.txt?token=secret", "https://example.com/security/users", "https://example.com/repository/raw-a/%2e%2e/secret", "https://example.com/repository/raw-a/x/%2E%2E/a.txt", "https://example.com/repository/raw-a/%252e%252e/secret", "https://example.com/repository/raw-a/a.txt#secret", "https://example.com/repository/raw-a/a.txt%3ftoken=secret", "https://example.com/repository/raw-a/a.txt%23secret"} {
		t.Run(download, func(t *testing.T) {
			service, id, _ := fixtureService(t, func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != "/service/rest/v1/assets" {
					return false
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"repository": "raw-a", "format": "raw", "path": "a.txt", "fileSize": 1, "downloadUrl": download}}, "continuationToken": nil})
				return true
			})
			_, err := service.Discover(context.Background(), id, DiscoveryInput{Repositories: []string{"raw-a"}})
			requireCode(t, err, "source_download_url_denied")
		})
	}
}
func TestNexusTLSAndPrivateAddressFailuresRemainFailures(t *testing.T) {
	service, id, fixture := fixtureService(t, nil)
	service.options.Client = nil
	_, err := service.Preflight(context.Background(), id)
	requireCode(t, err, "source_tls_failed")
	if len(fixture.recordedRequests()) != 0 {
		t.Fatal("untrusted TLS server received HTTP request")
	}
	service.options.EgressHooks.LookupIP = func(context.Context, string, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}
	_, err = service.Preflight(context.Background(), id)
	requireCode(t, err, "source_egress_denied")
}
func TestNexusMappingsValidateFormatTypeMemberOrderAndMissingDependencies(t *testing.T) {
	base := []Mapping{{SourceRepository: "releases", TargetName: "native-release", Format: "maven", Type: "hosted"}, {SourceRepository: "central", TargetName: "native-central", Format: "maven", Type: "proxy"}, {SourceRepository: "public", TargetName: "native-public", Format: "maven", Type: "group", TargetMembers: []string{"native-release", "native-central"}}}
	cases := []struct {
		name   string
		change func([]Mapping) []Mapping
		valid  bool
	}{
		{"valid", func(v []Mapping) []Mapping { return v }, true},
		{"wrong-format", func(v []Mapping) []Mapping { v[0].Format = "raw"; return v }, false},
		{"wrong-type", func(v []Mapping) []Mapping { v[0].Type = "proxy"; return v }, false},
		{"wrong-member-order", func(v []Mapping) []Mapping {
			v[2].TargetMembers = []string{"native-central", "native-release"}
			return v
		}, false},
		{"missing-member", func(v []Mapping) []Mapping { return []Mapping{v[0], v[2]} }, false},
		{"duplicate-target", func(v []Mapping) []Mapping { v[1].TargetName = v[0].TargetName; return v }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, id, _ := fixtureService(t, nil)
			mappings := slices.Clone(base)
			out, err := service.PreviewMapping(context.Background(), id, tc.change(mappings))
			if err != nil || out.Valid != tc.valid {
				t.Fatalf("preview=%#v err=%v", out, err)
			}
			if tc.valid && (out.Mappings[0].Status != "ready" || out.Mappings[1].Status != "manual" || out.Mappings[2].Status != "manual") {
				t.Fatalf("mapping states=%#v", out.Mappings)
			}
		})
	}
	service, id, _ := fixtureService(t, nil)
	out, err := service.PreviewMapping(context.Background(), id, []Mapping{{SourceRepository: "snapshots", TargetName: "snapshot", Format: "maven", Type: "hosted"}})
	if err != nil || out.Valid || out.Mappings[0].Status != "unsupported" {
		t.Fatalf("snapshot preview=%#v err=%v", out, err)
	}
}
func TestNexusTotalResponseBudget(t *testing.T) {
	service, id, fixture := fixtureService(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/service/rest/v1/assets" {
			return false
		}
		next := fmt.Sprint(time.Now().UnixNano())
		_, _ = fmt.Fprintf(w, `{"items":[],"continuationToken":%q,"padding":%q}`, next, strings.Repeat("x", (2<<20)-1000))
		return true
	})
	_, err := service.Discover(context.Background(), id, DiscoveryInput{Repositories: []string{"raw-a"}, MaxPages: 100})
	requireCode(t, err, "source_limit_exceeded")
	if len(fixture.recordedRequests()) > 12 {
		t.Fatalf("response budget requests=%d", len(fixture.recordedRequests()))
	}
}

func TestNexusOperatorPrivateAllowlistPinsOnlyApprovedHostAndCIDR(t *testing.T) {
	service, id, _ := fixtureService(t, nil)
	service.options.EgressHooks.LookupIP = func(context.Context, string, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("10.20.30.9")}, nil
	}
	service.options.PrivateEndpoints = map[string][]netip.Prefix{"example.com": {netip.MustParsePrefix("10.20.30.0/24")}}
	if _, err := service.Preflight(context.Background(), id); err != nil {
		t.Fatalf("approved internal endpoint failed: %v", err)
	}
	service.options.PrivateEndpoints = map[string][]netip.Prefix{"other.example": {netip.MustParsePrefix("10.20.30.0/24")}}
	_, err := service.Preflight(context.Background(), id)
	requireCode(t, err, "source_egress_denied")
	service.options.PrivateEndpoints = map[string][]netip.Prefix{"example.com": {netip.MustParsePrefix("10.20.31.0/24")}}
	_, err = service.Preflight(context.Background(), id)
	requireCode(t, err, "source_egress_denied")
	service.options.PrivateEndpoints = map[string][]netip.Prefix{"example.com": {netip.MustParsePrefix("127.0.0.1/32")}}
	service.options.EgressHooks.LookupIP = func(context.Context, string, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}
	_, err = service.Preflight(context.Background(), id)
	requireCode(t, err, "source_egress_denied")
}

func (f *nexusFixture) recordedRequests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests)
}

func TestNexusDiscoveryNormalizesEquivalentEncodedReleasePaths(t *testing.T) {
	const metadataPath = "com/acme/lib/1.0+build/lib-1.0+build.jar"
	const canonical = "https://example.com/repository/releases/" + metadataPath
	for _, download := range []string{
		"https://example.com/repository/releases/com/acme/lib/1.0+build/lib-1.0%2Bbuild.jar",
		"https://example.com/repository/releases/com/acme/lib/1.0+build/lib-1.0%2bbuild.jar",
		"https://example.com/repository/releases/com/acme/lib/1.0%2Bbuild/lib-1.0%2bbuild.jar",
		canonical,
	} {
		t.Run(download, func(t *testing.T) {
			service, id, fixture := fixtureService(t, func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != "/service/rest/v1/assets" {
					return false
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"repository": "releases", "format": "maven2", "path": metadataPath, "fileSize": 1, "downloadUrl": download}}, "continuationToken": nil})
				return true
			})
			out, err := service.Discover(context.Background(), id, DiscoveryInput{Repositories: []string{"releases"}})
			if err != nil {
				t.Fatalf("equivalent release URL was refused: %v", err)
			}
			if len(out.Assets) != 1 || out.Assets[0].Path != metadataPath || out.Assets[0].DownloadURL != canonical {
				t.Fatalf("release URL was not reconstructed from its safe metadata path: %#v", out.Assets)
			}
			if out.Stats.Requests != 3 || len(fixture.recordedRequests()) != 3 {
				t.Fatal("discovery must validate the download URL without fetching artifact bytes")
			}
		})
	}
}

func TestNexusDiscoveryKeepsLiteralReservedMetadataPathsUnsupported(t *testing.T) {
	for _, metadataPath := range []string{"literal%name.txt", "literal#name.txt", "literal?name.txt", "%2e%2e/secret", "%252e%252e/secret"} {
		t.Run(metadataPath, func(t *testing.T) {
			service, id, _ := fixtureService(t, func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != "/service/rest/v1/assets" {
					return false
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"repository": "raw-a", "format": "raw", "path": metadataPath, "fileSize": 1, "downloadUrl": "https://example.com/repository/raw-a/a.txt"}}, "continuationToken": nil})
				return true
			})
			_, err := service.Discover(context.Background(), id, DiscoveryInput{Repositories: []string{"raw-a"}})
			requireCode(t, err, "source_invalid_response")
		})
	}
}
