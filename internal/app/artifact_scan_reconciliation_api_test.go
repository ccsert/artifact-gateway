package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/scanning"
	"github.com/google/uuid"
)

type artifactScanAPIFixture struct {
	ctx     context.Context
	store   *repository.MemoryStore
	repo    repository.HostedRepository
	handler http.Handler
}

func newArtifactScanAPIFixture(t *testing.T) artifactScanAPIFixture {
	t.Helper()
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "raw-releases", Format: repository.FormatRaw,
	})
	if err != nil {
		t.Fatal(err)
	}
	policy := repository.DefaultRepositorySecurityPolicy()
	policy.AutoScanOnPublish = true
	if _, err = store.ReplaceRepositorySecurityPolicy(ctx, repo.ID, policy, policy.Version); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{
		ArtifactScanner: scanning.ScannerFunc(func(context.Context, scanning.Artifact) (scanning.Report, error) {
			return scanning.Report{}, nil
		}),
		ArtifactScannerFormats: []repository.Format{repository.FormatRaw},
	}, store, TestAdapter{}, Authenticator{AdminToken: "admin", AdminActor: "operator"})
	return artifactScanAPIFixture{ctx: ctx, store: store, repo: repo, handler: handler}
}

func (f artifactScanAPIFixture) putAsset(t *testing.T, path string) repository.RawAsset {
	t.Helper()
	asset := repository.RawAsset{
		RepositoryID: f.repo.ID,
		Path:         path,
		Digest:       testScanDigest(path),
		ObjectKey:    "raw/" + path,
		Size:         1,
	}
	stored, err := f.store.PutRawAsset(f.ctx, asset)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func (f artifactScanAPIFixture) status(t *testing.T, asset repository.RawAsset) adminopenapi.ArtifactScanStatus {
	t.Helper()
	path := fmt.Sprintf("/api/v2/repositories/%s/artifact-scans?coordinate=%s&digest=%s", f.repo.ID, url.QueryEscape(asset.Path), url.QueryEscape(asset.Digest))
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer admin")
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status code=%d body=%s", response.Code, response.Body.String())
	}
	var result adminopenapi.ArtifactScanStatus
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func (f artifactScanAPIFixture) reconcile(t *testing.T) adminopenapi.ArtifactScanReconciliation {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+f.repo.ID+"/artifact-scans:reconcile?limit=10", nil)
	request.Header.Set("Authorization", "Bearer admin")
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("reconcile code=%d body=%s", response.Code, response.Body.String())
	}
	var result adminopenapi.ArtifactScanReconciliation
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestArtifactScanStatusAPIUsesLatestLifecycleJob(t *testing.T) {
	fixture := newArtifactScanAPIFixture(t)
	asset := fixture.putAsset(t, "widget.bin")
	if before := fixture.status(t, asset); before.State != adminopenapi.ArtifactScanStatusStateNever || before.Job != nil {
		t.Fatalf("before=%#v", before)
	}
	job, _, err := repository.EnqueueArtifactScanJob(fixture.ctx, fixture.store, fixture.repo.ID, "pending", repository.ArtifactScanPayload{
		Format: repository.FormatRaw, Coordinate: asset.Path, Digest: asset.Digest,
	})
	if err != nil {
		t.Fatal(err)
	}
	after := fixture.status(t, asset)
	if after.State != adminopenapi.ArtifactScanStatusStatePending || after.Job == nil || after.Job.Id != job.ID {
		t.Fatalf("after=%#v", after)
	}
}

func TestArtifactScanReconciliationQueuesRetriesAndSkips(t *testing.T) {
	fixture := newArtifactScanAPIFixture(t)
	missing := fixture.putAsset(t, "missing.bin")
	cancelledAsset := fixture.putAsset(t, "cancelled.bin")
	pendingAsset := fixture.putAsset(t, "pending.bin")

	cancelled, _, err := repository.EnqueueArtifactScanJob(fixture.ctx, fixture.store, fixture.repo.ID, "cancelled", repository.ArtifactScanPayload{
		Format: repository.FormatRaw, Coordinate: cancelledAsset.Path, Digest: cancelledAsset.Digest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.store.CancelLifecycleJob(fixture.ctx, fixture.repo.ID, cancelled.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = repository.EnqueueArtifactScanJob(fixture.ctx, fixture.store, fixture.repo.ID, "pending", repository.ArtifactScanPayload{
		Format: repository.FormatRaw, Coordinate: pendingAsset.Path, Digest: pendingAsset.Digest,
	}); err != nil {
		t.Fatal(err)
	}

	result := fixture.reconcile(t)
	if result.Inspected != 3 || result.Enqueued != 1 || result.Retried != 1 || result.Skipped != 1 || len(result.JobIds) != 2 {
		t.Fatalf("result=%#v", result)
	}
	if status := fixture.status(t, missing); status.State != adminopenapi.ArtifactScanStatusStatePending || status.Job == nil {
		t.Fatalf("missing status=%#v", status)
	}
}

func TestArtifactScanReconciliationIsIdempotent(t *testing.T) {
	fixture := newArtifactScanAPIFixture(t)
	fixture.putAsset(t, "widget.bin")
	first := fixture.reconcile(t)
	second := fixture.reconcile(t)
	if first.Enqueued != 1 || second.Enqueued != 0 || second.Retried != 0 || second.Skipped != 1 {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
}

func TestArtifactScanReconciliationBackfillsWhenAutoScanIsDisabled(t *testing.T) {
	fixture := newArtifactScanAPIFixture(t)
	policy, err := fixture.store.GetRepositorySecurityPolicy(fixture.ctx, fixture.repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	policy.AutoScanOnPublish = false
	if _, err = fixture.store.ReplaceRepositorySecurityPolicy(fixture.ctx, fixture.repo.ID, policy, policy.Version); err != nil {
		t.Fatal(err)
	}
	fixture.putAsset(t, "historical.bin")
	result := fixture.reconcile(t)
	if result.Enqueued != 1 || result.Inspected != 1 {
		t.Fatalf("result=%#v", result)
	}
}
