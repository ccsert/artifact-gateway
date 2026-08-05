package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestHostedRepositoryDeleteAdvancesToDeletedAfterWorkerRun(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID:     uuid.NewString(),
		Name:   "repository-deletion-api",
		Format: repository.FormatRaw,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())

	request := httptest.NewRequest(http.MethodDelete, "/api/v2/repositories/"+repo.ID, nil)
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("delete status=%d body=%s", response.Code, response.Body.String())
	}

	if finalized, err := (RepositoryDeletionWorker{Store: store}).Run(ctx); err != nil || finalized != 1 {
		t.Fatalf("finalized=%d err=%v", finalized, err)
	}

	getRequest := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID, nil)
	authorize(getRequest, "admin-secret")
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, getRequest)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", getResponse.Code, getResponse.Body.String())
	}
	var current repository.HostedRepository
	if err := json.NewDecoder(getResponse.Body).Decode(&current); err != nil {
		t.Fatal(err)
	}
	if current.State != repository.RepositoryDeleted {
		t.Fatalf("state=%q, want deleted", current.State)
	}
}
