//go:build integration

package app

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresRustFSCargoPromotionAndReplicationReadback(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	endpoint := os.Getenv("TEST_RUSTFS_ENDPOINT")
	accessKey := os.Getenv("TEST_RUSTFS_ACCESS_KEY")
	secretKey := os.Getenv("TEST_RUSTFS_SECRET_KEY")
	if databaseURL == "" || endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and RustFS integration environment is required")
	}
	ctx := context.Background()
	storeA, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = storeA.Close() }()
	storeB, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = storeB.Close() }()
	bucketSuffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	sourceObjects, err := NewRustFSOCIObjectStore(endpoint, accessKey, secretKey, "cargo-dist-source-"+bucketSuffix)
	if err != nil {
		t.Fatal(err)
	}
	if err := sourceObjects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	destinationObjects, err := NewRustFSOCIObjectStore(endpoint, accessKey, secretKey, "cargo-dist-target-"+bucketSuffix)
	if err != nil {
		t.Fatal(err)
	}
	if err := destinationObjects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	source, err := storeA.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "cargo-dist-src-" + uuid.NewString()[:8], Format: repository.FormatCargo})
	if err != nil {
		t.Fatal(err)
	}
	promotionTarget, err := storeA.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "cargo-dist-prm-" + uuid.NewString()[:8], Format: repository.FormatCargo})
	if err != nil {
		t.Fatal(err)
	}
	replicationTarget, err := storeA.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "cargo-dist-rep-" + uuid.NewString()[:8], Format: repository.FormatCargo})
	if err != nil {
		t.Fatal(err)
	}
	crateName := "dist" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	payload, archive := cargoC0PublishFixture(t, crateName, "1.0.0", crateName)
	handlerA := NewGatewayHandler(Dependencies{NativeCargoObjectStore: sourceObjects}, storeA, TestAdapter{}, testAuthenticator())
	request := func(handler http.Handler, method, path string, body []byte) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Host = "localhost:8080"
		authorize(r, "admin-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := request(handlerA, http.MethodPut, "/cargo/"+source.Name+"/api/v1/crates/new", payload); w.Code != http.StatusOK {
		t.Fatalf("source publish=%d %s", w.Code, w.Body.String())
	}
	publication, err := storeB.GetCargoPublication(ctx, source.ID, crateName, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := storeA.SetCargoYanked(ctx, source.ID, crateName, "1.0.0", true); err != nil {
		t.Fatal(err)
	}
	promoter := NativeCargoPromotion{Store: storeB, Objects: sourceObjects, Intelligence: storeB}
	if _, _, err := promoter.Enqueue(ctx, promotionTarget.ID, "cargo-pg-promote", CargoPromotionPayload{SourceRepositoryID: source.ID,
		Name: crateName, Version: "1.0.0", Digest: publication.Digest}); err != nil {
		t.Fatal(err)
	}
	if err := promoter.RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	promoted, err := storeA.GetCargoPublication(ctx, promotionTarget.ID, crateName, "1.0.0")
	if err != nil || !promoted.Yanked || promoted.Digest != publication.Digest || !bytes.Equal(promoted.IndexRow, publication.IndexRow) {
		t.Fatalf("PostgreSQL promoted Cargo=%+v err=%v", promoted, err)
	}
	plan, _, err := storeA.CreateReplicationPlan(ctx, repository.ReplicationPlan{ID: uuid.NewString(), SourceRepositoryID: source.ID,
		TargetRepositoryID: replicationTarget.ID, Format: repository.FormatCargo, Coordinate: crateName + "@1.0.0",
		Digest: publication.Digest, IdempotencyKey: "cargo-pg-replicate"}, []repository.ReplicationCheckpoint{{
		SourceObjectKey: publication.ObjectKey, ObjectKey: publication.ObjectKey, Digest: publication.Digest, Size: publication.Size,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storeB.GetCargoPublication(ctx, replicationTarget.ID, crateName, "1.0.0"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("target visible before byte copy: %v", err)
	}
	if _, err := destinationObjects.Stat(ctx, publication.ObjectKey); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("target bytes existed before copy: %v", err)
	}
	if err := (CargoReplication{Store: storeB, Source: sourceObjects, Destination: destinationObjects, ChunkBytes: 8}).RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	completed, err := storeA.GetReplicationPlan(ctx, replicationTarget.ID, plan.ID)
	if err != nil || completed.State != "completed" {
		t.Fatalf("replication plan=%+v err=%v", completed, err)
	}
	if err := storeB.Close(); err != nil {
		t.Fatal(err)
	}
	storeB, err = repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	replicated, err := storeB.GetCargoPublication(ctx, replicationTarget.ID, crateName, "1.0.0")
	if err != nil || !replicated.Yanked || replicated.Digest != publication.Digest || !bytes.Equal(replicated.IndexRow, publication.IndexRow) {
		t.Fatalf("restarted target Cargo=%+v err=%v", replicated, err)
	}
	handlerB := NewGatewayHandler(Dependencies{NativeCargoObjectStore: destinationObjects}, storeB, TestAdapter{}, testAuthenticator())
	download := "/cargo/" + replicationTarget.Name + "/api/v1/crates/" + crateName + "/1.0.0/download"
	if w := request(handlerB, http.MethodGet, download, nil); w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), archive) {
		t.Fatalf("replicated target download=%d %s", w.Code, w.Body.String())
	}
}
