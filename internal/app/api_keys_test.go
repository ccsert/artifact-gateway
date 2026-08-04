package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestAPIKeyManagementCreatesAuthenticatesAndRevokesKey(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, Authenticator{AdminToken: "root", AdminActor: "root", APIKeys: store})
	create := httptest.NewRequest(http.MethodPost, "/api/v2/api-keys", bytes.NewBufferString(`{"name":"automation","roles":["admin"]}`))
	create.Header.Set("Authorization", "Bearer root")
	create.Header.Set("Content-Type", "application/json")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var result struct {
		ID         string `json:"id"`
		Token      string `json:"token"`
		SecretHash string `json:"secretHash"`
		ExpiresAt  string `json:"expiresAt"`
	}
	if err := json.NewDecoder(created.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.ID == "" || result.Token == "" || result.SecretHash != "" || result.ExpiresAt == "" {
		t.Fatalf("created key=%#v", result)
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, result.ExpiresAt)
	if err != nil || time.Until(expiresAt) < 89*24*time.Hour || time.Until(expiresAt) > 91*24*time.Hour {
		t.Fatalf("default expiry=%q parsed=%v err=%v", result.ExpiresAt, expiresAt, err)
	}

	list := httptest.NewRequest(http.MethodGet, "/api/v2/api-keys", nil)
	list.Header.Set("Authorization", "Bearer "+result.Token)
	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, list)
	if listed.Code != http.StatusOK || bytes.Contains(listed.Body.Bytes(), []byte(result.Token)) || !bytes.Contains(listed.Body.Bytes(), []byte("lastUsedAt")) {
		t.Fatalf("list status=%d body=%s", listed.Code, listed.Body.String())
	}

	revoke := httptest.NewRequest(http.MethodDelete, "/api/v2/api-keys/"+result.ID, nil)
	revoke.Header.Set("Authorization", "Bearer root")
	revoked := httptest.NewRecorder()
	handler.ServeHTTP(revoked, revoke)
	if revoked.Code != http.StatusOK {
		t.Fatalf("revoke status=%d body=%s", revoked.Code, revoked.Body.String())
	}

	afterRevoke := httptest.NewRequest(http.MethodGet, "/api/v2/api-keys", nil)
	afterRevoke.Header.Set("Authorization", "Bearer "+result.Token)
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, afterRevoke)
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("revoked key status=%d body=%s", denied.Code, denied.Body.String())
	}
}

func TestAPIKeyManagementValidatesExpiryAndRequestShape(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, Authenticator{AdminToken: "root", AdminActor: "root", APIKeys: store})

	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "already expired", body: `{"name":"expired","roles":["reader"],"expiresAt":"2020-01-01T00:00:00Z"}`},
		{name: "too far in future", body: `{"name":"long-lived","roles":["reader"],"expiresAt":"2099-01-01T00:00:00Z"}`},
		{name: "unknown field", body: `{"name":"surprising","roles":["reader"],"neverExpires":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v2/api-keys", bytes.NewBufferString(tc.body))
			request.Header.Set("Authorization", "Bearer root")
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
