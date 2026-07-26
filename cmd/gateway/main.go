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
	"github.com/artifact-gateway/artifact-gateway/internal/evidence"
	rawmaintenance "github.com/artifact-gateway/artifact-gateway/internal/maintenance/raw"
	"github.com/artifact-gateway/artifact-gateway/internal/preflight"
	conanprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/conan"
	rawprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/raw"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
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
	store, err := repository.NewPostgresStore(cfg.DatabaseURL)
	if err != nil {
		slog.Error("open repository store", "error", err)
		os.Exit(1)
	}
	defer func() { _ = store.Close() }()
	coordinator, err := app.NewPostgresCacheCoordinator(cfg.DatabaseURL)
	if err != nil {
		slog.Error("open PostgreSQL cache coordinator", "error", err)
		os.Exit(1)
	}
	defer func() { _ = coordinator.Close() }()
	cacheStore, err := app.NewPostgresCacheControlStore(objectStore, cfg.DatabaseURL)
	if err != nil {
		slog.Error("open PostgreSQL cache control store", "error", err)
		os.Exit(1)
	}
	defer func() { _ = cacheStore.Close() }()
	taskQueue, err := app.NewPostgresCacheTaskQueue(cfg.DatabaseURL)
	if err != nil {
		slog.Error("open PostgreSQL cache task queue", "error", err)
		os.Exit(1)
	}
	defer func() { _ = taskQueue.Close() }()
	quota := app.NewCacheQuota(cacheStore, cfg.RepositoryCacheQuotas).WithCoordinator(coordinator)
	ociCache := app.NewDefaultOCICache(cacheStore, cfg.OCIProxyAllowedHosts).WithCoordinator(coordinator).WithQuota(quota)
	rawCache := app.NewDefaultRawCache(cacheStore, cfg.RawProxyAllowedHosts).WithCoordinator(coordinator).WithQuota(quota).WithMaxObjectBytes(cfg.RawCacheMaxObjectBytes)
	conanCache := app.NewDefaultConanCache(cacheStore, nil).WithCoordinator(coordinator).WithQuota(quota).WithMaxObjectBytes(cfg.ConanCacheMaxObjectBytes)
	maintenance := app.NewCacheMaintenanceWithRaw(cacheStore, ociCache, rawCache).WithConan(conanCache)
	runtimeContext := signalContext()
	taskQueue.StartCacheCollection(runtimeContext, 5*time.Minute, maintenance.Run)
	app.NativeMavenMaintenance{Store: store, Objects: objectStore}.Start(runtimeContext, time.Hour)
	app.NativeMavenRetention{Store: store}.Start(runtimeContext, time.Hour)
	app.NativeMavenPromotion{Store: store}.Start(runtimeContext, time.Minute)
	app.NativeOCIMaintenance{Store: store, Objects: objectStore}.Start(runtimeContext, time.Hour)
	app.NativeOCIPromotion{Store: store, Objects: objectStore}.Start(runtimeContext, time.Minute)
	rawprotocol.NativePromotion{Store: store}.Start(runtimeContext, time.Minute)
	rawmaintenance.Collector{Store: store, Objects: objectStore}.Start(runtimeContext, time.Hour)
	app.NativeConanMaintenance{Store: store, Objects: objectStore}.Start(runtimeContext, time.Hour)
	conanprotocol.NativePromotion{Store: store}.Start(runtimeContext, time.Minute)
	server := &http.Server{
		Addr: cfg.ListenAddress,
		Handler: app.NewGatewayHandlerWithFormatCaches(dependencies, store, app.TestAdapter{}, app.Authenticator{
			AdminToken:        cfg.AdminToken,
			ResolverToken:     cfg.ResolverToken,
			AdminActor:        cfg.AdminActor,
			ResolverActor:     cfg.ResolverActor,
			RepositoryReaders: cfg.RepositoryReaders,
			OIDC: app.NewOIDCValidator(app.OIDCConfig{
				Issuer:        cfg.OIDCIssuer,
				Audience:      cfg.OIDCAudience,
				JWKSURL:       cfg.OIDCJWKSURL,
				AdminSubjects: cfg.OIDCAdminSubjects,
			}),
		}, ociCache, app.NewDefaultMavenCache(cacheStore, cfg.MavenProxyAllowedHosts).WithCoordinator(coordinator).WithQuota(quota), rawCache, conanCache, maintenance, app.UpstreamClient{}),
		ReadHeaderTimeout: 5 * time.Second,
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
