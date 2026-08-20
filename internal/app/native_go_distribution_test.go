package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestRepositoryGoPromotionAndReplicationPublishCompleteTargetVersions(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	create := func(name string) repository.HostedRepository {
		t.Helper()
		repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
			ID: uuid.NewString(), Name: name, Format: repository.FormatGo, Type: repository.RepositoryTypeHosted,
		})
		if err != nil {
			t.Fatal(err)
		}
		return repo
	}
	source, promotedTarget, replicatedTarget := create("go-distribution-source"), create("go-promotion-target"), create("go-replication-target")
	const modulePath, version = "example.com/team/distributed", "v1.2.3"
	bodies := map[string][]byte{
		"info": []byte(`{"Version":"v1.2.3","Time":"2026-08-19T00:00:00Z"}`),
		"mod":  []byte("module " + modulePath + "\n\ngo 1.26\n"),
		"zip":  []byte("verified-go-module-zip"),
	}
	assets := make([]repository.GoModuleAsset, 0, 3)
	for _, kind := range []string{"info", "mod", "zip"} {
		sum := sha256.Sum256(bodies[kind])
		digestHex := hex.EncodeToString(sum[:])
		asset := repository.GoModuleAsset{
			RepositoryID: source.ID, Module: modulePath, Version: version, Kind: kind,
			Digest: "sha256:" + digestHex, ObjectKey: "native/go/sha256/" + digestHex, Size: int64(len(bodies[kind])),
		}
		if err := objects.Put(ctx, asset.ObjectKey, bodies[kind]); err != nil {
			t.Fatal(err)
		}
		assets = append(assets, asset)
	}
	if _, _, err := store.PublishGoModule(ctx, repository.GoModulePublication{
		Version: repository.GoModuleVersion{RepositoryID: source.ID, Module: modulePath, Version: version, PublishedAt: time.Now().UTC(), Publisher: "distribution-test"},
		Assets:  assets,
	}); err != nil {
		t.Fatal(err)
	}
	zipDigest := assets[2].Digest
	handler := NewGatewayHandler(Dependencies{NativeGoObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	distribute := func(operation string, target repository.HostedRepository, key string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+source.ID+"/"+operation,
			strings.NewReader(`{"targetRepositoryId":"`+target.ID+`","coordinate":"`+modulePath+`@`+version+`","digest":"`+zipDigest+`"}`))
		authorize(request, "admin-secret")
		request.Header.Set("Idempotency-Key", key)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	promotion := distribute("promotions", promotedTarget, "go-promote-v1.2.3")
	if promotion.Code != http.StatusAccepted {
		t.Fatalf("Go promotion=%d body=%s", promotion.Code, promotion.Body.String())
	}
	if err := (NativeGoPromotion{Store: store}).RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	for _, sourceAsset := range assets {
		promoted, err := store.GetGoModuleAsset(ctx, promotedTarget.ID, modulePath, version, sourceAsset.Kind)
		if err != nil || promoted.Digest != sourceAsset.Digest || promoted.ObjectKey != sourceAsset.ObjectKey {
			t.Fatalf("promoted %s=%#v err=%v", sourceAsset.Kind, promoted, err)
		}
	}

	replication := distribute("replications", replicatedTarget, "go-replicate-v1.2.3")
	if replication.Code != http.StatusAccepted {
		t.Fatalf("Go replication=%d body=%s", replication.Code, replication.Body.String())
	}
	if err := (GoReplication{Store: store, Source: objects, Destination: objects, ChunkBytes: 4}).RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	for _, sourceAsset := range assets {
		replicated, err := store.GetGoModuleAsset(ctx, replicatedTarget.ID, modulePath, version, sourceAsset.Kind)
		if err != nil || replicated.Digest != sourceAsset.Digest || replicated.ObjectKey == sourceAsset.ObjectKey {
			t.Fatalf("replicated %s=%#v err=%v", sourceAsset.Kind, replicated, err)
		}
		body, err := objects.Get(ctx, replicated.ObjectKey)
		if err != nil || !bytes.Equal(body, bodies[sourceAsset.Kind]) {
			t.Fatalf("replicated %s bytes=%q err=%v", sourceAsset.Kind, body, err)
		}
	}
	identities, err := store.ListArtifactIdentities(ctx, source.ID, repository.FormatGo, repository.ArtifactIdentityDistribution, modulePath, 10)
	if err != nil || len(identities) != 1 || identities[0].Coordinate != modulePath+"@"+version || identities[0].Digest != zipDigest {
		t.Fatalf("Go distribution identities=%#v err=%v", identities, err)
	}
}

func TestRepositoryGoDistributionRejectsQuarantinedRepresentationBeforeEnqueue(t *testing.T) {
	fixture := newGoDistributionFixture(t)
	coordinate := fixture.modulePath + "@" + fixture.version
	if _, err := fixture.store.ReplaceArtifactQuarantine(fixture.ctx, repository.ArtifactQuarantine{
		RepositoryID: fixture.source.ID,
		Format:       repository.FormatGo,
		Coordinate:   coordinate,
		Digest:       fixture.assets[0].Digest,
		State:        repository.ArtifactQuarantineStateQuarantined,
		Reason:       "quarantined Go info representation",
		UpdatedBy:    "security-admin",
	}, "0"); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"promotions", "replications"} {
		response := fixture.distribute(t, operation, fixture.target.ID, "go-quarantine-"+operation)
		if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"artifact_quarantined"`) {
			t.Fatalf("%s=%d body=%s", operation, response.Code, response.Body.String())
		}
	}
	jobs, err := fixture.store.ListLifecycleJobs(fixture.ctx, fixture.target.ID, 10)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("quarantined Go promotion jobs=%#v err=%v", jobs, err)
	}
	plans, err := fixture.store.ListReplicationPlans(fixture.ctx, fixture.target.ID, 10)
	if err != nil || len(plans) != 0 {
		t.Fatalf("quarantined Go replication plans=%#v err=%v", plans, err)
	}
}

func TestRepositoryGoDistributionRejectsUnavailableSourceBeforeEnqueue(t *testing.T) {
	fixture := newGoDistributionFixture(t)
	missingDigest := "sha256:" + strings.Repeat("f", 64)
	for _, operation := range []string{"promotions", "replications"} {
		response := fixture.distributeDigest(t, operation, fixture.target.ID, "go-missing-"+operation, missingDigest)
		if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"not_found"`) {
			t.Fatalf("%s unavailable source=%d body=%s", operation, response.Code, response.Body.String())
		}
	}
	jobs, err := fixture.store.ListLifecycleJobs(fixture.ctx, fixture.target.ID, 10)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("unavailable Go promotion jobs=%#v err=%v", jobs, err)
	}
	plans, err := fixture.store.ListReplicationPlans(fixture.ctx, fixture.target.ID, 10)
	if err != nil || len(plans) != 0 {
		t.Fatalf("unavailable Go replication plans=%#v err=%v", plans, err)
	}
}

func TestGoPromotionRejectsDifferentExistingInfoSnapshot(t *testing.T) {
	fixture := newGoDistributionFixture(t)
	source, err := loadGoDistributionPublication(fixture.ctx, fixture.store, fixture.source.ID, fixture.modulePath, fixture.version, fixture.assets[2].Digest)
	if err != nil {
		t.Fatal(err)
	}
	existing := goTargetPublication(source, fixture.target.ID, nil)
	alternateInfo := []byte(`{"Version":"v1.0.0","Time":"2026-08-20T00:00:00Z"}`)
	sum := sha256.Sum256(alternateInfo)
	for index := range existing.Assets {
		if existing.Assets[index].Kind == "info" {
			existing.Assets[index].Digest = "sha256:" + hex.EncodeToString(sum[:])
			existing.Assets[index].ObjectKey = "native/go/sha256/" + hex.EncodeToString(sum[:])
			existing.Assets[index].Size = int64(len(alternateInfo))
		}
	}
	if _, _, err = fixture.store.PublishGoModule(fixture.ctx, existing); err != nil {
		t.Fatal(err)
	}
	promotion := NativeGoPromotion{Store: fixture.store}
	if _, _, err = promotion.Enqueue(fixture.ctx, fixture.target.ID, "go-strict-info", GoPromotionPayload{
		SourceRepositoryID: fixture.source.ID,
		Module:             fixture.modulePath,
		Version:            fixture.version,
		Digest:             fixture.assets[2].Digest,
	}); err != nil {
		t.Fatal(err)
	}
	if err = promotion.RunJobs(fixture.ctx, 1); err == nil || !strings.Contains(err.Error(), "publish target Go module version failed") {
		t.Fatalf("promotion with different target info err=%v", err)
	}
	stored, err := fixture.store.GetGoModuleAsset(fixture.ctx, fixture.target.ID, fixture.modulePath, fixture.version, "info")
	if err != nil || stored.Digest != existing.Assets[0].Digest {
		t.Fatalf("target info changed=%#v err=%v", stored, err)
	}
	jobs, err := fixture.store.ListLifecycleJobs(fixture.ctx, fixture.target.ID, 10)
	if err != nil || len(jobs) != 1 || jobs[0].State != repository.LifecycleJobRetrying {
		t.Fatalf("strict Go promotion jobs=%#v err=%v", jobs, err)
	}
}

func TestGoPromotionWorkerRechecksAllRepresentationQuarantine(t *testing.T) {
	fixture := newGoDistributionFixture(t)
	promotion := NativeGoPromotion{Store: fixture.store}
	if _, _, err := promotion.Enqueue(fixture.ctx, fixture.target.ID, "go-worker-quarantine", GoPromotionPayload{
		SourceRepositoryID: fixture.source.ID,
		Module:             fixture.modulePath,
		Version:            fixture.version,
		Digest:             fixture.assets[2].Digest,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.ReplaceArtifactQuarantine(fixture.ctx, repository.ArtifactQuarantine{
		RepositoryID: fixture.source.ID,
		Format:       repository.FormatGo,
		Coordinate:   fixture.modulePath + "@" + fixture.version,
		Digest:       fixture.assets[1].Digest,
		State:        repository.ArtifactQuarantineStateQuarantined,
		Reason:       "quarantined Go mod representation",
		UpdatedBy:    "security-admin",
	}, "0"); err != nil {
		t.Fatal(err)
	}

	err := promotion.RunJobs(fixture.ctx, 1)
	if err == nil || !strings.Contains(err.Error(), repository.ArtifactQuarantinedReason) {
		t.Fatalf("Go promotion worker err=%v", err)
	}
	if _, err = fixture.store.GetGoModuleVersion(fixture.ctx, fixture.target.ID, fixture.modulePath, fixture.version); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("quarantined target Go version should remain unavailable, err=%v", err)
	}
	jobs, err := fixture.store.ListLifecycleJobs(fixture.ctx, fixture.target.ID, 10)
	if err != nil || len(jobs) != 1 || jobs[0].State != repository.LifecycleJobRetrying || jobs[0].LastError != repository.ArtifactQuarantinedReason {
		t.Fatalf("Go promotion jobs=%#v err=%v", jobs, err)
	}
}

func TestGoReplicationWorkerParksWhenNonZipRepresentationIsQuarantined(t *testing.T) {
	fixture := newGoDistributionFixture(t)
	coordinate := fixture.modulePath + "@" + fixture.version
	plan := repository.ReplicationPlan{
		ID: "go-quarantine-plan", SourceRepositoryID: fixture.source.ID, TargetRepositoryID: fixture.target.ID,
		Format: repository.FormatGo, Coordinate: coordinate, Digest: fixture.assets[2].Digest, IdempotencyKey: "go-quarantine",
	}
	checkpoints := make([]repository.ReplicationCheckpoint, 0, 3)
	for _, asset := range fixture.assets {
		checkpoints = append(checkpoints, repository.ReplicationCheckpoint{
			SourceObjectKey: asset.ObjectKey,
			ObjectKey:       goReplicationTargetObjectKey(fixture.target.ID, asset.Digest, asset.Kind),
			Digest:          asset.Digest,
			Size:            asset.Size,
		})
	}
	if _, _, err := fixture.store.CreateReplicationPlan(fixture.ctx, plan, checkpoints); err != nil {
		t.Fatal(err)
	}
	destination := &quarantineBeforeReplicationPublishStore{
		OCIObjectStore: fixture.objects,
		Repository:     fixture.store,
		Plan:           plan,
		Digest:         fixture.assets[1].Digest,
		Reason:         "block queued Go replication on mod representation",
	}

	if err := (GoReplication{Store: fixture.store, Source: fixture.objects, Destination: destination, ChunkBytes: 4}).RunJobs(fixture.ctx, 1); err != nil {
		t.Fatalf("run Go replication worker: %v", err)
	}
	requireQuarantineBeforeReplicationPublish(t, destination)
	if _, err := fixture.store.GetGoModuleVersion(fixture.ctx, fixture.target.ID, fixture.modulePath, fixture.version); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("quarantined target Go version should remain unavailable, err=%v", err)
	}
	requireQuarantineReplicationPlanParked(t, fixture.ctx, fixture.store, plan)
}

func TestRepositoryGoDistributionRejectsProxyTarget(t *testing.T) {
	fixture := newGoDistributionFixture(t)
	proxy, err := fixture.store.CreateHostedRepository(fixture.ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "go-distribution-proxy-target", Format: repository.FormatGo,
		Type: repository.RepositoryTypeProxy, Endpoint: "https://proxy.example", AllowedHosts: []string{"proxy.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"promotions", "replications"} {
		response := fixture.distribute(t, operation, proxy.ID, "go-proxy-target-"+operation)
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"invalid_target"`) {
			t.Fatalf("%s proxy target=%d body=%s", operation, response.Code, response.Body.String())
		}
	}
}

func TestGoReplicationSnapshotRequiresCanonicalTargetKeys(t *testing.T) {
	fixture := newGoDistributionFixture(t)
	checkpoints := make([]repository.ReplicationCheckpoint, 0, 3)
	for _, asset := range fixture.assets {
		checkpoints = append(checkpoints, repository.ReplicationCheckpoint{
			SourceObjectKey: asset.ObjectKey,
			ObjectKey:       goReplicationTargetObjectKey(fixture.target.ID, asset.Digest, asset.Kind),
			Digest:          asset.Digest,
			Size:            asset.Size,
		})
	}
	publication, err := loadGoDistributionPublication(fixture.ctx, fixture.store, fixture.source.ID, fixture.modulePath, fixture.version, fixture.assets[2].Digest)
	if err != nil {
		t.Fatal(err)
	}
	if !goReplicationSnapshotMatches(publication, checkpoints, fixture.target.ID) {
		t.Fatal("canonical Go replication snapshot did not match")
	}
	checkpoints[0].ObjectKey = "native/go/replication/other/" + strings.TrimPrefix(checkpoints[0].Digest, "sha256:") + "/info"
	if goReplicationSnapshotMatches(publication, checkpoints, fixture.target.ID) {
		t.Fatal("Go replication accepted a checkpoint outside the target namespace")
	}
}

type goDistributionFixture struct {
	ctx        context.Context
	store      *repository.MemoryStore
	objects    *MemoryOCIObjectStore
	handler    http.Handler
	source     repository.HostedRepository
	target     repository.HostedRepository
	modulePath string
	version    string
	assets     []repository.GoModuleAsset
}

func newGoDistributionFixture(t *testing.T) goDistributionFixture {
	t.Helper()
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	create := func(name string) repository.HostedRepository {
		t.Helper()
		item, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
			ID: uuid.NewString(), Name: name, Format: repository.FormatGo, Type: repository.RepositoryTypeHosted,
		})
		if err != nil {
			t.Fatal(err)
		}
		return item
	}
	source, target := create("go-distribution-fixture-source"), create("go-distribution-fixture-target")
	const modulePath, version = "example.com/team/quarantine", "v1.0.0"
	bodies := map[string][]byte{
		"info": []byte(`{"Version":"v1.0.0","Time":"2026-08-19T00:00:00Z"}`),
		"mod":  []byte("module " + modulePath + "\n\ngo 1.26\n"),
		"zip":  []byte("complete-go-distribution-zip"),
	}
	assets := make([]repository.GoModuleAsset, 0, 3)
	for _, kind := range []string{"info", "mod", "zip"} {
		sum := sha256.Sum256(bodies[kind])
		digestHex := hex.EncodeToString(sum[:])
		asset := repository.GoModuleAsset{
			RepositoryID: source.ID, Module: modulePath, Version: version, Kind: kind,
			Digest: "sha256:" + digestHex, ObjectKey: "native/go/sha256/" + digestHex, Size: int64(len(bodies[kind])),
		}
		if err := objects.Put(ctx, asset.ObjectKey, bodies[kind]); err != nil {
			t.Fatal(err)
		}
		assets = append(assets, asset)
	}
	if _, _, err := store.PublishGoModule(ctx, repository.GoModulePublication{
		Version: repository.GoModuleVersion{RepositoryID: source.ID, Module: modulePath, Version: version, PublishedAt: time.Now().UTC(), Publisher: "distribution-test"},
		Assets:  assets,
	}); err != nil {
		t.Fatal(err)
	}
	return goDistributionFixture{
		ctx: ctx, store: store, objects: objects,
		handler: NewGatewayHandler(Dependencies{NativeGoObjectStore: objects}, store, TestAdapter{}, testAuthenticator()),
		source:  source, target: target, modulePath: modulePath, version: version, assets: assets,
	}
}

func (f goDistributionFixture) distribute(t *testing.T, operation, targetID, key string) *httptest.ResponseRecorder {
	return f.distributeDigest(t, operation, targetID, key, f.assets[2].Digest)
}

func (f goDistributionFixture) distributeDigest(t *testing.T, operation, targetID, key, digest string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+f.source.ID+"/"+operation,
		strings.NewReader(`{"targetRepositoryId":"`+targetID+`","coordinate":"`+f.modulePath+`@`+f.version+`","digest":"`+digest+`"}`))
	authorize(request, "admin-secret")
	request.Header.Set("Idempotency-Key", key)
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return response
}
