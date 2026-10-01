package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestOverviewStatisticsScopesRowsAndTotals(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	owned, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "overview-owned", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	zero, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "overview-zero", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "overview-foreign", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{owned.ID, zero.ID} {
		if _, err := store.ReplaceRepositoryGrants(ctx, id, []repository.RepositoryGrant{{Principal: "repo-admin", Scopes: []string{"repositories:admin"}}}, "1"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.PutRawAsset(ctx, repository.RawAsset{RepositoryID: owned.ID, Path: "a.txt", Digest: "sha256:overview", Size: 17}); err != nil {
		t.Fatal(err)
	}
	for _, record := range []repository.AuditRecord{
		{Repository: owned.Name, Format: "raw", Outcome: repository.AuditResolved, OccurredAt: time.Now().Add(-time.Hour)},
		{Repository: owned.Name, Format: "raw", Outcome: repository.AuditAccessDenied, OccurredAt: time.Now().Add(-time.Hour)},
		{Repository: foreign.Name, Format: "raw", Outcome: repository.AuditResolved, OccurredAt: time.Now().Add(-time.Hour)},
		{Repository: "deleted-orphan", Format: "raw", Outcome: repository.AuditResolved, OccurredAt: time.Now().Add(-time.Hour)},
	} {
		if err := store.RecordAudit(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	call := func(token string) adminopenapi.OverviewStatistics {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v2/overview-statistics", nil)
		authorize(req, token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		var stats adminopenapi.OverviewStatistics
		if err := json.Unmarshal(response.Body.Bytes(), &stats); err != nil {
			t.Fatal(err)
		}
		return stats
	}
	stats := call(authenticator.IssueToken("repo-admin"))
	if len(stats.Repositories) != 2 || stats.Totals.Requests.OneDay != 2 || stats.Totals.Denied.OneDay != 1 || stats.Totals.ObjectCount != 1 || stats.Totals.UsedBytes != 17 {
		t.Fatalf("scoped statistics=%#v", stats)
	}
	for _, item := range stats.Repositories {
		if item.RepositoryId.String() == zero.ID && (item.Requests.OneDay != 0 || item.ObjectCount != 0) {
			t.Fatalf("zero row=%#v", item)
		}
		if item.RepositoryId.String() == foreign.ID {
			t.Fatalf("foreign row leaked: %#v", item)
		}
	}
	global := call("admin-secret")
	if len(global.Repositories) != 3 || global.Totals.Requests.OneDay != 3 {
		t.Fatalf("global statistics=%#v", global)
	}
}
