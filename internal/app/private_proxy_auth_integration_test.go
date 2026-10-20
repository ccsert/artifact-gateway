//go:build integration

package app

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresRustFSPrivateProxyCredentials(t *testing.T) {
	for _, format := range []repository.Format{repository.FormatMaven, repository.FormatOCI} {
		for _, scheme := range []repository.UpstreamAuthScheme{repository.UpstreamAuthSchemeBasic, repository.UpstreamAuthSchemeBearer} {
			for _, route := range []string{"proxy", "group"} {
				for _, issuer := range []bool{false, true} {
					if issuer && format != repository.FormatOCI {
						continue
					}
					t.Run(fmt.Sprintf("%s/%s/%s/issuer=%v", format, scheme, route, issuer), func(t *testing.T) {
						privateProxyCredentialsContract(t, privateProxyCase{format, scheme, route, issuer}, newPrivateProxyPostgresBackend(t))
					})
				}
			}
		}
	}
}

func newPrivateProxyPostgresBackend(t *testing.T) privateProxyBackend {
	t.Helper()
	databaseURL, endpoint := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_RUSTFS_ENDPOINT")
	accessKey, secretKey := os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY")
	if databaseURL == "" || endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("isolated PostgreSQL and RustFS are required")
	}
	objects, err := objectstore.NewRustFSStore(endpoint, accessKey, secretKey, "private-auth-"+strings.ReplaceAll(uuid.NewString(), "-", ""))
	if err != nil {
		t.Fatal(err)
	}
	if err := objects.EnsureBucket(t.Context()); err != nil {
		t.Fatal(err)
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	control, err := NewPostgresCacheControlStore(objects, databaseURL)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(); _ = control.Close() })
	return privateProxyBackend{store: store, objects: objects, cache: control, reload: func() (GatewayStore, objectstore.Store) {
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		if err := control.Close(); err != nil {
			t.Fatal(err)
		}
		var err error
		store, err = repository.NewPostgresStore(databaseURL)
		if err != nil {
			t.Fatal(err)
		}
		control, err = NewPostgresCacheControlStore(objects, databaseURL)
		if err != nil {
			t.Fatal(err)
		}
		return store, control
	}}
}
