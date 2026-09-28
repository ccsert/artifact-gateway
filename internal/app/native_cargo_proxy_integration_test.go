//go:build integration

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresRustFSCargoProxyOfflineRestart(t *testing.T) {
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
	bucket := "cargo-proxy-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	objectsA, err := NewRustFSOCIObjectStore(endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	if err := objectsA.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	_, archive := cargoC0PublishFixture(t, "demo", "1.0.0", "demo")
	sum := sha256.Sum256(archive)
	checksum := hex.EncodeToString(sum[:])
	row := `{"name":"demo","vers":"1.0.0","deps":[],"cksum":"` + checksum + `","features":{},"yanked":false}` + "\n"
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/config.json":
			_ = json.NewEncoder(w).Encode(map[string]string{"dl": upstream.URL + "/crates/{crate}/{version}/download"})
		case "/de/mo/demo":
			_, _ = io.WriteString(w, row)
		case "/crates/demo/1.0.0/download":
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	repo, err := storeA.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "cargo-proxy-" + uuid.NewString()[:8],
		Format: repository.FormatCargo, Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	group, _, err := storeA.CreateHostedGroupIdempotently(ctx, repository.HostedGroup{ID: uuid.NewString(),
		Name: "cargo-proxy-group-" + uuid.NewString()[:8], Format: repository.FormatCargo,
		Members: []repository.GroupMember{{RepositoryID: repo.ID, Position: 0}}}, "integration", uuid.NewString(), "cargo-group")
	if err != nil {
		t.Fatal(err)
	}
	request := func(server *httptest.Server, path string) (int, []byte) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "resolver-secret")
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, body
	}
	warm := httptest.NewServer(NewGatewayHandler(Dependencies{NativeCargoObjectStore: objectsA, CargoUpstreamClient: upstream.Client()},
		storeA, TestAdapter{}, testAuthenticator()))
	base := "/cargo/" + repo.Name
	if status, _ := request(warm, base+"/config.json"); status != 200 {
		t.Fatalf("config status=%d", status)
	}
	if status, body := request(warm, base+"/de/mo/demo"); status != 200 || string(body) != row {
		t.Fatalf("index status=%d body=%s", status, body)
	}
	if status, body := request(warm, base+"/api/v1/crates/demo/1.0.0/download"); status != 200 || !bytes.Equal(body, archive) {
		t.Fatalf("download status=%d body=%q", status, body)
	}
	groupBase := "/cargo/" + group.Name
	if status, body := request(warm, groupBase+"/de/mo/demo"); status != 200 || string(body) != row {
		t.Fatalf("group index status=%d body=%s", status, body)
	}
	warm.Close()
	upstream.Close()
	now := time.Now().UTC()
	index, err := storeA.GetCargoProxyIndex(ctx, repo.ID, "demo")
	if err != nil {
		t.Fatal(err)
	}
	index.FetchedAt, index.ExpiresAt = now.Add(-2*time.Hour), now.Add(-time.Hour)
	if err := storeA.PutCargoProxyIndex(ctx, index); err != nil {
		t.Fatal(err)
	}
	config, err := storeA.GetCargoProxyConfig(ctx, repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	config.FetchedAt, config.ExpiresAt = now.Add(-2*time.Hour), now.Add(-time.Hour)
	if err := storeA.PutCargoProxyConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	storeB, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = storeB.Close() }()
	objectsB, err := NewRustFSOCIObjectStore(endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	restarted := httptest.NewServer(NewGatewayHandler(Dependencies{NativeCargoObjectStore: objectsB, CargoUpstreamClient: upstream.Client()},
		storeB, TestAdapter{}, testAuthenticator()))
	defer restarted.Close()
	if status, body := request(restarted, base+"/de/mo/demo"); status != 200 || string(body) != row {
		t.Fatalf("restart index=%d body=%s", status, body)
	}
	if status, body := request(restarted, base+"/api/v1/crates/demo/1.0.0/download"); status != 200 || !bytes.Equal(body, archive) {
		t.Fatalf("restart download=%d body=%q", status, body)
	}
	if status, body := request(restarted, groupBase+"/de/mo/demo"); status != 200 || string(body) != row {
		t.Fatalf("restart group index=%d body=%s", status, body)
	}
	if status, body := request(restarted, groupBase+"/api/v1/crates/demo/1.0.0/download"); status != 200 || !bytes.Equal(body, archive) {
		t.Fatalf("restart group download=%d body=%q", status, body)
	}
	owner, err := storeB.GetCargoGroupVersion(ctx, group.ID, "demo", "1.0.0")
	if err != nil || owner.SourceRepositoryID != repo.ID || owner.Checksum != checksum {
		t.Fatalf("restart group owner=%+v err=%v", owner, err)
	}
}
