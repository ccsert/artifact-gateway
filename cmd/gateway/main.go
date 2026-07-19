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
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	dependencies := app.NewDependencies(cfg)
	objectStore, err := app.NewS3OCIObjectStore(cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3Bucket)
	if err != nil {
		slog.Error("create OCI object store", "error", err)
		os.Exit(1)
	}
	if err := objectStore.EnsureBucket(context.Background()); err != nil {
		slog.Error("ensure OCI cache bucket", "error", err)
		os.Exit(1)
	}
	store, err := repository.NewPostgresStore(cfg.DatabaseURL)
	if err != nil {
		slog.Error("open repository store", "error", err)
		os.Exit(1)
	}
	defer func() { _ = store.Close() }()
	server := &http.Server{
		Addr: cfg.ListenAddress,
		Handler: app.NewGatewayHandlerWithCaches(dependencies, store, app.TestAdapter{}, app.Authenticator{
			AdminToken:        cfg.AdminToken,
			ResolverToken:     cfg.ResolverToken,
			AdminActor:        cfg.AdminActor,
			ResolverActor:     cfg.ResolverActor,
			RepositoryReaders: cfg.RepositoryReaders,
		}, app.NewDefaultOCICache(objectStore, cfg.OCIProxyAllowedHosts).WithCoordinator(app.NewRedisOCICacheCoordinator(cfg.RedisAddress)), app.NewDefaultMavenCache(objectStore, cfg.MavenProxyAllowedHosts), app.GiteaClient{Username: cfg.GiteaUsername, Token: cfg.GiteaToken}),
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
	case <-signalContext().Done():
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
