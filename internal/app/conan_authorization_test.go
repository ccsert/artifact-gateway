package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type conanStatusClient struct {
	status int
	body   string
}

func (c conanStatusClient) FetchConan(_ context.Context, _ string, _ repository.Member, _ string, _ http.Header) (*http.Response, error) {
	return &http.Response{StatusCode: c.status, Body: io.NopCloser(strings.NewReader(c.body)), Header: http.Header{}}, nil
}

func TestConanAnonymousReadRequiresPublicGroupAndMember(t *testing.T) {
	store := repository.NewMemoryStore()
	enableAnonymousAccess(t, store)
	for _, group := range []repository.Group{
		{Name: "private", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Anonymous: true}}},
		{Name: "partial", Anonymous: true, Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted}}},
		{Name: "public", Anonymous: true, Members: []repository.Member{{Name: "private", Type: repository.MemberHosted}, {Name: "public", Type: repository.MemberHosted, Anonymous: true}}},
	} {
		if _, err := store.CreateConanGroup(context.Background(), group); err != nil {
			t.Fatal(err)
		}
	}
	client := &conanAnonymousClient{}
	h := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Cache: NewConanCache(nil), Metrics: &Metrics{}}
	path := "/conan/v2/%s/conans/pkg/1.0/user/stable/revisions"
	for _, group := range []string{"private", "partial"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, fmt.Sprintf(path, group), nil))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s status=%d", group, w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, fmt.Sprintf(path, "public"), nil))
	if w.Code != http.StatusOK || client.member != "public" {
		t.Fatalf("response=%d member=%q", w.Code, client.member)
	}
}

func TestConanAnonymousReadDoesNotUsePrivateMemberCache(t *testing.T) {
	store := repository.NewMemoryStore()
	enableAnonymousAccess(t, store)
	private := repository.Member{Name: "private", Type: repository.MemberHosted, Endpoint: "https://private.example"}
	public := repository.Member{Name: "public", Type: repository.MemberHosted, Endpoint: "https://public.example", Anonymous: true}
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Anonymous: true, Members: []repository.Member{private, public}})
	cache := NewConanCache(nil)
	path := "pkg/1.0/user/stable/revisions"
	if err := cache.store(context.Background(), cache.key("central", path, private), conanCacheEntry{body: []byte(`{"revisions":[{"revision":"private","time":1}]}`), contentType: "application/json", member: private.Name, endpoint: private.Endpoint}, "central", 1024, time.Minute); err != nil {
		t.Fatal(err)
	}
	h := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: &conanAnonymousClient{}, Cache: cache, Metrics: &Metrics{}}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/conan/v2/central/conans/"+path, nil))
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "private") {
		t.Fatalf("response=%d body=%q", w.Code, w.Body.String())
	}
}

func TestConanAuthenticatedReadChecksMemberAuthorizationBeforeCache(t *testing.T) {
	store := repository.NewMemoryStore()
	private := repository.Member{Name: "private", Type: repository.MemberHosted, Endpoint: "https://private.example", Position: 0}
	public := repository.Member{Name: "public", Type: repository.MemberHosted, Endpoint: "https://public.example", Position: 1}
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Members: []repository.Member{private, public}})
	cache := NewConanCache(nil)
	path := "pkg/1.0/user/stable/revisions"
	if err := cache.store(context.Background(), cache.key("central", path, private), conanCacheEntry{body: []byte(`{"revisions":[]}`), contentType: "application/json", member: private.Name, endpoint: private.Endpoint}, "central", 1024, time.Minute); err != nil {
		t.Fatal(err)
	}
	auth := Authenticator{ResolverToken: "resolver-secret", RepositoryReaders: map[string][]string{"build-agent": {"central", "public"}}}
	w := httptest.NewRecorder()
	ConanHandler{Store: store, Authenticator: auth, Client: &conanAnonymousClient{}, Cache: cache}.ServeHTTP(w, conanRequest("/conan/v2/central/conans/"+path))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestConanUsesManagedRepositoryGrantsForBoundMembers(t *testing.T) {
	store := repository.NewMemoryStore()
	deniedRepository, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "conan-denied", Name: "conan-denied", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	allowedRepository, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "conan-allowed", Name: "conan-allowed", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(context.Background(), deniedRepository.ID, nil, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(context.Background(), allowedRepository.ID, []repository.RepositoryGrant{{Principal: "reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Members: []repository.Member{
		{Name: "denied", Type: repository.MemberHosted, Endpoint: "https://denied.example", Position: 0, RepositoryID: deniedRepository.ID},
		{Name: "allowed", Type: repository.MemberHosted, Endpoint: "https://allowed.example", Position: 1, RepositoryID: allowedRepository.ID},
	}})
	if err != nil {
		t.Fatal(err)
	}
	authenticator := Authenticator{ResolverToken: "resolver-secret", RepositoryReaders: map[string][]string{"reader": {}}}
	metrics := &Metrics{}
	handler := ConanHandler{
		Store:         store,
		Repositories:  store,
		Authorizer:    RepositoryAuthorizer{Grants: store, Legacy: authenticator},
		Authenticator: authenticator,
		Client:        conanStatusClient{status: http.StatusOK, body: `{"revisions":[{"revision":"abc","time":1}]}`},
		Cache:         NewConanCache(nil),
		Metrics:       metrics,
	}
	path := "/conan/v2/central/conans/pkg/1.0/user/stable/revisions"
	readerRequest := httptest.NewRequest(http.MethodGet, path, nil)
	readerRequest.SetBasicAuth("reader", "resolver-secret")
	readerResponse := httptest.NewRecorder()
	handler.ServeHTTP(readerResponse, readerRequest)
	if readerResponse.Code != http.StatusOK {
		t.Fatalf("reader=%d body=%s", readerResponse.Code, readerResponse.Body.String())
	}
	foundAuthorizationAudit := false
	for _, audit := range store.Audits {
		if audit.MemberName == "denied" && audit.AuthorizationSource == "repository_grants" && audit.AuthorizationReason == "scope_not_granted" {
			foundAuthorizationAudit = true
		}
	}
	if !foundAuthorizationAudit {
		t.Fatalf("audits=%#v", store.Audits)
	}
	deniedRequest := httptest.NewRequest(http.MethodGet, path, nil)
	deniedRequest.SetBasicAuth("other", "resolver-secret")
	deniedResponse := httptest.NewRecorder()
	handler.ServeHTTP(deniedResponse, deniedRequest)
	if deniedResponse.Code != http.StatusForbidden {
		t.Fatalf("denied=%d body=%s", deniedResponse.Code, deniedResponse.Body.String())
	}
	metricResponse := httptest.NewRecorder()
	metrics.Handler(metricResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(metricResponse.Body.String(), `artifact_gateway_repository_authorization_denials_total{format="conan",authorization_source="repository_grants",authorization_reason="scope_not_granted"} 1`) {
		t.Fatalf("Conan authorization metric=%s", metricResponse.Body.String())
	}
}

func TestConanBasicAuthenticationRespectsGroupAndMemberPermissions(t *testing.T) {
	store := repository.NewMemoryStore()
	member := repository.Member{Name: "hosted", Type: repository.MemberHosted, Endpoint: "https://hosted.example"}
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Members: []repository.Member{member}})
	path := "/conan/v2/central/conans/pkg/1.0/user/stable/revisions"

	for name, readers := range map[string][]string{
		"allowed": {"central", "hosted"},
		"denied":  {"central"},
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.SetBasicAuth("conan", "resolver-secret")
			response := httptest.NewRecorder()
			ConanHandler{
				Store:         store,
				Authenticator: Authenticator{ResolverToken: "resolver-secret", RepositoryReaders: map[string][]string{"conan": readers}},
				Client:        &conanAnonymousClient{},
				Cache:         NewConanCache(nil),
			}.ServeHTTP(response, request)
			want := http.StatusOK
			if name == "denied" {
				want = http.StatusForbidden
			}
			if response.Code != want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestConanHandshakeRejectsMissingAndDisabledGroups(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "disabled", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted}}})
	_ = store.DisableConanGroup(context.Background(), "disabled")
	h := ConanHandler{Store: store, Authenticator: Authenticator{ResolverToken: "resolver-secret"}, Client: &conanAnonymousClient{}, Cache: NewConanCache(nil)}
	for _, path := range []string{
		"/conan/missing/v1/ping",
		"/conan/missing/v1/users/authenticate",
		"/conan/disabled/v1/ping",
		"/conan/disabled/v1/users/authenticate",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.SetBasicAuth("reader", "resolver-secret")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("path=%s status=%d", path, response.Code)
		}
	}
}
