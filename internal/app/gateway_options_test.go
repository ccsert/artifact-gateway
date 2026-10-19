package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/apptest"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

func explicitGatewayOptions() GatewayOptions {
	return GatewayOptions{
		Store: repository.NewMemoryStore(), Storage: apptest.MemoryObjectStores(nil),
		Adapter: TestAdapter{}, Authenticator: testAuthenticator(),
	}
}

func TestGatewayCompositionRejectsMissingDependenciesBeforeServing(t *testing.T) {
	for _, test := range []struct {
		name    string
		change  func(*GatewayOptions)
		message string
	}{
		{"metadata", func(o *GatewayOptions) { o.Store = nil }, "metadata store is required"},
		{"typed nil metadata", func(o *GatewayOptions) { o.Store = (*repository.PostgresStore)(nil) }, "metadata store is required"},
		{"adapter", func(o *GatewayOptions) { o.Adapter = nil }, "repository availability adapter is required"},
		{"typed nil adapter", func(o *GatewayOptions) { o.Adapter = (*TestAdapter)(nil) }, "repository availability adapter is required"},
		{"no objects", func(o *GatewayOptions) { o.Storage = nil }, "maven object store is required"},
		{"unknown format", func(o *GatewayOptions) { o.Storage[repository.Format("unknown")] = objectstore.NewMemoryStore() }, `unsupported object-store format "unknown"`},
		{"typed nil upstream", func(o *GatewayOptions) { o.Upstream = (*UpstreamClient)(nil) }, "upstream client is nil"},
		{"maintenance objects", func(o *GatewayOptions) { o.Caches.Maintenance = NewCacheMaintenance(nil, nil) }, "cache maintenance object store is required"},
		{"OCI cache objects", func(o *GatewayOptions) { o.Caches.OCI = NewDefaultOCICache(nil, nil) }, "OCI cache: object store is required"},
		{"Maven cache objects", func(o *GatewayOptions) { o.Caches.Maven = NewDefaultMavenCache(nil, nil) }, "Maven cache: object store is required"},
		{"Raw cache typed nil objects", func(o *GatewayOptions) { o.Caches.Raw = NewDefaultRawCache((*objectstore.MemoryStore)(nil), nil) }, "Raw cache: object store is required"},
		{"Conan cache objects", func(o *GatewayOptions) { o.Caches.Conan = NewConanCache(nil) }, "Conan cache: object store is required"},
		{"unconfigured Conan cache", func(o *GatewayOptions) { o.Caches.Conan = &ConanCache{} }, "Conan cache: object store is required"},
	} {
		t.Run(test.name, func(t *testing.T) {
			options := explicitGatewayOptions()
			test.change(&options)
			handler, err := NewGatewayHandler(options)
			if handler != nil || err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("handler=%v error=%v, want failure containing %q", handler, err, test.message)
			}
		})
	}
}

func TestGatewayCompositionRequiresEveryFormatObjectStore(t *testing.T) {
	for _, format := range repository.SupportedFormats() {
		t.Run(string(format), func(t *testing.T) {
			for _, typedNil := range []bool{false, true} {
				options := explicitGatewayOptions()
				if typedNil {
					options.Storage[format] = (*objectstore.MemoryStore)(nil)
				} else {
					delete(options.Storage, format)
				}
				handler, err := NewGatewayHandler(options)
				if handler != nil || err == nil || !strings.Contains(err.Error(), string(format)+" object store is required") {
					t.Fatalf("typedNil=%v handler=%v error=%v", typedNil, handler, err)
				}
			}
		})
	}
}

func TestGatewayExplicitStorageSurvivesHandlerRecompositionAndUsesRawNamespace(t *testing.T) {
	options := explicitGatewayOptions()
	store := options.Store
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "explicit-raw", Name: "explicit-raw", Format: repository.FormatRaw}); err != nil {
		t.Fatal(err)
	}
	first, err := NewGatewayHandler(options)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/raw/explicit-raw/releases/widget.txt", strings.NewReader("explicit object bytes"))
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	first.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("publish=%d %s", response.Code, response.Body.String())
	}
	rawKeys, err := options.Storage[repository.FormatRaw].List(context.Background(), "")
	if err != nil || len(rawKeys) == 0 {
		t.Fatalf("Raw object keys=%v error=%v", rawKeys, err)
	}
	ociKeys, err := options.Storage[repository.FormatOCI].List(context.Background(), "")
	if err != nil || len(ociKeys) != 0 {
		t.Fatalf("OCI namespace received Raw bytes: keys=%v error=%v", ociKeys, err)
	}
	second, err := NewGatewayHandler(options)
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, "/raw/explicit-raw/releases/widget.txt", nil)
	authorize(request, "resolver-secret")
	response = httptest.NewRecorder()
	second.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "explicit object bytes" {
		t.Fatalf("recomposed read=%d %q", response.Code, response.Body.String())
	}
	isolated := options
	isolated.Storage = apptest.MemoryObjectStores(nil)
	other, err := NewGatewayHandler(isolated)
	if err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	other.ServeHTTP(response, request)
	if response.Code == http.StatusOK {
		t.Fatalf("a new fixture unexpectedly shared object bytes: %s", response.Body.String())
	}
}
