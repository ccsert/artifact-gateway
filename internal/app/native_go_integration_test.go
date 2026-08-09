//go:build integration

package app

import (
	"bytes"
	"context"
	"errors"
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

func TestPostgresMinIOGoProxyCacheIsVisibleAcrossGatewayInstances(t *testing.T) {
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
	bucket := "native-go-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
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
