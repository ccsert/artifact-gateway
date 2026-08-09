// native-npm-fixture starts an in-memory npm registry for real-client E2E
// coverage. PostgreSQL and S3 behavior remains covered by integration tests.
package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/artifact-gateway/artifact-gateway/internal/app"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func main() {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "packages", Format: repository.FormatNPM, AnonymousRead: true,
	}); err != nil {
		log.Fatal(err)
	}
	if _, err := store.ReplaceAnonymousAccessPolicy(ctx, repository.AnonymousAccessPolicy{Enabled: true}, "1"); err != nil {
		log.Fatal(err)
	}
	address := os.Getenv("LISTEN_ADDR")
	if address == "" {
		address = "127.0.0.1:18083"
	}
	handler := app.NewGatewayHandler(
		app.Dependencies{NativeNPMObjectStore: app.NewMemoryOCIObjectStore()},
		store,
		app.TestAdapter{},
		app.Authenticator{ResolverToken: "fixture-secret", ResolverActor: "npm-fixture"},
	)
	log.Printf("Native npm fixture listening on %s", address)
	log.Fatal(http.ListenAndServe(address, handler))
}
