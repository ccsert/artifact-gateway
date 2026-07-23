// native-maven-fixture starts an in-memory Native Maven endpoint for the
// Maven and Gradle protocol E2E fixture. It is deliberately not a deployment
// binary: PostgreSQL and S3 integration coverage lives in the application
// tests, while this process exercises real client HTTP behavior.
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
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "deploys", Format: repository.FormatMaven}); err != nil {
		log.Fatal(err)
	}
	address := os.Getenv("LISTEN_ADDR")
	if address == "" {
		address = "127.0.0.1:18080"
	}
	handler := app.NewGatewayHandler(app.Dependencies{}, store, app.TestAdapter{}, app.Authenticator{
		ResolverToken:     "fixture-secret",
		RepositoryWriters: map[string][]string{"fixture": {"deploys"}},
	})
	log.Printf("Native Maven fixture listening on %s", address)
	log.Fatal(http.ListenAndServe(address, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		handler.ServeHTTP(w, r)
	})))
}
