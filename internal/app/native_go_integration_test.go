//go:build integration

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
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type blockingGoDeleteObjectStore struct {
	OCIObjectStore
	once    sync.Once
	entered chan string
	release chan struct{}
}

type blockingGoVisibleReferenceStore struct {
	*repository.PostgresStore
	once    sync.Once
	entered chan string
	release chan struct{}
}

func (s *blockingGoVisibleReferenceStore) GoModuleObjectHasVisibleReference(ctx context.Context, key string) (bool, error) {
	var waitErr error
	s.once.Do(func() {
		s.entered <- key
		select {
		case <-s.release:
		case <-ctx.Done():
			waitErr = ctx.Err()
		}
	})
	if waitErr != nil {
		return false, waitErr
	}
	return s.PostgresStore.GoModuleObjectHasVisibleReference(ctx, key)
}

type failOnceGoCollectedPostgresStore struct {
	*repository.PostgresStore
	fail bool
}

type blockingGoPromotionAdmissionStore struct {
	*repository.PostgresStore
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

type blockingGoPromotionPublishStore struct {
	*repository.PostgresStore
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (s *blockingGoPromotionAdmissionStore) GetArtifactQuarantine(ctx context.Context, repositoryID string, format repository.Format, coordinate, digest string) (repository.ArtifactQuarantine, error) {
	var waitErr error
	s.once.Do(func() {
		close(s.entered)
		select {
		case <-s.release:
		case <-ctx.Done():
			waitErr = ctx.Err()
		}
	})
	if waitErr != nil {
		return repository.ArtifactQuarantine{}, waitErr
	}
	return s.PostgresStore.GetArtifactQuarantine(ctx, repositoryID, format, coordinate, digest)
}

func (s *blockingGoPromotionPublishStore) PublishGoModule(ctx context.Context, publication repository.GoModulePublication) (repository.GoModuleVersion, bool, error) {
	var waitErr error
	s.once.Do(func() {
		close(s.entered)
		select {
		case <-s.release:
		case <-ctx.Done():
			waitErr = ctx.Err()
		}
	})
	if waitErr != nil {
		return repository.GoModuleVersion{}, false, waitErr
	}
	return s.PostgresStore.PublishGoModule(ctx, publication)
}

func (s *failOnceGoCollectedPostgresStore) MarkGoModuleObjectCollected(ctx context.Context, key string) error {
	if s.fail {
		s.fail = false
		return errors.New("database unavailable after RustFS delete")
	}
	return s.PostgresStore.MarkGoModuleObjectCollected(ctx, key)
}

func goIntegrationAsset(repositoryID, modulePath, version, kind string, body []byte) repository.GoModuleAsset {
	sum := sha256.Sum256(body)
	digestHex := hex.EncodeToString(sum[:])
	return repository.GoModuleAsset{
		RepositoryID: repositoryID, Module: modulePath, Version: version, Kind: kind,
		Digest: "sha256:" + digestHex, ObjectKey: "native/go/sha256/" + digestHex, Size: int64(len(body)),
	}
}

func (s *blockingGoDeleteObjectStore) Delete(ctx context.Context, key string) error {
	var waitErr error
	s.once.Do(func() {
		s.entered <- key
		select {
		case <-s.release:
		case <-ctx.Done():
			waitErr = ctx.Err()
		}
	})
	if waitErr != nil {
		return waitErr
	}
	return s.OCIObjectStore.Delete(ctx, key)
}

func TestPostgresRustFSGoProxyCacheIsVisibleAcrossGatewayInstances(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	endpoint := os.Getenv("TEST_RUSTFS_ENDPOINT")
	accessKey := os.Getenv("TEST_RUSTFS_ACCESS_KEY")
	secretKey := os.Getenv("TEST_RUSTFS_SECRET_KEY")
	if databaseURL == "" || endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and S3 integration environment is required")
	}
	ctx := context.Background()
	storeA, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeA.Close()
	storeB, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeB.Close()
	bucket := "native-go-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	objectsA, err := NewRustFSOCIObjectStore(endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	if err = objectsA.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	objectsB, err := NewRustFSOCIObjectStore(endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}

	const (
		modulePath    = "example.com/Acme/postgres-widget"
		escapedModule = "example.com/!acme/postgres-widget"
		version       = "v1.4.2"
	)
	info := []byte(`{"Version":"v1.4.2","Time":"2026-08-09T09:00:00Z"}`)
	mod := []byte("module " + modulePath + "\n\ngo 1.26\n")
	archive := goModuleFixtureZip(t, modulePath, version, map[string]string{
		"go.mod": string(mod), "widget.go": "package widget\n\nconst Version = \"v1.4.2\"\n",
	})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + escapedModule + "/@v/list":
			_, _ = io.WriteString(w, version+"\n")
		case "/" + escapedModule + "/@v/" + version + ".info":
			_, _ = w.Write(info)
		case "/" + escapedModule + "/@v/" + version + ".mod":
			_, _ = w.Write(mod)
		case "/" + escapedModule + "/@v/" + version + ".zip":
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	parsedUpstream, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := storeA.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "go-proxy-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		Format: repository.FormatGo, Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL,
		AllowedHosts: []string{parsedUpstream.Hostname()},
	})
	if err != nil {
		t.Fatal(err)
	}
	serverA := httptest.NewServer(NewGatewayHandler(
		Dependencies{NativeGoObjectStore: objectsA}, storeA, TestAdapter{}, testAuthenticator(),
		UpstreamClient{HTTPClient: upstream.Client()},
	))
	defer serverA.Close()
	serverB := httptest.NewServer(NewGatewayHandler(
		Dependencies{NativeGoObjectStore: objectsB}, storeB, TestAdapter{}, testAuthenticator(),
		UpstreamClient{HTTPClient: upstream.Client()},
	))
	defer serverB.Close()

	basePath := "/go/" + repo.Name + "/" + escapedModule
	get := func(server *httptest.Server, suffix string) (int, []byte) {
		t.Helper()
		request, requestErr := http.NewRequest(http.MethodGet, server.URL+basePath+suffix, nil)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		authorize(request, "resolver-secret")
		response, requestErr := server.Client().Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		return response.StatusCode, body
	}
	if status, body := get(serverA, "/@v/list"); status != http.StatusOK || string(body) != version+"\n" {
		t.Fatalf("instance A list=%d body=%q", status, body)
	}
	for suffix, expected := range map[string][]byte{
		"/@v/" + version + ".info": info,
		"/@v/" + version + ".mod":  mod,
		"/@v/" + version + ".zip":  archive,
	} {
		if status, body := get(serverA, suffix); status != http.StatusOK || !bytes.Equal(body, expected) {
			t.Fatalf("instance A %s=%d bytes=%d", suffix, status, len(body))
		}
	}

	projection, err := storeB.SearchArtifactProjection(ctx, repo.ID, repository.FormatGo, repository.ArtifactSearchQuery{
		Mode: repository.ArtifactSearchByCoordinate, Value: modulePath,
	}, 10, repository.ArtifactSearchPosition{})
	if err != nil || len(projection) != 1 || projection[0].Version != version || projection[0].Digest == "" {
		t.Fatalf("cross-instance projection=%#v err=%v", projection, err)
	}
	expectedBytes := int64(len(info) + len(mod) + len(archive))
	capacity, err := storeB.GetRepositoryCapacity(ctx, repo.ID)
	if err != nil || capacity.UsedBytes != expectedBytes || capacity.ObjectCount != 3 {
		t.Fatalf("cross-instance capacity=%#v err=%v", capacity, err)
	}
	records, err := storeB.ListRepositoryCapacityRecords(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foundCapacity := false
	for _, record := range records {
		if record.Repository.ID == repo.ID {
			foundCapacity = record.Capacity.UsedBytes == expectedBytes && record.Capacity.ObjectCount == 3
		}
	}
	if !foundCapacity {
		t.Fatalf("Go capacity missing from repository records: %#v", records)
	}

	asset, err := storeB.GetGoModuleAsset(ctx, repo.ID, modulePath, version, "mod")
	if err != nil {
		t.Fatal(err)
	}
	changed := asset
	changed.Digest = "sha256:" + strings.Repeat("f", 64)
	changed.ObjectKey = "native/go/sha256/" + strings.Repeat("f", 64)
	if _, err = storeB.CacheGoModuleAsset(ctx, changed); !errors.Is(err, repository.ErrUpstreamChanged) {
		t.Fatalf("changed cross-instance asset error=%v", err)
	}

	upstream.Close()
	if status, body := get(serverB, "/@v/list"); status != http.StatusOK || string(body) != version+"\n" {
		t.Fatalf("offline instance B list=%d body=%q", status, body)
	}
	for suffix, expected := range map[string][]byte{
		"/@v/" + version + ".info": info,
		"/@v/" + version + ".mod":  mod,
		"/@v/" + version + ".zip":  archive,
	} {
		if status, body := get(serverB, suffix); status != http.StatusOK || !bytes.Equal(body, expected) {
			t.Fatalf("offline instance B %s=%d bytes=%d", suffix, status, len(body))
		}
	}
}

func TestPostgresRustFSGoHostedPublicationIsAtomicAcrossGatewayInstances(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	endpoint := os.Getenv("TEST_RUSTFS_ENDPOINT")
	accessKey := os.Getenv("TEST_RUSTFS_ACCESS_KEY")
	secretKey := os.Getenv("TEST_RUSTFS_SECRET_KEY")
	if databaseURL == "" || endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and S3 integration environment is required")
	}
	ctx := context.Background()
	storeA, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeA.Close()
	storeB, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeB.Close()
	bucket := "native-go-hosted-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	objectsA, err := NewRustFSOCIObjectStore(endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	if err = objectsA.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	objectsB, err := NewRustFSOCIObjectStore(endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := storeA.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "go-hosted-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		Format: repository.FormatGo, Type: repository.RepositoryTypeHosted,
	})
	if err != nil {
		t.Fatal(err)
	}
	grantGoPublisher(t, storeA, repo.ID)
	serverA := httptest.NewServer(NewGatewayHandler(Dependencies{NativeGoObjectStore: objectsA}, storeA, TestAdapter{}, testAuthenticator()))
	defer serverA.Close()
	serverB := httptest.NewServer(NewGatewayHandler(Dependencies{NativeGoObjectStore: objectsB}, storeB, TestAdapter{}, testAuthenticator()))
	defer serverB.Close()

	const (
		modulePath    = "example.com/Acme/hosted-postgres"
		escapedModule = "example.com/!acme/hosted-postgres"
		version       = "v1.5.0"
	)
	mod := []byte("module " + modulePath + "\n\ngo 1.26\n")
	archive := goModuleFixtureZip(t, modulePath, version, map[string]string{
		"go.mod": string(mod), "hosted.go": "package hostedpostgres\n",
	})
	path := "/go/" + repo.Name + "/" + escapedModule + "/@v/" + version + ".zip"
	put := func(server *httptest.Server, body []byte) (int, string) {
		t.Helper()
		request, requestErr := http.NewRequest(http.MethodPut, server.URL+path, bytes.NewReader(body))
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		authorize(request, "resolver-secret")
		response, requestErr := server.Client().Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		responseBody, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		return response.StatusCode, string(responseBody)
	}
	if status, body := put(serverA, archive); status != http.StatusCreated {
		t.Fatalf("instance A publish=%d body=%s", status, body)
	}
	if status, body := put(serverB, archive); status != http.StatusOK || !strings.Contains(body, `"replayed":true`) {
		t.Fatalf("instance B replay=%d body=%s", status, body)
	}
	changed := goModuleFixtureZip(t, modulePath, version, map[string]string{
		"go.mod": string(mod), "hosted.go": "package hostedpostgres\n\nconst Changed = true\n",
	})
	if status, body := put(serverB, changed); status != http.StatusConflict {
		t.Fatalf("instance B conflict=%d body=%s", status, body)
	}
	basePath := "/go/" + repo.Name + "/" + escapedModule
	get := func(suffix string) (int, []byte) {
		t.Helper()
		request, requestErr := http.NewRequest(http.MethodGet, serverB.URL+basePath+suffix, nil)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		authorize(request, "resolver-secret")
		response, requestErr := serverB.Client().Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		return response.StatusCode, body
	}
	if status, body := get("/@v/list"); status != http.StatusOK || string(body) != version+"\n" {
		t.Fatalf("instance B list=%d body=%q", status, body)
	}
	if status, body := get("/@v/" + version + ".mod"); status != http.StatusOK || !bytes.Equal(body, mod) {
		t.Fatalf("instance B mod=%d body=%q", status, body)
	}
	if status, body := get("/@v/" + version + ".zip"); status != http.StatusOK || !bytes.Equal(body, archive) {
		t.Fatalf("instance B zip=%d bytes=%d", status, len(body))
	}
	capacity, err := storeB.GetRepositoryCapacity(ctx, repo.ID)
	if err != nil || capacity.ObjectCount != 3 || capacity.UsedBytes <= int64(len(archive)) {
		t.Fatalf("Go Hosted capacity=%#v err=%v", capacity, err)
	}
	orphanKey := "native/go/sha256/" + strings.Repeat("d", 64)
	if err = objectsA.Put(ctx, orphanKey, []byte("orphan")); err != nil {
		t.Fatal(err)
	}
	orphanJob, err := enqueueGoPublicationReclaim(ctx, storeA, repo.ID, orphanKey)
	if err != nil {
		t.Fatal(err)
	}
	maintenance := NativeGoMaintenance{Store: storeB, Objects: objectsB}
	// The publication created three older intents. The worker retains every
	// referenced publication object before it reaches and deletes the orphan.
	for range 4 {
		if err = maintenance.RunReclaimJobs(ctx, 10); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = objectsB.Stat(ctx, orphanKey); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("cross-instance orphan object still exists: %v", err)
	}
	orphanJob, err = storeA.GetLifecycleJob(ctx, repo.ID, orphanJob.ID)
	if err != nil || orphanJob.State != repository.LifecycleJobCompleted {
		t.Fatalf("cross-instance reclaim job=%#v err=%v", orphanJob, err)
	}
	zipAsset, err := storeA.GetGoModuleAsset(ctx, repo.ID, modulePath, version, "zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = objectsA.Stat(ctx, zipAsset.ObjectKey); err != nil {
		t.Fatalf("referenced Hosted ZIP was reclaimed: %v", err)
	}
}

func TestPostgresRustFSGoPromotionAndReplicationPublishCompleteSnapshotsAcrossGatewayInstances(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	endpoint := os.Getenv("TEST_RUSTFS_ENDPOINT")
	accessKey := os.Getenv("TEST_RUSTFS_ACCESS_KEY")
	secretKey := os.Getenv("TEST_RUSTFS_SECRET_KEY")
	if databaseURL == "" || endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and S3 integration environment is required")
	}
	ctx := context.Background()
	storeA, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeA.Close()
	storeB, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeB.Close()
	bucket := "native-go-distribution-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	objectsA, err := NewRustFSOCIObjectStore(endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	if err = objectsA.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	objectsB, err := NewRustFSOCIObjectStore(endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	create := func(name string) repository.HostedRepository {
		t.Helper()
		item, createErr := storeA.CreateHostedRepository(ctx, repository.HostedRepository{
			ID: uuid.NewString(), Name: name + "-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
			Format: repository.FormatGo, Type: repository.RepositoryTypeHosted,
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return item
	}
	source := create("go-distribution-source")
	promotedTarget := create("go-distribution-promoted")
	replicatedTarget := create("go-distribution-replicated")
	const (
		modulePath    = "example.com/Acme/distributed-postgres"
		escapedModule = "example.com/!acme/distributed-postgres"
		version       = "v1.2.0"
	)
	mod := []byte("module " + modulePath + "\n\ngo 1.26\n")
	bodies := map[string][]byte{
		"info": []byte(`{"Version":"v1.2.0","Time":"2026-08-19T00:00:00Z"}`),
		"mod":  mod,
		"zip": goModuleFixtureZip(t, modulePath, version, map[string]string{
			"go.mod": string(mod), "distributed.go": "package distributedpostgres\n",
		}),
	}
	assets := make([]repository.GoModuleAsset, 0, 3)
	for _, kind := range []string{"info", "mod", "zip"} {
		asset := goIntegrationAsset(source.ID, modulePath, version, kind, bodies[kind])
		if err = objectsA.PutVerifiedReader(ctx, asset.ObjectKey, bytes.NewReader(bodies[kind]), asset.Size, asset.Digest); err != nil {
			t.Fatal(err)
		}
		assets = append(assets, asset)
	}
	if _, _, err = storeA.PublishGoModule(ctx, repository.GoModulePublication{
		Version: repository.GoModuleVersion{
			RepositoryID: source.ID, Module: modulePath, Version: version,
			PublishedAt: time.Now().UTC(), Publisher: "distribution-integration",
		},
		Assets: assets,
	}); err != nil {
		t.Fatal(err)
	}
	coordinate, zipDigest := modulePath+"@"+version, assets[2].Digest

	promotion := NativeGoPromotion{Store: storeA, Intelligence: storeA}
	promotionJob, replayed, err := promotion.Enqueue(ctx, promotedTarget.ID, "go-integration-promotion", GoPromotionPayload{
		SourceRepositoryID: source.ID, Module: modulePath, Version: version, Digest: zipDigest,
	})
	if err != nil || replayed {
		t.Fatalf("enqueue PostgreSQL Go promotion replayed=%t job=%#v err=%v", replayed, promotionJob, err)
	}
	if err = (NativeGoPromotion{Store: storeB, Intelligence: storeB}).RunJobs(ctx, 1); err != nil {
		t.Fatalf("cross-instance Go promotion: %v", err)
	}
	if replay, wasReplay, replayErr := promotion.Enqueue(ctx, promotedTarget.ID, "go-integration-promotion", GoPromotionPayload{
		SourceRepositoryID: source.ID, Module: modulePath, Version: version, Digest: zipDigest,
	}); replayErr != nil || !wasReplay || replay.ID != promotionJob.ID || replay.State != repository.LifecycleJobCompleted {
		t.Fatalf("PostgreSQL Go promotion replayed=%t job=%#v err=%v", wasReplay, replay, replayErr)
	}

	checkpoints := make([]repository.ReplicationCheckpoint, 0, 3)
	for _, asset := range assets {
		checkpoints = append(checkpoints, repository.ReplicationCheckpoint{
			SourceObjectKey: asset.ObjectKey,
			ObjectKey:       goReplicationTargetObjectKey(replicatedTarget.ID, asset.Digest, asset.Kind),
			Digest:          asset.Digest,
			Size:            asset.Size,
		})
	}
	plan := repository.ReplicationPlan{
		ID: uuid.NewString(), SourceRepositoryID: source.ID, TargetRepositoryID: replicatedTarget.ID,
		Format: repository.FormatGo, Coordinate: coordinate, Digest: zipDigest, IdempotencyKey: "go-integration-replication",
	}
	persistedPlan, replayed, err := storeA.CreateReplicationPlan(ctx, plan, checkpoints)
	if err != nil || replayed {
		t.Fatalf("create PostgreSQL Go replication plan replayed=%t plan=%#v err=%v", replayed, persistedPlan, err)
	}
	if err = (GoReplication{Store: storeB, Source: objectsB, Destination: objectsB, ChunkBytes: 7}).RunJobs(ctx, 1); err != nil {
		t.Fatalf("cross-instance Go replication: %v", err)
	}
	if replay, wasReplay, replayErr := storeA.CreateReplicationPlan(ctx, repository.ReplicationPlan{
		ID: uuid.NewString(), SourceRepositoryID: source.ID, TargetRepositoryID: replicatedTarget.ID,
		Format: repository.FormatGo, Coordinate: coordinate, Digest: zipDigest, IdempotencyKey: "go-integration-replication",
	}, checkpoints); replayErr != nil || !wasReplay || replay.ID != persistedPlan.ID || replay.State != "completed" {
		t.Fatalf("PostgreSQL Go replication replayed=%t plan=%#v err=%v", wasReplay, replay, replayErr)
	}

	wantCapacity := repository.RepositoryCapacity{}
	for _, asset := range assets {
		wantCapacity.UsedBytes += asset.Size
		wantCapacity.ObjectCount++
		promoted, loadErr := storeB.GetGoModuleAsset(ctx, promotedTarget.ID, modulePath, version, asset.Kind)
		if loadErr != nil || promoted.Digest != asset.Digest || promoted.ObjectKey != asset.ObjectKey {
			t.Fatalf("cross-instance promoted %s=%#v err=%v", asset.Kind, promoted, loadErr)
		}
		replicated, loadErr := storeA.GetGoModuleAsset(ctx, replicatedTarget.ID, modulePath, version, asset.Kind)
		if loadErr != nil || replicated.Digest != asset.Digest || replicated.ObjectKey == asset.ObjectKey {
			t.Fatalf("cross-instance replicated %s=%#v err=%v", asset.Kind, replicated, loadErr)
		}
		body, readErr := objectsA.Get(ctx, replicated.ObjectKey)
		if readErr != nil || !bytes.Equal(body, bodies[asset.Kind]) {
			t.Fatalf("cross-instance replicated %s bytes=%d err=%v", asset.Kind, len(body), readErr)
		}
	}
	for _, target := range []repository.HostedRepository{promotedTarget, replicatedTarget} {
		capacity, capacityErr := storeB.GetRepositoryCapacity(ctx, target.ID)
		if capacityErr != nil || capacity.UsedBytes != wantCapacity.UsedBytes || capacity.ObjectCount != wantCapacity.ObjectCount {
			t.Fatalf("target %s capacity=%#v want=%#v err=%v", target.Name, capacity, wantCapacity, capacityErr)
		}
		projection, searchErr := storeA.SearchArtifactProjection(ctx, target.ID, repository.FormatGo, repository.ArtifactSearchQuery{
			Mode: repository.ArtifactSearchByCoordinate, Value: modulePath,
		}, 10, repository.ArtifactSearchPosition{})
		if searchErr != nil || len(projection) != 1 || projection[0].Version != version || projection[0].Digest != zipDigest {
			t.Fatalf("target %s projection=%#v err=%v", target.Name, projection, searchErr)
		}
		identities, identityErr := storeB.ListArtifactIdentities(ctx, target.ID, repository.FormatGo, repository.ArtifactIdentityDistribution, modulePath, 10)
		if identityErr != nil || len(identities) != 1 || identities[0].Coordinate != coordinate || identities[0].Digest != zipDigest {
			t.Fatalf("target %s distribution identities=%#v err=%v", target.Name, identities, identityErr)
		}
	}

	server := httptest.NewServer(NewGatewayHandler(Dependencies{NativeGoObjectStore: objectsA}, storeA, TestAdapter{}, testAuthenticator()))
	defer server.Close()
	basePath := "/go/" + replicatedTarget.Name + "/" + escapedModule + "/@v/" + version
	for suffix, expected := range map[string][]byte{".info": bodies["info"], ".mod": bodies["mod"], ".zip": bodies["zip"]} {
		request, requestErr := http.NewRequest(http.MethodGet, server.URL+basePath+suffix, nil)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		authorize(request, "resolver-secret")
		response, requestErr := server.Client().Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil || response.StatusCode != http.StatusOK || !bytes.Equal(body, expected) {
			t.Fatalf("replicated protocol %s=%d bytes=%d err=%v", suffix, response.StatusCode, len(body), readErr)
		}
	}
}

func TestPostgresGoPromotionPublicationIsFencedAfterLeaseExpiry(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	storeA, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeA.Close()
	storeB, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeB.Close()
	create := func(name string) repository.HostedRepository {
		t.Helper()
		item, createErr := storeA.CreateHostedRepository(ctx, repository.HostedRepository{
			ID: uuid.NewString(), Name: name + "-" + uuid.NewString()[:8], Format: repository.FormatGo, Type: repository.RepositoryTypeHosted,
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return item
	}
	source, target := create("go-promotion-fence-source"), create("go-promotion-fence-target")
	const modulePath, version = "example.com/team/promotion-fence", "v1.0.0"
	bodies := map[string][]byte{
		"info": []byte(`{"Version":"v1.0.0","Time":"2026-08-20T00:00:00Z"}`),
		"mod":  []byte("module " + modulePath + "\n\ngo 1.26\n"),
		"zip":  []byte("lease-fenced-go-promotion"),
	}
	assets := make([]repository.GoModuleAsset, 0, 3)
	for _, kind := range []string{"info", "mod", "zip"} {
		assets = append(assets, goIntegrationAsset(source.ID, modulePath, version, kind, bodies[kind]))
	}
	if _, _, err = storeA.PublishGoModule(ctx, repository.GoModulePublication{
		Version: repository.GoModuleVersion{RepositoryID: source.ID, Module: modulePath, Version: version, PublishedAt: time.Now().UTC(), Publisher: "lease-fence-test"},
		Assets:  assets,
	}); err != nil {
		t.Fatal(err)
	}
	promotion := NativeGoPromotion{Store: storeA}
	job, replayed, err := promotion.Enqueue(ctx, target.ID, "go-promotion-lease-fence", GoPromotionPayload{
		SourceRepositoryID: source.ID, Module: modulePath, Version: version, Digest: assets[2].Digest,
	})
	if err != nil || replayed {
		t.Fatalf("enqueue promotion replayed=%t job=%#v err=%v", replayed, job, err)
	}
	blockingStore := &blockingGoPromotionAdmissionStore{PostgresStore: storeA, entered: make(chan struct{}), release: make(chan struct{})}
	workerDone := make(chan error, 1)
	go func() {
		workerDone <- (NativeGoPromotion{Store: blockingStore}).RunJobs(ctx, 1)
	}()
	select {
	case <-blockingStore.entered:
	case <-ctx.Done():
		t.Fatal("promotion worker did not reach final admission barrier")
	}
	running, err := storeB.GetLifecycleJob(ctx, target.ID, job.ID)
	if err != nil || running.State != repository.LifecycleJobRunning || running.LeaseToken == "" {
		t.Fatalf("running promotion job=%#v err=%v", running, err)
	}
	if recovered, recoverErr := storeB.RecoverExpiredLifecycleJobs(ctx, time.Now().UTC().Add(11*time.Minute)); recoverErr != nil || recovered != 1 {
		t.Fatalf("recover expired promotion jobs=%d err=%v", recovered, recoverErr)
	}
	close(blockingStore.release)
	select {
	case runErr := <-workerDone:
		if !errors.Is(runErr, repository.ErrNotFound) {
			t.Fatalf("stale promotion worker err=%v", runErr)
		}
	case <-ctx.Done():
		t.Fatal("stale promotion worker did not finish")
	}
	if _, err = storeB.GetGoModuleVersion(ctx, target.ID, modulePath, version); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("expired promotion worker published target version: %v", err)
	}
	recovered, err := storeB.GetLifecycleJob(ctx, target.ID, job.ID)
	if err != nil || recovered.State != repository.LifecycleJobRetrying || recovered.LeaseToken != "" || recovered.LastError != "worker lease expired before completion" {
		t.Fatalf("recovered promotion job=%#v err=%v", recovered, err)
	}
}

func TestPostgresGoPromotionHeartbeatsAndFencesPublicationThroughCompletion(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	storeA, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeA.Close()
	storeB, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeB.Close()
	create := func(name string) repository.HostedRepository {
		t.Helper()
		item, createErr := storeA.CreateHostedRepository(ctx, repository.HostedRepository{
			ID: uuid.NewString(), Name: name + "-" + uuid.NewString(), Format: repository.FormatGo, Type: repository.RepositoryTypeHosted,
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return item
	}
	source, target := create("go-promo-heartbeat-src"), create("go-promo-heartbeat-dst")
	const modulePath, version = "example.com/team/promotion-heartbeat", "v1.0.0"
	bodies := map[string][]byte{
		"info": []byte(`{"Version":"v1.0.0","Time":"2026-08-20T00:00:00Z"}`),
		"mod":  []byte("module " + modulePath + "\n\ngo 1.26\n"),
		"zip":  []byte("heartbeat-fenced-go-promotion"),
	}
	assets := make([]repository.GoModuleAsset, 0, 3)
	for _, kind := range []string{"info", "mod", "zip"} {
		assets = append(assets, goIntegrationAsset(source.ID, modulePath, version, kind, bodies[kind]))
	}
	if _, _, err = storeA.PublishGoModule(ctx, repository.GoModulePublication{
		Version: repository.GoModuleVersion{RepositoryID: source.ID, Module: modulePath, Version: version, PublishedAt: time.Now().UTC(), Publisher: "lease-heartbeat-test"},
		Assets:  assets,
	}); err != nil {
		t.Fatal(err)
	}
	promotion := NativeGoPromotion{Store: storeA}
	job, replayed, err := promotion.Enqueue(ctx, target.ID, "go-promotion-heartbeat-fence", GoPromotionPayload{
		SourceRepositoryID: source.ID, Module: modulePath, Version: version, Digest: assets[2].Digest,
	})
	if err != nil || replayed {
		t.Fatalf("enqueue promotion replayed=%t job=%#v err=%v", replayed, job, err)
	}
	blockingStore := &blockingGoPromotionPublishStore{PostgresStore: storeA, entered: make(chan struct{}), release: make(chan struct{})}
	workerDone := make(chan error, 1)
	go func() {
		workerDone <- (NativeGoPromotion{Store: blockingStore, LeaseHeartbeatInterval: 20 * time.Millisecond}).RunJobs(ctx, 1)
	}()
	select {
	case <-blockingStore.entered:
	case <-ctx.Done():
		t.Fatal("promotion worker did not reach fenced publication")
	}
	running, err := storeB.GetLifecycleJob(ctx, target.ID, job.ID)
	if err != nil || running.State != repository.LifecycleJobRunning || running.LeaseToken == "" {
		t.Fatalf("running promotion job=%#v err=%v", running, err)
	}
	time.Sleep(100 * time.Millisecond)
	renewed, err := storeB.GetLifecycleJob(ctx, target.ID, job.ID)
	if err != nil || !renewed.LeaseExpiresAt.After(running.LeaseExpiresAt) {
		t.Fatalf("promotion heartbeat did not extend lease before=%#v after=%#v err=%v", running, renewed, err)
	}
	if recovered, recoverErr := storeB.RecoverExpiredLifecycleJobs(ctx, time.Now().UTC().Add(11*time.Minute)); recoverErr != nil || recovered != 0 {
		t.Fatalf("fenced promotion recovery=%d err=%v", recovered, recoverErr)
	}
	stillRunning, err := storeB.GetLifecycleJob(ctx, target.ID, job.ID)
	if err != nil || stillRunning.State != repository.LifecycleJobRunning || stillRunning.LeaseToken != running.LeaseToken {
		t.Fatalf("fenced promotion changed ownership=%#v err=%v", stillRunning, err)
	}
	close(blockingStore.release)
	select {
	case runErr := <-workerDone:
		if runErr != nil {
			t.Fatal(runErr)
		}
	case <-ctx.Done():
		t.Fatal("promotion worker did not complete fenced publication")
	}
	completed, err := storeB.GetLifecycleJob(ctx, target.ID, job.ID)
	if err != nil || completed.State != repository.LifecycleJobCompleted {
		t.Fatalf("completed promotion job=%#v err=%v", completed, err)
	}
	if _, err = storeB.GetGoModuleVersion(ctx, target.ID, modulePath, version); err != nil {
		t.Fatalf("fenced promotion target is unavailable: %v", err)
	}
}

func TestPostgresRustFSGoHostedLifecycleIsSerializedAcrossGatewayInstances(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	endpoint := os.Getenv("TEST_RUSTFS_ENDPOINT")
	accessKey := os.Getenv("TEST_RUSTFS_ACCESS_KEY")
	secretKey := os.Getenv("TEST_RUSTFS_SECRET_KEY")
	if databaseURL == "" || endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and S3 integration environment is required")
	}
	ctx := context.Background()
	storeA, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeA.Close()
	storeB, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeB.Close()
	bucket := "native-go-lifecycle-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	objectsA, err := NewRustFSOCIObjectStore(endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	if err = objectsA.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	objectsB, err := NewRustFSOCIObjectStore(endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := storeA.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "go-lifecycle-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		Format: repository.FormatGo, Type: repository.RepositoryTypeHosted,
	})
	if err != nil {
		t.Fatal(err)
	}
	grantGoPublisher(t, storeA, repo.ID)
	serverA := httptest.NewServer(NewGatewayHandler(Dependencies{NativeGoObjectStore: objectsA}, storeA, TestAdapter{}, testAuthenticator()))
	defer serverA.Close()
	serverB := httptest.NewServer(NewGatewayHandler(Dependencies{NativeGoObjectStore: objectsB}, storeB, TestAdapter{}, testAuthenticator()))
	defer serverB.Close()

	const (
		modulePath    = "example.com/Acme/lifecycle-postgres"
		escapedModule = "example.com/!acme/lifecycle-postgres"
		version       = "v1.6.0"
	)
	archive := goModuleFixtureZip(t, modulePath, version, map[string]string{
		"go.mod": "module " + modulePath + "\n\ngo 1.26\n", "lifecycle.go": "package lifecyclepostgres\n",
	})
	path := "/go/" + repo.Name + "/" + escapedModule + "/@v/" + version + ".zip"
	put := func(server *httptest.Server) (int, string) {
		t.Helper()
		request, requestErr := http.NewRequest(http.MethodPut, server.URL+path, bytes.NewReader(archive))
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		authorize(request, "resolver-secret")
		response, requestErr := server.Client().Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		return response.StatusCode, string(body)
	}
	get := func(server *httptest.Server) (int, []byte) {
		t.Helper()
		request, requestErr := http.NewRequest(http.MethodGet, server.URL+path, nil)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		authorize(request, "resolver-secret")
		response, requestErr := server.Client().Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		return response.StatusCode, body
	}
	if status, body := put(serverA); status != http.StatusCreated {
		t.Fatalf("publish=%d body=%s", status, body)
	}
	modules, err := storeB.ListGoModules(ctx, repo.ID, "example.com/", 10, "")
	if err != nil || len(modules) != 1 || modules[0] != modulePath {
		t.Fatalf("cross-instance Go module listing=%#v err=%v", modules, err)
	}
	publishedVersion, err := storeA.GetGoModuleVersion(ctx, repo.ID, modulePath, version)
	if err != nil {
		t.Fatal(err)
	}
	publication := repository.GoModulePublication{Version: publishedVersion, Assets: make([]repository.GoModuleAsset, 0, 3)}
	for _, kind := range []string{"info", "mod", "zip"} {
		asset, assetErr := storeA.GetGoModuleAsset(ctx, repo.ID, modulePath, version, kind)
		if assetErr != nil {
			t.Fatal(assetErr)
		}
		publication.Assets = append(publication.Assets, asset)
	}
	capacity, err := storeB.GetRepositoryCapacity(ctx, repo.ID)
	if err != nil || capacity.ObjectCount != 3 {
		t.Fatalf("published capacity=%#v err=%v", capacity, err)
	}

	runConcurrently := func(operations ...func() error) []error {
		start := make(chan struct{})
		results := make(chan error, len(operations))
		var ready sync.WaitGroup
		ready.Add(len(operations))
		for _, operation := range operations {
			go func(operation func() error) {
				ready.Done()
				<-start
				results <- operation()
			}(operation)
		}
		ready.Wait()
		close(start)
		errors := make([]error, 0, len(operations))
		for range operations {
			errors = append(errors, <-results)
		}
		return errors
	}
	assertOneSuccess := func(operation string, results []error) {
		t.Helper()
		var successes, notFound int
		for _, result := range results {
			switch {
			case result == nil:
				successes++
			case errors.Is(result, repository.ErrNotFound):
				notFound++
			default:
				t.Fatalf("concurrent %s error=%v", operation, result)
			}
		}
		if successes != 1 || notFound != 1 {
			t.Fatalf("concurrent %s results=%v", operation, results)
		}
	}
	assertOneSuccess("tombstone", runConcurrently(
		func() error {
			_, operationErr := storeA.TombstoneGoModuleVersion(ctx, repo.ID, modulePath, version)
			return operationErr
		},
		func() error {
			_, operationErr := storeB.TombstoneGoModuleVersion(ctx, repo.ID, modulePath, version)
			return operationErr
		},
	))
	if status, body := get(serverB); status != http.StatusNotFound {
		t.Fatalf("deleted cross-instance zip=%d body=%q", status, body)
	}
	projection, err := storeB.SearchArtifactProjection(ctx, repo.ID, repository.FormatGo, repository.ArtifactSearchQuery{Mode: repository.ArtifactSearchByCoordinate, Value: modulePath}, 10, repository.ArtifactSearchPosition{})
	if err != nil || len(projection) != 0 {
		t.Fatalf("deleted projection=%#v err=%v", projection, err)
	}
	identities, err := storeB.ListArtifactIdentities(ctx, repo.ID, repository.FormatGo, repository.ArtifactIdentityScan, "", 10)
	if err != nil || len(identities) != 0 {
		t.Fatalf("deleted identities=%#v err=%v", identities, err)
	}
	deletedCapacity, err := storeB.GetRepositoryCapacity(ctx, repo.ID)
	if err != nil || deletedCapacity != capacity {
		t.Fatalf("deleted physical capacity=%#v want=%#v err=%v", deletedCapacity, capacity, err)
	}
	if status, body := put(serverB); status != http.StatusConflict || !strings.Contains(body, "restore") {
		t.Fatalf("republish tombstoned version=%d body=%s", status, body)
	}
	if _, _, err = storeB.PublishGoModule(ctx, publication); !errors.Is(err, repository.ErrArtifactTombstoned) {
		t.Fatalf("PostgreSQL republish tombstoned version error=%v", err)
	}

	assertOneSuccess("restore", runConcurrently(
		func() error {
			_, operationErr := storeA.RestoreGoModuleVersion(ctx, repo.ID, modulePath, version)
			return operationErr
		},
		func() error {
			_, operationErr := storeB.RestoreGoModuleVersion(ctx, repo.ID, modulePath, version)
			return operationErr
		},
	))
	if status, body := get(serverA); status != http.StatusOK || !bytes.Equal(body, archive) {
		t.Fatalf("restored cross-instance zip=%d bytes=%d", status, len(body))
	}
	projection, err = storeA.SearchArtifactProjection(ctx, repo.ID, repository.FormatGo, repository.ArtifactSearchQuery{Mode: repository.ArtifactSearchByCoordinate, Value: modulePath}, 10, repository.ArtifactSearchPosition{})
	if err != nil || len(projection) != 1 || projection[0].Version != version {
		t.Fatalf("restored projection=%#v err=%v", projection, err)
	}
	identities, err = storeA.ListArtifactIdentities(ctx, repo.ID, repository.FormatGo, repository.ArtifactIdentityScan, "", 10)
	if err != nil || len(identities) != 1 || identities[0].Coordinate != modulePath+"@"+version {
		t.Fatalf("restored identities=%#v err=%v", identities, err)
	}
	restoredCapacity, err := storeA.GetRepositoryCapacity(ctx, repo.ID)
	if err != nil || restoredCapacity != capacity {
		t.Fatalf("restored physical capacity=%#v want=%#v err=%v", restoredCapacity, capacity, err)
	}
	if _, err = storeA.GetArtifactTombstone(ctx, repo.ID, repository.FormatGo, modulePath+"@"+version); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("restored tombstone remained: %v", err)
	}

	if _, err = storeA.TombstoneGoModuleVersion(ctx, repo.ID, modulePath, version); err != nil {
		t.Fatal(err)
	}
	blockingObjects := &blockingGoDeleteObjectStore{OCIObjectStore: objectsB, entered: make(chan string, 1), release: make(chan struct{})}
	maintenance := NativeGoMaintenance{Store: storeB, Objects: blockingObjects}
	if err = maintenance.EnqueueReclaimJobs(ctx, time.Now().UTC().Add(time.Hour), 10); err != nil {
		t.Fatal(err)
	}
	restoreResult, reclaimResult := make(chan error, 1), make(chan error, 1)
	go func() {
		reclaimResult <- maintenance.RunReclaimJobs(ctx, 10)
	}()
	select {
	case <-blockingObjects.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Go reclaim did not enter the blocking object delete")
	}
	go func() {
		_, operationErr := storeA.RestoreGoModuleVersion(ctx, repo.ID, modulePath, version)
		restoreResult <- operationErr
	}()
	select {
	case restoreErr := <-restoreResult:
		t.Fatalf("restore bypassed the reclaim object lock: %v", restoreErr)
	case <-time.After(150 * time.Millisecond):
	}
	close(blockingObjects.release)
	reclaimErr := <-reclaimResult
	if reclaimErr != nil {
		t.Fatalf("concurrent delayed reclaim: %v", reclaimErr)
	}
	restoreErr := <-restoreResult
	if !errors.Is(restoreErr, repository.ErrDisabled) {
		t.Fatalf("concurrent restore/reclaim error=%v", restoreErr)
	}
	collectedCapacity, err := storeA.GetRepositoryCapacity(ctx, repo.ID)
	if err != nil || collectedCapacity.UsedBytes != 0 || collectedCapacity.ObjectCount != 0 {
		t.Fatalf("collected capacity=%#v err=%v", collectedCapacity, err)
	}
	if _, err = storeA.RestoreGoModuleVersion(ctx, repo.ID, modulePath, version); !errors.Is(err, repository.ErrDisabled) {
		t.Fatalf("collected version restored: %v", err)
	}
	for _, asset := range publication.Assets {
		if _, err = objectsA.Stat(ctx, asset.ObjectKey); !errors.Is(err, objectstore.ErrNotFound) {
			t.Fatalf("collected %s object remains: %v", asset.Kind, err)
		}
	}
}

func TestPostgresGoReclaimWaitsForNewestSharedObjectTombstone(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("PostgreSQL integration environment is required")
	}
	ctx := context.Background()
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "go-shared-reclaim-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		Format: repository.FormatGo, Type: repository.RepositoryTypeHosted,
	})
	if err != nil {
		t.Fatal(err)
	}
	const (
		modulePath   = "example.com/team/shared-reclaim-postgres"
		sharedKey    = "native/go/shared-reclaim/go.mod"
		sharedDigest = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	)
	publish := func(version, suffix string) {
		t.Helper()
		if _, _, publishErr := store.PublishGoModule(ctx, repository.GoModulePublication{
			Version: repository.GoModuleVersion{RepositoryID: repo.ID, Module: modulePath, Version: version, PublishedAt: time.Now().UTC()},
			Assets: []repository.GoModuleAsset{
				{RepositoryID: repo.ID, Module: modulePath, Version: version, Kind: "info", Digest: "sha256:" + strings.Repeat(suffix, 64), ObjectKey: "native/go/shared-reclaim/" + suffix + "/info", Size: 1},
				{RepositoryID: repo.ID, Module: modulePath, Version: version, Kind: "mod", Digest: sharedDigest, ObjectKey: sharedKey, Size: 2},
				{RepositoryID: repo.ID, Module: modulePath, Version: version, Kind: "zip", Digest: "sha256:" + strings.Repeat(string(suffix[0]+2), 64), ObjectKey: "native/go/shared-reclaim/" + suffix + "/zip", Size: 3},
			},
		}); publishErr != nil {
			t.Fatal(publishErr)
		}
	}
	publish("v1.0.0", "a")
	publish("v1.1.0", "b")
	if _, err = store.TombstoneGoModuleVersion(ctx, repo.ID, modulePath, "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	oldest, err := store.GetArtifactTombstone(ctx, repo.ID, repository.FormatGo, modulePath+"@v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if _, err = store.TombstoneGoModuleVersion(ctx, repo.ID, modulePath, "v1.1.0"); err != nil {
		t.Fatal(err)
	}
	newest, err := store.GetArtifactTombstone(ctx, repo.ID, repository.FormatGo, modulePath+"@v1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	matches, err := store.GoModuleObjectMatchesTombstone(ctx, sharedKey, oldest.TombstonedAt)
	if err != nil || matches {
		t.Fatalf("stale PostgreSQL tombstone generation matched=%t err=%v", matches, err)
	}
	matches, err = store.GoModuleObjectMatchesTombstone(ctx, sharedKey, newest.TombstonedAt)
	if err != nil || !matches {
		t.Fatalf("newest PostgreSQL tombstone generation matched=%t err=%v", matches, err)
	}
	objects, err := store.ListReclaimableGoModuleObjects(ctx, newest.TombstonedAt, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range objects {
		if object.ObjectKey == sharedKey {
			t.Fatal("shared PostgreSQL object became reclaimable inside the newest recovery window")
		}
	}
	objects, err = store.ListReclaimableGoModuleObjects(ctx, newest.TombstonedAt.Add(time.Millisecond), 10, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range objects {
		if object.ObjectKey == sharedKey {
			return
		}
	}
	t.Fatal("shared PostgreSQL object did not become reclaimable after every recovery window elapsed")
}

func TestPostgresRustFSGoReclaimSerializesSharedTombstoneAndKeepsVisibleObject(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	endpoint := os.Getenv("TEST_RUSTFS_ENDPOINT")
	accessKey := os.Getenv("TEST_RUSTFS_ACCESS_KEY")
	secretKey := os.Getenv("TEST_RUSTFS_SECRET_KEY")
	if databaseURL == "" || endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and S3 integration environment is required")
	}
	ctx := context.Background()
	storeA, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeA.Close()
	storeB, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeB.Close()
	bucket := "go-shared-lock-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	objects, err := NewRustFSOCIObjectStore(endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	repoA, err := storeA.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "go-shared-lock-a-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		Format: repository.FormatGo, Type: repository.RepositoryTypeHosted,
	})
	if err != nil {
		t.Fatal(err)
	}
	repoB, err := storeA.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "go-shared-lock-b-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		Format: repository.FormatGo, Type: repository.RepositoryTypeHosted,
	})
	if err != nil {
		t.Fatal(err)
	}
	const modulePath, version = "example.com/team/shared-lock", "v1.0.0"
	body := []byte("one physical Go object shared by every representation")
	assetFor := func(repositoryID, kind string) repository.GoModuleAsset {
		return goIntegrationAsset(repositoryID, modulePath, version, kind, body)
	}
	sharedAsset := assetFor(repoA.ID, "zip")
	if err = objects.PutVerifiedReader(ctx, sharedAsset.ObjectKey, bytes.NewReader(body), sharedAsset.Size, sharedAsset.Digest); err != nil {
		t.Fatal(err)
	}
	for _, repo := range []repository.HostedRepository{repoA, repoB} {
		assets := []repository.GoModuleAsset{assetFor(repo.ID, "info"), assetFor(repo.ID, "mod"), assetFor(repo.ID, "zip")}
		if _, _, err = storeA.PublishGoModule(ctx, repository.GoModulePublication{
			Version: repository.GoModuleVersion{RepositoryID: repo.ID, Module: modulePath, Version: version, PublishedAt: time.Now().UTC()}, Assets: assets,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = storeA.TombstoneGoModuleVersion(ctx, repoA.ID, modulePath, version); err != nil {
		t.Fatal(err)
	}
	tombstoneA, err := storeA.GetArtifactTombstone(ctx, repoA.ID, repository.FormatGo, modulePath+"@"+version)
	if err != nil {
		t.Fatal(err)
	}
	blockingStore := &blockingGoVisibleReferenceStore{
		PostgresStore: storeA, entered: make(chan string, 1), release: make(chan struct{}),
	}
	maintenance := NativeGoMaintenance{Store: blockingStore, Objects: objects}
	if err = maintenance.EnqueueReclaimJobs(ctx, tombstoneA.TombstonedAt.Add(time.Millisecond), 10); err != nil {
		t.Fatal(err)
	}
	reclaimResult := make(chan error, 1)
	go func() { reclaimResult <- maintenance.RunReclaimJobs(ctx, 10) }()
	select {
	case key := <-blockingStore.entered:
		if key != sharedAsset.ObjectKey {
			t.Fatalf("blocked unexpected shared key %q", key)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Go reclaim did not reach visible-reference barrier")
	}
	tombstoneResult := make(chan error, 1)
	go func() {
		_, operationErr := storeB.TombstoneGoModuleVersion(ctx, repoB.ID, modulePath, version)
		tombstoneResult <- operationErr
	}()
	select {
	case tombstoneErr := <-tombstoneResult:
		t.Fatalf("shared tombstone bypassed the reclaim object lock: %v", tombstoneErr)
	case <-time.After(150 * time.Millisecond):
	}
	close(blockingStore.release)
	if err = <-reclaimResult; err != nil {
		t.Fatalf("shared reclaim: %v", err)
	}
	if err = <-tombstoneResult; err != nil {
		t.Fatalf("serialized shared tombstone: %v", err)
	}
	if _, err = objects.Stat(ctx, sharedAsset.ObjectKey); err != nil {
		t.Fatalf("visible shared RustFS object was deleted: %v", err)
	}
	capacityA, err := storeB.GetRepositoryCapacity(ctx, repoA.ID)
	if err != nil || capacityA.UsedBytes != 0 || capacityA.ObjectCount != 0 {
		t.Fatalf("collected shared-reference capacity=%#v err=%v", capacityA, err)
	}
	capacityB, err := storeB.GetRepositoryCapacity(ctx, repoB.ID)
	wantB := int64(3 * len(body))
	if err != nil || capacityB.UsedBytes != wantB || capacityB.ObjectCount != 3 {
		t.Fatalf("newly tombstoned shared-reference capacity=%#v wantBytes=%d err=%v", capacityB, wantB, err)
	}
	if _, err = storeB.RestoreGoModuleVersion(ctx, repoA.ID, modulePath, version); !errors.Is(err, repository.ErrDisabled) {
		t.Fatalf("collected shared reference restored: %v", err)
	}
	if _, err = storeB.RestoreGoModuleVersion(ctx, repoB.ID, modulePath, version); err != nil {
		t.Fatalf("new shared tombstone lost its recovery window: %v", err)
	}
}

func TestPostgresRustFSGoReclaimFailsRestoreClosedAfterCollectedMarkFailure(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	endpoint := os.Getenv("TEST_RUSTFS_ENDPOINT")
	accessKey := os.Getenv("TEST_RUSTFS_ACCESS_KEY")
	secretKey := os.Getenv("TEST_RUSTFS_SECRET_KEY")
	if databaseURL == "" || endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and S3 integration environment is required")
	}
	ctx := context.Background()
	storeA, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeA.Close()
	storeB, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeB.Close()
	bucket := "go-collecting-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	objects, err := NewRustFSOCIObjectStore(endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	repo, err := storeA.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "go-collecting-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		Format: repository.FormatGo, Type: repository.RepositoryTypeHosted,
	})
	if err != nil {
		t.Fatal(err)
	}
	const modulePath, version = "example.com/team/collecting-postgres", "v1.0.0"
	bodies := map[string][]byte{"info": []byte("collecting-info"), "mod": []byte("collecting-mod"), "zip": []byte("collecting-zip")}
	assets := make([]repository.GoModuleAsset, 0, 3)
	for _, kind := range []string{"info", "mod", "zip"} {
		asset := goIntegrationAsset(repo.ID, modulePath, version, kind, bodies[kind])
		if err = objects.PutVerifiedReader(ctx, asset.ObjectKey, bytes.NewReader(bodies[kind]), asset.Size, asset.Digest); err != nil {
			t.Fatal(err)
		}
		assets = append(assets, asset)
	}
	if _, _, err = storeA.PublishGoModule(ctx, repository.GoModulePublication{
		Version: repository.GoModuleVersion{RepositoryID: repo.ID, Module: modulePath, Version: version, PublishedAt: time.Now().UTC()}, Assets: assets,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = storeA.TombstoneGoModuleVersion(ctx, repo.ID, modulePath, version); err != nil {
		t.Fatal(err)
	}
	tombstone, err := storeA.GetArtifactTombstone(ctx, repo.ID, repository.FormatGo, modulePath+"@"+version)
	if err != nil {
		t.Fatal(err)
	}
	failingStore := &failOnceGoCollectedPostgresStore{PostgresStore: storeA, fail: true}
	maintenance := NativeGoMaintenance{Store: failingStore, Objects: objects}
	if err = maintenance.EnqueueReclaimJobs(ctx, tombstone.TombstonedAt.Add(time.Millisecond), 10); err != nil {
		t.Fatal(err)
	}
	if err = maintenance.RunReclaimJobs(ctx, 10); err == nil {
		t.Fatal("PostgreSQL collected mark failure was not surfaced")
	}
	for _, asset := range assets {
		if _, statErr := objects.Stat(ctx, asset.ObjectKey); !errors.Is(statErr, objectstore.ErrNotFound) {
			t.Fatalf("RustFS object %s remained after delete/mark failure: %v", asset.Kind, statErr)
		}
	}
	if _, err = storeB.RestoreGoModuleVersion(ctx, repo.ID, modulePath, version); !errors.Is(err, repository.ErrDisabled) {
		t.Fatalf("cross-instance restore bypassed PostgreSQL collecting fence: %v", err)
	}
	capacity, err := storeB.GetRepositoryCapacity(ctx, repo.ID)
	if err != nil || capacity.UsedBytes == 0 || capacity.ObjectCount == 0 {
		t.Fatalf("collecting reference stopped charging capacity before final mark: %#v err=%v", capacity, err)
	}
	jobs, err := storeB.ListLifecycleJobs(ctx, repo.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if job.State == repository.LifecycleJobRetrying {
			if _, err = storeB.RunLifecycleJobNow(ctx, repo.ID, job.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = maintenance.RunReclaimJobs(ctx, 10); err != nil {
		t.Fatalf("collecting retry: %v", err)
	}
	capacity, err = storeB.GetRepositoryCapacity(ctx, repo.ID)
	if err != nil || capacity.UsedBytes != 0 || capacity.ObjectCount != 0 {
		t.Fatalf("capacity after PostgreSQL collecting retry=%#v err=%v", capacity, err)
	}
}
