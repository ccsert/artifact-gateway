package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
	}
	if err := json.NewDecoder(created.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.ID == "" || result.Token == "" || result.SecretHash != "" {
		t.Fatalf("created key=%#v", result)
	}

	list := httptest.NewRequest(http.MethodGet, "/api/v2/api-keys", nil)
	list.Header.Set("Authorization", "Bearer "+result.Token)
	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, list)
	if listed.Code != http.StatusOK || bytes.Contains(listed.Body.Bytes(), []byte(result.Token)) {
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
