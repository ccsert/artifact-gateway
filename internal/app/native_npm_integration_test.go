//go:build integration

package app

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestPostgresMinIONPMProxyCacheIsVisibleAcrossGatewayInstances(t *testing.T) {
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
	bucket := "proxy-npm-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
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

	packageName, version := "proxy-integration-widget", "3.2.1"
	tarball := npmFixtureTarball(t, packageName, version)
	sha512Sum := sha512.Sum512(tarball)
	sha1Sum := sha1.Sum(tarball)
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + packageName:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name": packageName, "dist-tags": map[string]string{"latest": version},
				"versions": map[string]any{version: map[string]any{
					"name": packageName, "version": version,
					"dist": map[string]string{
						"tarball":   upstream.URL + "/tarballs/" + packageName + "-" + version + ".tgz",
						"integrity": "sha512-" + base64.StdEncoding.EncodeToString(sha512Sum[:]),
						"shasum":    hex.EncodeToString(sha1Sum[:]),
					},
				}},
			})
		case "/tarballs/" + packageName + "-" + version + ".tgz":
			_, _ = w.Write(tarball)
		default:
			http.NotFound(w, r)
		}
	}))
	parsedUpstream, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := storeA.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "npm-proxy-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		Format: repository.FormatNPM, Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL,
		AllowedHosts: []string{parsedUpstream.Hostname()},
	})
	if err != nil {
		t.Fatal(err)
	}
	serverA := httptest.NewServer(NewGatewayHandler(
		Dependencies{NativeNPMObjectStore: objectsA}, storeA, TestAdapter{}, testAuthenticator(),
		UpstreamClient{HTTPClient: upstream.Client()},
	))
	defer serverA.Close()
	serverB := httptest.NewServer(NewGatewayHandler(
		Dependencies{NativeNPMObjectStore: objectsB}, storeB, TestAdapter{}, testAuthenticator(),
		UpstreamClient{HTTPClient: upstream.Client()},
	))
	defer serverB.Close()

	metadataURL := serverA.URL + "/npm/" + repo.Name + "/" + packageName
	metadataRequest, _ := http.NewRequest(http.MethodGet, metadataURL, nil)
	metadataRequest.Header.Set("Authorization", "Bearer resolver-secret")
	metadata, err := serverA.Client().Do(metadataRequest)
	if err != nil {
		t.Fatal(err)
	}
	metadataBody, err := io.ReadAll(metadata.Body)
	_ = metadata.Body.Close()
	if err != nil || metadata.StatusCode != http.StatusOK {
		t.Fatalf("metadata=%d body=%s err=%v", metadata.StatusCode, metadataBody, err)
	}
	var packument struct {
		Versions map[string]struct {
			Dist struct {
				Tarball string `json:"tarball"`
			} `json:"dist"`
		} `json:"versions"`
	}
	if err = json.Unmarshal(metadataBody, &packument); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	tarballURL, err := url.Parse(packument.Versions[version].Dist.Tarball)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = storeA.ReplaceRepositoryCapacityQuota(ctx, repo.ID, int64(len(tarball)-1)); err != nil {
		t.Fatal(err)
	}
	overQuotaRequest, _ := http.NewRequest(http.MethodGet, serverA.URL+tarballURL.RequestURI(), nil)
	overQuotaRequest.Header.Set("Authorization", "Bearer resolver-secret")
	overQuota, err := serverA.Client().Do(overQuotaRequest)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, overQuota.Body)
	_ = overQuota.Body.Close()
	if overQuota.StatusCode != http.StatusInsufficientStorage {
		t.Fatalf("over quota download=%d", overQuota.StatusCode)
	}
	if _, err = storeA.ReplaceRepositoryCapacityQuota(ctx, repo.ID, 0); err != nil {
		t.Fatal(err)
	}
	downloadRequest, _ := http.NewRequest(http.MethodGet, serverA.URL+tarballURL.RequestURI(), nil)
	downloadRequest.Header.Set("Authorization", "Bearer resolver-secret")
	download, err := serverA.Client().Do(downloadRequest)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, download.Body)
	_ = download.Body.Close()
	if download.StatusCode != http.StatusOK {
		t.Fatalf("download=%d", download.StatusCode)
	}
	upstream.Close()

	offlineRequest, _ := http.NewRequest(http.MethodGet, serverB.URL+tarballURL.RequestURI(), nil)
	offlineRequest.Header.Set("Authorization", "Bearer resolver-secret")
	offline, err := serverB.Client().Do(offlineRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = offline.Body.Close() }()
	offlineBody, err := io.ReadAll(offline.Body)
	if err != nil || offline.StatusCode != http.StatusOK || !bytes.Equal(offlineBody, tarball) {
		t.Fatalf("offline=%d bytes=%d err=%v", offline.StatusCode, len(offlineBody), err)
	}
}
