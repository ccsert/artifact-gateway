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

func TestRepositoryPyPIRetentionTombstoneAndRestoreLifecycle(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "pypi-retention", Format: repository.FormatPyPI})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	files := []repository.PyPIFile{
		{RepositoryID: repo.ID, Project: "widget", Version: "1.0.0", Filename: "widget-1.0.0.tar.gz", Digest: "sha256:" + strings.Repeat("a", 64), ObjectKey: "native/pypi/old", Size: 10, CreatedAt: now.Add(-48 * time.Hour)},
		{RepositoryID: repo.ID, Project: "widget", Version: "2.0.0", Filename: "widget-2.0.0.tar.gz", Digest: "sha256:" + strings.Repeat("b", 64), ObjectKey: "native/pypi/new", Size: 10, CreatedAt: now},
	}
	for _, file := range files {
		if _, err = store.PublishPyPIFile(ctx, file); err != nil {
			t.Fatal(err)
		}
	}
	policy, err := store.ReplaceRepositoryRetentionPolicy(ctx, repo.ID, repository.RepositoryRetentionPolicy{Enabled: true, KeepDays: 36500, MinimumVersions: 1, MaximumVersions: 1}, "1")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := func(method, target, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		authorize(req, "admin-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	dryRun := request(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/retention:dry-run", "")
	if dryRun.Code != http.StatusOK || !strings.Contains(dryRun.Body.String(), `"coordinate":"widget@1.0.0"`) {
		t.Fatalf("dry-run=%d body=%s", dryRun.Code, dryRun.Body.String())
	}
	executeRequest := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/retention:execute", nil)
	authorize(executeRequest, "admin-secret")
	executeRequest.Header.Set("Idempotency-Key", "pypi-retention-run")
	executeRequest.Header.Set("If-Match", policy.Version)
	execute := httptest.NewRecorder()
	handler.ServeHTTP(execute, executeRequest)
	if execute.Code != http.StatusAccepted {
		t.Fatalf("execute=%d body=%s", execute.Code, execute.Body.String())
	}
	if err = (NativeRepositoryRetention{Store: store}).RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	visible, err := store.ListPyPIProjectFiles(ctx, repo.ID, "widget")
	if err != nil || len(visible) != 1 || visible[0].Version != "2.0.0" {
		t.Fatalf("retained=%#v err=%v", visible, err)
	}
	if _, err = store.GetArtifactTombstone(ctx, repo.ID, repository.FormatPyPI, "widget@1.0.0"); err != nil {
		t.Fatalf("tombstone=%v", err)
	}
	restore := request(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/restore", `{"coordinate":"widget@1.0.0"}`)
	if restore.Code != http.StatusNoContent {
		t.Fatalf("restore=%d body=%s", restore.Code, restore.Body.String())
	}
	visible, err = store.ListPyPIProjectFiles(ctx, repo.ID, "widget")
	if err != nil || len(visible) != 2 {
		t.Fatalf("restored=%#v err=%v", visible, err)
	}
}

func TestNativePyPIMaintenanceReclaimsAfterRecoveryWindow(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "pypi-reclaim", Name: "pypi-reclaim", Format: repository.FormatPyPI})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("pypi artifact")
	sum := sha256.Sum256(body)
	file := repository.PyPIFile{RepositoryID: repo.ID, Project: "widget", Version: "1.0.0", Filename: "widget-1.0.0.tar.gz", Digest: "sha256:" + hex.EncodeToString(sum[:]), ObjectKey: "native/pypi/reclaim", Size: int64(len(body))}
	if err = objects.Put(ctx, file.ObjectKey, body); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PublishPyPIFile(ctx, file); err != nil {
		t.Fatal(err)
	}
	if _, err = store.TombstonePyPIVersion(ctx, repo.ID, file.Project, file.Version); err != nil {
		t.Fatal(err)
	}
	maintenance := NativePyPIMaintenance{Store: store, Objects: objects, Now: func() time.Time { return time.Now().UTC().Add(25 * time.Hour) }}
	if err = maintenance.Collect(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = objects.Get(ctx, file.ObjectKey); err == nil {
		t.Fatal("reclaimed object remained")
	}
	if _, err = store.RestorePyPIVersion(ctx, repo.ID, file.Project, file.Version); !errors.Is(err, repository.ErrDisabled) {
		t.Fatalf("collected version restored: %v", err)
	}
}

func TestNativePyPIMaintenanceSerializesReclaimAndRestore(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "pypi-reclaim-race", Name: "pypi-reclaim-race", Format: repository.FormatPyPI})
	if err != nil {
		t.Fatal(err)
	}
	file := repository.PyPIFile{
		RepositoryID: repo.ID, Project: "widget", Version: "1.0.0", Filename: "widget-1.0.0.tar.gz",
		Digest: "sha256:" + strings.Repeat("a", 64), ObjectKey: "native/pypi/sha256/reclaim-race", Size: 4,
	}
	if err = objects.Put(ctx, file.ObjectKey, []byte("pypi")); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PublishPyPIFile(ctx, file); err != nil {
		t.Fatal(err)
	}
	if _, err = store.TombstonePyPIVersion(ctx, repo.ID, file.Project, file.Version); err != nil {
		t.Fatal(err)
	}

	blocking := blockingLifecycleDeleteStore{OCIObjectStore: objects, entered: make(chan struct{}, 1), release: make(chan struct{})}
	maintenance := NativePyPIMaintenance{Store: store, Objects: blocking}
	if err = maintenance.EnqueueReclaimJobs(ctx, time.Now().UTC().Add(time.Hour), 10); err != nil {
		t.Fatal(err)
	}
	reclaimResult := make(chan error, 1)
	go func() { reclaimResult <- maintenance.RunReclaimJobs(ctx, 10) }()
	select {
	case <-blocking.entered:
	case <-time.After(time.Second):
		t.Fatal("PyPI reclaim did not reach object deletion")
	}

	restoreStarted := make(chan struct{})
	restoreResult := make(chan error, 1)
	go func() {
		close(restoreStarted)
		_, restoreErr := store.RestorePyPIVersion(ctx, repo.ID, file.Project, file.Version)
		restoreResult <- restoreErr
	}()
	<-restoreStarted
	select {
	case restoreErr := <-restoreResult:
		t.Fatalf("PyPI restore bypassed object lock: %v", restoreErr)
	case <-time.After(100 * time.Millisecond):
	}
	close(blocking.release)
	if err = <-reclaimResult; err != nil {
		t.Fatal(err)
	}
	if restoreErr := <-restoreResult; !errors.Is(restoreErr, repository.ErrDisabled) {
		t.Fatalf("PyPI restore after reclaim error=%v", restoreErr)
	}
}

func TestNativePyPIMaintenanceCompletesStaleReclaimWhenObjectIsVisibleAgain(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "pypi-reclaim-visible", Name: "pypi-reclaim-visible", Format: repository.FormatPyPI})
	if err != nil {
		t.Fatal(err)
	}
	file := repository.PyPIFile{RepositoryID: repo.ID, Project: "widget", Version: "1.0.0", Filename: "widget-1.0.0.tar.gz", Digest: "sha256:" + strings.Repeat("b", 64), ObjectKey: "native/pypi/sha256/visible-again", Size: 4}
	if err = objects.Put(ctx, file.ObjectKey, []byte("pypi")); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PublishPyPIFile(ctx, file); err != nil {
		t.Fatal(err)
	}
	if _, err = store.TombstonePyPIVersion(ctx, repo.ID, file.Project, file.Version); err != nil {
		t.Fatal(err)
	}
	maintenance := NativePyPIMaintenance{Store: store, Objects: objects}
	if err = maintenance.EnqueueReclaimJobs(ctx, time.Now().UTC().Add(time.Hour), 10); err != nil {
		t.Fatal(err)
	}
	if _, err = store.RestorePyPIVersion(ctx, repo.ID, file.Project, file.Version); err != nil {
		t.Fatal(err)
	}
	if err = maintenance.RunReclaimJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.ListLifecycleJobs(ctx, repo.ID, 10)
	if err != nil || len(jobs) != 1 || jobs[0].State != repository.LifecycleJobCompleted || jobs[0].LastError != "" {
		t.Fatalf("stale reclaim job=%#v err=%v", jobs, err)
	}
	if _, err = objects.Get(ctx, file.ObjectKey); err != nil {
		t.Fatalf("visible PyPI object was deleted: %v", err)
	}
}

func TestRepositoryPyPIPromotionAndReplication(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	source, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "pypi-source", Format: repository.FormatPyPI})
	if err != nil {
		t.Fatal(err)
	}
	promotionTarget, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "pypi-promoted", Format: repository.FormatPyPI})
	if err != nil {
		t.Fatal(err)
	}
	replicationTarget, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "pypi-replicated", Format: repository.FormatPyPI})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("replicated PyPI artifact")
	sum := sha256.Sum256(body)
	file := repository.PyPIFile{RepositoryID: source.ID, Project: "widget", Version: "3.0.0", Filename: "widget-3.0.0.tar.gz", Digest: "sha256:" + hex.EncodeToString(sum[:]), ObjectKey: "native/pypi/source", Size: int64(len(body)), Publisher: "publisher"}
	if err = objects.Put(ctx, file.ObjectKey, body); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PublishPyPIFile(ctx, file); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativePyPIObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	post := func(target, key, payload string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(payload))
		authorize(request, "admin-secret")
		request.Header.Set("Idempotency-Key", key)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	coordinate := file.Project + "@" + file.Version
	promotion := post("/api/v2/repositories/"+source.ID+"/promotions", "pypi-promote", `{"targetRepositoryId":"`+promotionTarget.ID+`","coordinate":"`+coordinate+`","digest":"`+file.Digest+`"}`)
	if promotion.Code != http.StatusAccepted {
		t.Fatalf("promotion=%d body=%s", promotion.Code, promotion.Body.String())
	}
	if err = (NativePyPIPromotion{Store: store}).RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	promoted, err := store.ListPyPIProjectFiles(ctx, promotionTarget.ID, file.Project)
	if err != nil || len(promoted) != 1 || promoted[0].ObjectKey != file.ObjectKey {
		t.Fatalf("promoted=%#v err=%v", promoted, err)
	}
	replication := post("/api/v2/repositories/"+source.ID+"/replications", "pypi-replicate", `{"targetRepositoryId":"`+replicationTarget.ID+`","coordinate":"`+coordinate+`","digest":"`+file.Digest+`"}`)
	if replication.Code != http.StatusAccepted {
		t.Fatalf("replication=%d body=%s", replication.Code, replication.Body.String())
	}
	if err = (PyPIReplication{Store: store, Source: objects, Destination: objects}).RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	replicated, err := store.ListPyPIProjectFiles(ctx, replicationTarget.ID, file.Project)
	if err != nil || len(replicated) != 1 || replicated[0].ObjectKey == file.ObjectKey {
		t.Fatalf("replicated=%#v err=%v", replicated, err)
	}
	stored, err := objects.Get(ctx, replicated[0].ObjectKey)
	if err != nil || !bytes.Equal(stored, body) {
		t.Fatalf("replicated bytes=%q err=%v", stored, err)
	}
}
