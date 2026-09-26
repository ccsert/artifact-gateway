package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestRepositoryGrantsUpsertDeleteHTTP(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "11111111-1111-1111-1111-111111111111", Name: "releases", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := func(method, path, body, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			authorize(r, token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	grantsPath := "/api/v2/repositories/" + repo.ID + "/grants"

	created := request(http.MethodPost, grantsPath, `{"principal":"user:alice","scopes":["repositories:read"],"resourcePrefix":"com.example"}`, "admin-secret")
	if created.Code != http.StatusOK || created.Header().Get("ETag") != "2" || !strings.Contains(created.Body.String(), "user:alice") {
		t.Fatalf("upsert create = %d etag=%q %s", created.Code, created.Header().Get("ETag"), created.Body.String())
	}

	// The same (principal, resource prefix) key is replaced in place instead of
	// duplicated, and other rows are left alone.
	replaced := request(http.MethodPost, grantsPath, `{"principal":"user:alice","scopes":["repositories:read","repositories:write"],"resourcePrefix":"com.example"}`, "admin-secret")
	if replaced.Code != http.StatusOK || replaced.Header().Get("ETag") != "3" {
		t.Fatalf("upsert replace = %d etag=%q %s", replaced.Code, replaced.Header().Get("ETag"), replaced.Body.String())
	}
	var afterReplace []struct {
		Principal string   `json:"principal"`
		Scopes    []string `json:"scopes"`
	}
	if err := json.NewDecoder(replaced.Body).Decode(&afterReplace); err != nil {
		t.Fatal(err)
	}
	if len(afterReplace) != 1 || len(afterReplace[0].Scopes) != 2 {
		t.Fatalf("upsert must replace the keyed row, got %#v", afterReplace)
	}

	second := request(http.MethodPost, grantsPath, `{"principal":"service-account:ci","scopes":["repositories:read"]}`, "admin-secret")
	if second.Code != http.StatusOK || !strings.Contains(second.Body.String(), "user:alice") {
		t.Fatalf("second upsert must keep existing rows = %d %s", second.Code, second.Body.String())
	}

	if got := request(http.MethodPost, grantsPath, `{"principal":"user:mallory","scopes":[]}`, "admin-secret"); got.Code != http.StatusBadRequest {
		t.Fatalf("empty scopes = %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, grantsPath, `{"principal":"user:mallory","scopes":["repositories:own"]}`, "admin-secret"); got.Code != http.StatusBadRequest {
		t.Fatalf("unknown scope = %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, grantsPath, `{"principal":"user:mallory","scopes":["repositories:read"]}`, "resolver-secret"); got.Code != http.StatusForbidden {
		t.Fatalf("non-admin upsert = %d %s", got.Code, got.Body.String())
	}

	deleted := request(http.MethodDelete, grantsPath+"?principal=user:alice&resourcePrefix=com.example", "", "admin-secret")
	if deleted.Code != http.StatusNoContent || deleted.Header().Get("ETag") == "" {
		t.Fatalf("delete = %d etag=%q %s", deleted.Code, deleted.Header().Get("ETag"), deleted.Body.String())
	}
	listed := request(http.MethodGet, grantsPath, "", "admin-secret")
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), "user:alice") || !strings.Contains(listed.Body.String(), "service-account:ci") {
		t.Fatalf("list after delete = %d %s", listed.Code, listed.Body.String())
	}
	if got := request(http.MethodDelete, grantsPath+"?principal=user:alice&resourcePrefix=com.example", "", "admin-secret"); got.Code != http.StatusNotFound {
		t.Fatalf("delete missing grant = %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodDelete, grantsPath+"?principal=", "", "admin-secret"); got.Code != http.StatusBadRequest {
		t.Fatalf("delete without principal = %d %s", got.Code, got.Body.String())
	}

	upserts, deletes := 0, 0
	for _, record := range store.Audits {
		if record.Resource != "repositories/"+repo.ID+"/grants" {
			continue
		}
		switch record.Operation {
		case "repository.grants.upsert":
			upserts++
			if record.Actor != "alice" || record.Repository != repo.Name || record.Status != http.StatusOK {
				t.Fatalf("upsert audit = %#v", record)
			}
		case "repository.grants.delete":
			deletes++
			if record.Actor != "alice" || record.Repository != repo.Name || record.Status != http.StatusNoContent {
				t.Fatalf("delete audit = %#v", record)
			}
		}
	}
	if upserts != 3 || deletes != 1 {
		t.Fatalf("rejected requests must not be audited as mutations: upserts=%d deletes=%d audits=%#v", upserts, deletes, store.Audits)
	}
}
