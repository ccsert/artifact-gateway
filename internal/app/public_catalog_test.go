package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestPublicRepositoryCatalogExposesOnlyAnonymousRepositories(t *testing.T) {
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "private", Format: repository.FormatRaw}); err != nil {
		t.Fatal(err)
	}
	public, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "public", Format: repository.FormatMaven, AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	enableAnonymousAccess(t, store)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v2/public/repositories", nil)
	publicRepositoryCatalogHandler{repositories: store, anonymous: store}.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("catalog status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Enabled bool `json:"enabled"`
		Items []publicRepositoryCatalogEntry `json:"items"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if !response.Enabled || len(response.Items) != 1 || response.Items[0].ID != public.ID || response.Items[0].Name != public.Name {
		t.Fatalf("catalog items = %#v", response.Items)
	}
}
