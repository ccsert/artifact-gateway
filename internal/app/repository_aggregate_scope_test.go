package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

// The cross-repository management views show a repository administrator its own
// repositories and nothing else, so a repository it does not administer
// contributes no rows and cannot be told apart from one that does not exist.
func TestRepositoryConsoleAggregatesFollowRepositoryAdministration(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	managed, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "aggregate-managed", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "aggregate-foreign", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	for repositoryID, principal := range map[string]string{managed.ID: "repo-admin", foreign.ID: "other-admin"} {
		if _, err := store.ReplaceRepositoryGrants(ctx, repositoryID, []repository.RepositoryGrant{
			{Principal: principal, Scopes: []string{"repositories:admin"}},
		}, "1"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.PutRawAsset(ctx, repository.RawAsset{RepositoryID: managed.ID, Path: "release.txt", Digest: "sha256:scoped", Size: 11}); err != nil {
		t.Fatal(err)
	}
	job := repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: managed.ID, Kind: repository.LifecycleJobRetention, IdempotencyKey: "scoped-retention"}
	if _, _, err := store.EnqueueLifecycleJob(ctx, job); err != nil {
		t.Fatal(err)
	}

	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := func(path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if token != "" {
			authorize(req, token)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	grantRepositoryNames := func(token string) []string {
		t.Helper()
		response := request("/api/v2/repository-grants", token)
		if response.Code != http.StatusOK {
			t.Fatalf("grants=%d body=%s", response.Code, response.Body.String())
		}
		var records adminopenapi.RepositoryGrantRecordList
		if err := json.Unmarshal(response.Body.Bytes(), &records); err != nil {
			t.Fatal(err)
		}
		names := make([]string, 0, len(records))
		for _, record := range records {
			names = append(names, record.RepositoryName)
		}
		return names
	}
	jobRepositoryNames := func(token string) []string {
		t.Helper()
		response := request("/api/v2/lifecycle-jobs?limit=10", token)
		if response.Code != http.StatusOK {
			t.Fatalf("jobs=%d body=%s", response.Code, response.Body.String())
		}
		var jobs adminopenapi.RepositoryLifecycleJobList
		if err := json.Unmarshal(response.Body.Bytes(), &jobs); err != nil {
			t.Fatal(err)
		}
		names := make([]string, 0, len(jobs))
		for _, item := range jobs {
			names = append(names, item.RepositoryName)
		}
		return names
	}
	capacityRepositoryCount := func(token string) int {
		t.Helper()
		response := request("/api/v2/repository-capacities", token)
		if response.Code != http.StatusOK {
			t.Fatalf("capacities=%d body=%s", response.Code, response.Body.String())
		}
		var capacities adminopenapi.RepositoryCapacityList
		if err := json.Unmarshal(response.Body.Bytes(), &capacities); err != nil {
			t.Fatal(err)
		}
		return len(capacities)
	}

	repoAdmin := authenticator.IssueToken("repo-admin")
	otherAdmin := authenticator.IssueToken("other-admin")
	stranger := authenticator.IssueToken("stranger")

	if names := grantRepositoryNames(repoAdmin); len(names) != 1 || names[0] != managed.Name {
		t.Fatalf("repository administrator sees grants for %v", names)
	}
	if names := grantRepositoryNames(otherAdmin); len(names) != 1 || names[0] != foreign.Name {
		t.Fatalf("other administrator sees grants for %v", names)
	}
	if names := grantRepositoryNames(stranger); len(names) != 0 {
		t.Fatalf("a caller with no administration sees grants for %v", names)
	}
	if names := grantRepositoryNames("admin-secret"); len(names) != 2 {
		t.Fatalf("platform administrator sees grants for %v", names)
	}

	if names := jobRepositoryNames(repoAdmin); len(names) != 1 || names[0] != managed.Name {
		t.Fatalf("repository administrator sees jobs for %v", names)
	}
	if names := jobRepositoryNames(otherAdmin); len(names) != 0 {
		t.Fatalf("other administrator sees jobs for %v", names)
	}
	if names := jobRepositoryNames("admin-secret"); len(names) != 1 {
		t.Fatalf("platform administrator sees jobs for %v", names)
	}

	if count := capacityRepositoryCount(repoAdmin); count != 1 {
		t.Fatalf("repository administrator sees %d capacities", count)
	}
	if count := capacityRepositoryCount(stranger); count != 0 {
		t.Fatalf("a caller with no administration sees %d capacities", count)
	}
	if count := capacityRepositoryCount("admin-secret"); count != 2 {
		t.Fatalf("platform administrator sees %d capacities", count)
	}
}
