package app

import (
	"net/http"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/apptest"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

func newTestGateway(t *testing.T, options GatewayOptions) http.Handler {
	t.Helper()
	options.Storage = apptest.MemoryObjectStores(options.Storage)
	if options.Caches.OCI == nil {
		options.Caches.OCI = NewDefaultOCICache(options.Storage[repository.FormatOCI], nil)
	}
	if options.Caches.Conan == nil {
		options.Caches.Conan = NewConanCache(options.Storage[repository.FormatConan])
	}
	return apptest.NewGateway(t, options, NewGatewayHandler)
}
