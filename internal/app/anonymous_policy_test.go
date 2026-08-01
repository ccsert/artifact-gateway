package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func enableAnonymousAccess(t *testing.T, store *repository.MemoryStore) {
	t.Helper()
	policy, err := store.GetAnonymousAccessPolicy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceAnonymousAccessPolicy(context.Background(), repository.AnonymousAccessPolicy{Enabled: true}, policy.Version); err != nil {
		t.Fatal(err)
	}
}

func TestAnonymousPolicyRequiresGroupAndMember(t *testing.T) {
	store := repository.NewMemoryStore()
	groups := []repository.Group{
		{Name: "disabled", Anonymous: true, Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Anonymous: true}}},
		{Name: "private", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Anonymous: true}}},
		{Name: "partial", Anonymous: true, Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted}}},
		{Name: "public", Anonymous: true, Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Anonymous: true}}},
	}
	for _, group := range groups {
		if _, err := store.CreateGroup(context.Background(), group); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.DisableGroup(context.Background(), "disabled"); err != nil {
		t.Fatal(err)
	}
	h := OCIHandler{Resolver: Resolver{Store: store}}
	if h.anonymousOCIAllowed(context.Background(), "public") {
		t.Fatal("anonymous reads allowed while global policy is disabled")
	}
	enableAnonymousAccess(t, store)
	for _, tc := range []struct {
		group string
		want  bool
	}{{"disabled", false}, {"private", false}, {"partial", false}, {"public", true}} {
		if got := h.anonymousOCIAllowed(context.Background(), tc.group); got != tc.want {
			t.Errorf("group %s: got %v want %v", tc.group, got, tc.want)
		}
	}
}

func TestAnonymousHostedRepositoryReadRequiresGlobalPolicy(t *testing.T) {
	store := repository.NewMemoryStore()
	repo := repository.HostedRepository{AnonymousRead: true}
	if anonymousHostedRepositoryReadAllowed(context.Background(), store, repo, http.MethodGet) {
		t.Fatal("repository policy bypassed disabled global policy")
	}
	enableAnonymousAccess(t, store)
	if !anonymousHostedRepositoryReadAllowed(context.Background(), store, repo, http.MethodGet) {
		t.Fatal("global and repository policies did not admit anonymous read")
	}
}

func TestAnonymousPolicyDenialsAreAuditedBeforeProtocolResponses(t *testing.T) {
	store := repository.NewMemoryStore()
	metrics := &Metrics{}
	if _, err := store.CreateGroup(context.Background(), repository.Group{Name: "private", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateMavenGroup(context.Background(), repository.Group{Name: "private", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateConanGroup(context.Background(), repository.Group{Name: "private", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted}}}); err != nil {
		t.Fatal(err)
	}

	oci := OCIHandler{Resolver: Resolver{Store: store, Metrics: metrics}, Authenticator: testAuthenticator()}
	maven := MavenHandler{Store: store, Authenticator: testAuthenticator(), Metrics: metrics}
	conan := ConanHandler{Store: store, Authenticator: testAuthenticator(), Metrics: metrics}
	requests := []struct {
		handler http.Handler
		method  string
		path    string
		status  int
		format  string
	}{
		{oci, http.MethodGet, "/v2/private/app/manifests/latest", http.StatusUnauthorized, "oci"},
		{oci, http.MethodPost, "/v2/private/app/manifests/latest", http.StatusMethodNotAllowed, "oci"},
		{maven, http.MethodGet, "/maven/private/com/example/app.pom", http.StatusUnauthorized, "maven"},
		{maven, http.MethodPost, "/maven/private/com/example/app.pom", http.StatusMethodNotAllowed, "maven"},
		{conan, http.MethodPost, "/conan/v2/private/conans/pkg/1.0/u/c/revisions", http.StatusNotFound, "conan"},
	}
	for _, tc := range requests {
		response := httptest.NewRecorder()
		tc.handler.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
		if response.Code != tc.status {
			t.Fatalf("%s %s status=%d want=%d", tc.method, tc.path, response.Code, tc.status)
		}
		last := store.Audits[len(store.Audits)-1]
		outcome := repository.AuditAccessDenied
		if tc.format == "conan" {
			outcome = repository.AuditNotFound
		}
		if last.Actor != "anonymous" || last.Outcome != outcome || last.Format != tc.format || last.Operation != strings.ToLower(tc.method) || last.Status != tc.status || last.CacheDisposition != "bypass" {
			t.Fatalf("%s %s audit=%#v", tc.method, tc.path, last)
		}
	}
	if got := metrics.anonymousReads.Load(); got != 0 {
		t.Fatalf("anonymous reads=%d want=0", got)
	}
}

func TestAnonymousConanPolicyNeverAllowsWrites(t *testing.T) {
	store := repository.NewMemoryStore()
	if _, err := store.ReplaceAnonymousAccessPolicy(context.Background(), repository.AnonymousAccessPolicy{Enabled: true}, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateConanGroup(context.Background(), repository.Group{Name: "public", Anonymous: true, Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Anonymous: true}}}); err != nil {
		t.Fatal(err)
	}
	h := ConanHandler{Store: store, Authenticator: testAuthenticator(), Cache: NewConanCache(nil)}
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodDelete, "/conan/v2/public/conans/pkg/1.0/u/c/revisions/rev", nil),
		httptest.NewRequest(http.MethodPost, "/conan/v2/public/conans/pkg/1.0/u/c/revisions/rev:restore", nil),
	} {
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s status=%d body=%s", request.Method, response.Code, response.Body.String())
		}
	}
}
