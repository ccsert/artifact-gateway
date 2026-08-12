//go:build integration

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresRustFSPyPIPublicationLifecycleAcrossGatewayInstances(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	endpoint := os.Getenv("TEST_S3_ENDPOINT")
	accessKey := os.Getenv("TEST_S3_ACCESS_KEY")
	secretKey := os.Getenv("TEST_S3_SECRET_KEY")
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
	bucket := "native-pypi-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	objectsA, err := NewS3OCIObjectStore(endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	if err = objectsA.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	objectsB, err := NewS3OCIObjectStore(endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := storeA.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "pypi-integration-" + uuid.NewString(), Format: repository.FormatPyPI})
	if err != nil {
		t.Fatal(err)
	}
	serverA := httptest.NewServer(NewGatewayHandler(Dependencies{NativePyPIObjectStore: objectsA}, storeA, TestAdapter{}, testAuthenticator()))
	defer serverA.Close()
	serverB := httptest.NewServer(NewGatewayHandler(Dependencies{NativePyPIObjectStore: objectsB}, storeB, TestAdapter{}, testAuthenticator()))
	defer serverB.Close()
	wheel := pypiFixtureWheel(t, "postgres_widget", "1.2.3")
	sum := sha256.Sum256(wheel)
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	for key, value := range map[string]string{":action": "file_upload", "name": "postgres-widget", "version": "1.2.3", "filetype": "bdist_wheel", "pyversion": "py3", "sha256_digest": hex.EncodeToString(sum[:])} {
		if err = writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile("content", "postgres_widget-1.2.3-py3-none-any.whl")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(wheel); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	upload, err := http.NewRequest(http.MethodPost, serverA.URL+"/pypi/"+repo.Name+"/legacy/", body)
	if err != nil {
		t.Fatal(err)
	}
	upload.Header.Set("Content-Type", writer.FormDataContentType())
	upload.Header.Set("Authorization", "Bearer resolver-secret")
	response, err := serverA.Client().Do(upload)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("upload=%d", response.StatusCode)
	}
	files, err := storeB.ListPyPIProjectFiles(ctx, repo.ID, "postgres-widget")
	if err != nil || len(files) != 1 || files[0].Version != "1.2.3" {
		t.Fatalf("cross-instance files=%#v err=%v", files, err)
	}
	download, err := http.NewRequest(http.MethodGet, serverB.URL+"/pypi/"+repo.Name+"/packages/"+files[0].Filename, nil)
	if err != nil {
		t.Fatal(err)
	}
	download.Header.Set("Authorization", "Bearer resolver-secret")
	downloaded, err := serverB.Client().Do(download)
	if err != nil {
		t.Fatal(err)
	}
	downloadedBody := new(bytes.Buffer)
	_, _ = downloadedBody.ReadFrom(downloaded.Body)
	_ = downloaded.Body.Close()
	if downloaded.StatusCode != http.StatusOK || !bytes.Equal(downloadedBody.Bytes(), wheel) {
		t.Fatalf("download=%d bytes=%d", downloaded.StatusCode, downloadedBody.Len())
	}
	projection, err := storeB.SearchArtifactProjection(ctx, repo.ID, repository.FormatPyPI, repository.ArtifactSearchQuery{Mode: repository.ArtifactSearchByCoordinate, Value: "postgres-widget"}, 10, repository.ArtifactSearchPosition{})
	if err != nil || len(projection) != 1 || projection[0].Version != "1.2.3" {
		t.Fatalf("projection=%#v err=%v", projection, err)
	}
	if _, err = storeA.TombstonePyPIVersion(ctx, repo.ID, "postgres-widget", "1.2.3"); err != nil {
		t.Fatal(err)
	}
	if _, err = storeB.ListPyPIProjectFiles(ctx, repo.ID, "postgres-widget"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("cross-instance tombstone remained visible: %v", err)
	}
	if _, err = storeB.RestorePyPIVersion(ctx, repo.ID, "postgres-widget", "1.2.3"); err != nil {
		t.Fatal(err)
	}
	if restored, restoreErr := storeA.GetPyPIFile(ctx, repo.ID, files[0].Filename); restoreErr != nil || restored.Digest != files[0].Digest {
		t.Fatalf("restored=%#v err=%v", restored, restoreErr)
	}

	if _, err = storeA.TombstonePyPIVersion(ctx, repo.ID, "postgres-widget", "1.2.3"); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.ExecContext(ctx, `UPDATE native_pypi_files SET deleted_at=now()-interval '25 hours' WHERE repository_id::text=$1 AND project=$2 AND version=$3`, repo.ID, "postgres-widget", "1.2.3"); err != nil {
		t.Fatal(err)
	}
	blocking := blockingLifecycleDeleteStore{OCIObjectStore: objectsA, entered: make(chan struct{}, 1), release: make(chan struct{})}
	maintenance := NativePyPIMaintenance{Store: storeA, Objects: blocking}
	if err = maintenance.EnqueueReclaimJobs(ctx, time.Now().UTC().Add(-24*time.Hour), 10); err != nil {
		t.Fatalf("enqueue PyPI reclaim with repository scope: %v", err)
	}
	jobs, err := storeA.ListLifecycleJobs(ctx, repo.ID, 10)
	if err != nil || len(jobs) != 1 || jobs[0].RepositoryID != repo.ID {
		t.Fatalf("PyPI reclaim jobs=%#v err=%v", jobs, err)
	}
	reclaimResult := make(chan error, 1)
	go func() { reclaimResult <- maintenance.RunReclaimJobs(ctx, 10) }()
	select {
	case <-blocking.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("cross-instance PyPI reclaim did not reach object deletion")
	}
	restoreResult := make(chan error, 1)
	go func() {
		_, restoreErr := storeB.RestorePyPIVersion(ctx, repo.ID, "postgres-widget", "1.2.3")
		restoreResult <- restoreErr
	}()
	select {
	case restoreErr := <-restoreResult:
		t.Fatalf("cross-instance PyPI restore bypassed advisory lock: %v", restoreErr)
	case <-time.After(150 * time.Millisecond):
	}
	close(blocking.release)
	if err = <-reclaimResult; err != nil {
		t.Fatal(err)
	}
	if restoreErr := <-restoreResult; !errors.Is(restoreErr, repository.ErrDisabled) {
		t.Fatalf("cross-instance PyPI restore after reclaim error=%v", restoreErr)
	}
}
