package app

import (
	"context"
	"net/http"
	"strings"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type Adapter interface {
	Available(context.Context, repository.Member, string) bool
}

// openAPIServeMux bridges OpenAPI's `{sessionId}:commit` template to the
// standard library router, whose wildcard path segment must end with `}`.
type openAPIServeMux struct {
	mux       *http.ServeMux
	authorize func(http.ResponseWriter, *http.Request) (Principal, bool)
}

func (m openAPIServeMux) guarded(handler func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if m.authorize != nil {
			if _, ok := m.authorize(w, r); !ok {
				return
			}
		}
		handler(w, r)
	}
}

func (m openAPIServeMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	const commitPatternSuffix = "/publish-sessions/{sessionId}:commit"
	if strings.HasSuffix(pattern, commitPatternSuffix) {
		prefix := strings.TrimSuffix(pattern, "{sessionId}:commit")
		m.mux.HandleFunc(prefix, m.guarded(func(w http.ResponseWriter, r *http.Request) {
			sessionID := strings.TrimPrefix(r.URL.Path, strings.TrimPrefix(prefix, http.MethodPost+" "))
			if sessionID == "" || strings.Contains(sessionID, "/") || !strings.HasSuffix(sessionID, ":commit") {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			r.SetPathValue("sessionId", strings.TrimSuffix(sessionID, ":commit"))
			handler(w, r)
		}))
		return
	}
	m.mux.HandleFunc(pattern, m.guarded(handler))
}

func (m openAPIServeMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mux.ServeHTTP(w, r)
}

type TestAdapter struct{}

func (TestAdapter) Available(_ context.Context, member repository.Member, _ string) bool {
	return member.Endpoint != "test://unavailable"
}

type GatewayStore interface {
	repository.Store
	repository.MavenStore
	repository.RawStore
	repository.ConanStore
	repository.HostedRepositoryStore
	repository.HostedGroupStore
	repository.RepositoryGrantStore
	repository.RepositoryRetentionPolicyStore
	repository.NativeMavenStore
	repository.NativeOCIStore
	repository.NativeRawStore
	repository.NativeConanStore
}

func NewGatewayHandler(dependencies Dependencies, store GatewayStore, adapter Adapter, authenticator Authenticator, ociClients ...OCIClient) http.Handler {
	return NewGatewayHandlerWithOCICache(dependencies, store, adapter, authenticator, NewDefaultOCICache(NewMemoryOCIObjectStore(), nil), ociClients...)
}

func NewGatewayHandlerWithOCICache(dependencies Dependencies, store GatewayStore, adapter Adapter, authenticator Authenticator, cache *OCICache, ociClients ...OCIClient) http.Handler {
	return NewGatewayHandlerWithCaches(dependencies, store, adapter, authenticator, cache, nil, ociClients...)
}

func NewGatewayHandlerWithCaches(dependencies Dependencies, store GatewayStore, adapter Adapter, authenticator Authenticator, cache *OCICache, mavenCache *MavenCache, ociClients ...OCIClient) http.Handler {
	return newGatewayHandlerWithCaches(dependencies, store, adapter, authenticator, cache, mavenCache, nil, NewConanCache(nil), nil, ociClients...)
}

func NewGatewayHandlerWithCacheMaintenance(dependencies Dependencies, store GatewayStore, adapter Adapter, authenticator Authenticator, cache *OCICache, mavenCache *MavenCache, maintenance *CacheMaintenance, ociClients ...OCIClient) http.Handler {
	return newGatewayHandlerWithCaches(dependencies, store, adapter, authenticator, cache, mavenCache, nil, NewConanCache(nil), maintenance, ociClients...)
}

func NewGatewayHandlerWithRawCache(dependencies Dependencies, store GatewayStore, adapter Adapter, authenticator Authenticator, cache *OCICache, mavenCache *MavenCache, rawCache *RawCache, maintenance *CacheMaintenance, ociClients ...OCIClient) http.Handler {
	return newGatewayHandlerWithCaches(dependencies, store, adapter, authenticator, cache, mavenCache, rawCache, NewConanCache(nil), maintenance, ociClients...)
}

func NewGatewayHandlerWithFormatCaches(dependencies Dependencies, store GatewayStore, adapter Adapter, authenticator Authenticator, cache *OCICache, mavenCache *MavenCache, rawCache *RawCache, conanCache *ConanCache, maintenance *CacheMaintenance, ociClients ...OCIClient) http.Handler {
	return newGatewayHandlerWithCaches(dependencies, store, adapter, authenticator, cache, mavenCache, rawCache, conanCache, maintenance, ociClients...)
}

func newGatewayHandlerWithCaches(dependencies Dependencies, store GatewayStore, adapter Adapter, authenticator Authenticator, cache *OCICache, mavenCache *MavenCache, rawCache *RawCache, conanCache *ConanCache, maintenance *CacheMaintenance, ociClients ...OCIClient) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /readyz", dependencies.ready)
	metrics := &Metrics{}
	resolver := Resolver{Store: store, Adapter: adapter, Metrics: metrics}
	api := apiHandler{store: store, repositories: store, resolver: resolver, authenticator: authenticator}
	ociClient := OCIClient(UpstreamClient{})
	if len(ociClients) > 0 {
		ociClient = ociClients[0]
	}
	mavenClient := MavenClient(UpstreamClient{})
	if client, ok := ociClient.(MavenClient); ok {
		mavenClient = client
	}
	rawClient := RawClient(UpstreamClient{})
	if client, ok := ociClient.(RawClient); ok {
		rawClient = client
	}
	conanClient := ConanClient(UpstreamClient{})
	if client, ok := ociClient.(ConanClient); ok {
		conanClient = client
	}
	oci := OCIHandler{Resolver: resolver, Repositories: store, Authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator}, Client: ociClient, Authenticator: authenticator, Cache: cache}
	nativeOCI := newNativeOCIHandler(store, dependencies.NativeOCIObjectStore, authenticator).withMetrics(metrics)
	nativeRaw := newNativeRawHandler(store, dependencies.NativeOCIObjectStore, authenticator).withMetrics(metrics)
	mux.Handle("GET /metrics", http.HandlerFunc(metrics.Handler))
	mux.Handle("/api/v1/oci/groups", api)
	mux.Handle("/api/v1/oci/groups/", api)
	mux.Handle("/api/v1/maven/groups", mavenAPIHandler{store: store, repositories: store, authenticator: authenticator})
	mux.Handle("/api/v1/maven/groups/", mavenAPIHandler{store: store, repositories: store, authenticator: authenticator})
	rawAPI := rawAPIHandler{store: store, repositories: store, authenticator: authenticator, cache: rawCache}
	mux.Handle("/api/v1/raw/groups", rawAPI)
	mux.Handle("/api/v1/raw/groups/", rawAPI)
	mux.Handle("POST /api/v1/raw/cache/invalidate", rawCacheInvalidationHandler{store: store, authenticator: authenticator, cache: rawCache})
	conanAPI := conanAPIHandler{store: store, authenticator: authenticator}
	mux.Handle("/api/v1/conan/groups", conanAPI)
	mux.Handle("/api/v1/conan/groups/", conanAPI)
	nativeObjects := dependencies.NativeMavenObjectStore
	if nativeObjects == nil {
		nativeObjects = NewMemoryOCIObjectStore()
	}
	nativeMaven := newNativeMavenHandler(store, nativeObjects, authenticator).withMetrics(metrics)
	nativeConanObjects := dependencies.NativeConanObjectStore
	if nativeConanObjects == nil {
		nativeConanObjects = NewMemoryOCIObjectStore()
	}
	nativeConanPublish := newNativeConanPublishHandler(store, nativeConanObjects, authenticator)
	publishRouter := nativePublishRouter{maven: nativeMaven, conan: nativeConanPublish}
	hostedRepositories := hostedRepositoryAPIHandler{store: store, authenticator: authenticator}
	adminopenapi.HandlerWithOptions(generatedRepositoryAPIAdapter{hostedRepositoryAPIHandler: hostedRepositories, sessions: nativeMaven, groups: store, grants: store, retentionPolicies: store, oci: store, conan: store, authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator}, audit: store, metrics: metrics}, adminopenapi.StdHTTPServerOptions{
		BaseURL:    "/api/v2",
		BaseRouter: openAPIServeMux{mux: mux, authorize: hostedRepositories.authenticate},
		ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		},
	})
	mux.Handle("/api/v2/repositories/", publishRouter)
	mux.Handle("/api/v2/publish-sessions/", nativeMaven)
	mux.Handle("/api/v2/conan-publish-sessions/", nativeConanPublish)
	mux.Handle("POST /api/v1/conan/cache/invalidate", conanCacheInvalidationHandler{store: store, authenticator: authenticator, cache: conanCache})
	mux.Handle("GET /api/v1/audits", auditAPIHandler{store: store, authenticator: authenticator})
	if maintenance != nil {
		mux.Handle("GET /api/v1/operations/cache", cacheOperationsHandler{maintenance: maintenance, authenticator: authenticator})
		mux.Handle("POST /api/v1/operations/cache/collect", cacheCollectionHandler{maintenance: maintenance, authenticator: authenticator})
		mux.Handle("GET /api/v1/operations/repositories", repositoryOperationsHandler{maintenance: maintenance, metrics: metrics, authenticator: authenticator})
	}
	mux.Handle("/v2/", hostedRepositoryGuard{store: store, authenticator: authenticator, format: repository.FormatOCI, next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if nativeOCI.ServeHTTP(w, r) {
			return
		}
		oci.ServeHTTP(w, r)
	})})
	mux.Handle("/repository/maven/", nativeMaven)
	mux.Handle("/maven/", hostedRepositoryGuard{store: store, authenticator: authenticator, format: repository.FormatMaven, next: MavenHandler{Store: store, Repositories: store, Authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator}, Authenticator: authenticator, Client: mavenClient, Metrics: metrics, Cache: mavenCache}})
	mux.Handle("/raw/", hostedRepositoryGuard{store: store, authenticator: authenticator, format: repository.FormatRaw, next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if nativeRaw.ServeHTTP(w, r) {
			return
		}
		RawHandler{Store: store, Repositories: store, Authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator}, Authenticator: authenticator, Client: rawClient, Metrics: metrics, Cache: rawCache}.ServeHTTP(w, r)
	})})
	conan := ConanHandler{Store: store, NativeStore: store, Repositories: store, Authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator}, Authenticator: authenticator, Client: conanClient, Metrics: metrics, Cache: conanCache, NativeObjects: nativeConanObjects}
	mux.Handle("/conan/v2/", conan)
	mux.Handle("/conan/", conan)
	mux.HandleFunc("GET /auth/token", oci.Token)
	return tracedHTTPHandler(mux)
}
