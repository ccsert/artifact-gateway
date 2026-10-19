//go:build integration

package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresRustFSGatewayExplicitCompositionAcrossRestart(t *testing.T) {
	databaseURL, endpoint := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_RUSTFS_ENDPOINT")
	accessKey, secretKey := os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY")
	if databaseURL == "" || endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and RustFS integration environment is required")
	}
	bucket := "composition-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	name := "raw-composition-" + uuid.NewString()
	start := func() (*httptest.Server, *repository.PostgresStore) {
		t.Helper()
		store, err := repository.NewPostgresStore(databaseURL)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		objects, err := objectstore.NewRustFSStore(endpoint, accessKey, secretKey, bucket)
		if err != nil {
			t.Fatal(err)
		}
		if err = objects.EnsureBucket(context.Background()); err != nil {
			t.Fatal(err)
		}
		handler, err := NewGatewayHandler(GatewayOptions{Store: store, Storage: SharedObjectStores(objects), Adapter: TestAdapter{}, Authenticator: testAuthenticator()})
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(handler)
		t.Cleanup(server.Close)
		return server, store
	}
	first, store := start()
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: name, Format: repository.FormatRaw}); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPut, first.URL+"/raw/"+name+"/releases/widget.txt", strings.NewReader("durable composition bytes"))
	if err != nil {
		t.Fatal(err)
	}
	authorize(request, "resolver-secret")
	response, err := first.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("publish=%d", response.StatusCode)
	}
	first.Close()
	_ = store.Close()
	second, _ := start()
	request, err = http.NewRequest(http.MethodGet, second.URL+"/raw/"+name+"/releases/widget.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	authorize(request, "resolver-secret")
	response, err = second.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || string(body) != "durable composition bytes" {
		t.Fatalf("fresh PostgreSQL/RustFS clients read=%d %q", response.StatusCode, body)
	}
}
