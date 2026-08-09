// native-npm-fixture starts an in-memory npm registry for real-client E2E
// coverage. PostgreSQL and S3 behavior remains covered by integration tests.
package main

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"os"

	"github.com/artifact-gateway/artifact-gateway/internal/app"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func main() {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	packages := repository.HostedRepository{
		ID: uuid.NewString(), Name: "packages", Format: repository.FormatNPM,
		Type: repository.RepositoryTypeHosted, AnonymousRead: true,
	}
	if endpoint := os.Getenv("NPM_PROXY_ENDPOINT"); endpoint != "" {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Hostname() == "" {
			log.Fatal("NPM_PROXY_ENDPOINT must be an absolute URL")
		}
		packages.Type = repository.RepositoryTypeProxy
		packages.Endpoint = endpoint
		packages.AllowedHosts = []string{parsed.Hostname()}
	}
	if _, err := store.CreateHostedRepository(ctx, packages); err != nil {
		log.Fatal(err)
	}
	if packages.Type == repository.RepositoryTypeProxy {
		private := repository.HostedRepository{
			ID: uuid.NewString(), Name: "private", Format: repository.FormatNPM,
			Type: repository.RepositoryTypeHosted, AnonymousRead: true,
		}
		if _, err := store.CreateHostedRepository(ctx, private); err != nil {
			log.Fatal(err)
		}
		if _, _, err := store.CreateHostedGroupIdempotently(ctx, repository.HostedGroup{
			ID: uuid.NewString(), Name: "all-packages", Format: repository.FormatNPM, AnonymousRead: true,
			Members: []repository.GroupMember{{RepositoryID: private.ID, Position: 0}, {RepositoryID: packages.ID, Position: 1}},
		}, "native-npm-fixture", "all-packages", "all-packages"); err != nil {
			log.Fatal(err)
		}
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
	log.Printf("Native npm %s fixture listening on %s", packages.Type, address)
	log.Fatal(http.ListenAndServe(address, handler))
}
