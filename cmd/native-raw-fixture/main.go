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
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "downloads", Format: repository.FormatRaw}); err != nil {
		log.Fatal(err)
	}
	address := os.Getenv("LISTEN_ADDR")
	if address == "" {
		address = "127.0.0.1:18082"
	}
	handler := app.NewGatewayHandler(app.Dependencies{NativeOCIObjectStore: app.NewMemoryOCIObjectStore()}, store, app.TestAdapter{}, app.Authenticator{ResolverToken: "fixture-secret"})
	log.Fatal(http.ListenAndServe(address, handler))
}
