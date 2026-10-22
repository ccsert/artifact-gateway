package app

import (
	"errors"
	"fmt"
	"reflect"

	helmformat "github.com/ccsert/artifact-gateway/internal/format/helm"
	nugetformat "github.com/ccsert/artifact-gateway/internal/format/nuget"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/ocidiscovery"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

// ObjectStore is the format-independent byte-store port used by native
// publication, replication, maintenance, and protocol caches.
type ObjectStore = objectstore.Store
type ObjectInfo = objectstore.Info

// ObjectStores supplies each format's byte namespace explicitly. Formats may
// share one durable store because their object keys are separately namespaced.
// Every admitted format is required: the Gateway exposes all of their routes.
type ObjectStores map[repository.Format]ObjectStore

// SharedObjectStores explicitly assigns one store to all admitted formats.
// It never creates storage or replaces a missing dependency.
func SharedObjectStores(store ObjectStore) ObjectStores {
	stores := make(ObjectStores)
	for _, format := range repository.SupportedFormats() {
		stores[format] = store
	}
	return stores
}

// GatewayCaches is the explicitly assembled proxy-cache layer. A nil format
// cache disables that cache; no replacement byte store is created by the router.
type GatewayCaches struct {
	OCI         *OCICache
	Maven       *MavenCache
	Raw         *RawCache
	Conan       *ConanCache
	Maintenance *CacheMaintenance
}

// GatewayOptions is the only HTTP composition input. The production runtime
// supplies PostgreSQL metadata and verified S3/RustFS storage before invoking
// NewGatewayHandler. Tests and client fixtures explicitly supply test stores.
type GatewayOptions struct {
	Dependencies      Dependencies
	Store             GatewayStore
	ConanPublications repository.ConanPublicationStore
	NPMMetadata       repository.NPMMetadataStore
	NPMRestore        repository.NPMRestoreStore
	OCIDiscoveryStore ocidiscovery.Store
	Storage           ObjectStores
	// NuGet is an opt-in preview, outside the admitted format catalog.
	// Its persistent ports are supplied explicitly, never inferred from Store.
	HelmPreviewEnabled  bool
	HelmStore           repository.NativeHelmStore
	HelmSources         repository.HelmSourceStore
	HelmUpstream        helmformat.Upstream
	HelmObjects         ObjectStore
	NuGetPreviewEnabled bool
	NuGetStore          repository.NativeNuGetStore
	NuGetSources        repository.NuGetSourceStore
	NuGetUpstream       nugetformat.Upstream
	NuGetObjects        ObjectStore
	Adapter             Adapter
	Authenticator       Authenticator
	Caches              GatewayCaches
	// Upstream defaults to the standard HTTP transport. If it also implements
	// another format's client port, that implementation is used for that format.
	Upstream OCIClient
	Metrics  *Metrics
}

func (o GatewayOptions) validate() error {
	var failures []error
	if o.HelmPreviewEnabled {
		if nilDependency(o.HelmStore) {
			failures = append(failures, errors.New("helm preview publication store is required"))
		}
		if nilDependency(o.HelmSources) {
			failures = append(failures, errors.New("helm preview source store is required"))
		}
		if o.HelmUpstream != nil && nilDependency(o.HelmUpstream) {
			failures = append(failures, errors.New("helm preview upstream client is typed nil"))
		}
		if err := objectstore.Validate(o.HelmObjects); err != nil {
			failures = append(failures, fmt.Errorf("helm preview %w", err))
		}
	} else if o.HelmStore != nil || o.HelmSources != nil || o.HelmUpstream != nil || o.HelmObjects != nil {
		failures = append(failures, errors.New("helm preview dependencies require explicit enablement"))
	}
	if o.NuGetPreviewEnabled {
		if nilDependency(o.NuGetStore) {
			failures = append(failures, errors.New("NuGet preview publication store is required"))
		}
		if nilDependency(o.NuGetSources) {
			failures = append(failures, errors.New("NuGet preview source store is required"))
		}
		if o.NuGetUpstream != nil && nilDependency(o.NuGetUpstream) {
			failures = append(failures, errors.New("NuGet preview upstream client is typed nil"))
		}
		if err := objectstore.Validate(o.NuGetObjects); err != nil {
			failures = append(failures, fmt.Errorf("NuGet preview %w", err))
		}
	} else if o.NuGetStore != nil || o.NuGetObjects != nil || o.NuGetSources != nil || o.NuGetUpstream != nil {
		failures = append(failures, errors.New("NuGet preview dependencies require explicit enablement"))
	}
	if nilDependency(o.Store) {
		failures = append(failures, errors.New("metadata store is required"))
	}
	if nilDependency(o.NPMMetadata) {
		failures = append(failures, errors.New("npm metadata mutation store is required"))
	}
	if nilDependency(o.NPMRestore) {
		failures = append(failures, errors.New("npm restore store is required"))
	}
	if nilDependency(o.Adapter) {
		failures = append(failures, errors.New("repository availability adapter is required"))
	}
	for _, format := range repository.SupportedFormats() {
		if err := objectstore.Validate(o.Storage[format]); err != nil {
			failures = append(failures, fmt.Errorf("%s %w", format, err))
		}
	}
	for format := range o.Storage {
		if !repository.IsSupportedFormat(format) {
			failures = append(failures, fmt.Errorf("unsupported object-store format %q", format))
		}
	}
	if o.Upstream != nil && nilDependency(o.Upstream) {
		failures = append(failures, errors.New("upstream client is nil"))
	}
	if o.OCIDiscoveryStore != nil && nilDependency(o.OCIDiscoveryStore) {
		failures = append(failures, errors.New("OCI discovery store is nil"))
	}
	if o.ConanPublications != nil && nilDependency(o.ConanPublications) {
		failures = append(failures, errors.New("conan publication capability is nil"))
	}
	for _, configured := range []struct {
		name  string
		cache interface{ Validate() error }
	}{{"OCI", o.Caches.OCI}, {"Maven", o.Caches.Maven}, {"Raw", o.Caches.Raw}, {"Conan", o.Caches.Conan}} {
		if !nilDependency(configured.cache) {
			if err := configured.cache.Validate(); err != nil {
				failures = append(failures, fmt.Errorf("%s cache: %w", configured.name, err))
			}
		}
	}
	if o.Caches.Maintenance != nil {
		if err := objectstore.Validate(o.Caches.Maintenance.store); err != nil {
			failures = append(failures, fmt.Errorf("cache maintenance %w", err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("configure Gateway: %w", errors.Join(failures...))
	}
	return nil
}

func nilDependency(value any) bool {
	if value == nil {
		return true
	}
	switch reflect.ValueOf(value).Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflect.ValueOf(value).IsNil()
	}
	return false
}
