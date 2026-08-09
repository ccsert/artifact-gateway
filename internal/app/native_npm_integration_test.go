//go:build integration

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresMinIONPMPublicationIsVisibleAcrossGatewayInstances(t *testing.T) {
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
	bucket := "native-npm-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
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
	repo, err := storeA.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "npm-integration-" + uuid.NewString(), Format: repository.FormatNPM,
	})
	if err != nil {
		t.Fatal(err)
	}
	serverA := httptest.NewServer(NewGatewayHandler(Dependencies{NativeNPMObjectStore: objectsA}, storeA, TestAdapter{}, testAuthenticator()))
	defer serverA.Close()
	serverB := httptest.NewServer(NewGatewayHandler(Dependencies{NativeNPMObjectStore: objectsB}, storeB, TestAdapter{}, testAuthenticator()))
	defer serverB.Close()

	packageName, version := "@scope/postgres-widget", "1.2.3"
	tarball := npmFixtureTarball(t, packageName, version)
	body := npmFixturePublishDocument(t, packageName, version, "@scope/postgres-widget-1.2.3.tgz", tarball)
	publish, err := http.NewRequest(http.MethodPut, serverA.URL+"/npm/"+repo.Name+"/@scope%2Fpostgres-widget", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	publish.Header.Set("Authorization", "Bearer resolver-secret")
	published, err := serverA.Client().Do(publish)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = published.Body.Close() }()
	if published.StatusCode != http.StatusCreated {
		t.Fatalf("publish=%d", published.StatusCode)
	}

	metadataRequest, err := http.NewRequest(http.MethodGet, serverB.URL+"/npm/"+repo.Name+"/@scope%2Fpostgres-widget", nil)
	if err != nil {
		t.Fatal(err)
	}
	metadataRequest.Header.Set("Authorization", "Bearer resolver-secret")
	metadata, err := serverB.Client().Do(metadataRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = metadata.Body.Close() }()
	var packument struct {
		DistTags map[string]string `json:"dist-tags"`
	}
	if err = json.NewDecoder(metadata.Body).Decode(&packument); err != nil {
		t.Fatal(err)
	}
	if metadata.StatusCode != http.StatusOK || packument.DistTags["latest"] != version {
		t.Fatalf("metadata=%d packument=%#v", metadata.StatusCode, packument)
	}

	downloadRequest, err := http.NewRequest(http.MethodGet, serverB.URL+"/npm/"+repo.Name+"/@scope/postgres-widget/-/postgres-widget-1.2.3.tgz", nil)
	if err != nil {
		t.Fatal(err)
	}
	downloadRequest.Header.Set("Authorization", "Bearer resolver-secret")
	download, err := serverB.Client().Do(downloadRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = download.Body.Close() }()
	buffer := new(bytes.Buffer)
	_, _ = buffer.ReadFrom(download.Body)
	if download.StatusCode != http.StatusOK || !bytes.Equal(buffer.Bytes(), tarball) {
		t.Fatalf("download=%d bytes=%d", download.StatusCode, buffer.Len())
	}

	packages, err := storeB.SearchNPMPackages(ctx, repo.ID, "@scope/", 10, "")
	if err != nil || len(packages) != 1 || packages[0].Latest.Version != version || packages[0].VersionCount != 1 {
		t.Fatalf("packages=%#v err=%v", packages, err)
	}
	projection, err := storeB.SearchArtifactProjection(ctx, repo.ID, repository.FormatNPM, repository.ArtifactSearchQuery{Mode: repository.ArtifactSearchByCoordinate, Value: packageName}, 10, repository.ArtifactSearchPosition{})
	if err != nil || len(projection) != 1 || projection[0].Version != version {
		t.Fatalf("projection=%#v err=%v", projection, err)
	}
}
