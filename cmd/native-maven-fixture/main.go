// native-maven-fixture starts an in-memory Native Maven endpoint for the
// Maven and Gradle protocol E2E fixture. It is deliberately not a deployment
// binary: PostgreSQL and S3 integration coverage lives in the application
// tests, while this process exercises real client HTTP behavior.
package main

import (
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/artifact-gateway/artifact-gateway/internal/app"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func main() {
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "deploys", Format: repository.FormatMaven}); err != nil {
		log.Fatal(err)
	}
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "strict-deploys", Format: repository.FormatMaven, MavenStrictPublication: true}); err != nil {
		log.Fatal(err)
	}
	address := os.Getenv("LISTEN_ADDR")
	if address == "" {
		address = "127.0.0.1:18080"
	}
	objects := app.NewMemoryOCIObjectStore()
	deps := app.Dependencies{NativeMavenObjectStore: objects}
	auth := app.Authenticator{
		ResolverToken:     "fixture-secret",
		RepositoryWriters: map[string][]string{"fixture": {"deploys", "strict-deploys"}},
	}
	// Exercise real client -> Group -> first miss -> second upstream traffic.
	// The second HTTP server exposes the same published bytes as the Hosted
	// fixture, including generated metadata and checksum sidecars.
	hosted := app.NewGatewayHandler(deps, store, app.TestAdapter{}, auth)
	missing := httptest.NewServer(http.NotFoundHandler())
	defer missing.Close()
	available := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.Clone(r.Context())
		r.URL.Path = "/maven/deploys" + r.URL.Path
		r.SetBasicAuth("fixture", "fixture-secret")
		hosted.ServeHTTP(w, r)
	}))
	defer available.Close()
	var members []repository.GroupMember
	var allowedHosts []string
	for i, upstream := range []*httptest.Server{missing, available} {
		host := strings.TrimPrefix(upstream.URL, "http://")
		allowedHosts = append(allowedHosts, host)
		repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
			ID: uuid.NewString(), Name: "upstream-" + uuid.NewString(), Format: repository.FormatMaven,
			Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL, AllowedHosts: []string{host},
		})
		if err != nil {
			log.Fatal(err)
		}
		members = append(members, repository.GroupMember{RepositoryID: repo.ID, Position: i})
	}
	if _, _, err := store.CreateHostedGroupIdempotently(context.Background(), repository.HostedGroup{ID: uuid.NewString(), Name: "maven-chain", Format: repository.FormatMaven, Members: members}, "fixture", "maven-chain", "maven-chain"); err != nil {
		log.Fatal(err)
	}
	handler := app.NewGatewayHandlerWithCaches(deps, store, app.TestAdapter{}, auth, app.NewDefaultOCICache(app.NewMemoryOCIObjectStore(), nil), app.NewDefaultMavenCache(app.NewMemoryOCIObjectStore(), allowedHosts), app.UpstreamClient{})
	log.Printf("Native Maven fixture listening on %s", address)
	log.Fatal(http.ListenAndServe(address, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		handler.ServeHTTP(w, r)
	})))
}
