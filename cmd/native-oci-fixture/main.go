// native-oci-fixture starts a self-contained Registry V2 endpoint for the
// protocol E2E fixture. Deployment uses PostgreSQL and S3-compatible storage; this process
// exists solely to exercise client-visible HTTP behaviour without an external
// package service.
package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/ccsert/artifact-gateway/internal/app"
	"github.com/ccsert/artifact-gateway/internal/apptest"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func main() {
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "team", Format: repository.FormatOCI}); err != nil {
		log.Fatal(err)
	}
	address := os.Getenv("LISTEN_ADDR")
	if address == "" {
		address = "127.0.0.1:18081"
	}
	handler := apptest.NewGateway(log.Default(), app.GatewayOptions{Store: store, NPMMetadata: store, NPMRestore: store, Storage: apptest.MemoryObjectStores(nil), Adapter: app.TestAdapter{}, Authenticator: app.Authenticator{ResolverToken: "fixture-secret"}}, app.NewGatewayHandler)
	log.Printf("Native OCI fixture listening on %s", address)
	log.Fatal(http.ListenAndServe(address, handler))
}
