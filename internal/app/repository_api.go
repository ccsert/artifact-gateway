package app

import (
	"context"
	"net/http"
	"strings"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/aptpublication"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type Adapter interface {
	Available(context.Context, repository.Member, string) bool
}

// openAPIServeMux bridges OpenAPI action templates such as
// `{sessionId}:commit` to the standard library router, whose wildcard path
// segment must end with `}`.
type openAPIServeMux struct {
	mux       *http.ServeMux
	authorize func(http.ResponseWriter, *http.Request) (Principal, bool)
}

const managementJSONBodyLimit = 1 << 20

func (m openAPIServeMux) guarded(handler func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if m.authorize != nil && !isPublicManagementRequest(r) {
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

func isPublicManagementRequest(r *http.Request) bool {
	return r.Method == http.MethodGet && r.URL.Path == "/api/v2/site-settings"
}

func isManagementObjectUpload(r *http.Request) bool {
	if r.Method != http.MethodPut {
		return false
	}
	segments := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(segments) == 8 && segments[0] == "api" && segments[1] == "v2" && segments[2] == "repositories" && segments[3] != "" && segments[4] == "apt" && segments[5] == "publication-sessions" && segments[6] != "" && segments[7] == "package" {
		return true
	}
	for index := 0; index+3 < len(segments); index++ {
		if segments[index] == "publish-sessions" && segments[index+1] != "" && segments[index+2] == "objects" && segments[index+3] != "" {
			return index+4 == len(segments)
		}
	}
	return false
}

func (m openAPIServeMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	if m.handlePathParameterAction(pattern, "sessionId", "commit", handler) || m.handlePathParameterAction(pattern, "deliveryId", "replay", handler) {
		return
	}
	m.mux.HandleFunc(pattern, m.guarded(handler))
}

func (m openAPIServeMux) handlePathParameterAction(pattern, parameter, action string, handler func(http.ResponseWriter, *http.Request)) bool {
	template := "{" + parameter + "}:" + action
	if !strings.HasSuffix(pattern, template) {
		return false
	}
	method, _, ok := strings.Cut(pattern, " ")
	if !ok {
		return false
	}
	prefix := strings.TrimSuffix(pattern, template)
	pathPrefix := strings.TrimPrefix(prefix, method+" ")
	m.mux.HandleFunc(prefix, m.guarded(func(w http.ResponseWriter, r *http.Request) {
		value := strings.TrimPrefix(r.URL.Path, pathPrefix)
		suffix := ":" + action
		if value == "" || strings.Contains(value, "/") || !strings.HasSuffix(value, suffix) {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		r.SetPathValue(parameter, strings.TrimSuffix(value, suffix))
		handler(w, r)
	}))
	return true
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
	repository.SiteSettingsStore
	repository.OIDCSettingsStore
	repository.HostedGroupStore
	repository.RepositoryGrantStore
	repository.AuthorizationTemplateStore
	repository.AuthorizationRoleStore
	repository.RepositoryRetentionPolicyStore
	repository.RepositorySecurityPolicyStore
	repository.RepositoryQuarantineReadPolicyStore
	repository.RepositoryCapacityStore
	repository.ArtifactTombstoneStore
	repository.ArtifactIntelligenceStore
	repository.ArtifactQuarantineStore
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
	repository.NativeAPTPublicationStore
	repository.APIKeyStore
	repository.ServiceAccountStore
	repository.UserStore
	repository.UserIdentityStore
	repository.UserSessionStore
	repository.RuntimeNodeStore
	repository.ScheduledTaskStore
	repository.WebhookStore
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
	authenticator.ServiceAccounts = store
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
	publicationScanner := newPublicationScanScheduler(store, dependencies.ArtifactScanner != nil, dependencies.ArtifactScannerFormats, metrics)
	nativeOCI := newNativeOCIHandler(store, dependencies.NativeOCIObjectStore, authenticator).withMetrics(metrics).withProxy(oci).withPublicationScanner(publicationScanner)
	nativeRaw := newNativeRawHandler(store, dependencies.NativeOCIObjectStore, authenticator).withMetrics(metrics).withProxy(rawClient, rawCache).withPublicationScanner(publicationScanner)
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
	nativeMaven := newNativeMavenHandler(store, nativeObjects, authenticator).withMetrics(metrics).withProxy(mavenClient, mavenCache).withPublicationScanner(publicationScanner)
	mavenProxyOperations := mavenProxyOperationsHandler{store: store, authenticator: authenticator, authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator}, client: mavenClient, cache: mavenCache, maintenance: maintenance}
	proxyCacheBrowse := proxyCacheBrowseHandler{store: store, entriesStore: store, audit: store, maintenance: maintenance, authenticator: authenticator, authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator}}
	nativeConanObjects := dependencies.NativeConanObjectStore
	if nativeConanObjects == nil {
		nativeConanObjects = NewMemoryOCIObjectStore()
	}
	nativeConanPublish := newNativeConanPublishHandler(store, nativeConanObjects, authenticator).withPublicationScanner(publicationScanner)
	nativeNPMObjects := dependencies.NativeNPMObjectStore
	if nativeNPMObjects == nil {
		nativeNPMObjects = NewMemoryOCIObjectStore()
	}
	nativeNPM := newNativeNPMHandler(store, nativeNPMObjects, authenticator).withMetrics(metrics).withProxy(npmClient).withPublicationScanner(publicationScanner)
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
	nativePyPI := newNativePyPIHandler(store, nativePyPIObjects, authenticator).withMetrics(metrics).withProxy(pypiClient).withPublicationScanner(publicationScanner)
	nativeGoObjects := dependencies.NativeGoObjectStore
	if nativeGoObjects == nil {
		nativeGoObjects = NewMemoryOCIObjectStore()
	}
	nativeGo := newNativeGoHandler(store, nativeGoObjects, authenticator).withMetrics(metrics).withProxy(goClient).withPublicationScanner(publicationScanner)
	nativeAPTObjects := dependencies.NativeAPTObjectStore
	if nativeAPTObjects == nil {
		nativeAPTObjects = NewMemoryOCIObjectStore()
	}
	aptClient := APTClient(UpstreamClient{})
	if client, ok := upstreamClient.(APTClient); ok {
		aptClient = client
	}
	nativeAPT := newNativeAPTHandler(store, nativeAPTObjects, authenticator).withProxy(aptClient)
	aptPublication := aptpublication.NewManager(store, nativeAPTObjects)
	var aptSnapshotPublisher *aptpublication.Publisher
	if dependencies.APTSigner != nil {
		aptSnapshotPublisher = aptpublication.NewPublisher(store, nativeAPTObjects, dependencies.APTSigner).WithMetrics(metrics)
	}
	publishRouter := nativePublishRouter{maven: nativeMaven, conan: nativeConanPublish}
	hostedRepositories := hostedRepositoryAPIHandler{store: store, groups: store, authenticator: authenticator}
	var searchProjection repository.ArtifactSearchStore
	if candidate, ok := any(store).(repository.ArtifactSearchStore); ok {
		searchProjection = candidate
	}
	adminopenapi.HandlerWithOptions(generatedRepositoryAPIAdapter{hostedRepositoryAPIHandler: hostedRepositories, sessions: nativeMaven, aptPublication: aptPublication, aptSnapshotPublisher: aptSnapshotPublisher, aptPublications: store, groups: store, grants: store, templates: store, authorizationRoles: store, retentionPolicies: store, securityPolicies: store, quarantineReadPolicies: store, capacities: store, tombstones: store, intelligence: store, quarantine: store, lifecycleJobs: store, auditRetention: store, anonymousAccess: store, siteSettings: store, consoleThemes: dependencies.ConsoleThemes, oidcRuntime: dependencies.OIDCRuntime, replication: store, oci: store, conan: store, apiKeys: store, serviceAccounts: store, users: store, authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator}, audit: store, metrics: metrics, maintenance: maintenance, proxyCache: proxyCacheBrowse, mavenProxy: mavenProxyOperations, searchProjection: searchProjection, runtimeNodes: store, scheduledTasks: store, webhooks: store, queueStats: store, diagnostics: dependencies, artifactScanner: dependencies.ArtifactScanner, artifactScanFormats: dependencies.ArtifactScannerFormats}, adminopenapi.StdHTTPServerOptions{
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
	mavenProtocol := hostedRepositoryGuard{store: store, authenticator: authenticator, format: repository.FormatMaven, next: mavenGroupRouter}
	mux.Handle("/maven/", mavenProtocol)
	rawRepositoryHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if nativeRaw.ServeHTTP(w, r) {
			return
		}
		RawHandler{Store: store, Repositories: store, Authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator}, Authenticator: authenticator, Client: rawClient, Metrics: metrics, Cache: rawCache}.ServeHTTP(w, r)
	})
	rawGroupRouter := v2GroupRouter{format: repository.FormatRaw, groups: store, repos: store, audit: store, auth: authenticator,
		authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator},
		raw:        &v2GroupRawHandler{native: &nativeRaw, auth: authenticator},
		next:       rawRepositoryHandler}
	rawProtocol := hostedRepositoryGuard{store: store, authenticator: authenticator, format: repository.FormatRaw, next: rawGroupRouter}
	rawRepositoryProtocol := hostedRepositoryGuard{store: store, authenticator: authenticator, format: repository.FormatRaw, next: rawRepositoryHandler}
	mux.Handle("/raw/", rawProtocol)
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
	conan := ConanHandler{Store: store, NativeStore: store, Repositories: store, Authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator}, Authenticator: authenticator, Client: conanClient, Metrics: metrics, Cache: conanCache, NativeObjects: nativeConanObjects, ReadPolicies: store, Quarantine: store}
	conanGroupRouter := v2GroupRouter{format: repository.FormatConan, groups: store, repos: store, audit: store, auth: authenticator,
		conan: &v2GroupConanHandler{conan: &conan, auth: authenticator},
		next:  conan}
	mux.Handle("/conan/v2/", conanGroupRouter)
	mux.Handle("/conan/", conan)
	nexusCompatibility := nexusRepositoryCompatibilityRouter{
		repositories: store,
		groups:       store,
		routes: map[repository.Format]nexusRepositoryCompatibilityRoute{
			repository.FormatMaven: {
				repositoryPrefix: "/repository/maven/", repositoryHandler: nativeMaven,
				groupPrefix: "/maven/", groupHandler: mavenProtocol,
			},
			repository.FormatNPM: {
				repositoryPrefix: "/npm/", repositoryHandler: npmGroupRouter,
				groupPrefix: "/npm/", groupHandler: npmGroupRouter,
			},
			repository.FormatPyPI: {
				repositoryPrefix: "/pypi/", repositoryHandler: pypiGroupRouter,
				groupPrefix: "/pypi/", groupHandler: pypiGroupRouter,
			},
			repository.FormatRaw: {
				repositoryPrefix: "/raw/", repositoryHandler: rawRepositoryProtocol,
				groupPrefix: "/raw/", groupHandler: rawProtocol,
			},
			repository.FormatGo: {
				repositoryPrefix: "/go/", repositoryHandler: nexusGoRepositoryCompatibilityHandler{native: nativeGo},
				groupPrefix: "/go/", groupHandler: goGroupRouter,
			},
		},
	}
	mux.Handle("/repository/", nexusCompatibility)
	// This legacy canonical Maven prefix is intentionally more specific than
	// the Nexus-style catch-all. A compatibility target named "maven" is
	// therefore reserved rather than ambiguously routing artifact path segments.
	mux.Handle("/repository/maven/", nativeMaven)
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
