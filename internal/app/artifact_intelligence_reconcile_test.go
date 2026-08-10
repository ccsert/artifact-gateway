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

func TestArtifactIntelligenceReconciliationRequeuesOnlyFailedCopyJobs(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "intelligence-reconcile", Format: repository.FormatOCI,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: "reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(repository.ArtifactIntelligenceCopyPayload{
		Format:             repository.FormatOCI,
		SourceRepositoryID: uuid.NewString(),
		Coordinate:         "library/widget",
		Digest:             "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, _, err = store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{
			ID: uuid.NewString(), RepositoryID: repo.ID, Kind: repository.LifecycleJobIntelligence,
			IdempotencyKey: uuid.NewString(), Payload: payload, MaxAttempts: 1,
		}); err != nil {
			t.Fatal(err)
		}
		claimed, claimErr := store.ClaimLifecycleJobsByKindAndFormat(ctx, repository.LifecycleJobIntelligence, repository.FormatOCI, 1)
		if claimErr != nil || len(claimed) != 1 {
			t.Fatalf("claimed=%#v err=%v", claimed, claimErr)
		}
		if err = store.FailLifecycleJob(ctx, claimed[0].ID, claimed[0].LeaseToken, "scanner unavailable"); err != nil {
			t.Fatal(err)
		}
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	path := "/api/v2/repositories/" + repo.ID + "/lifecycle-jobs:reconcile-intelligence?limit=1"

	denied := httptest.NewRequest(http.MethodPost, path, nil)
	authorize(denied, authenticator.IssueToken("reader"))
	deniedResponse := httptest.NewRecorder()
	handler.ServeHTTP(deniedResponse, denied)
	if deniedResponse.Code != http.StatusForbidden {
		t.Fatalf("reader status=%d body=%s", deniedResponse.Code, deniedResponse.Body.String())
	}

	request := httptest.NewRequest(http.MethodPost, path, nil)
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var result adminopenapi.LifecycleJobReconciliation
	if err = json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.RepositoryId.String() != repo.ID || result.Requeued != 1 || len(result.RequeuedJobIds) != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	jobs, err := store.ListLifecycleJobs(ctx, repo.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	pending, failed := 0, 0
	for _, job := range jobs {
		if job.Kind != repository.LifecycleJobIntelligence {
			continue
		}
		switch job.State {
		case repository.LifecycleJobPending:
			pending++
		case repository.LifecycleJobFailed:
			failed++
		}
	}
	if pending != 1 || failed != 1 {
		t.Fatalf("pending=%d failed=%d jobs=%#v", pending, failed, jobs)
	}
	audits, err := store.ListAudits(ctx, repository.AuditQuery{Repository: repo.Name, Operation: "lifecycle.intelligence_reconcile"})
	if err != nil || len(audits) != 1 || audits[0].Actor != "alice" {
		t.Fatalf("audits=%#v err=%v", audits, err)
	}
}
