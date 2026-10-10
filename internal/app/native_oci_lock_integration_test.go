//go:build integration

package app

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/database"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type gatedOCIUploadObjects struct {
	OCIObjectStore
	entered chan struct{}
	resume  chan struct{}
}

func (s gatedOCIUploadObjects) PutReader(ctx context.Context, key string, reader io.Reader, size int64) error {
	if strings.HasPrefix(key, "native/oci/uploads/") {
		select {
		case s.entered <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.resume:
		}
	}
	return s.OCIObjectStore.PutReader(ctx, key, reader, size)
}

func TestPostgresRustFSOCIConcurrentHTTPUploadsKeepMetadataAvailable(t *testing.T) {
	url, endpoint := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_RUSTFS_ENDPOINT")
	accessKey, secretKey := os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY")
	if url == "" || endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and S3 integration environment is required")
	}
	open := func(size int) *sql.DB {
		config := database.DefaultPoolConfig()
		config.MaxOpenConns, config.MaxIdleConns = size, size
		pool, err := database.OpenPostgres(url, config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = pool.Close() })
		return pool
	}
	// A smaller metadata pool makes exhaustion deterministic without requiring
	// 32 physical DB sessions. The HTTP workload still contains 32 uploads.
	primary, notifications, locks := open(2), open(1), open(4)
	store, err := repository.NewPostgresStoreWithPools(primary, notifications, locks)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: uuid.NewString(), Name: "oci-pool-http-" + uuid.NewString(), Format: repository.FormatOCI,
	})
	if err != nil {
		t.Fatal(err)
	}
	objects, err := NewRustFSOCIObjectStore(endpoint, accessKey, secretKey, "oci-pool-"+strings.ReplaceAll(uuid.NewString(), "-", ""))
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(context.Background()); err != nil {
		t.Fatal(err)
	}
	gate := gatedOCIUploadObjects{OCIObjectStore: objects, entered: make(chan struct{}, 4), resume: make(chan struct{})}
	dependencies := Dependencies{NativeOCIObjectStore: gate, checkers: []Checker{postgresPoolChecker{db: primary}}}
	server := httptest.NewServer(NewGatewayHandler(dependencies, store, TestAdapter{}, testAuthenticator()))
	defer server.Close()
	var resumeOnce sync.Once
	resume := func() { resumeOnce.Do(func() { close(gate.resume) }) }
	defer resume()
	client := server.Client()
	client.Timeout = 30 * time.Second
	request := func(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
		r, err := http.NewRequestWithContext(ctx, method, server.URL+path, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		r.Header.Set("Authorization", "Bearer resolver-secret")
		if strings.Contains(path, "/manifests/") {
			r.Header.Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		}
		return client.Do(r)
	}
	expect := func(method, path string, body []byte, status int) *http.Response {
		t.Helper()
		response, err := request(context.Background(), method, path, body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != status {
			data, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			t.Fatalf("%s %s: status=%d want=%d body=%s", method, path, response.StatusCode, status, data)
		}
		return response
	}
	root := "/v2/" + repo.Name + "/app"
	control := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`)
	controlResponse := expect(http.MethodPut, root+"/manifests/control", control, http.StatusCreated)
	_ = controlResponse.Body.Close()
	const count = 32
	locations, digests, bodies := make([]string, count), make([]string, count), make([][]byte, count)
	diffIDs := make([]string, 0, count-1)
	for i := range count {
		start := expect(http.MethodPost, root+"/blobs/uploads/", nil, http.StatusAccepted)
		locations[i] = start.Header.Get("Location")
		_ = start.Body.Close()
		if locations[i] == "" {
			t.Fatal("missing upload location")
		}
		if i > 0 {
			payload := bytes.Repeat([]byte(fmt.Sprintf("layer-%02d-", i)), 32768)
			var layer bytes.Buffer
			writer := tar.NewWriter(&layer)
			if err := writer.WriteHeader(&tar.Header{Name: fmt.Sprintf("layer-%02d.txt", i), Mode: 0o644, Size: int64(len(payload))}); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Write(payload); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			bodies[i] = layer.Bytes()
			diffIDs = append(diffIDs, fmt.Sprintf("sha256:%x", sha256.Sum256(bodies[i])))
		}
	}
	bodies[0], err = json.Marshal(map[string]any{"architecture": "amd64", "os": "linux", "rootfs": map[string]any{"type": "layers", "diff_ids": diffIDs}})
	if err != nil {
		t.Fatal(err)
	}
	for i := range count {
		digests[i] = fmt.Sprintf("sha256:%x", sha256.Sum256(bodies[i]))
	}
	// Cancel an actual client while object I/O holds its upload lock; the open
	// upload must remain resumable and its dedicated connection must be released.
	cancelCtx, cancelRequest := context.WithCancel(context.Background())
	cancelled := make(chan error, 1)
	go func() {
		response, err := request(cancelCtx, http.MethodPut, locations[0]+"?digest="+digests[0], bodies[0])
		if response != nil {
			_ = response.Body.Close()
		}
		cancelled <- err
	}()
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		cancelRequest()
		t.Fatal("cancelled upload did not enter object I/O")
	}
	cancelRequest()
	if err := <-cancelled; !errors.Is(err, context.Canceled) {
		t.Fatalf("client cancellation=%v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for locks.Stats().InUse != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if locks.Stats().InUse != 0 {
		t.Fatal("client cancellation leaked its lock connection")
	}
	status := expect(http.MethodGet, locations[0], nil, http.StatusNoContent)
	_ = status.Body.Close()
	results := make(chan error, count)
	for i := range count {
		go func() {
			response, err := request(context.Background(), http.MethodPut, locations[i]+"?digest="+digests[i], bodies[i])
			if err == nil {
				if response.StatusCode != http.StatusCreated || response.Header.Get("Docker-Content-Digest") != digests[i] {
					err = fmt.Errorf("upload %d status=%d digest=%s", i, response.StatusCode, response.Header.Get("Docker-Content-Digest"))
				}
				_ = response.Body.Close()
			}
			results <- err
		}()
	}
	for range 4 {
		select {
		case <-gate.entered:
		case <-time.After(5 * time.Second):
			t.Fatalf("concurrent uploads did not fill dedicated lock pool: primary=%+v locks=%+v", primary.Stats(), locks.Stats())
		}
	}
	for range 5 {
		ready := expect(http.MethodGet, "/readyz", nil, http.StatusNoContent)
		_ = ready.Body.Close()
		read := expect(http.MethodGet, root+"/manifests/control", nil, http.StatusOK)
		got, err := io.ReadAll(read.Body)
		_ = read.Body.Close()
		if err != nil || !bytes.Equal(got, control) {
			t.Fatalf("repository read during upload: %v", err)
		}
	}
	if primary.Stats().InUse != 0 || locks.Stats().InUse != 4 {
		t.Fatalf("pool isolation while object I/O is held: primary=%+v locks=%+v", primary.Stats(), locks.Stats())
	}
	t.Logf("32 HTTP uploads, slow object I/O: primary=%+v locks=%+v", primary.Stats(), locks.Stats())
	resume()
	for range count {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	layers := make([]map[string]any, count-1)
	for i := range count {
		read := expect(http.MethodGet, root+"/blobs/"+digests[i], nil, http.StatusOK)
		got, err := io.ReadAll(read.Body)
		_ = read.Body.Close()
		if err != nil || !bytes.Equal(got, bodies[i]) || fmt.Sprintf("sha256:%x", sha256.Sum256(got)) != digests[i] {
			t.Fatalf("blob %d byte/digest mismatch: %v", i, err)
		}
		if i > 0 {
			layers[i-1] = map[string]any{"mediaType": "application/vnd.oci.image.layer.v1.tar", "digest": digests[i], "size": len(bodies[i])}
		}
	}
	manifest, err := json.Marshal(map[string]any{
		"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json",
		"config": map[string]any{"mediaType": "application/vnd.oci.image.config.v1+json", "digest": digests[0], "size": len(bodies[0])}, "layers": layers,
	})
	if err != nil {
		t.Fatal(err)
	}
	published := expect(http.MethodPut, root+"/manifests/latest", manifest, http.StatusCreated)
	manifestDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(manifest))
	if published.Header.Get("Docker-Content-Digest") != manifestDigest {
		t.Fatal("manifest digest mismatch")
	}
	_ = published.Body.Close()
	read := expect(http.MethodGet, root+"/manifests/"+manifestDigest, nil, http.StatusOK)
	got, err := io.ReadAll(read.Body)
	_ = read.Body.Close()
	if err != nil || !bytes.Equal(got, manifest) {
		t.Fatalf("manifest byte mismatch: %v", err)
	}
	if locks.Stats().InUse != 0 {
		t.Fatal("completed HTTP uploads leaked lock connections")
	}
}
