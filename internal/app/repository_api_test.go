package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestOpenAPIServeMuxBoundsManagementBodies(t *testing.T) {
	mux := http.NewServeMux()
	handler := openAPIServeMux{mux: mux}
	handler.HandleFunc("POST /payload", func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err == nil {
			t.Error("management body read error = nil, want size limit")
		}
		w.WriteHeader(http.StatusRequestEntityTooLarge)
	})
	request := httptest.NewRequest(
		http.MethodPost,
		"/payload",
		strings.NewReader(strings.Repeat("x", managementJSONBodyLimit+1)),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestOpenAPIServeMuxDoesNotTruncateArtifactUploads(t *testing.T) {
	mux := http.NewServeMux()
	handler := openAPIServeMux{mux: mux}
	body := strings.Repeat("x", managementJSONBodyLimit+1)
	handler.HandleFunc("PUT /api/v2/publish-sessions/{sessionId}/objects/{objectName}", func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil || len(data) != len(body) {
			t.Fatalf("upload body length=%d err=%v", len(data), err)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPut, "/api/v2/publish-sessions/session/objects/artifact.jar", strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
}

type selectiveAdapter struct{ available map[string]bool }

func (a selectiveAdapter) Available(_ context.Context, member repository.Member, _ string) bool {
	return a.available[member.Name]
}

type failingAuditStore struct{ *repository.MemoryStore }

func (f failingAuditStore) RecordAudit(context.Context, repository.AuditRecord) error {
	return errors.New("audit unavailable")
}

type failingGetStore struct{ *repository.MemoryStore }

func (f failingGetStore) GetGroup(context.Context, string) (repository.Group, error) {
	return repository.Group{}, errors.New("database read failed")
}

func testAuthenticator() Authenticator {
	return Authenticator{AdminToken: "admin-secret", ResolverToken: "resolver-secret", AdminActor: "alice", ResolverActor: "build-agent", RepositoryWriters: map[string][]string{"build-agent": {"releases", "deploys"}, "maven": {"releases", "deploys"}}}
}

func authorize(request *http.Request, token string) {
	request.Header.Set("Authorization", "Bearer "+token)
}

func TestRepositoryAuthorizationMetricsUseOnlyBoundedGrantLabels(t *testing.T) {
	metrics := &Metrics{}
	metrics.recordRepositoryAuthorizationDenied("raw", "repository_grants", "scope_not_granted")
	metrics.recordRepositoryAuthorizationDenied("management", "repository_grants", "grant_lookup_failed")
	metrics.recordRepositoryAuthorizationDenied("raw", "legacy_static", "scope_not_granted")
	metrics.recordRepositoryAuthorizationDenied("untrusted-format", "repository_grants", "scope_not_granted")
	metrics.recordRepositoryAuthorizationDenied("raw", "repository_grants", "untrusted-reason")

	response := httptest.NewRecorder()
	metrics.Handler(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := response.Body.String()
	for _, want := range []string{
		`artifact_gateway_repository_authorization_denials_total{format="raw",authorization_source="repository_grants",authorization_reason="scope_not_granted"} 1`,
		`artifact_gateway_repository_authorization_denials_total{format="management",authorization_source="repository_grants",authorization_reason="grant_lookup_failed"} 1`,
		`artifact_gateway_repository_authorization_denials_total{format="raw",authorization_source="repository_grants",authorization_reason="grant_lookup_failed"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q:\n%s", want, body)
		}
	}
	for _, unexpected := range []string{"legacy_static", "untrusted-format", "untrusted-reason"} {
		if strings.Contains(body, unexpected) {
			t.Fatalf("unbounded authorization label %q in:\n%s", unexpected, body)
		}
	}
}

func TestBackgroundOperationMetricsUseOnlyBoundedLabels(t *testing.T) {
	metrics := &Metrics{}
	metrics.RecordBackgroundOperation("promotion", repository.FormatRaw, "started")
	metrics.RecordBackgroundOperation("promotion", repository.FormatRaw, "completed")
	metrics.RecordBackgroundOperation("unknown", repository.FormatRaw, "started")
	metrics.RecordBackgroundOperation("promotion", repository.FormatRaw, "unknown")
	metrics.AddBackgroundOperationInFlight("promotion", repository.FormatRaw, 1)
	metrics.AddBackgroundOperationInFlight("promotion", repository.FormatRaw, -1)
	response := httptest.NewRecorder()
	metrics.Handler(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := response.Body.String()
	for _, want := range []string{
		`artifact_gateway_background_operations_total{kind="promotion",format="raw",outcome="started"} 1`,
		`artifact_gateway_background_operations_total{kind="promotion",format="raw",outcome="completed"} 1`,
		`artifact_gateway_background_operations_in_flight{kind="promotion",format="raw"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "unknown") {
		t.Fatalf("unbounded background metric label in:\n%s", body)
	}
}

func TestGroupManagementAndResolverVerticalSlice(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, selectiveAdapter{available: map[string]bool{"proxy": true}}, testAuthenticator())
	group := `{"name":"engineering","members":[{"name":"hosted","type":"hosted","endpoint":"http://legacy","position":0},{"name":"proxy","type":"proxy","endpoint":"https://registry.example","position":1}]}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/oci/groups", strings.NewReader(group))
	authorize(request, "admin-secret")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, request)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", created.Code, created.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/oci/groups/engineering", nil)
	authorize(request, "admin-secret")
	got := httptest.NewRecorder()
	handler.ServeHTTP(got, request)
	if got.Code != http.StatusOK {
		t.Fatalf("get status = %d", got.Code)
	}
	var stored repository.Group
	if err := json.NewDecoder(got.Body).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	if !stored.Enabled || len(stored.Members) != 2 || stored.Members[0].Name != "hosted" || stored.Members[1].Type != repository.MemberProxy {
		t.Fatalf("stored group = %#v", stored)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/oci/groups/engineering/resolve?repository=team/app", nil)
	authorize(request, "resolver-secret")
	resolved := httptest.NewRecorder()
	handler.ServeHTTP(resolved, request)
	if resolved.Code != http.StatusOK || !strings.Contains(resolved.Body.String(), `"name":"proxy"`) {
		t.Fatalf("resolve = %d %s", resolved.Code, resolved.Body.String())
	}
	if len(store.Audits) != 1 || store.Audits[0].Outcome != repository.AuditResolved || store.Audits[0].MemberName != "proxy" {
		t.Fatalf("audits = %#v", store.Audits)
	}
	if store.Audits[0].Actor != "build-agent" {
		t.Fatalf("audit actor = %q", store.Audits[0].Actor)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/oci/groups/engineering/disable", nil)
	authorize(request, "admin-secret")
	disabled := httptest.NewRecorder()
	handler.ServeHTTP(disabled, request)
	if disabled.Code != http.StatusNoContent {
		t.Fatalf("disable status = %d", disabled.Code)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/oci/groups/engineering/resolve?repository=team/app", nil)
	authorize(request, "resolver-secret")
	blocked := httptest.NewRecorder()
	handler.ServeHTTP(blocked, request)
	if blocked.Code != http.StatusConflict || !strings.Contains(blocked.Body.String(), `group_disabled`) {
		t.Fatalf("disabled resolve = %d %s", blocked.Code, blocked.Body.String())
	}
}

func TestAPIRejectsUnauthenticatedAndInvalidGroup(t *testing.T) {
	handler := NewGatewayHandler(Dependencies{}, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator())
	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodPost, "/api/v1/oci/groups", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", unauthenticated.Code)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/oci/groups", strings.NewReader(`{"name":"bad/name","members":[]}`))
	authorize(request, "admin-secret")
	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, request)
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), `"code":"invalid_group"`) {
		t.Fatalf("invalid = %d %s", invalid.Code, invalid.Body.String())
	}
}

func TestRawGroupContractAndExactCacheInvalidation(t *testing.T) {
	store := repository.NewMemoryStore()
	objectStore := NewMemoryOCIObjectStore()
	rawCache := NewRawCache(objectStore, time.Hour, time.Hour, nil).WithQuota(NewCacheQuota(objectStore, nil))
	handler := NewGatewayHandlerWithRawCache(Dependencies{}, store, TestAdapter{}, testAuthenticator(), NewDefaultOCICache(objectStore, nil), nil, rawCache, nil)

	for _, payload := range []string{
		`{"name":"Raw","cacheQuotaBytes":5,"members":[{"name":"hosted","type":"hosted","endpoint":"http://legacy","position":0}]}`,
		`{"name":"api","cacheQuotaBytes":5,"members":[{"name":"hosted","type":"hosted","endpoint":"http://legacy","position":0}]}`,
		`{"name":"downloads","cacheQuotaBytes":5,"members":[{"name":"proxy","type":"proxy","endpoint":"https://proxy.example","position":0}]}`,
		`{"name":"downloads","cacheQuotaBytes":0,"members":[{"name":"hosted","type":"hosted","endpoint":"http://legacy","position":0}]}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/raw/groups", strings.NewReader(payload))
		authorize(request, "admin-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid Raw group status=%d body=%s", response.Code, response.Body.String())
		}
	}

	group := `{"name":"downloads","cacheQuotaBytes":5,"members":[{"name":"hosted","type":"hosted","endpoint":"http://legacy","position":0},{"name":"proxy","type":"proxy","endpoint":"https://proxy.example","position":1,"allowedHosts":["proxy.example"]}]}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/raw/groups", strings.NewReader(group))
	authorize(request, "admin-secret")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, request)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"cacheQuotaBytes":5`) {
		t.Fatalf("Raw group creation=%d %s", created.Code, created.Body.String())
	}
	hostedKey := rawCache.Key("downloads", "release/app.txt", "hosted", "http://legacy")
	proxyKey := rawCache.Key("downloads", "release/app.txt", "proxy", "https://proxy.example")
	otherKey := rawCache.Key("downloads", "release/other.txt", "hosted", "http://legacy")
	for _, key := range []string{hostedKey, proxyKey, otherKey} {
		if err := rawCache.Store(context.Background(), key, RawContent{Body: []byte(key), Repository: "downloads", CacheQuotaBytes: 10000}); err != nil {
			t.Fatal(err)
		}
	}
	invalidate := httptest.NewRequest(http.MethodPost, "/api/v1/raw/cache/invalidate", strings.NewReader(`{"group":"downloads","path":"release/app.txt","member":"proxy","endpoint":"https://proxy.example"}`))
	authorize(invalidate, "admin-secret")
	invalidated := httptest.NewRecorder()
	handler.ServeHTTP(invalidated, invalidate)
	if invalidated.Code != http.StatusNoContent {
		t.Fatalf("invalidation=%d %s", invalidated.Code, invalidated.Body.String())
	}
	if _, err := rawCache.Load(context.Background(), proxyKey); err == nil {
		t.Fatalf("proxy cache after invalidation = %v", err)
	}
	if _, err := rawCache.Load(context.Background(), hostedKey); err != nil {
		t.Fatalf("hosted cache was invalidated: %v", err)
	}
	if _, err := rawCache.Load(context.Background(), otherKey); err != nil {
		t.Fatalf("unrelated Raw cache was invalidated: %v", err)
	}
	missingMember := httptest.NewRequest(http.MethodPost, "/api/v1/raw/cache/invalidate", strings.NewReader(`{"group":"downloads","path":"release/app.txt"}`))
	authorize(missingMember, "admin-secret")
	missingMemberResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingMemberResponse, missingMember)
	if missingMemberResponse.Code != http.StatusBadRequest {
		t.Fatalf("memberless invalidation=%d %s", missingMemberResponse.Code, missingMemberResponse.Body.String())
	}
	for _, payload := range []string{
		`{"group":"downloads","path":"release/%2e%2e/private","member":"proxy","endpoint":"https://proxy.example"}`,
		`{"group":"downloads","path":"release/app.txt","member":"missing","endpoint":"https://proxy.example"}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/raw/cache/invalidate", strings.NewReader(payload))
		authorize(request, "admin-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest && response.Code != http.StatusNotFound {
			t.Fatalf("invalid invalidation=%d %s", response.Code, response.Body.String())
		}
	}
}

func TestConanCacheInvalidationTargetsMemberAndEndpoint(t *testing.T) {
	store := repository.NewMemoryStore()
	first := repository.Member{Name: "hosted", Type: repository.MemberHosted, Endpoint: "https://one.example", Position: 0}
	second := repository.Member{Name: "proxy", Type: repository.MemberProxy, Endpoint: "https://two.example", Position: 1}
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Members: []repository.Member{first, second}})
	cache := NewConanCache(nil)
	path := "pkg/1.0/user/stable/revisions"
	for _, member := range []repository.Member{first, second} {
		if err := cache.store(context.Background(), cache.key("central", path, member), conanCacheEntry{body: []byte(member.Name), member: member.Name, endpoint: member.Endpoint}, "central", 1024, time.Hour, "central", path, ""); err != nil {
			t.Fatal(err)
		}
	}
	handler := conanCacheInvalidationHandler{store: store, authenticator: testAuthenticator(), cache: cache}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/conan/cache/invalidate", strings.NewReader(`{"group":"central","path":"pkg/1.0/user/stable/revisions","member":"proxy","endpoint":"https://two.example"}`))
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if _, ok := cache.load(context.Background(), cache.key("central", path, first)); !ok {
		t.Fatal("unrelated member was invalidated")
	}
	if _, ok := cache.load(context.Background(), cache.key("central", path, second)); ok {
		t.Fatal("target member was not invalidated")
	}
}

func TestConanGroupAPIRequiresProxyAllowlistAndDefaultsQuota(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	handler := NewGatewayHandlerWithFormatCaches(Dependencies{}, store, TestAdapter{}, testAuthenticator(), NewDefaultOCICache(objects, nil), nil, nil, NewConanCache(nil), nil)
	for _, test := range []struct {
		name, body string
		want       int
	}{
		{"legacy-default", `{"name":"central","members":[{"name":"hosted","type":"hosted","endpoint":"https://hosted.example","position":0}]}`, http.StatusCreated},
		{"missing-proxy-allowlist", `{"name":"blocked","members":[{"name":"proxy","type":"proxy","endpoint":"https://proxy.example","position":0}]}`, http.StatusBadRequest},
		{"member-allowlist", `{"name":"proxy","cacheQuotaBytes":7,"members":[{"name":"proxy","type":"proxy","endpoint":"https://proxy.example","position":0,"allowedHosts":["proxy.example"]}]}`, http.StatusCreated},
		{"uppercase-name", `{"name":"Metrics","members":[{"name":"hosted","type":"hosted","endpoint":"https://hosted.example","position":0}]}`, http.StatusBadRequest},
		{"reserved-name", `{"name":"conan","members":[{"name":"hosted","type":"hosted","endpoint":"https://hosted.example","position":0}]}`, http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/v1/conan/groups", strings.NewReader(test.body))
			authorize(r, "admin-secret")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != test.want {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
	group, err := store.GetConanGroup(context.Background(), "central")
	if err != nil || group.CacheQuotaBytes != 1<<30 {
		t.Fatalf("group=%#v err=%v", group, err)
	}
	group, err = store.GetConanGroup(context.Background(), "proxy")
	if err != nil || len(group.Members[0].AllowedHosts) != 1 || group.Members[0].AllowedHosts[0] != "proxy.example" {
		t.Fatalf("group=%#v err=%v", group, err)
	}
}

func TestConanGroupAPIValidatesManagedRepositoryBinding(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "conan-repository", Name: "conan-remote", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	create := func(group, repositoryID string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/conan/groups", strings.NewReader(`{"name":"`+group+`","members":[{"name":"remote","type":"hosted","endpoint":"https://conan.example","repositoryId":"`+repositoryID+`"}]}`))
		authorize(r, "admin-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if response := create("central", repo.ID); response.Code != http.StatusCreated {
		t.Fatalf("valid binding=%d body=%s", response.Code, response.Body.String())
	}
	if response := create("invalid", "missing"); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid binding=%d body=%s", response.Code, response.Body.String())
	}
}

func TestResolverTokenCannotManageGroups(t *testing.T) {
	handler := NewGatewayHandler(Dependencies{}, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodPost, "/api/v1/oci/groups", strings.NewReader(`{"name":"engineering","members":[{"name":"hosted","type":"hosted","endpoint":"test://available","position":0}]}`))
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"forbidden"`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestRepositoryReadPermissionRejectsAndAuditsUnauthorizedOCIResolution(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "test://available", Position: 0}}})
	authenticator := testAuthenticator()
	authenticator.RepositoryReaders = map[string][]string{"build-agent": {"team/allowed/*"}}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/oci/groups/team/resolve?repository=team/denied/app", nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"forbidden"`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if len(store.Audits) != 1 || store.Audits[0].Outcome != repository.AuditAccessDenied || store.Audits[0].Repository != "team/denied/app" {
		t.Fatalf("audits = %#v", store.Audits)
	}
}

func TestAdminCanQueryRepositoryAudits(t *testing.T) {
	store := repository.NewMemoryStore()
	store.Audits = []repository.AuditRecord{
		{GroupName: "engineering", Repository: "team/old", Actor: "alice", Outcome: repository.AuditResolved},
		{GroupName: "engineering", Repository: "team/app", Actor: "bob", Outcome: repository.AuditUpstreamError},
		{GroupName: "engineering", Repository: "team/app", Actor: "carol", Outcome: repository.AuditResolved},
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())

	request := httptest.NewRequest(http.MethodGet, "/api/v1/audits?group=engineering&repository=team/app", nil)
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var audits []repository.AuditRecord
	if err := json.NewDecoder(response.Body).Decode(&audits); err != nil {
		t.Fatal(err)
	}
	if len(audits) != 2 || audits[0].Actor != "carol" || audits[1].Actor != "bob" {
		t.Fatalf("audits = %#v", audits)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/audits?repository=team/app", nil)
	authorize(request, "resolver-secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("resolver status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestResolverFailsWhenAuditCannotBeRecorded(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "group", Members: []repository.Member{{Name: "one", Type: repository.MemberHosted, Endpoint: "test://available", Position: 0}}})
	handler := NewGatewayHandler(Dependencies{}, failingAuditStore{store}, TestAdapter{}, testAuthenticator())
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/oci/groups/group/resolve?repository=app", nil)
	authorize(request, "resolver-secret")
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), `resolver_error`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	metrics := httptest.NewRecorder()
	handler.ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(metrics.Body.String(), `outcome="failed"} 1`) {
		t.Fatalf("metrics = %s", metrics.Body.String())
	}
}

func TestResolverAuditsStorageFailureAccurately(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, failingGetStore{store}, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodGet, "/api/v1/oci/groups/group/resolve?repository=app", nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), `resolver_error`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if len(store.Audits) != 1 || store.Audits[0].Outcome != repository.AuditStorageError {
		t.Fatalf("audits = %#v", store.Audits)
	}
	metrics := httptest.NewRecorder()
	handler.ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(metrics.Body.String(), `outcome="failed"} 1`) {
		t.Fatalf("metrics = %s", metrics.Body.String())
	}
}

func TestMetricsReportResolverOutcomes(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "group", Members: []repository.Member{{Name: "one", Type: repository.MemberHosted, Endpoint: "http://legacy", Position: 0}}})
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/oci/groups/group/resolve?repository=app", nil)
	authorize(request, "resolver-secret")
	handler.ServeHTTP(response, request)
	metrics := httptest.NewRecorder()
	handler.ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(metrics.Body.String(), `outcome="resolved"} 1`) {
		t.Fatalf("metrics = %s", metrics.Body.String())
	}
}

func TestRepositoryMetricsUseCanonicalNamesAndBoundCardinality(t *testing.T) {
	metrics := &Metrics{}
	metrics.recordRequest("engineering/com/example/library/1.0/library-1.0.jar")
	metrics.recordCache("engineering/com/example/library/1.0/library-1.0.jar", true)
	metrics.recordAudit("engineering/com/example/library/1.0/library-1.0.jar", repository.AuditUpstreamError)
	metric := metrics.repository("engineering/com/example/library/1.0/library-1.0.jar")
	if metric.Requests != 1 || metric.CacheHits != 1 || metric.UpstreamErrors != 1 {
		t.Fatalf("metric = %#v", metric)
	}
	for i := 0; i < maxRepositoryMetrics+1; i++ {
		metrics.recordRequest(fmt.Sprintf("team/app-%d", i))
	}
	if len(metrics.repositories) != maxRepositoryMetrics {
		t.Fatalf("repository metric count = %d", len(metrics.repositories))
	}
}

func TestMetricsCountDisabledAndMissingGroupsAsFailures(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "disabled", Members: []repository.Member{{Name: "one", Type: repository.MemberHosted, Endpoint: "test://available", Position: 0}}})
	if err := store.DisableGroup(context.Background(), "disabled"); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	for _, target := range []string{"/api/v1/oci/groups/missing/resolve?repository=app", "/api/v1/oci/groups/disabled/resolve?repository=app", "/api/v1/oci/groups/disabled/resolve"} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		authorize(request, "resolver-secret")
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}
	metrics := httptest.NewRecorder()
	handler.ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(metrics.Body.String(), `outcome="failed"} 3`) {
		t.Fatalf("metrics = %s", metrics.Body.String())
	}
}

func TestGroupMemberRepositoryBindingsRequireMatchingFormat(t *testing.T) {
	store := repository.NewMemoryStore()
	oci, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "oci-target", Name: "oci-target", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	maven, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "maven-target", Name: "maven-target", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "raw-target", Name: "raw-target", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, repositoryID string
		format             repository.Format
	}{
		{name: "OCI", repositoryID: oci.ID, format: repository.FormatOCI},
		{name: "Maven", repositoryID: maven.ID, format: repository.FormatMaven},
		{name: "Raw", repositoryID: raw.ID, format: repository.FormatRaw},
	} {
		t.Run(test.name, func(t *testing.T) {
			group := repository.Group{Members: []repository.Member{{RepositoryID: test.repositoryID}}}
			if err := validateGroupRepositoryBindings(context.Background(), store, group, test.format); err != nil {
				t.Fatal(err)
			}
			group.Members[0].RepositoryID = "missing"
			if err := validateGroupRepositoryBindings(context.Background(), store, group, test.format); err == nil {
				t.Fatal("missing repository binding was accepted")
			}
		})
	}
	if err := validateGroupRepositoryBindings(context.Background(), store, repository.Group{Members: []repository.Member{{RepositoryID: raw.ID}}}, repository.FormatMaven); err == nil {
		t.Fatal("format-mismatched repository binding was accepted")
	}
}

func TestListGroupsEndpoints(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())

	create := func(path, payload string) {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(payload))
		authorize(request, "admin-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusCreated {
			t.Fatalf("create %s status = %d, body=%s", path, response.Code, response.Body.String())
		}
	}
	create("/api/v1/oci/groups", `{"name":"oci-a","members":[{"name":"hosted","type":"hosted","endpoint":"http://legacy","position":0}]}`)
	create("/api/v1/oci/groups", `{"name":"oci-b","members":[{"name":"proxy","type":"proxy","endpoint":"https://registry.example","position":0}]}`)
	create("/api/v1/maven/groups", `{"name":"maven-a","members":[{"name":"hosted","type":"hosted","endpoint":"http://legacy","position":0}]}`)
	create("/api/v1/maven/groups", `{"name":"maven-b","members":[{"name":"proxy","type":"proxy","endpoint":"https://maven.example","position":0}]}`)
	create("/api/v1/raw/groups", `{"name":"raw-a","cacheQuotaBytes":5,"members":[{"name":"hosted","type":"hosted","endpoint":"http://legacy","position":0}]}`)
	create("/api/v1/raw/groups", `{"name":"raw-b","cacheQuotaBytes":5,"members":[{"name":"proxy","type":"proxy","endpoint":"https://raw.example","position":0,"allowedHosts":["raw.example"]}]}`)
	create("/api/v1/conan/groups", `{"name":"conan-a","members":[{"name":"hosted","type":"hosted","endpoint":"http://legacy","position":0}]}`)
	create("/api/v1/conan/groups", `{"name":"conan-b","members":[{"name":"proxy","type":"proxy","endpoint":"https://conan.example","position":0,"allowedHosts":["conan.example"]}]}`)

	for _, test := range []struct {
		path  string
		names []string
	}{
		{"/api/v1/oci/groups", []string{"oci-a", "oci-b"}},
		{"/api/v1/maven/groups", []string{"maven-a", "maven-b"}},
		{"/api/v1/raw/groups", []string{"raw-a", "raw-b"}},
		{"/api/v1/conan/groups", []string{"conan-a", "conan-b"}},
	} {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		authorize(request, "admin-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("list %s status = %d, body=%s", test.path, response.Code, response.Body.String())
		}
		var body struct {
			Items []repository.Group `json:"items"`
		}
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatalf("list %s decode: %v", test.path, err)
		}
		if len(body.Items) != len(test.names) {
			t.Fatalf("list %s returned %d items: %s", test.path, len(body.Items), response.Body.String())
		}
		for i, name := range test.names {
			if body.Items[i].Name != name {
				t.Fatalf("list %s item %d name = %q, want %q", test.path, i, body.Items[i].Name, name)
			}
			if len(body.Items[i].Members) != 1 {
				t.Fatalf("list %s item %q has %d members", test.path, name, len(body.Items[i].Members))
			}
		}
	}
}

func TestListGroupsRequiresAdmin(t *testing.T) {
	handler := NewGatewayHandler(Dependencies{}, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator())
	// rawAPIHandler collapses authentication and admin checks into a single 403.
	unauthenticatedStatus := map[string]int{
		"/api/v1/oci/groups":   http.StatusUnauthorized,
		"/api/v1/maven/groups": http.StatusUnauthorized,
		"/api/v1/raw/groups":   http.StatusForbidden,
		"/api/v1/conan/groups": http.StatusUnauthorized,
	}
	for path, want := range unauthenticatedStatus {
		unauthenticated := httptest.NewRecorder()
		handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, path, nil))
		if unauthenticated.Code != want {
			t.Fatalf("unauthenticated list %s status = %d, want %d", path, unauthenticated.Code, want)
		}
		request := httptest.NewRequest(http.MethodGet, path, nil)
		authorize(request, "resolver-secret")
		forbidden := httptest.NewRecorder()
		handler.ServeHTTP(forbidden, request)
		if forbidden.Code != http.StatusForbidden {
			t.Fatalf("non-admin list %s status = %d", path, forbidden.Code)
		}
	}
}
