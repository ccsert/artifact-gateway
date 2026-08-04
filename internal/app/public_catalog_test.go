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
		Enabled bool                           `json:"enabled"`
		Items   []publicRepositoryCatalogEntry `json:"items"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if !response.Enabled || len(response.Items) != 1 || response.Items[0].ID != public.ID || response.Items[0].Name != public.Name {
		t.Fatalf("catalog items = %#v", response.Items)
	}
}

func TestPublicRepositoryCatalogExposesAnonymousGroupsWithAnonymousMembers(t *testing.T) {
	store := repository.NewMemoryStore()
	public, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "public-member", Format: repository.FormatOCI, AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	private, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "private-member", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	visible, _, err := store.CreateHostedGroupIdempotently(context.Background(), repository.HostedGroup{
		ID: uuid.NewString(), Name: "public-group", Format: repository.FormatOCI, AnonymousRead: true,
		Members: []repository.GroupMember{{RepositoryID: public.ID, Position: 0}},
	}, "test", "visible-group", "visible-group")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateHostedGroupIdempotently(context.Background(), repository.HostedGroup{
		ID: uuid.NewString(), Name: "no-public-members", Format: repository.FormatOCI, AnonymousRead: true,
		Members: []repository.GroupMember{{RepositoryID: private.ID, Position: 0}},
	}, "test", "hidden-group", "hidden-group"); err != nil {
		t.Fatal(err)
	}
	enableAnonymousAccess(t, store)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v2/public/repositories", nil)
	publicRepositoryCatalogHandler{repositories: store, groups: store, anonymous: store}.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("catalog status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Items []publicRepositoryCatalogEntry `json:"items"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	var found *publicRepositoryCatalogEntry
	for i := range response.Items {
		if response.Items[i].ID == visible.ID {
			found = &response.Items[i]
		}
		if response.Items[i].Name == "no-public-members" {
			t.Fatalf("group without anonymous members leaked into catalog: %#v", response.Items[i])
		}
	}
	if found == nil || found.Name != visible.Name || found.Type != "group" {
		t.Fatalf("public group missing from catalog: %#v", response.Items)
	}
}
