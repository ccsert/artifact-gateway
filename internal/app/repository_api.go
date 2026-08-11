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

const managementJSONBodyLimit = 1 << 20

func (m openAPIServeMux) guarded(handler func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if m.authorize != nil {
			if _, ok := m.authorize(w, r); !ok {
				return
			}
		}
		if r.Body != nil && !isManagementObjectUpload(r) {
			r.Body = http.MaxBytesReader(w, r.Body, managementJSONBodyLimit)
		}
		handler(w, r)
	}
}

func isManagementObjectUpload(r *http.Request) bool {
	if r.Method != http.MethodPut {
		return false
	}
	segments := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	for index := 0; index+3 < len(segments); index++ {
		if segments[index] == "publish-sessions" && segments[index+1] != "" && segments[index+2] == "objects" && segments[index+3] != "" {
			return index+4 == len(segments)
		}
	}
	return false
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
	repository.AnonymousAccessPolicyStore
	repository.OIDCSettingsStore
	repository.HostedGroupStore
	repository.RepositoryGrantStore
	repository.AuthorizationTemplateStore
	repository.AuthorizationRoleStore
	repository.RepositoryRetentionPolicyStore
	repository.RepositorySecurityPolicyStore
	repository.RepositoryCapacityStore
	repository.ArtifactTombstoneStore
	repository.ArtifactIntelligenceStore
	repository.AuditRetentionStore
	repository.LifecycleJobStore
	repository.ReplicationStore
	repository.NativeMavenStore
	repository.NativeOCIStore
	repository.NativeRawStore
	repository.NativeConanStore
	repository.NativeNPMStore
	repository.NativePyPIStore
	repository.NativeGoStore
	repository.NativeAPTStore
	repository.APIKeyStore
	repository.UserStore
	repository.UserIdentityStore
	repository.UserSessionStore
	repository.RuntimeNodeStore
	repository.ScheduledTaskStore
	repository.BackgroundOperationQueueStore
}

func NewGatewayHandler(dependencies Dependencies, store GatewayStore, adapter Adapter, authenticator Authenticator, ociClients ...OCIClient) http.Handler {
	return NewGatewayHandlerWithOCICache(dependencies, store, adapter, authenticator, NewDefaultOCICache(NewMemoryOCIObjectStore(), nil), ociClients...)
}

func NewGatewayHandlerWithOCICache(dependencies Dependencies, store GatewayStore, adapter Adapter, authenticator Authenticator, cache *OCICache, ociClients ...OCIClient) http.Handler {
	return NewGatewayHandlerWithCaches(dependencies, store, adapter, authenticator, cache, nil, ociClients...)
}

func NewGatewayHandlerWithCaches(dependencies Dependencies, store GatewayStore, adapter Adapter, authenticator Authenticator, cache *OCICache, mavenCache *MavenCache, ociClients ...OCIClient) http.Handler {
	return newGatewayHandlerWithCaches(dependencies, store, adapter, authenticator, cache, mavenCache, nil, NewConanCache(nil), nil, nil, ociClients...)
}

func NewGatewayHandlerWithCacheMaintenance(dependencies Dependencies, store GatewayStore, adapter Adapter, authenticator Authenticator, cache *OCICache, mavenCache *MavenCache, maintenance *CacheMaintenance, ociClients ...OCIClient) http.Handler {
	return newGatewayHandlerWithCaches(dependencies, store, adapter, authenticator, cache, mavenCache, nil, NewConanCache(nil), maintenance, nil, ociClients...)
}

func NewGatewayHandlerWithRawCache(dependencies Dependencies, store GatewayStore, adapter Adapter, authenticator Authenticator, cache *OCICache, mavenCache *MavenCache, rawCache *RawCache, maintenance *CacheMaintenance, ociClients ...OCIClient) http.Handler {
	return newGatewayHandlerWithCaches(dependencies, store, adapter, authenticator, cache, mavenCache, rawCache, NewConanCache(nil), maintenance, nil, ociClients...)
}

func NewGatewayHandlerWithFormatCaches(dependencies Dependencies, store GatewayStore, adapter Adapter, authenticator Authenticator, cache *OCICache, mavenCache *MavenCache, rawCache *RawCache, conanCache *ConanCache, maintenance *CacheMaintenance, ociClients ...OCIClient) http.Handler {
	return newGatewayHandlerWithCaches(dependencies, store, adapter, authenticator, cache, mavenCache, rawCache, conanCache, maintenance, nil, ociClients...)
}

func NewGatewayHandlerWithFormatCachesAndMetrics(dependencies Dependencies, store GatewayStore, adapter Adapter, authenticator Authenticator, cache *OCICache, mavenCache *MavenCache, rawCache *RawCache, conanCache *ConanCache, maintenance *CacheMaintenance, metrics *Metrics, ociClients ...OCIClient) http.Handler {
	return newGatewayHandlerWithCaches(dependencies, store, adapter, authenticator, cache, mavenCache, rawCache, conanCache, maintenance, metrics, ociClients...)
}

func newGatewayHandlerWithCaches(dependencies Dependencies, store GatewayStore, adapter Adapter, authenticator Authenticator, cache *OCICache, mavenCache *MavenCache, rawCache *RawCache, conanCache *ConanCache, maintenance *CacheMaintenance, metrics *Metrics, ociClients ...OCIClient) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /readyz", dependencies.ready)
	if metrics == nil {
		metrics = &Metrics{}
	}
	resolver := Resolver{Store: store, Adapter: adapter, Metrics: metrics}
	mux.Handle("GET /api/v2/public/repositories", publicRepositoryCatalogHandler{repositories: store, groups: store, anonymous: store})
	authenticator.Users = store
	authenticator.UserIdentities = store
	authenticator.UserSessions = store
	if dependencies.OIDCRuntime != nil {
		authenticator.OIDCSource = dependencies.OIDCRuntime
	}
	api := apiHandler{store: store, repositories: store, resolver: resolver, authenticator: authenticator}
	upstreamClient := OCIClient(UpstreamClient{})
	if len(ociClients) > 0 {
		upstreamClient = ociClients[0]
	}
	mavenClient := MavenClient(UpstreamClient{})
	if client, ok := upstreamClient.(MavenClient); ok {
		mavenClient = client
	}
	rawClient := RawClient(UpstreamClient{})
	if client, ok := upstreamClient.(RawClient); ok {
		rawClient = client
	}
	conanClient := ConanClient(UpstreamClient{})
	if client, ok := upstreamClient.(ConanClient); ok {
		conanClient = client
	}
	npmClient := NPMClient(UpstreamClient{})
	if client, ok := upstreamClient.(NPMClient); ok {
		npmClient = client
	}
	pypiClient := PyPIClient(UpstreamClient{})
	if client, ok := upstreamClient.(PyPIClient); ok {
		pypiClient = client
	}
	goClient := GoClient(UpstreamClient{})
	if client, ok := upstreamClient.(GoClient); ok {
		goClient = client
	}
	oci := OCIHandler{Resolver: resolver, Repositories: store, Authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator}, Client: upstreamClient, Authenticator: authenticator, Cache: cache}
	nativeOCI := newNativeOCIHandler(store, dependencies.NativeOCIObjectStore, authenticator).withMetrics(metrics).withProxy(oci)
	nativeRaw := newNativeRawHandler(store, dependencies.NativeOCIObjectStore, authenticator).withMetrics(metrics).withProxy(rawClient, rawCache)
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
	nativeMaven := newNativeMavenHandler(store, nativeObjects, authenticator).withMetrics(metrics).withProxy(mavenClient, mavenCache)
	mavenProxyOperations := mavenProxyOperationsHandler{store: store, authenticator: authenticator, authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator}, client: mavenClient, cache: mavenCache, maintenance: maintenance}
	proxyCacheBrowse := proxyCacheBrowseHandler{store: store, entriesStore: store, audit: store, maintenance: maintenance, authenticator: authenticator, authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator}}
	nativeConanObjects := dependencies.NativeConanObjectStore
	if nativeConanObjects == nil {
		nativeConanObjects = NewMemoryOCIObjectStore()
	}
	nativeConanPublish := newNativeConanPublishHandler(store, nativeConanObjects, authenticator)
	nativeNPMObjects := dependencies.NativeNPMObjectStore
	if nativeNPMObjects == nil {
		nativeNPMObjects = NewMemoryOCIObjectStore()
	}
	nativeNPM := newNativeNPMHandler(store, nativeNPMObjects, authenticator).withMetrics(metrics).withProxy(npmClient)
	if dependencies.NPMMetadataTTL > 0 {
		nativeNPM.metadataTTL = dependencies.NPMMetadataTTL
	}
	if dependencies.NPMNegativeTTL > 0 {
		nativeNPM.negativeTTL = dependencies.NPMNegativeTTL
	}
	nativeNPM.protection = newNPMProxyProtection(dependencies.NPMProxyCoordinator, dependencies.NPMBreakerTTL)
	nativePyPIObjects := dependencies.NativePyPIObjectStore
	if nativePyPIObjects == nil {
		nativePyPIObjects = NewMemoryOCIObjectStore()
	}
	nativePyPI := newNativePyPIHandler(store, nativePyPIObjects, authenticator).withMetrics(metrics).withProxy(pypiClient)
	nativeGoObjects := dependencies.NativeGoObjectStore
	if nativeGoObjects == nil {
		nativeGoObjects = NewMemoryOCIObjectStore()
	}
	nativeGo := newNativeGoHandler(store, nativeGoObjects, authenticator).withMetrics(metrics).withProxy(goClient)
	nativeAPTObjects := dependencies.NativeAPTObjectStore
	if nativeAPTObjects == nil {
		nativeAPTObjects = NewMemoryOCIObjectStore()
	}
	aptClient := APTClient(UpstreamClient{})
	if client, ok := upstreamClient.(APTClient); ok {
		aptClient = client
	}
	nativeAPT := newNativeAPTHandler(store, nativeAPTObjects, authenticator).withProxy(aptClient)
	publishRouter := nativePublishRouter{maven: nativeMaven, conan: nativeConanPublish}
	hostedRepositories := hostedRepositoryAPIHandler{store: store, groups: store, authenticator: authenticator}
	var searchProjection repository.ArtifactSearchStore
	if candidate, ok := any(store).(repository.ArtifactSearchStore); ok {
		searchProjection = candidate
	}
	adminopenapi.HandlerWithOptions(generatedRepositoryAPIAdapter{hostedRepositoryAPIHandler: hostedRepositories, sessions: nativeMaven, groups: store, grants: store, templates: store, authorizationRoles: store, retentionPolicies: store, securityPolicies: store, capacities: store, tombstones: store, intelligence: store, lifecycleJobs: store, auditRetention: store, anonymousAccess: store, oidcRuntime: dependencies.OIDCRuntime, replication: store, oci: store, conan: store, apiKeys: store, users: store, authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator}, audit: store, metrics: metrics, maintenance: maintenance, proxyCache: proxyCacheBrowse, mavenProxy: mavenProxyOperations, searchProjection: searchProjection, runtimeNodes: store, scheduledTasks: store, queueStats: store, diagnostics: dependencies, artifactScanner: dependencies.ArtifactScanner, artifactScanFormats: dependencies.ArtifactScannerFormats}, adminopenapi.StdHTTPServerOptions{
		BaseURL:    "/api/v2",
		BaseRouter: openAPIServeMux{mux: mux, authorize: hostedRepositories.authenticateManagementRequest},
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
		mux.Handle("GET /api/v1/operations/cache/entries", cacheEntriesHandler{store: store, maintenance: maintenance, authenticator: authenticator})
		mux.Handle("POST /api/v1/operations/cache/collect", cacheCollectionHandler{maintenance: maintenance, authenticator: authenticator})
		mux.Handle("GET /api/v1/operations/repositories", repositoryOperationsHandler{maintenance: maintenance, metrics: metrics, authenticator: authenticator})
	}
	ociGroupRouter := v2GroupRouter{format: repository.FormatOCI, groups: store, repos: store, audit: store, auth: authenticator,
		authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator},
		oci:        &v2GroupOCIHandler{native: &nativeOCI, proxy: &oci, auth: authenticator},
		next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if nativeOCI.ServeHTTP(w, r) {
				return
			}
			oci.ServeHTTP(w, r)
		})}
	mux.Handle("/v2/", hostedRepositoryGuard{store: store, authenticator: authenticator, format: repository.FormatOCI, next: ociGroupRouter})
	mux.Handle("/repository/maven/", nativeMaven)
	mavenLegacy := MavenHandler{Store: store, Repositories: store, Authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator}, Authenticator: authenticator, Client: mavenClient, Metrics: metrics, Cache: mavenCache}
	mavenGroupRouter := v2GroupRouter{format: repository.FormatMaven, groups: store, repos: store, audit: store, auth: authenticator,
		authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator},
		maven:      &v2GroupMavenHandler{native: &nativeMaven, proxy: &mavenLegacy, auth: authenticator},
		next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if nativeMaven.ServeRepositoryHTTP(w, r) {
				return
			}
			mavenLegacy.ServeHTTP(w, r)
		})}
	mux.Handle("/maven/", hostedRepositoryGuard{store: store, authenticator: authenticator, format: repository.FormatMaven, next: mavenGroupRouter})
	rawGroupRouter := v2GroupRouter{format: repository.FormatRaw, groups: store, repos: store, audit: store, auth: authenticator,
		authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator},
		raw:        &v2GroupRawHandler{native: &nativeRaw, auth: authenticator},
		next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if nativeRaw.ServeHTTP(w, r) {
				return
			}
			RawHandler{Store: store, Repositories: store, Authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator}, Authenticator: authenticator, Client: rawClient, Metrics: metrics, Cache: rawCache}.ServeHTTP(w, r)
		})}
	mux.Handle("/raw/", hostedRepositoryGuard{store: store, authenticator: authenticator, format: repository.FormatRaw, next: rawGroupRouter})
	npmGroupRouter := v2GroupRouter{format: repository.FormatNPM, groups: store, repos: store, audit: store, auth: authenticator,
		authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator},
		npm:        &v2GroupNPMHandler{native: &nativeNPM},
		next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !nativeNPM.ServeHTTP(w, r) {
				http.NotFound(w, r)
			}
		})}
	mux.Handle("/npm/", npmGroupRouter)
	pypiGroupRouter := v2GroupRouter{format: repository.FormatPyPI, groups: store, repos: store, audit: store, auth: authenticator,
		authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator},
		pypi:       &v2GroupPyPIHandler{native: &nativePyPI},
		next:       nativePyPI}
	mux.Handle("/pypi/", pypiGroupRouter)
	goGroupRouter := v2GroupRouter{format: repository.FormatGo, groups: store, repos: store, audit: store, auth: authenticator,
		authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator},
		goModules:  &v2GroupGoHandler{native: &nativeGo},
		next:       nativeGo}
	mux.Handle("/go/", goGroupRouter)
	aptGroupRouter := v2GroupRouter{format: repository.FormatAPT, groups: store, repos: store, audit: store, auth: authenticator,
		authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator},
		apt:        &v2GroupAPTHandler{native: &nativeAPT},
		next:       nativeAPT}
	mux.Handle("/apt/", aptGroupRouter)
	conan := ConanHandler{Store: store, NativeStore: store, Repositories: store, Authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator}, Authenticator: authenticator, Client: conanClient, Metrics: metrics, Cache: conanCache, NativeObjects: nativeConanObjects}
	conanGroupRouter := v2GroupRouter{format: repository.FormatConan, groups: store, repos: store, audit: store, auth: authenticator,
		conan: &v2GroupConanHandler{conan: &conan, auth: authenticator},
		next:  conan}
	mux.Handle("/conan/v2/", conanGroupRouter)
	mux.Handle("/conan/", conan)
	mux.HandleFunc("GET /auth/token", oci.Token)
	mux.HandleFunc("POST /auth/login", userLoginHandler(store, store, authenticator))
	mux.HandleFunc("POST /auth/change-password", userChangePasswordHandler(store, store, authenticator))
	oidcLoginValidator := dependencies.OIDCLoginValidator
	if oidcLoginValidator == nil {
		// Keep embedded handlers compatible. The runtime supplies a dedicated
		// validator because the browser client and API may use different audiences.
		oidcLoginValidator = authenticator.OIDC
	}
	oidcLogin := oidcLoginHandler{
		client: dependencies.OIDCClient, validator: oidcLoginValidator,
		runtime: dependencies.OIDCRuntime, authenticator: authenticator, identities: store, sessions: store,
	}
	mux.HandleFunc("GET /auth/oidc/config", oidcLogin.config)
	mux.HandleFunc("GET /auth/oidc/login", oidcLogin.start)
	mux.HandleFunc("GET /auth/oidc/callback", oidcLogin.callback)
	mux.HandleFunc("GET /auth/session", oidcLogin.session)
	mux.HandleFunc("POST /auth/logout", oidcLogin.logout)
	return sessionCookieAuthentication(tracedHTTPHandler(mux))
}
