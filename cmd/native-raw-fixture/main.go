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
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "downloads", Format: repository.FormatRaw}); err != nil {
		log.Fatal(err)
	}
	address := os.Getenv("LISTEN_ADDR")
	if address == "" {
		address = "127.0.0.1:18082"
	}
	handler := apptest.NewGateway(log.Default(), app.GatewayOptions{Store: store, Storage: apptest.MemoryObjectStores(nil), Adapter: app.TestAdapter{}, Authenticator: app.Authenticator{ResolverToken: "fixture-secret"}}, app.NewGatewayHandler)
	log.Fatal(http.ListenAndServe(address, handler))
}
