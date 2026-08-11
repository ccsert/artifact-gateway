package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/authorization"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestUserManagementProfilePasswordSessionsAndLastAdminProtection(t *testing.T) {
	store := repository.NewMemoryStore()
	authenticator := testAuthenticator()
	authenticator.AdminActor = "platform-admin"
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	adminRequest := func(method, target, body, version string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, target, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer admin-secret")
		if version != "" {
			request.Header.Set("If-Match", version)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	createdResponse := adminRequest(http.MethodPost, "/api/v2/users", `{"name":"alice","displayName":"Alice Example","email":"alice@example.test","description":"release owner","password":"initial-password","role":"admin","mustChangePassword":true}`, "")
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("create=%d body=%s", createdResponse.Code, createdResponse.Body.String())
	}
	var created struct {
		ID                 string `json:"id"`
		DisplayName        string `json:"displayName"`
		Email              string `json:"email"`
		Description        string `json:"description"`
		MustChangePassword bool   `json:"mustChangePassword"`
		Version            string `json:"version"`
	}
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &created); err != nil || created.DisplayName != "Alice Example" || created.Email != "alice@example.test" || created.Description != "release owner" || !created.MustChangePassword {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	listed := adminRequest(http.MethodGet, "/api/v2/users?search=alice%40example.test&role=admin&state=active&limit=20&offset=0", "", "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"total":1`) || !strings.Contains(listed.Body.String(), `"limit":20`) {
		t.Fatalf("list=%d body=%s", listed.Code, listed.Body.String())
	}

	if demoted := adminRequest(http.MethodPatch, "/api/v2/users/"+created.ID, `{"role":"reader"}`, created.Version); demoted.Code != http.StatusConflict {
		t.Fatalf("last admin demotion=%d body=%s", demoted.Code, demoted.Body.String())
	}
	if deleted := adminRequest(http.MethodDelete, "/api/v2/users/"+created.ID, "", ""); deleted.Code != http.StatusConflict {
		t.Fatalf("last admin delete=%d body=%s", deleted.Code, deleted.Body.String())
	}

	reset := adminRequest(http.MethodPost, "/api/v2/users/"+created.ID+"/password", `{"password":"reset-password","mustChangePassword":true}`, created.Version)
	if reset.Code != http.StatusOK {
		t.Fatalf("reset=%d body=%s", reset.Code, reset.Body.String())
	}
	var resetUser struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(reset.Body.Bytes(), &resetUser)
	loginRequest := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"username":"alice","password":"reset-password"}`))
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, loginRequest)
	var login struct {
		Token              string `json:"token"`
		MustChangePassword bool   `json:"mustChangePassword"`
	}
	_ = json.Unmarshal(loginResponse.Body.Bytes(), &login)
	if loginResponse.Code != http.StatusOK || login.Token == "" || !login.MustChangePassword {
		t.Fatalf("login=%d payload=%+v body=%s", loginResponse.Code, login, loginResponse.Body.String())
	}
	forcedRequest := httptest.NewRequest(http.MethodGet, "/api/v2/users", nil)
	forcedRequest.Header.Set("Authorization", "Bearer "+login.Token)
	forcedResponse := httptest.NewRecorder()
	handler.ServeHTTP(forcedResponse, forcedRequest)
	if forcedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("must-change session management access=%d want=401", forcedResponse.Code)
	}

	changeRequest := httptest.NewRequest(http.MethodPost, "/auth/change-password", strings.NewReader(`{"currentPassword":"reset-password","newPassword":"final-password"}`))
	changeRequest.Header.Set("Authorization", "Bearer "+login.Token)
	changeResponse := httptest.NewRecorder()
	handler.ServeHTTP(changeResponse, changeRequest)
	if changeResponse.Code != http.StatusNoContent {
		t.Fatalf("change password=%d body=%s", changeResponse.Code, changeResponse.Body.String())
	}
	if _, ok := testAuthenticatorWithUsers(store).Authenticate("Bearer " + login.Token); ok {
		t.Fatal("password change did not revoke previous session")
	}
	if sessions, listErr := store.ListUserSessions(context.Background(), created.ID, false); listErr != nil || len(sessions) != 0 {
		t.Fatalf("active sessions after password change=%+v err=%v", sessions, listErr)
	}

	loginRequest = httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"username":"alice","password":"final-password"}`))
	loginResponse = httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, loginRequest)
	_ = json.Unmarshal(loginResponse.Body.Bytes(), &login)
	if loginResponse.Code != http.StatusOK || login.MustChangePassword {
		t.Fatalf("final login=%d body=%s", loginResponse.Code, loginResponse.Body.String())
	}
	current, err := store.GetUser(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	revoked := adminRequest(http.MethodPost, "/api/v2/users/"+created.ID+"/sessions:revoke", "", current.Version)
	if revoked.Code != http.StatusOK {
		t.Fatalf("revoke=%d body=%s", revoked.Code, revoked.Body.String())
	}

	audits, err := store.ListAudits(context.Background(), repository.AuditQuery{Actor: "platform-admin"})
	if err != nil || len(audits) == 0 {
		t.Fatalf("management audits=%+v err=%v", audits, err)
	}
	for _, audit := range audits {
		if !strings.HasPrefix(audit.Resource, "users/"+created.ID) {
			t.Fatalf("management audit target=%q want users/%s", audit.Resource, created.ID)
		}
	}
	passwordAudits, err := store.ListAudits(context.Background(), repository.AuditQuery{Operation: "user.password.change"})
	if err != nil || len(passwordAudits) != 1 || passwordAudits[0].Actor != "user:alice" || passwordAudits[0].Resource != "auth/change-password" {
		t.Fatalf("password audits=%+v err=%v", passwordAudits, err)
	}
}

func TestUserManagementValidatesProfileAndPasswordBounds(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	adminRequest := func(method, target string, body any, version string) *httptest.ResponseRecorder {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(method, target, strings.NewReader(string(encoded)))
		request.Header.Set("Authorization", "Bearer admin-secret")
		if version != "" {
			request.Header.Set("If-Match", version)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	invalidCreates := []struct {
		name string
		body map[string]any
	}{
		{"long username", map[string]any{"name": strings.Repeat("n", 129), "password": "valid-password", "role": "reader"}},
		{"long display name", map[string]any{"name": "long-display", "displayName": strings.Repeat("d", 129), "password": "valid-password", "role": "reader"}},
		{"invalid email", map[string]any{"name": "invalid-email", "email": "not-an-email", "password": "valid-password", "role": "reader"}},
		{"long description", map[string]any{"name": "long-description", "description": strings.Repeat("d", 513), "password": "valid-password", "role": "reader"}},
		{"password over 72 ASCII bytes", map[string]any{"name": "long-password", "password": strings.Repeat("p", 73), "role": "reader"}},
		{"password over 72 multibyte bytes", map[string]any{"name": "long-multibyte-password", "password": strings.Repeat("密", 25), "role": "reader"}},
	}
	for _, test := range invalidCreates {
		t.Run(test.name, func(t *testing.T) {
			response := adminRequest(http.MethodPost, "/api/v2/users", test.body, "")
			if response.Code != http.StatusBadRequest {
				t.Fatalf("create=%d body=%s", response.Code, response.Body.String())
			}
		})
	}

	createdResponse := adminRequest(http.MethodPost, "/api/v2/users", map[string]any{
		"name": "boundary-user", "email": "boundary@example.test",
		"password": strings.Repeat("密码", 4), "role": "reader",
	}, "")
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("valid multibyte password create=%d body=%s", createdResponse.Code, createdResponse.Body.String())
	}
	var created struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	invalidUpdates := []struct {
		name string
		body map[string]any
	}{
		{"invalid email", map[string]any{"email": "not-an-email"}},
		{"long display name", map[string]any{"displayName": strings.Repeat("d", 129)}},
		{"long description", map[string]any{"description": strings.Repeat("d", 513)}},
	}
	for _, test := range invalidUpdates {
		t.Run("update "+test.name, func(t *testing.T) {
			response := adminRequest(http.MethodPatch, "/api/v2/users/"+created.ID, test.body, created.Version)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("update=%d body=%s", response.Code, response.Body.String())
			}
		})
	}

	tooLongReset := adminRequest(http.MethodPost, "/api/v2/users/"+created.ID+"/password", map[string]any{
		"password": strings.Repeat("p", 73),
	}, created.Version)
	if tooLongReset.Code != http.StatusBadRequest {
		t.Fatalf("long password reset=%d body=%s", tooLongReset.Code, tooLongReset.Body.String())
	}
	maximumReset := adminRequest(http.MethodPost, "/api/v2/users/"+created.ID+"/password", map[string]any{
		"password": strings.Repeat("p", 72), "mustChangePassword": false,
	}, created.Version)
	if maximumReset.Code != http.StatusOK {
		t.Fatalf("72-byte password reset=%d body=%s", maximumReset.Code, maximumReset.Body.String())
	}
}

func TestUserSessionManagementSupportsIndependentRevocation(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	hash, err := authorization.HashPassword("session-password")
	if err != nil {
		t.Fatal(err)
	}
	user, err := store.CreateUser(ctx, repository.User{
		ID: uuid.NewString(), Name: "session-owner", SecretHash: hash, Role: "admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	login := func(agent string) string {
		request := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"username":"session-owner","password":"session-password"}`))
		request.Header.Set("User-Agent", agent)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		var payload struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || response.Code != http.StatusOK || payload.Token == "" {
			t.Fatalf("login=%d body=%s err=%v", response.Code, response.Body.String(), err)
		}
		return payload.Token
	}
	adminRequest := func(method, target, version string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, target, nil)
		request.Header.Set("Authorization", "Bearer admin-secret")
		if version != "" {
			request.Header.Set("If-Match", version)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	list := func(target string) []struct {
		ID        string     `json:"id"`
		UserAgent string     `json:"userAgent"`
		RevokedAt *time.Time `json:"revokedAt"`
	} {
		response := adminRequest(http.MethodGet, target, "")
		var payload struct {
			Items []struct {
				ID        string     `json:"id"`
				UserAgent string     `json:"userAgent"`
				RevokedAt *time.Time `json:"revokedAt"`
			} `json:"items"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || response.Code != http.StatusOK {
			t.Fatalf("list=%d body=%s err=%v", response.Code, response.Body.String(), err)
		}
		return payload.Items
	}

	firstToken := login("first-client")
	firstItems := list("/api/v2/users/" + user.ID + "/sessions")
	if len(firstItems) != 1 || firstItems[0].UserAgent != "first-client" || strings.Contains(firstItems[0].UserAgent, firstToken) {
		t.Fatalf("first sessions=%+v", firstItems)
	}
	firstSessionID := firstItems[0].ID
	secondToken := login("second-client")
	if items := list("/api/v2/users/" + user.ID + "/sessions"); len(items) != 2 {
		t.Fatalf("active sessions=%+v", items)
	}
	currentRequest := httptest.NewRequest(http.MethodGet, "/api/v2/users/"+user.ID+"/sessions", nil)
	currentRequest.Header.Set("Authorization", "Bearer "+secondToken)
	currentResponse := httptest.NewRecorder()
	handler.ServeHTTP(currentResponse, currentRequest)
	if currentResponse.Code != http.StatusOK || !strings.Contains(currentResponse.Body.String(), `"current":true`) {
		t.Fatalf("current session=%d body=%s", currentResponse.Code, currentResponse.Body.String())
	}
	revoked := adminRequest(http.MethodDelete, "/api/v2/users/"+user.ID+"/sessions/"+firstSessionID, "")
	if revoked.Code != http.StatusOK || !strings.Contains(revoked.Body.String(), `"revokedAt"`) {
		t.Fatalf("revoke=%d body=%s", revoked.Code, revoked.Body.String())
	}
	if _, ok := (Authenticator{AdminToken: "admin-secret", Users: store, UserSessions: store}).Authenticate("Bearer " + firstToken); ok {
		t.Fatal("individually revoked login token authenticated")
	}
	if _, ok := (Authenticator{AdminToken: "admin-secret", Users: store, UserSessions: store}).Authenticate("Bearer " + secondToken); !ok {
		t.Fatal("unrelated login token was revoked")
	}
	if items := list("/api/v2/users/" + user.ID + "/sessions"); len(items) != 1 || items[0].UserAgent != "second-client" {
		t.Fatalf("remaining active sessions=%+v", items)
	}
	if items := list("/api/v2/users/" + user.ID + "/sessions?includeInactive=true"); len(items) != 2 {
		t.Fatalf("session history=%+v", items)
	}
	current, err := store.GetUser(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	allRevoked := adminRequest(http.MethodPost, "/api/v2/users/"+user.ID+"/sessions:revoke", current.Version)
	if allRevoked.Code != http.StatusOK {
		t.Fatalf("revoke all=%d body=%s", allRevoked.Code, allRevoked.Body.String())
	}
	if _, ok := (Authenticator{AdminToken: "admin-secret", Users: store, UserSessions: store}).Authenticate("Bearer " + secondToken); ok {
		t.Fatal("bulk-revoked login token authenticated")
	}
}

func TestUserManagementIdentityBindingLifecycle(t *testing.T) {
	store := repository.NewMemoryStore()
	runtime := NewOIDCRuntime(store, OIDCRuntimeConfig{Issuer: "https://issuer.example.test"})
	handler := NewGatewayHandler(Dependencies{OIDCRuntime: runtime}, store, TestAdapter{}, testAuthenticator())
	adminRequest := func(method, target, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, target, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer admin-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	created := adminRequest(http.MethodPost, "/api/v2/users", `{"name":"identity-owner","password":"initial-password","role":"reader"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create user=%d body=%s", created.Code, created.Body.String())
	}
	var user struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &user); err != nil || user.ID == "" {
		t.Fatalf("created user=%+v err=%v", user, err)
	}

	initial := adminRequest(http.MethodGet, "/api/v2/users/"+user.ID+"/identities", "")
	if initial.Code != http.StatusOK || !strings.Contains(initial.Body.String(), `"items":[]`) {
		t.Fatalf("initial identities=%d body=%s", initial.Code, initial.Body.String())
	}
	linked := adminRequest(http.MethodPost, "/api/v2/users/"+user.ID+"/identities", `{"issuer":"https://issuer.example.test/","subject":"gitlab-subject"}`)
	if linked.Code != http.StatusCreated || !strings.Contains(linked.Body.String(), `"issuer":"https://issuer.example.test"`) {
		t.Fatalf("link=%d body=%s", linked.Code, linked.Body.String())
	}
	var identity struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(linked.Body.Bytes(), &identity); err != nil || identity.ID == "" {
		t.Fatalf("linked identity=%+v err=%v", identity, err)
	}
	duplicate := adminRequest(http.MethodPost, "/api/v2/users/"+user.ID+"/identities", `{"issuer":"https://issuer.example.test","subject":"gitlab-subject"}`)
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate link=%d body=%s", duplicate.Code, duplicate.Body.String())
	}
	wrongIssuer := adminRequest(http.MethodPost, "/api/v2/users/"+user.ID+"/identities", `{"issuer":"https://other.example.test","subject":"other"}`)
	if wrongIssuer.Code != http.StatusBadRequest {
		t.Fatalf("wrong issuer=%d body=%s", wrongIssuer.Code, wrongIssuer.Body.String())
	}
	deleted := adminRequest(http.MethodDelete, "/api/v2/users/"+user.ID+"/identities/"+identity.ID, "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("unlink=%d body=%s", deleted.Code, deleted.Body.String())
	}
	missing := adminRequest(http.MethodDelete, "/api/v2/users/"+user.ID+"/identities/"+identity.ID, "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("repeat unlink=%d body=%s", missing.Code, missing.Body.String())
	}
}

func testAuthenticatorWithUsers(users repository.UserStore) Authenticator {
	auth := testAuthenticator()
	auth.Users = users
	return auth
}
