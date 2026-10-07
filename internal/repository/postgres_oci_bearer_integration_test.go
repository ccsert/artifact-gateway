//go:build integration

package repository

import (
	"os"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresOCIBearerConfigurationRoundTrip(t *testing.T) {
	store, err := NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	config := &OCIBearer{Realm: "https://auth.example.test/token", Service: "registry.example.test"}
	repo := HostedRepository{ID: uuid.NewString(), Name: "oci-" + uuid.NewString(), Format: FormatOCI, Type: RepositoryTypeProxy, Endpoint: "https://registry.example.test", OCIBearer: config}
	key := uuid.NewString()
	created, replayed, err := store.CreateHostedRepositoryIdempotently(t.Context(), repo, "admin", key, "oci-bearer-payload")
	if err != nil || replayed || created.OCIBearer == nil || *created.OCIBearer != *config {
		t.Fatalf("created issuer/audience=%+v replayed=%v err=%v", created.OCIBearer, replayed, err)
	}
	replay, replayed, err := store.CreateHostedRepositoryIdempotently(t.Context(), repo, "admin", key, "oci-bearer-payload")
	if err != nil || !replayed || replay.ID != created.ID || replay.OCIBearer == nil || *replay.OCIBearer != *config {
		t.Fatalf("replayed issuer/audience=%+v replayed=%v err=%v", replay.OCIBearer, replayed, err)
	}
	read, err := store.GetHostedRepository(t.Context(), repo.ID)
	if err != nil || read.OCIBearer == nil || *read.OCIBearer != *config {
		t.Fatalf("read issuer/audience=%+v err=%v", read.OCIBearer, err)
	}
	byName, err := store.GetHostedRepositoryByName(t.Context(), repo.Name)
	if err != nil || byName.OCIBearer == nil || *byName.OCIBearer != *config {
		t.Fatalf("read by name issuer/audience=%+v err=%v", byName.OCIBearer, err)
	}
	read.OCIBearer = &OCIBearer{Realm: "https://auth.example.test/v2/token", Service: "next-service"}
	updated, err := store.UpdateHostedRepository(t.Context(), read, "1")
	if err != nil || updated.Version != "2" || updated.OCIBearer == nil || *updated.OCIBearer != *read.OCIBearer {
		t.Fatalf("updated issuer/audience=%+v version=%q err=%v", updated.OCIBearer, updated.Version, err)
	}
	updated.OCIBearer = nil
	cleared, err := store.UpdateHostedRepository(t.Context(), updated, "2")
	if err != nil || cleared.Version != "3" || cleared.OCIBearer != nil {
		t.Fatalf("cleared issuer/audience=%+v version=%q err=%v", cleared.OCIBearer, cleared.Version, err)
	}
	legacy, err := store.CreateHostedRepository(t.Context(), HostedRepository{ID: uuid.NewString(), Name: "oci-" + uuid.NewString(), Format: FormatOCI, Type: RepositoryTypeProxy, Endpoint: "https://registry.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	legacyRead, err := store.GetHostedRepository(t.Context(), legacy.ID)
	if err != nil || legacyRead.OCIBearer != nil {
		t.Fatalf("legacy issuer/audience=%+v err=%v", legacyRead.OCIBearer, err)
	}
}
