package app

import (
	"context"
	"maps"
	"net/http"
	"strings"

	migrationapi "github.com/ccsert/artifact-gateway/internal/admin/nexusmigration"
	sourceapi "github.com/ccsert/artifact-gateway/internal/admin/nexussource"
	adminopenapi "github.com/ccsert/artifact-gateway/internal/admin/openapi"
	"github.com/ccsert/artifact-gateway/internal/aptpublication"
	formatmodule "github.com/ccsert/artifact-gateway/internal/format"
	rawformat "github.com/ccsert/artifact-gateway/internal/format/raw"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

type Adapter interface {
	Available(context.Context, repository.Member, string) bool
}

// openAPIServeMux bridges OpenAPI action templates such as
// `{sessionId}:commit` to the standard library router, whose wildcard path
// segment must end with `}`.
type openAPIServeMux struct {
	mux interface {
		http.Handler
		HandleFunc(string, func(http.ResponseWriter, *http.Request))
	}
	authorize func(http.ResponseWriter, *http.Request) (Principal, bool)
}

const managementJSONBodyLimit = 1 << 20

func (m openAPIServeMux) guarded(handler func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Query().Get("match") == "contains" && (r.URL.Path == "/api/v2/artifact-search" || strings.HasPrefix(r.URL.Path, "/api/v2/repositories/") && strings.HasSuffix(r.URL.Path, "/artifact-search")) {
			bounded, cancel := artifactSearchRequest(r, repository.ArtifactSearchContains)
			defer cancel()
			r = bounded
		}
		if strings.HasPrefix(r.URL.Path, "/api/v2/email-") || strings.HasPrefix(r.URL.Path, "/api/v2/repository-quota-alert-") {
			w.Header().Set("Cache-Control", "no-store")
		}
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
	segments := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if r.Method == http.MethodPost && len(segments) == 7 && segments[0] == "api" && segments[1] == "v2" && segments[2] == "repositories" && segments[3] != "" && segments[4] == "apt" && segments[5] == "snapshots" && segments[6] == "restore" {
		return true
	}
	if r.Method != http.MethodPut {
		return false
	}
	if len(segments) == 8 && segments[0] == "api" && segments[1] == "v2" && segments[2] == "repositories" && segments[3] != "" && segments[4] == "apt" && segments[5] == "publication-sessions" && segments[6] != "" && segments[7] == "package" {
		return true
	}
	for index := 0; index+3 < len(segments); index++ {
		if (segments[index] == "publish-sessions" || segments[index] == "conan-publish-sessions") && segments[index+1] != "" && segments[index+2] == "objects" && segments[index+3] != "" {
			return index+4 == len(segments)
		}
	}
	return false
}

func (m openAPIServeMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	if m.handlePathParameterAction(pattern, "sessionId", "commit", handler) || m.handlePathParameterAction(pattern, "deliveryId", "replay", handler) {
		return
	}
	guarded := m.guarded(handler)
	if pattern == "GET /api/v2/runtime/logs" {
		// Authentication and generated parameter parsing can reject requests
		// before the log adapter runs, so apply its policy before both.
		m.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			guarded(w, r)
		})
		return
	}
	m.mux.HandleFunc(pattern, guarded)
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
	if strings.HasPrefix(r.URL.Path, "/api/v2/email-") || strings.HasPrefix(r.URL.Path, "/api/v2/repository-quota-alert-") {
		w.Header().Set("Cache-Control", "no-store")
	}
	m.mux.ServeHTTP(w, r)
}

type TestAdapter struct{}

func (TestAdapter) Available(_ context.Context, member repository.Member, _ string) bool {
	return member.Endpoint != "test://unavailable"
}

type GatewayStore interface {
	repository.APTArtifactStore
	repository.Store
	repository.MavenStore
	repository.RawStore
	repository.ConanStore
	repository.HostedRepositoryStore
	repository.AnonymousAccessPolicyStore
	repository.SiteSettingsStore
	repository.ConsoleThemeStore
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
	repository.NativeCargoStore
	repository.CargoProxyStore
	repository.CargoGroupStore
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
	repository.EmailStore
	repository.RepositoryQuotaAlertStore
	repository.BackgroundOperationQueueStore
	repository.ArtifactBrowseStore
	repository.ArtifactUsageStore
}

// NewGatewayHandler validates persistent dependencies before exposing any route.
// Tests and client fixtures must explicitly supply their object stores too.
func NewGatewayHandler(options GatewayOptions) (http.Handler, error) {
	if err := options.validate(); err != nil {
		return nil, err
	}
	options.Storage = maps.Clone(options.Storage)
	dependencies, store := options.Dependencies, options.Store
	adapter, authenticator := options.Adapter, options.Authenticator
	cache, mavenCache := options.Caches.OCI, options.Caches.Maven
	rawCache, conanCache := options.Caches.Raw, options.Caches.Conan
	maintenance, metrics := options.Caches.Maintenance, options.Metrics
	mux := newRuntimeLogMux(dependencies.LogBuffer)
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
	upstreamClient := OCIClient(UpstreamClient{OCIBearerCache: NewOCIBearerTokenCache()})
	if options.Upstream != nil {
		upstreamClient = options.Upstream
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
	publicationScanner := NewPublicationScanScheduler(store, dependencies.ArtifactScanner != nil, dependencies.ArtifactScannerFormats, metrics)
	nativeOCI := newNativeOCIHandler(store, options.Storage[repository.FormatOCI], authenticator).withMetrics(metrics).withProxy(oci).withPublicationScanner(publicationScanner)
	rawLegacy := RawHandler{Store: store, Repositories: store, Authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator}, Authenticator: authenticator, Client: rawClient, Metrics: metrics, Cache: rawCache}
	rawModule := rawformat.New(rawformat.Options{Store: store, Repositories: store, Groups: store, Objects: options.Storage[repository.FormatRaw], Authenticator: authenticator,
		Authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator, LegacyFallback: func(Principal, repository.HostedRepository, RepositoryOperation) AuthorizationDecision {
			return AuthorizationDecision{Allowed: true, Source: "legacy_protocol", Reason: "authenticated"}
		}},
		Audit: store, Anonymous: store, ReadPolicies: store, Quarantine: store, Client: rawClient, Cache: rawCache, Metrics: rawModuleMetrics{metrics}, Scanner: publicationScanner,
		GroupRuntime: rawModuleGroupRuntime{resolver: v2GroupResolver{groups: store, repos: store, audit: store, authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator}}, policy: store},
		LegacyStore:  store, LegacyRuntime: rawRuntime{handler: rawLegacy}, RecordFailure: recordRawFailure})
	formatModules, err := formatmodule.New(rawModule)
	if err != nil {
		return nil, err
	}
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
	nativeObjects := options.Storage[repository.FormatMaven]
	nativeMaven := newNativeMavenHandler(store, nativeObjects, authenticator).withMetrics(metrics).withProxy(mavenClient, mavenCache).withPublicationScanner(publicationScanner)
	mavenProxyOperations := mavenProxyOperationsHandler{store: store, authenticator: authenticator, authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator}, client: mavenClient, cache: mavenCache, maintenance: maintenance}
	proxyCacheBrowse := proxyCacheBrowseHandler{store: store, entriesStore: store, audit: store, maintenance: maintenance, authenticator: authenticator, authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator}}
	nativeConanObjects := options.Storage[repository.FormatConan]
	nativeConanPublish := newNativeConanPublishHandler(store, nativeConanObjects, authenticator).withPublicationScanner(publicationScanner)
	nativeNPMObjects := options.Storage[repository.FormatNPM]
	nativeNPM := newNativeNPMHandler(store, nativeNPMObjects, authenticator).withMetrics(metrics).withProxy(npmClient).withPublicationScanner(publicationScanner)
	if dependencies.NPMMetadataTTL > 0 {
		nativeNPM.metadataTTL = dependencies.NPMMetadataTTL
	}
	if dependencies.NPMNegativeTTL > 0 {
		nativeNPM.negativeTTL = dependencies.NPMNegativeTTL
	}
	nativeNPM.protection = newNPMProxyProtection(dependencies.NPMProxyCoordinator, dependencies.NPMBreakerTTL)
	nativePyPIObjects := options.Storage[repository.FormatPyPI]
	nativePyPI := newNativePyPIHandler(store, nativePyPIObjects, authenticator).withMetrics(metrics).withProxy(pypiClient).withPublicationScanner(publicationScanner)
	nativeGoObjects := options.Storage[repository.FormatGo]
	nativeGo := newNativeGoHandler(store, nativeGoObjects, authenticator).withMetrics(metrics).withProxy(goClient).withPublicationScanner(publicationScanner)
	nativeCargo := newNativeCargoHandler(store, options.Storage[repository.FormatCargo], authenticator).withPublicationScanner(publicationScanner)
	nativeCargo.upstream = UpstreamClient{HTTPClient: dependencies.CargoUpstreamClient}
	nativeAPTObjects := options.Storage[repository.FormatAPT]
	aptClient := APTClient(UpstreamClient{})
	if client, ok := upstreamClient.(APTClient); ok {
		aptClient = client
	}
	nativeAPT := newNativeAPTHandler(store, nativeAPTObjects, authenticator).withProxy(aptClient).withMetrics(metrics)
	aptPublication := aptpublication.NewManager(store, nativeAPTObjects)
	var aptSnapshotImporter *aptpublication.SnapshotArchiveImporter
	if dependencies.APTArchiveTrust != nil {
		aptSnapshotImporter = aptpublication.NewSnapshotArchiveImporter(store, nativeAPTObjects, dependencies.APTArchiveTrust)
	}
	var aptSnapshotPublisher *aptpublication.Publisher
	if dependencies.APTSigner != nil {
		aptSnapshotPublisher = aptpublication.NewPublisher(store, nativeAPTObjects, dependencies.APTSigner).WithMetrics(metrics).WithPublicationScanner(publicationScanner)
	}
	publishRouter := nativePublishRouter{maven: nativeMaven, conan: nativeConanPublish}
	hostedRepositories := hostedRepositoryAPIHandler{store: store, groups: store, authenticator: authenticator}
	var searchProjection repository.ArtifactSearchStore
	if candidate, ok := any(store).(repository.ArtifactSearchStore); ok {
		searchProjection = candidate
	}
	scope := newManagementScope(store, store, store, store, authenticator, metrics)
	repositoriesAPI := newRepositoryManagementHandler(repositoryManagementOptions{Scope: scope, Repositories: store, Groups: store, Capacities: store, Maintenance: maintenance, ArtifactScanner: dependencies.ArtifactScanner, ArtifactScanFormats: dependencies.ArtifactScannerFormats})
	identities, _ := any(store).(repository.ArtifactIdentityStore)
	artifactsAPI := newArtifactManagementHandler(artifactManagementOptions{
		Scope: scope, Repositories: store, Groups: store, Grants: store,
		MavenPublisher: nativeMaven, ConanPublisher: nativeConanPublish, ConanSessions: store,
		Maven: store, Raw: store, NPM: store, PyPI: store, APT: store, OCI: store, Conan: store, Cargo: store, CargoProxy: store,
		Intelligence: store, SearchProjection: searchProjection, Identities: identities, Usage: store, Browse: store,
		ProxyOperations: proxyCacheBrowse, ProxyDirectory: proxyCacheBrowse.directoryStore(), MavenProxyOperations: mavenProxyOperations, MavenCache: mavenCache,
	})
	grantRecords, _ := any(store).(repository.RepositoryGrantRecordStore)
	accessAPI := newAccessManagementHandler(accessManagementOptions{Scope: scope, Grants: store, GrantRecords: grantRecords, Templates: store, Roles: store})
	legacyAPI := generatedRepositoryAPIAdapter{BatchAdapter: migrationapi.New(dependencies.MigrationService, authenticator, store), Adapter: sourceapi.New(dependencies.SourceService, authenticator, store), managementScope: scope, store: store, sessions: nativeMaven, aptPublication: aptPublication, aptSnapshotPublisher: aptSnapshotPublisher, aptSnapshotImporter: aptSnapshotImporter, aptSnapshotExporter: aptpublication.SnapshotArchiveExporter{Store: store, Objects: nativeAPTObjects}, aptPublications: store, retentionPolicies: store, securityPolicies: store, quarantineReadPolicies: store, capacities: store, tombstones: store, intelligence: store, quarantine: store, lifecycleJobs: store, auditRetention: store, anonymousAccess: store, siteSettings: store, consoleThemePackages: store, consoleThemes: dependencies.ConsoleThemes, oidcRuntime: dependencies.OIDCRuntime, replication: store, oci: store, cargo: store, conan: store, apiKeys: store, serviceAccounts: store, users: store, audit: store, maintenance: maintenance, searchProjection: searchProjection, runtimeNodes: store, scheduledTasks: store, webhooks: store, emailTargets: store, quotaAlerts: store, queueStats: store, diagnostics: dependencies, objects: options.Storage, logBuffer: dependencies.LogBuffer, artifactScanner: dependencies.ArtifactScanner, artifactScanFormats: dependencies.ArtifactScannerFormats}
	adminopenapi.HandlerWithOptions(managementServer{repositoryManagementHandler: repositoriesAPI, artifactManagementHandler: artifactsAPI, accessManagementHandler: accessAPI, generatedRepositoryAPIAdapter: legacyAPI}, adminopenapi.StdHTTPServerOptions{
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
	if err := formatModules.Mount(mux); err != nil {
		return nil, err
	}
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
	cargoGroupRouter := v2GroupRouter{format: repository.FormatCargo, groups: store, repos: store, audit: store, auth: authenticator,
		authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator},
		cargo:      &v2GroupCargoHandler{native: &nativeCargo, owners: store}, next: nativeCargo}
	mux.Handle("/cargo/", cargoGroupRouter)
	aptGroupRouter := v2GroupRouter{format: repository.FormatAPT, groups: store, repos: store, audit: store, auth: authenticator,
		authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator},
		apt:        &v2GroupAPTHandler{native: &nativeAPT},
		next:       nativeAPT}
	mux.Handle("/apt/", aptGroupRouter)
	conan := ConanHandler{Store: store, NativeStore: store, Repositories: store, Authorizer: RepositoryAuthorizer{Grants: store, Legacy: authenticator}, Authenticator: authenticator, Client: conanClient, Metrics: metrics, Cache: conanCache, NativeObjects: nativeConanObjects, ReadPolicies: store, Quarantine: store}
	conan.Publications, conan.PublicationScanner = options.ConanPublications, &publicationScanner
	conanGroupRouter := v2GroupRouter{format: repository.FormatConan, groups: store, repos: store, audit: store, auth: authenticator,
		conan: &v2GroupConanHandler{conan: &conan, auth: authenticator},
		next:  conan}
	mux.Handle("/conan/", conanGroupRouter)
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
	return sessionCookieAuthentication(tracedHTTPHandler(dependencies.requestObservability(openAPIServeMux{mux: mux}))), nil
}
