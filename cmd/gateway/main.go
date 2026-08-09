package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/app"
	"github.com/artifact-gateway/artifact-gateway/internal/config"
	"github.com/artifact-gateway/artifact-gateway/internal/database"
	"github.com/artifact-gateway/artifact-gateway/internal/evidence"
	"github.com/artifact-gateway/artifact-gateway/internal/preflight"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "preflight" {
		os.Exit(preflight.RunCLI(context.Background(), os.Args[2:], os.Stdout, os.Stderr))
	}
	if len(os.Args) > 1 && os.Args[1] == "evidence" {
		os.Exit(evidence.RunCLI(context.Background(), os.Args[2:], os.Stdout, os.Stderr))
	}
	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	dependencies := app.NewDependencies(cfg)
	shutdownTracing, err := app.NewTracing(context.Background(), cfg)
	if err != nil {
		slog.Error("initialize tracing", "error", err)
		os.Exit(1)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracing(ctx); err != nil {
			slog.Error("shutdown tracing", "error", err)
		}
	}()
	objectStore, err := app.NewS3OCIObjectStore(cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3Bucket)
	if err != nil {
		slog.Error("create OCI object store", "error", err)
		os.Exit(1)
	}
	if err := objectStore.EnsureBucket(context.Background()); err != nil {
		slog.Error("ensure OCI cache bucket", "error", err)
		os.Exit(1)
	}
	dependencies.NativeMavenObjectStore = objectStore
	dependencies.NativeOCIObjectStore = objectStore
	dependencies.NativeConanObjectStore = objectStore
	dependencies.NativeNPMObjectStore = objectStore
	dependencies.NativePyPIObjectStore = objectStore
	dependencies.NativeGoObjectStore = objectStore
	databasePool, err := database.OpenPostgres(cfg.DatabaseURL, cfg.DatabasePool)
	if err != nil {
		slog.Error("open PostgreSQL connection pool", "error", err)
		os.Exit(1)
	}
	defer func() { _ = databasePool.Close() }()
	dependencies = dependencies.WithDatabasePool(databasePool)
	notificationPool, err := database.OpenPostgres(cfg.DatabaseURL, database.NotificationPoolConfig())
	if err != nil {
		slog.Error("open PostgreSQL notification pool", "error", err)
		os.Exit(1)
	}
	defer func() { _ = notificationPool.Close() }()
	store, err := repository.NewPostgresStoreWithPools(databasePool, notificationPool)
	if err != nil {
		slog.Error("open repository store", "error", err)
		os.Exit(1)
	}
	defer func() { _ = store.Close() }()
	oidcRuntime := app.NewOIDCRuntime(store, app.OIDCRuntimeConfig{
		Enabled: cfg.OIDCIssuer != "" && cfg.OIDCAudience != "", Issuer: cfg.OIDCIssuer,
		Audience: cfg.OIDCAudience, JWKSURL: cfg.OIDCJWKSURL, ClientID: cfg.OIDCClientID,
		ClientSecret: cfg.OIDCClientSecret, RedirectURL: cfg.OIDCRedirectURL, Scopes: cfg.OIDCScopes,
		AdminSubjects: cfg.OIDCAdminSubjects, Roles: cfg.OIDCRoles,
	})
	dependencies.OIDCRuntime = oidcRuntime
	coordinatorPool, err := database.OpenPostgres(cfg.DatabaseURL, cfg.DatabaseCoordinatorPool)
	if err != nil {
		slog.Error("open PostgreSQL cache coordinator pool", "error", err)
		os.Exit(1)
	}
	defer func() { _ = coordinatorPool.Close() }()
	coordinator, err := app.NewPostgresCacheCoordinatorWithPools(coordinatorPool, databasePool)
	if err != nil {
		slog.Error("open PostgreSQL cache coordinator", "error", err)
		os.Exit(1)
	}
	defer func() { _ = coordinator.Close() }()
	dependencies.NPMMetadataTTL = cfg.NPMMetadataCacheTTL
	dependencies.NPMNegativeTTL = cfg.NPMNegativeCacheTTL
	dependencies.NPMBreakerTTL = cfg.NPMProxyBreakerTTL
	dependencies.NPMProxyCoordinator = coordinator
	cacheStore, err := app.NewPostgresCacheControlStoreWithDB(objectStore, databasePool)
	if err != nil {
		slog.Error("open PostgreSQL cache control store", "error", err)
		os.Exit(1)
	}
	defer func() { _ = cacheStore.Close() }()
	taskQueue, err := app.NewPostgresCacheTaskQueueWithPools(databasePool, notificationPool)
	if err != nil {
		slog.Error("open PostgreSQL cache task queue", "error", err)
		os.Exit(1)
	}
	defer func() { _ = taskQueue.Close() }()
	quota := app.NewCacheQuota(cacheStore, cfg.RepositoryCacheQuotas).WithCoordinator(coordinator)
	ociCache := app.NewDefaultOCICache(cacheStore, cfg.OCIProxyAllowedHosts).WithCoordinator(coordinator).WithQuota(quota).WithTTL(cfg.OCICacheTTL)
	rawCache := app.NewDefaultRawCache(cacheStore, cfg.RawProxyAllowedHosts).WithCoordinator(coordinator).WithQuota(quota).WithMaxObjectBytes(cfg.RawCacheMaxObjectBytes).WithTTL(cfg.RawCacheTTL)
	conanCache := app.NewDefaultConanCache(cacheStore, nil).WithCoordinator(coordinator).WithQuota(quota).WithMaxObjectBytes(cfg.ConanCacheMaxObjectBytes).WithTTL(cfg.ConanCacheTTL)
	maintenance := app.NewCacheMaintenanceWithRaw(cacheStore, ociCache, rawCache).WithConan(conanCache)
	metrics := (&app.Metrics{}).
		WithDatabaseStats(databasePool.Stats).
		WithDatabasePoolStats("coordinator", coordinatorPool.Stats).
		WithDatabasePoolStats("notifications", notificationPool.Stats).
		WithNodeIdentity(cfg.InstanceID, nodeRoleStrings(cfg.NodeRoles))
	runtimeContext := signalContext()
	startAPI := cfg.HasRole(config.NodeRoleAPI)
	slog.Info("gateway runtime configured", "instance_id", cfg.InstanceID, "roles", cfg.NodeRoles, "worker_formats", cfg.WorkerFormats, "worker_kinds", cfg.WorkerKinds)
	heartbeat := &app.RuntimeNodeHeartbeat{
		Store: store,
		Node: repository.RuntimeNode{
			InstanceID:    cfg.InstanceID,
			SessionID:     uuid.NewString(),
			Roles:         nodeRoleStrings(cfg.NodeRoles),
			WorkerFormats: append([]string(nil), cfg.WorkerFormats...),
			WorkerKinds:   append([]string(nil), cfg.WorkerKinds...),
		},
		Retention:     cfg.RuntimeNodeRetention,
		PruneInterval: cfg.RuntimeNodePruneInterval,
	}
	heartbeatDone := heartbeat.Start(runtimeContext, 10*time.Second)
	(backgroundRuntime{store: store, objects: objectStore, taskQueue: taskQueue, maintenance: maintenance, metrics: metrics}).Start(runtimeContext, cfg)
	handler := http.Handler(app.NewOperationalHandler(dependencies, metrics))
	if startAPI {
		handler = app.NewGatewayHandlerWithFormatCachesAndMetrics(dependencies, store, app.TestAdapter{}, app.Authenticator{
			AdminToken:        cfg.AdminToken,
			ResolverToken:     cfg.ResolverToken,
			AdminActor:        cfg.AdminActor,
			ResolverActor:     cfg.ResolverActor,
			RepositoryReaders: cfg.RepositoryReaders,
			OIDCSource:        oidcRuntime,
			APIKeys:           store,
		}, ociCache, app.NewDefaultMavenCache(cacheStore, cfg.MavenProxyAllowedHosts).WithCoordinator(coordinator).WithQuota(quota).WithTTLs(cfg.MavenCacheTTL, cfg.MavenMetadataCacheTTL, cfg.MavenNegativeCacheTTL), rawCache, conanCache, maintenance, metrics, app.UpstreamClient{})
	}
	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           metrics.Instrument(handler),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	errs := make(chan error, 1)
	go func() { errs <- server.ListenAndServe() }()

	select {
	case err := <-errs:
		if !errors.Is(err, http.ErrServerClosed) {
			slog.Error("gateway stopped", "error", err)
			os.Exit(1)
		}
	case <-runtimeContext.Done():
		select {
		case <-heartbeatDone:
		case <-time.After(time.Second):
			slog.Warn("runtime node heartbeat did not stop before shutdown")
		}
		heartbeatContext, heartbeatCancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := heartbeat.Stop(heartbeatContext); err != nil {
			slog.Warn("mark runtime node stopped", "error", err)
		}
		heartbeatCancel()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			slog.Error("graceful shutdown failed", "error", err)
		}
	}
}

func signalContext() context.Context {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() { <-ctx.Done(); stop() }()
	return ctx
}

func nodeRoleStrings(roles []config.NodeRole) []string {
	values := make([]string, 0, len(roles))
	for _, role := range roles {
		values = append(values, string(role))
	}
	return values
}
