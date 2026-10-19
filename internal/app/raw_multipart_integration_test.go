//go:build integration

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestRawMultipartAcrossPostgresRustFSRestartAndStoppedUpstream(t *testing.T) {
	databaseURL, endpoint := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_RUSTFS_ENDPOINT")
	if databaseURL == "" || endpoint == "" || os.Getenv("TEST_RUSTFS_ACCESS_KEY") == "" || os.Getenv("TEST_RUSTFS_SECRET_KEY") == "" {
		t.Skip("PostgreSQL and S3 integration environment is required")
	}
	ctx := context.Background()
	body := bytes.Repeat([]byte("0123456789abcdef"), 65536)
	sum := sha256.Sum256(body)
	etag := fmt.Sprintf(`"sha256-%x"`, sum)
	var upstreamCalls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		if r.Header.Get("Authorization") != "" || r.Header.Get("Range") != "" || r.Header.Get("If-Range") != "" {
			t.Error("client authorization or partial representation reached upstream")
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(body)
	}))
	t.Cleanup(upstream.Close)
	host, port := rawTLSServerAddress(t, upstream.URL)
	withRawProxyNetwork(t, func(context.Context, string, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.7")}, nil
	}, func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != net.JoinHostPort("203.0.113.7", port) {
			t.Errorf("unvalidated target: %q", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(host, port))
	})
	bucket := "raw-ranges-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	newInstance := func() (*repository.PostgresStore, *httptest.Server) {
		t.Helper()
		store, err := repository.NewPostgresStore(databaseURL)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		objects, err := objectstore.NewRustFSStore(endpoint, os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY"), bucket)
		if err != nil {
			t.Fatal(err)
		}
		if err := objects.EnsureBucket(ctx); err != nil {
			t.Fatal(err)
		}
		coordinator, err := NewPostgresCacheCoordinator(databaseURL)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = coordinator.Close() })
		cache := NewRawCache(objects, time.Hour, time.Hour, []string{"example.com"}).WithCoordinator(coordinator)
		handler := newTestGateway(t, GatewayOptions{Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(),
			Storage: ObjectStores{repository.FormatRaw: objects, repository.FormatOCI: objectstore.NewMemoryStore()},
			Caches:  GatewayCaches{Raw: cache}, Upstream: UpstreamClient{HTTPClient: upstream.Client()}})
		server := httptest.NewServer(handler)
		t.Cleanup(server.Close)
		return store, server
	}
	store, first := newInstance()
	newRepo := func(kind repository.RepositoryType) repository.HostedRepository {
		t.Helper()
		repo := repository.HostedRepository{ID: uuid.NewString(), Name: "ranges-" + uuid.NewString()[:8], Format: repository.FormatRaw, Type: kind}
		if kind == repository.RepositoryTypeProxy {
			repo.Endpoint, repo.AllowedHosts = "https://example.com:"+port, []string{"example.com"}
		}
		repo, err := store.CreateHostedRepository(ctx, repo)
		if err != nil {
			t.Fatal(err)
		}
		return repo
	}
	hosted, proxy := newRepo(repository.RepositoryTypeHosted), newRepo(repository.RepositoryTypeProxy)
	group, _, err := store.CreateHostedGroupIdempotently(ctx, repository.HostedGroup{ID: uuid.NewString(), Name: "ranges-" + uuid.NewString()[:8], Format: repository.FormatRaw,
		Members: []repository.GroupMember{{RepositoryID: hosted.ID, Position: 0}, {RepositoryID: proxy.ID, Position: 1}}}, "range-test", uuid.NewString(), "ranges")
	if err != nil {
		t.Fatal(err)
	}
	request := func(server *httptest.Server, method, root, path string, content []byte, headers map[string]string) (int, http.Header, []byte) {
		t.Helper()
		r, err := http.NewRequest(method, server.URL+"/raw/"+root+"/"+path, bytes.NewReader(content))
		if err != nil {
			t.Fatal(err)
		}
		authorize(r, "admin-secret")
		for name, value := range headers {
			r.Header.Set(name, value)
		}
		response, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, response.Header, data
	}
	if status, _, data := request(first, http.MethodPut, hosted.Name, "hosted.bin", body, map[string]string{"If-None-Match": "*"}); status != 201 {
		t.Fatalf("publish=%d %s", status, data)
	}
	headers := map[string]string{"Range": "bytes=0-15,1048560-1048575", "If-Range": etag}
	status, metadata, data := request(first, http.MethodGet, group.Name, "proxy.bin", nil, headers)
	assertRawMultipart(t, status, metadata, data, []string{"0123456789abcdef", "0123456789abcdef"}, []string{"bytes 0-15/1048576", "bytes 1048560-1048575/1048576"})
	if upstreamCalls.Load() != 1 {
		t.Fatalf("cold ranges fetched multiple representations: %d", upstreamCalls.Load())
	}
	first.Close()
	_ = store.Close()
	upstream.Close()
	_, second := newInstance()
	for _, target := range []struct{ root, path string }{{hosted.Name, "hosted.bin"}, {group.Name, "hosted.bin"}, {proxy.Name, "proxy.bin"}, {group.Name, "proxy.bin"}} {
		status, metadata, data := request(second, http.MethodGet, target.root, target.path, nil, headers)
		assertRawMultipart(t, status, metadata, data, []string{"0123456789abcdef", "0123456789abcdef"}, []string{"bytes 0-15/1048576", "bytes 1048560-1048575/1048576"})
		if metadata.Get("ETag") != etag {
			t.Fatalf("restarted identity=%s", metadata.Get("ETag"))
		}
	}
	if upstreamCalls.Load() != 1 {
		t.Fatalf("warm cache contacted stopped upstream: %d", upstreamCalls.Load())
	}
}
