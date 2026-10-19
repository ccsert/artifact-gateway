package app

import (
	"net/http"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/apptest"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

func newTestGateway(t *testing.T, options GatewayOptions) http.Handler {
	t.Helper()
	// Test composition projects only explicitly supplied store capabilities;
	// it never creates replacement metadata for a missing port.
	if options.NPMMetadata == nil {
		options.NPMMetadata, _ = any(options.Store).(repository.NPMMetadataStore)
	}
	if options.NPMRestore == nil {
		options.NPMRestore, _ = any(options.Store).(repository.NPMRestoreStore)
	}
	options.Storage = apptest.MemoryObjectStores(options.Storage)
	if options.Caches.OCI == nil {
		options.Caches.OCI = NewDefaultOCICache(options.Storage[repository.FormatOCI], nil)
	}
	if options.Caches.Conan == nil {
		options.Caches.Conan = NewConanCache(options.Storage[repository.FormatConan])
	}
	return apptest.NewGateway(t, options, NewGatewayHandler)
}
