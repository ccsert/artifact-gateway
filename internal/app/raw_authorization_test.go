package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestRawRejectsUnsafePathsAndUnauthorizedRequests(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateRawGroup(context.Background(), repository.Group{Name: "downloads", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "http://legacy.local"}}})
	h := RawHandler{Store: store, Authenticator: testAuthenticator(), Client: &rawFixtureClient{responses: map[string]int{"hosted": http.StatusOK}}, Metrics: &Metrics{}}
	for _, path := range []string{"/raw/downloads/../secret", "/raw/downloads/%2e%2e/secret"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, rawRequest(http.MethodGet, path))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, rawRequest(http.MethodGet, "/raw/downloads/release/"))
	if w.Code != http.StatusNotFound {
		t.Fatalf("directory status=%d", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/raw/downloads/a", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated=%d", w.Code)
	}
}

func TestRawAnonymousPolicyRequiresPublicGroupAndMember(t *testing.T) {
	store := repository.NewMemoryStore()
	enableAnonymousAccess(t, store)
	for _, group := range []repository.Group{
		{Name: "private", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Anonymous: true}}},
		{Name: "member-private", Anonymous: true, Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted}}},
		{Name: "public", Anonymous: true, Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Anonymous: true}}},
	} {
		if _, err := store.CreateRawGroup(context.Background(), group); err != nil {
			t.Fatal(err)
		}
	}
	client := &rawFixtureClient{responses: map[string]int{"hosted": http.StatusOK}, body: []byte("artifact")}
	h := RawHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Metrics: &Metrics{}, Cache: NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, nil)}
	for group, want := range map[string]int{"private": http.StatusUnauthorized, "member-private": http.StatusUnauthorized, "public": http.StatusOK} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/raw/"+group+"/artifact", nil))
		if w.Code != want {
			t.Errorf("%s: got %d want %d", group, w.Code, want)
		}
	}
	var anonymousResolved bool
	for _, audit := range store.Audits {
		anonymousResolved = anonymousResolved || audit.GroupName == "public" && audit.Actor == "anonymous" && audit.Outcome == repository.AuditResolved
	}
	if !anonymousResolved {
		t.Fatalf("anonymous audit=%#v", store.Audits)
	}
}

func TestRawDoesNotExposeOCIGroupAndChecksMemberGrantBeforeCache(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "shared", Members: []repository.Member{{Name: "oci", Type: repository.MemberHosted}}})
	client := &rawFixtureClient{responses: map[string]int{"hosted": http.StatusOK}, body: []byte("artifact")}
	denied := RawHandler{Store: store, Authenticator: Authenticator{ResolverToken: "resolver-secret", RepositoryReaders: map[string][]string{"build-agent": {"downloads"}}}, Client: client, Cache: NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, nil)}
	w := httptest.NewRecorder()
	denied.ServeHTTP(w, rawRequest(http.MethodGet, "/raw/shared/a"))
	if w.Code != http.StatusNotFound {
		t.Fatalf("OCI group exposed as Raw: %d", w.Code)
	}
	_, _ = store.CreateRawGroup(context.Background(), repository.Group{Name: "downloads", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted}}})
	w = httptest.NewRecorder()
	denied.ServeHTTP(w, rawRequest(http.MethodGet, "/raw/downloads/a"))
	if w.Code != http.StatusForbidden || len(client.Calls()) != 0 {
		t.Fatalf("member grant status=%d calls=%v", w.Code, client.Calls())
	}
}

func TestRawUsesManagedGrantsForBoundMembers(t *testing.T) {
	store := repository.NewMemoryStore()
	deniedRepository, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "raw-denied", Name: "raw-denied", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	allowedRepository, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "raw-allowed", Name: "raw-allowed", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(context.Background(), deniedRepository.ID, nil, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(context.Background(), allowedRepository.ID, []repository.RepositoryGrant{{Principal: "build-agent", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateRawGroup(context.Background(), repository.Group{Name: "downloads", Members: []repository.Member{
		{Name: "denied", Type: repository.MemberHosted, Endpoint: "https://denied.example", Position: 0, RepositoryID: deniedRepository.ID},
		{Name: "allowed", Type: repository.MemberHosted, Endpoint: "https://allowed.example", Position: 1, RepositoryID: allowedRepository.ID},
	}})
	if err != nil {
		t.Fatal(err)
	}
	authenticator := Authenticator{ResolverToken: "resolver-secret", ResolverActor: "build-agent", RepositoryReaders: map[string][]string{"build-agent": {"downloads"}}}
	client := &rawFixtureClient{responses: map[string]int{"denied": http.StatusOK, "allowed": http.StatusOK}, body: []byte("artifact")}
	metrics := &Metrics{}
	handler := RawHandler{Store: store, Repositories: store, Authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator}, Authenticator: authenticator, Client: client, Metrics: metrics, Cache: NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, nil)}

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, rawRequest(http.MethodGet, "/raw/downloads/release/app.txt"))
	if w.Code != http.StatusOK || w.Body.String() != "artifact" || strings.Join(client.Calls(), ",") != "allowed" {
		t.Fatalf("response=%d body=%q calls=%v", w.Code, w.Body.String(), client.Calls())
	}
	var foundDeniedAudit bool
	for _, audit := range store.Audits {
		if audit.MemberName == "denied" && audit.Outcome == repository.AuditAccessDenied && audit.AuthorizationSource == "repository_grants" && audit.AuthorizationReason == "scope_not_granted" {
			foundDeniedAudit = true
		}
	}
	if !foundDeniedAudit {
		t.Fatalf("missing managed denial audit: %#v", store.Audits)
	}
	metricResponse := httptest.NewRecorder()
	metrics.Handler(metricResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(metricResponse.Body.String(), `artifact_gateway_repository_authorization_denials_total{format="raw",authorization_source="repository_grants",authorization_reason="scope_not_granted"} 1`) {
		t.Fatalf("authorization metrics=%s", metricResponse.Body.String())
	}
}

func TestRawPreservesAuthenticatedGlobalRoleAcrossProtocolBoundary(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "raw-role-target", Name: "raw-role-target", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, nil, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateRawGroup(ctx, repository.Group{Name: "role-downloads", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "https://hosted.example", RepositoryID: repo.ID}}}); err != nil {
		t.Fatal(err)
	}
	authenticator := Authenticator{ResolverToken: "resolver-secret", RepositoryReaders: map[string][]string{}}
	handler := RawHandler{
		Store: store, Repositories: store,
		Authorizer:    RepositoryAuthorizer{Grants: store, Legacy: authenticator},
		Authenticator: authenticator,
		Client:        &rawFixtureClient{responses: map[string]int{"hosted": http.StatusOK}, body: []byte("artifact")},
	}
	for _, role := range []Role{RoleReader, RoleWriter} {
		request := httptest.NewRequest(http.MethodGet, "/raw/role-downloads/release/app.txt", nil)
		request.Header.Set("Authorization", "Bearer "+authenticator.IssuePrincipalToken(Principal{Actor: string(role), Role: role}))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Body.String() != "artifact" {
			t.Fatalf("role %s: status=%d body=%q", role, response.Code, response.Body.String())
		}
	}
}

func TestRawBoundGrantDenialDoesNotServeCachedSource(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "raw-cached", Name: "raw-cached", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(context.Background(), repo.ID, []repository.RepositoryGrant{{Principal: "build-agent", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	member := repository.Member{Name: "cached", Type: repository.MemberHosted, Endpoint: "https://cached.example", RepositoryID: repo.ID}
	if _, err := store.CreateRawGroup(context.Background(), repository.Group{Name: "downloads", Members: []repository.Member{member}}); err != nil {
		t.Fatal(err)
	}
	authenticator := Authenticator{ResolverToken: "resolver-secret", ResolverActor: "build-agent", RepositoryReaders: map[string][]string{"build-agent": {"downloads"}}}
	client := &rawFixtureClient{responses: map[string]int{"cached": http.StatusOK}, body: []byte("artifact")}
	cache := NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, nil)
	handler := RawHandler{Store: store, Repositories: store, Authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator}, Authenticator: authenticator, Client: client, Cache: cache}

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, rawRequest(http.MethodGet, "/raw/downloads/release/app.txt"))
	if first.Code != http.StatusOK || len(client.Calls()) != 1 {
		t.Fatalf("first response=%d calls=%v", first.Code, client.Calls())
	}
	if _, err := store.ReplaceRepositoryGrants(context.Background(), repo.ID, nil, "2"); err != nil {
		t.Fatal(err)
	}
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, rawRequest(http.MethodGet, "/raw/downloads/release/app.txt"))
	if second.Code != http.StatusForbidden || len(client.Calls()) != 1 {
		t.Fatalf("cached grant denial response=%d calls=%v", second.Code, client.Calls())
	}
}
