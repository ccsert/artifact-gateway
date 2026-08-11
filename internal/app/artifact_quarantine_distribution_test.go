package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	rawprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/raw"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type rawQuarantineDistributionFixture struct {
	ctx        context.Context
	store      *repository.MemoryStore
	objects    *MemoryOCIObjectStore
	handler    http.Handler
	source     repository.HostedRepository
	target     repository.HostedRepository
	coordinate string
	digest     string
}

type blockingOpenRangeStore struct {
	OCIObjectStore
	opened  chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingOpenRangeStore) OpenRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, int64, error) {
	s.once.Do(func() { close(s.opened) })
	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	case <-s.release:
		return s.OCIObjectStore.OpenRange(ctx, key, offset, length)
	}
}

func newRawQuarantineDistributionFixture(t *testing.T) rawQuarantineDistributionFixture {
	t.Helper()
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	source, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "quarantine-distribution-source", Format: repository.FormatRaw,
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "quarantine-distribution-target", Format: repository.FormatRaw,
	})
	if err != nil {
		t.Fatal(err)
	}
	coordinate := "releases/quarantined-widget.bin"
	body := []byte("quarantine distribution widget")
	sum := sha256.Sum256(body)
	hexDigest := hex.EncodeToString(sum[:])
	digest := "sha256:" + hexDigest
	objectKey := "native/raw/sha256/" + hexDigest
	if err = objects.PutVerifiedReader(ctx, objectKey, bytes.NewReader(body), int64(len(body)), digest); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutRawAsset(ctx, repository.RawAsset{
		RepositoryID: source.ID,
		Path:         coordinate,
		Digest:       digest,
		ObjectKey:    objectKey,
		Size:         int64(len(body)),
	}); err != nil {
		t.Fatal(err)
	}
	return rawQuarantineDistributionFixture{
		ctx: ctx, store: store, objects: objects,
		handler:    NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator()),
		source:     source,
		target:     target,
		coordinate: coordinate,
		digest:     digest,
	}
}

func (f rawQuarantineDistributionFixture) replaceQuarantine(t *testing.T, state repository.ArtifactQuarantineState, expectedVersion string) repository.ArtifactQuarantine {
	t.Helper()
	value, err := f.store.ReplaceArtifactQuarantine(f.ctx, repository.ArtifactQuarantine{
		RepositoryID: f.source.ID,
		Format:       f.source.Format,
		Coordinate:   f.coordinate,
		Digest:       f.digest,
		State:        state,
		Reason:       "security review",
		UpdatedBy:    "security-admin",
	}, expectedVersion)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func (f rawQuarantineDistributionFixture) distributionRequest(t *testing.T, operation, idempotencyKey string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v2/repositories/"+f.source.ID+"/"+operation,
		strings.NewReader(`{"targetRepositoryId":"`+f.target.ID+`","coordinate":"`+f.coordinate+`","digest":"`+f.digest+`"}`),
	)
	authorize(request, "admin-secret")
	request.Header.Set("Idempotency-Key", idempotencyKey)
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return response
}

func (f rawQuarantineDistributionFixture) assertQuarantineDenialAudit(t *testing.T, operation string) {
	t.Helper()
	audits, err := f.store.ListAudits(f.ctx, repository.AuditQuery{Repository: f.target.Name, Operation: operation + ".quarantine"})
	if err != nil || len(audits) != 1 {
		t.Fatalf("%s quarantine audits=%#v err=%v", operation, audits, err)
	}
	audit := audits[0]
	if audit.Actor != "alice" || audit.Resource != f.coordinate || audit.Representation != f.digest || audit.AuthorizationReason != repository.ArtifactQuarantinedReason || audit.Status != http.StatusForbidden {
		t.Fatalf("%s quarantine audit=%#v", operation, audit)
	}
}

func TestRepositoryRawPromotionRejectsQuarantinedArtifactBeforeEnqueue(t *testing.T) {
	fixture := newRawQuarantineDistributionFixture(t)
	quarantine := fixture.replaceQuarantine(t, repository.ArtifactQuarantineStateQuarantined, "0")

	denied := fixture.distributionRequest(t, "promotions", "quarantined-raw-promotion")
	if denied.Code != http.StatusForbidden || !strings.Contains(denied.Body.String(), `"code":"artifact_quarantined"`) {
		t.Fatalf("quarantined promotion=%d body=%s", denied.Code, denied.Body.String())
	}
	fixture.assertQuarantineDenialAudit(t, "promote")
	jobs, err := fixture.store.ListLifecycleJobs(fixture.ctx, fixture.target.ID, 10)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("denied promotion jobs=%#v err=%v", jobs, err)
	}

	fixture.replaceQuarantine(t, repository.ArtifactQuarantineStateReleased, quarantine.Version)
	accepted := fixture.distributionRequest(t, "promotions", "quarantined-raw-promotion")
	if accepted.Code != http.StatusAccepted || !strings.Contains(accepted.Body.String(), `"kind":"promotion"`) {
		t.Fatalf("released promotion=%d body=%s", accepted.Code, accepted.Body.String())
	}
	jobs, err = fixture.store.ListLifecycleJobs(fixture.ctx, fixture.target.ID, 10)
	if err != nil || len(jobs) != 1 || jobs[0].Kind != repository.LifecycleJobPromotion {
		t.Fatalf("released promotion jobs=%#v err=%v", jobs, err)
	}
}

func TestRepositoryRawReplicationRejectsQuarantinedArtifactBeforePlanning(t *testing.T) {
	fixture := newRawQuarantineDistributionFixture(t)
	quarantine := fixture.replaceQuarantine(t, repository.ArtifactQuarantineStateQuarantined, "0")

	denied := fixture.distributionRequest(t, "replications", "quarantined-raw-replication")
	if denied.Code != http.StatusForbidden || !strings.Contains(denied.Body.String(), `"code":"artifact_quarantined"`) {
		t.Fatalf("quarantined replication=%d body=%s", denied.Code, denied.Body.String())
	}
	fixture.assertQuarantineDenialAudit(t, "replicate")
	plans, err := fixture.store.ListReplicationPlans(fixture.ctx, fixture.target.ID, 10)
	if err != nil || len(plans) != 0 {
		t.Fatalf("denied replication plans=%#v err=%v", plans, err)
	}

	fixture.replaceQuarantine(t, repository.ArtifactQuarantineStateReleased, quarantine.Version)
	accepted := fixture.distributionRequest(t, "replications", "quarantined-raw-replication")
	if accepted.Code != http.StatusAccepted || !strings.Contains(accepted.Body.String(), `"state":"pending"`) {
		t.Fatalf("released replication=%d body=%s", accepted.Code, accepted.Body.String())
	}
	plans, err = fixture.store.ListReplicationPlans(fixture.ctx, fixture.target.ID, 10)
	if err != nil || len(plans) != 1 || plans[0].State != "pending" {
		t.Fatalf("released replication plans=%#v err=%v", plans, err)
	}
}

func TestRawReplicationWorkerRechecksQuarantineBeforePublish(t *testing.T) {
	fixture := newRawQuarantineDistributionFixture(t)
	const idempotencyKey = "raw-replication-worker-quarantine"
	accepted := fixture.distributionRequest(t, "replications", idempotencyKey)
	if accepted.Code != http.StatusAccepted {
		t.Fatalf("replication=%d body=%s", accepted.Code, accepted.Body.String())
	}
	plans, err := fixture.store.ListReplicationPlans(fixture.ctx, fixture.target.ID, 10)
	if err != nil || len(plans) != 1 {
		t.Fatalf("replication plans=%#v err=%v", plans, err)
	}
	plan := plans[0]
	destination := NewMemoryOCIObjectStore()
	blockingSource := &blockingOpenRangeStore{
		OCIObjectStore: fixture.objects,
		opened:         make(chan struct{}),
		release:        make(chan struct{}),
	}
	workerDone := make(chan error, 1)
	go func() {
		workerDone <- (RawReplication{
			Store: fixture.store, Source: blockingSource, Destination: destination, ChunkBytes: 3,
		}).RunJobs(fixture.ctx, 1)
	}()
	select {
	case <-blockingSource.opened:
	case <-time.After(2 * time.Second):
		t.Fatal("replication worker did not reach byte copy")
	}
	quarantine := fixture.replaceQuarantine(t, repository.ArtifactQuarantineStateQuarantined, "0")
	close(blockingSource.release)
	select {
	case err = <-workerDone:
		if err != nil {
			t.Fatalf("run replication worker: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("replication worker did not finish after quarantine transition")
	}
	if _, err = fixture.store.GetRawAsset(fixture.ctx, fixture.target.ID, fixture.coordinate); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("target artifact should remain unpublished, err=%v", err)
	}
	persisted, err := fixture.store.GetReplicationPlan(fixture.ctx, fixture.target.ID, plan.ID)
	if err != nil || persisted.State != "failed" || persisted.LastError != repository.ArtifactQuarantinedReason || persisted.Attempts != 0 || !persisted.NextAttemptAt.IsZero() {
		t.Fatalf("replication plan=%#v err=%v", persisted, err)
	}
	if err = (RawReplication{Store: fixture.store, Source: fixture.objects, Destination: destination}).RunJobs(fixture.ctx, 1); err != nil {
		t.Fatalf("parked worker run: %v", err)
	}
	if unchanged, loadErr := fixture.store.GetReplicationPlan(fixture.ctx, fixture.target.ID, plan.ID); loadErr != nil || unchanged.State != "failed" || unchanged.Attempts != 0 {
		t.Fatalf("parked plan was reclaimed: plan=%#v err=%v", unchanged, loadErr)
	}

	fixture.replaceQuarantine(t, repository.ArtifactQuarantineStateReleased, quarantine.Version)
	replayed := fixture.distributionRequest(t, "replications", idempotencyKey)
	if replayed.Code != http.StatusAccepted {
		t.Fatalf("released replay=%d body=%s", replayed.Code, replayed.Body.String())
	}
	requeued, err := fixture.store.GetReplicationPlan(fixture.ctx, fixture.target.ID, plan.ID)
	if err != nil || requeued.ID != plan.ID || requeued.State != "pending" || requeued.Attempts != 0 {
		t.Fatalf("requeued plan=%#v err=%v", requeued, err)
	}
	if err = (RawReplication{Store: fixture.store, Source: fixture.objects, Destination: destination}).RunJobs(fixture.ctx, 1); err != nil {
		t.Fatalf("released worker run: %v", err)
	}
	published, err := fixture.store.GetRawAsset(fixture.ctx, fixture.target.ID, fixture.coordinate)
	if err != nil || published.Digest != fixture.digest {
		t.Fatalf("released target artifact=%#v err=%v", published, err)
	}
	completed, err := fixture.store.GetReplicationPlan(fixture.ctx, fixture.target.ID, plan.ID)
	if err != nil || completed.State != "completed" || completed.Attempts != 1 {
		t.Fatalf("completed plan=%#v err=%v", completed, err)
	}
}

func TestRawPromotionWorkerRechecksQuarantineBeforePublish(t *testing.T) {
	fixture := newRawQuarantineDistributionFixture(t)
	accepted := fixture.distributionRequest(t, "promotions", "raw-worker-quarantine")
	if accepted.Code != http.StatusAccepted {
		t.Fatalf("promotion=%d body=%s", accepted.Code, accepted.Body.String())
	}
	fixture.replaceQuarantine(t, repository.ArtifactQuarantineStateQuarantined, "0")

	err := (rawprotocol.NativePromotion{Store: fixture.store}).RunJobs(fixture.ctx, 1)
	if err == nil || !strings.Contains(err.Error(), repository.ArtifactQuarantinedReason) {
		t.Fatalf("run promotion err=%v", err)
	}
	if _, err = fixture.store.GetRawAsset(fixture.ctx, fixture.target.ID, fixture.coordinate); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("target artifact should remain unpublished, err=%v", err)
	}
	jobs, err := fixture.store.ListLifecycleJobs(fixture.ctx, fixture.target.ID, 10)
	if err != nil || len(jobs) != 1 || jobs[0].State != repository.LifecycleJobRetrying || jobs[0].LastError != repository.ArtifactQuarantinedReason {
		t.Fatalf("promotion jobs=%#v err=%v", jobs, err)
	}
}

func TestSecurityPolicyEvaluationReportsQuarantineWhenPolicyIsDisabled(t *testing.T) {
	fixture := newRawQuarantineDistributionFixture(t)
	fixture.replaceQuarantine(t, repository.ArtifactQuarantineStateQuarantined, "0")
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v2/repositories/"+fixture.target.ID+"/security-policy:evaluate",
		strings.NewReader(`{"sourceRepositoryId":"`+fixture.source.ID+`","coordinate":"`+fixture.coordinate+`","digest":"`+fixture.digest+`"}`),
	)
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"allowed":false`) || !strings.Contains(response.Body.String(), `"enforced":true`) || !strings.Contains(response.Body.String(), repository.ArtifactQuarantinedReason) {
		t.Fatalf("evaluation=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRawProtocolReadRemainsAvailableWhileArtifactIsQuarantined(t *testing.T) {
	fixture := newRawQuarantineDistributionFixture(t)
	fixture.replaceQuarantine(t, repository.ArtifactQuarantineStateQuarantined, "0")
	handler := NewGatewayHandler(Dependencies{NativeOCIObjectStore: fixture.objects}, fixture.store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodGet, "/raw/"+fixture.source.Name+"/"+fixture.coordinate, nil)
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "quarantine distribution widget" {
		t.Fatalf("raw read=%d body=%q", response.Code, response.Body.String())
	}
}
