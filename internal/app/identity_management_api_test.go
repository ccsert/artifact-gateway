package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	adminopenapi "github.com/ccsert/artifact-gateway/internal/admin/openapi"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

type identityUserPort struct{ identityUserStore }
type identityKeyPort struct{ identityAPIKeyStore }
type identityAccountPort struct{ identityServiceAccountStore }

func identityDomainHTTP(domain identityManagementHandler) http.Handler {
	return adminopenapi.HandlerWithOptions(managementServer{identityManagementHandler: domain}, adminopenapi.StdHTTPServerOptions{
		BaseURL: "/api/v2", BaseRouter: openAPIServeMux{mux: http.NewServeMux()},
	})
}

func TestIdentityManagementDomainIndependentHTTP(t *testing.T) {
	store := repository.NewMemoryStore()
	auth := testAuthenticator()
	audit := &managementAuditFixture{}
	// These ports expose no repository catalog, artifacts, login writes, or
	// background queues. Identity requests do not need those dependencies.
	domain := newIdentityManagementHandler(identityManagementOptions{
		Scope: managementScope{authenticator: auth, audit: audit},
		Users: identityUserPort{store}, APIKeys: identityKeyPort{store}, ServiceAccounts: identityAccountPort{store},
	})
	server := identityDomainHTTP(domain)
	call := func(token, method, path, body, version string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, "/api/v2"+path, strings.NewReader(body))
		if token != "" {
			authorize(request, token)
		}
		if version != "" {
			request.Header.Set("If-Match", version)
		}
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		return response
	}
	response := call(auth.IssueToken("user:member"), http.MethodGet, "/identity", "", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"actor":"user:member"`) {
		t.Fatalf("member identity=%d %s", response.Code, response.Body.String())
	}
	response = call("admin-secret", http.MethodPost, "/users", `{"name":"domain-user","password":"domain-user-password","role":"member"}`, "")
	var user adminopenapi.User
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &user) != nil || user.Version == "" || strings.Contains(response.Body.String(), "domain-user-password") {
		t.Fatalf("create user=%d %s", response.Code, response.Body.String())
	}
	userPath := "/users/" + user.Id.String()
	response = call("admin-secret", http.MethodPatch, userPath, `{"displayName":"Domain User"}`, user.Version)
	var updated adminopenapi.User
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &updated) != nil || updated.DisplayName != "Domain User" || updated.Version == user.Version {
		t.Fatalf("update user=%d %s", response.Code, response.Body.String())
	}
	response = call("admin-secret", http.MethodPatch, userPath, `{"displayName":"Stale"}`, user.Version)
	if response.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale user update=%d %s", response.Code, response.Body.String())
	}
	response = call("admin-secret", http.MethodPost, userPath+"/password", `{"password":"new-domain-user-password","mustChangePassword":true}`, updated.Version)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &updated) != nil || !updated.MustChangePassword {
		t.Fatalf("reset password=%d %s", response.Code, response.Body.String())
	}
	response = call("admin-secret", http.MethodPost, userPath+"/sessions:revoke", "", updated.Version)
	if response.Code != http.StatusOK {
		t.Fatalf("revoke user sessions=%d %s", response.Code, response.Body.String())
	}
	response = call("admin-secret", http.MethodPost, "/api-keys", `{"name":"domain-key","roles":["member"]}`, "")
	var key adminopenapi.CreatedAPIKey
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &key) != nil || key.Token == "" {
		t.Fatalf("create API key status=%d", response.Code)
	}
	response = call("admin-secret", http.MethodGet, "/api-keys", "", "")
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), key.Token) {
		t.Fatalf("API key list exposes a secret or failed: status=%d", response.Code)
	}
	response = call("admin-secret", http.MethodDelete, "/api-keys/"+key.Id.String(), "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("revoke API key=%d %s", response.Code, response.Body.String())
	}
	response = call("admin-secret", http.MethodPost, "/service-accounts", `{"name":"domain-ci"}`, "")
	var account adminopenapi.ServiceAccount
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &account) != nil {
		t.Fatalf("create service account=%d %s", response.Code, response.Body.String())
	}
	credentialsPath := "/service-accounts/" + account.Id.String() + "/credentials"
	response = call("admin-secret", http.MethodPost, credentialsPath, `{"name":"blue"}`, "")
	var credential adminopenapi.CreatedServiceAccountCredential
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &credential) != nil || credential.Token == "" {
		t.Fatalf("create service credential status=%d", response.Code)
	}
	response = call("admin-secret", http.MethodGet, credentialsPath, "", "")
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), credential.Token) {
		t.Fatalf("service credential list exposes a secret or failed: status=%d", response.Code)
	}
	response = call("admin-secret", http.MethodDelete, credentialsPath+"/"+credential.Id.String(), "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("revoke service credential=%d %s", response.Code, response.Body.String())
	}
	response = call("admin-secret", http.MethodGet, "/authentication/oidc", "", "")
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "not_supported") {
		t.Fatalf("unconfigured OIDC=%d %s", response.Code, response.Body.String())
	}
	operations := map[string]int{}
	for _, record := range audit.records {
		operations[record.Operation]++
		if record.Actor != "alice" || record.Status < 200 || record.Status >= 300 {
			t.Fatalf("identity audit=%+v", record)
		}
		data, err := json.Marshal(record)
		if err != nil || strings.Contains(string(data), key.Token) || strings.Contains(string(data), credential.Token) || strings.Contains(string(data), "domain-user-password") {
			t.Fatal("identity audit exposes a secret or cannot be serialized")
		}
	}
	for _, operation := range []string{"user.create", "user.update", "user.password.reset", "user.sessions.revoke", "service_account.create", "service_account.credential.create", "service_account.credential.revoke"} {
		if operations[operation] != 1 {
			t.Fatalf("audit operation %s count=%d", operation, operations[operation])
		}
	}
}

func TestIdentityManagementAuthorizesBeforeDomainStorage(t *testing.T) {
	auth := testAuthenticator()
	users := repository.NewMemoryStore()
	_, err := users.CreateUser(t.Context(), repository.User{Name: "password-pending", Role: "admin", MustChangePassword: true})
	if err != nil {
		t.Fatal(err)
	}
	auth.Users = users
	server := identityDomainHTTP(newIdentityManagementHandler(identityManagementOptions{Scope: managementScope{authenticator: auth}}))
	id := "1d7c65d1-284e-4859-82e7-2adca7e183f0"
	routes := []struct{ method, path string }{
		{http.MethodGet, "/api-keys"}, {http.MethodPost, "/api-keys"}, {http.MethodDelete, "/api-keys/" + id},
		{http.MethodGet, "/users"}, {http.MethodPost, "/users"}, {http.MethodGet, "/users/" + id},
		{http.MethodPatch, "/users/" + id}, {http.MethodDelete, "/users/" + id},
		{http.MethodPost, "/users/" + id + "/password"}, {http.MethodPost, "/users/" + id + "/sessions:revoke"},
		{http.MethodGet, "/users/" + id + "/sessions"}, {http.MethodDelete, "/users/" + id + "/sessions/" + id},
		{http.MethodGet, "/users/" + id + "/identities"}, {http.MethodPost, "/users/" + id + "/identities"},
		{http.MethodDelete, "/users/" + id + "/identities/" + id},
		{http.MethodGet, "/service-accounts"}, {http.MethodPost, "/service-accounts"}, {http.MethodPut, "/service-accounts/" + id},
		{http.MethodGet, "/service-accounts/" + id + "/credentials"}, {http.MethodPost, "/service-accounts/" + id + "/credentials"},
		{http.MethodDelete, "/service-accounts/" + id + "/credentials/" + id},
		{http.MethodGet, "/authentication/oidc"}, {http.MethodPut, "/authentication/oidc"}, {http.MethodPost, "/authentication/oidc:test"},
	}
	for _, credential := range []struct {
		name, token string
		status      int
	}{
		{"anonymous", "", http.StatusUnauthorized},
		{"member", "resolver-secret", http.StatusForbidden},
		{"password-change", auth.IssueToken("user:password-pending"), http.StatusForbidden},
	} {
		for _, route := range routes {
			t.Run(credential.name+"/"+route.method+route.path, func(t *testing.T) {
				request := httptest.NewRequest(route.method, "/api/v2"+route.path, strings.NewReader(`{}`))
				request.Header.Set("If-Match", "1")
				if credential.token != "" {
					authorize(request, credential.token)
				}
				response := httptest.NewRecorder()
				server.ServeHTTP(response, request)
				if response.Code != credential.status {
					t.Fatalf("status=%d want=%d body=%s", response.Code, credential.status, response.Body.String())
				}
				if credential.name == "password-change" && !strings.Contains(response.Body.String(), "password_change_required") {
					t.Fatalf("password-change reason=%s", response.Body.String())
				}
			})
		}
	}
}
