// Package apptest supplies explicit in-memory dependencies for unit tests and
// native-client fixture binaries. Production runtimes must not import it.
package apptest

import (
	"net/http"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

// Reporter is implemented by testing.T and log.Logger. Keeping this package
// independent of app lets app's same-package tests share it with cmd fixtures.
type Reporter interface {
	Fatalf(string, ...any)
}

// NewGateway calls the same validated composition entry point as production.
// The caller explicitly supplies prepared options and the app constructor.
func NewGateway[Options any](reporter Reporter, options Options, build func(Options) (http.Handler, error)) http.Handler {
	if helper, ok := reporter.(interface{ Helper() }); ok {
		helper.Helper()
	}
	handler, err := build(options)
	if err != nil {
		reporter.Fatalf("configure test Gateway: %v", err)
	}
	return handler
}

// MemoryObjectStores creates an isolated store for each format and preserves
// explicitly supplied stores, including deliberate nils for failure tests.
// No caller-owned map is modified, and no state is shared across invocations.
func MemoryObjectStores(overrides map[repository.Format]objectstore.Store) map[repository.Format]objectstore.Store {
	stores := make(map[repository.Format]objectstore.Store)
	for _, format := range repository.SupportedFormats() {
		stores[format] = objectstore.NewMemoryStore()
	}
	for format, store := range overrides {
		stores[format] = store
	}
	return stores
}
