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
)

func TestLocalLoginLocksRepeatedFailuresAndIssuesRevocableSession(t *testing.T) {
	store := repository.NewMemoryStore()
	hash, err := authorization.HashPassword("correct-password")
	if err != nil {
		t.Fatal(err)
	}
	user, err := store.CreateUser(context.Background(), repository.User{ID: "local-user", Name: "alice", DisplayName: "Alice", SecretHash: hash, Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	auth := testAuthenticator()
	auth.LocalAuthMaxAttempts = 3
	auth.LocalAuthLockout = time.Hour
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, auth)
	login := func(password string) (int, string) {
		request := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"username":"alice","password":"`+password+`"}`))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response.Code, response.Body.String()
	}
	for attempt := 1; attempt <= 3; attempt++ {
		if code, _ := login("wrong-password"); code != http.StatusUnauthorized {
			t.Fatalf("attempt %d code=%d want=401", attempt, code)
		}
	}
	if code, _ := login("correct-password"); code != http.StatusUnauthorized {
		t.Fatalf("locked login code=%d want=401", code)
	}
	locked, err := store.GetUser(context.Background(), user.ID)
	if err != nil || locked.LockedUntil == nil || locked.FailedLoginAttempts != 3 {
		t.Fatalf("locked user=%+v err=%v", locked, err)
	}
	if _, err := store.RecordUserLoginSuccess(context.Background(), user.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	code, body := login("correct-password")
	if code != http.StatusOK {
		t.Fatalf("successful login code=%d body=%s", code, body)
	}
	var payload struct {
		Token              string `json:"token"`
		DisplayName        string `json:"displayName"`
		MustChangePassword bool   `json:"mustChangePassword"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil || payload.Token == "" || payload.DisplayName != "Alice" || payload.MustChangePassword {
		t.Fatalf("login payload=%+v err=%v", payload, err)
	}
	reuseRequest := httptest.NewRequest(http.MethodPost, "/auth/change-password", strings.NewReader(`{"currentPassword":"correct-password","newPassword":"correct-password"}`))
	reuseRequest.Header.Set("Authorization", "Bearer "+payload.Token)
	reuseResponse := httptest.NewRecorder()
	handler.ServeHTTP(reuseResponse, reuseRequest)
	if reuseResponse.Code != http.StatusBadRequest {
		t.Fatalf("password reuse code=%d body=%s", reuseResponse.Code, reuseResponse.Body.String())
	}

	current, err := store.GetUser(context.Background(), user.ID)
	if err != nil || current.LastLoginAt == nil || current.FailedLoginAttempts != 0 {
		t.Fatalf("successful login state=%+v err=%v", current, err)
	}
	if _, err := store.RevokeUserSessions(context.Background(), user.ID, current.Version); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v2/users", nil)
	request.Header.Set("Authorization", "Bearer "+payload.Token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session code=%d want=401", response.Code)
	}

	audits, err := store.ListAudits(context.Background(), repository.AuditQuery{Operation: "user.login"})
	if err != nil || len(audits) < 5 {
		t.Fatalf("login audits=%+v err=%v", audits, err)
	}
	passwordAudits, err := store.ListAudits(context.Background(), repository.AuditQuery{Operation: "user.password.change"})
	if err != nil || len(passwordAudits) != 1 || passwordAudits[0].Resource != "auth/change-password" || passwordAudits[0].Outcome != repository.AuditAccessDenied {
		t.Fatalf("password reuse audits=%+v err=%v", passwordAudits, err)
	}
}
