package main

import (
	"context"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/app"
	"github.com/artifact-gateway/artifact-gateway/internal/aptpublication"
	"github.com/artifact-gateway/artifact-gateway/internal/config"
	rawmaintenance "github.com/artifact-gateway/artifact-gateway/internal/maintenance/raw"
	conanprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/conan"
	npmprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/npm"
	rawprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/raw"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/scanning"
)

type backgroundRuntime struct {
	store           *repository.PostgresStore
	objects         app.OCIObjectStore
	taskQueue       *app.PostgresCacheTaskQueue
	maintenance     *app.CacheMaintenance
	metrics         *app.Metrics
	scanner         scanning.Scanner
	scanResolver    app.ArtifactScanResolver
	scannerFormats  []repository.Format
	workerSessionID string
}

func (r backgroundRuntime) Start(ctx context.Context, cfg config.Config) {
	if cfg.HasRole(config.NodeRoleScheduler) {
		r.startSchedulers(ctx, app.NativeRepositoryRetention{Store: r.store, Metrics: r.metrics})
	}
	if cfg.HasRole(config.NodeRoleWorker) {
		r.startWorkers(ctx, cfg, app.NativeRepositoryRetention{Store: r.store, Metrics: r.metrics, WorkerFormats: cfg.WorkerFormats})
	}
	app.BackgroundOperationQueueObserver{Store: r.store, Metrics: r.metrics}.Start(ctx, time.Minute)
}

func (r backgroundRuntime) startSchedulers(ctx context.Context, retention app.NativeRepositoryRetention) {
	app.ScheduledTaskScheduler{Store: r.store}.Start(ctx, time.Minute)
	app.UserSessionJanitor{Store: r.store}.Start(ctx, time.Hour)
	r.taskQueue.StartCacheScheduler(ctx, 5*time.Minute)
	app.NativeMavenMaintenance{Store: r.store, Objects: r.objects, Metrics: r.metrics}.StartScheduler(ctx, time.Hour)
	app.NativeOCIMaintenance{Store: r.store, Objects: r.objects, Metrics: r.metrics}.StartScheduler(ctx, time.Hour)
	rawmaintenance.Collector{Store: r.store, Objects: r.objects, Metrics: r.metrics}.StartScheduler(ctx, time.Hour)
	app.NativeConanMaintenance{Store: r.store, Objects: r.objects, Metrics: r.metrics}.StartScheduler(ctx, time.Hour)
	app.NativeNPMMaintenance{Store: r.store, Objects: r.objects, Metrics: r.metrics}.StartScheduler(ctx, time.Hour)
	app.NativePyPIMaintenance{Store: r.store, Objects: r.objects, Metrics: r.metrics}.StartScheduler(ctx, time.Hour)
	aptpublication.Maintenance{Store: r.store, Objects: r.objects, Metrics: r.metrics}.StartScheduler(ctx, time.Hour)
	retention.StartScheduler(ctx, time.Hour)
}

func (r backgroundRuntime) startWorkers(ctx context.Context, cfg config.Config, retention app.NativeRepositoryRetention) {
	if cfg.WorkerKindEnabled("recovery") {
		app.LifecycleJobRecovery{Store: r.store}.Start(ctx, time.Minute)
	}
	if cfg.WorkerKindEnabled("deletion") {
		app.RepositoryDeletionWorker{Store: r.store}.Start(ctx, time.Minute)
	}
	if cfg.WorkerKindEnabled("cache") {
		r.taskQueue.StartCacheWorker(ctx, cfg.WorkerFormats, r.maintenance.RunFormat)
	}
	if cfg.WorkerKindEnabled("retention") {
		retention.StartWorker(ctx, time.Minute)
	}
	if cfg.WorkerKindEnabled("audit") {
		app.AuditRetentionWorker{Store: r.store, Metrics: r.metrics}.Start(ctx, time.Hour)
	}
	if cfg.WorkerKindEnabled("intelligence") {
		app.ArtifactIntelligenceCopyWorker{Store: r.store, WorkerFormats: cfg.WorkerFormats, Metrics: r.metrics}.Start(ctx, time.Minute)
	}
	if cfg.WorkerKindEnabled("scan") && r.scanner != nil && r.scanResolver != nil {
		app.ArtifactScanWorker{Store: r.store, Scanner: r.scanner, Resolver: r.scanResolver, WorkerFormats: r.scannerFormats, Metrics: r.metrics}.Start(ctx, time.Minute)
	}
	if cfg.WorkerKindEnabled("webhook") {
		owner := cfg.InstanceID + "/" + r.workerSessionID
		app.WebhookDeliveryWorker{Store: r.store, InstanceID: owner}.Start(ctx, 5*time.Second)
	}
	r.startMavenWorkers(ctx, cfg)
	r.startOCIWorkers(ctx, cfg)
	r.startRawWorkers(ctx, cfg)
	r.startConanWorkers(ctx, cfg)
	r.startNPMWorkers(ctx, cfg)
	r.startPyPIWorkers(ctx, cfg)
	r.startAPTWorkers(ctx, cfg)
}

func (r backgroundRuntime) startAPTWorkers(ctx context.Context, cfg config.Config) {
	if aptReclaimWorkerEnabled(cfg) {
		aptpublication.Maintenance{Store: r.store, Objects: r.objects, Metrics: r.metrics}.StartWorker(ctx, time.Minute)
	}
}

func aptReclaimWorkerEnabled(cfg config.Config) bool {
	return cfg.WorkerEnabled("apt", "reclaim")
}

func (r backgroundRuntime) startPyPIWorkers(ctx context.Context, cfg config.Config) {
	if !cfg.WorkerFormatEnabled("pypi") {
		return
	}
	if cfg.WorkerKindEnabled("reclaim") {
		app.NativePyPIMaintenance{Store: r.store, Objects: r.objects, Metrics: r.metrics}.StartWorker(ctx, time.Minute)
	}
	if cfg.WorkerKindEnabled("promotion") {
		app.NativePyPIPromotion{Store: r.store, Intelligence: r.store, Metrics: r.metrics}.Start(ctx, time.Minute)
	}
	if cfg.WorkerKindEnabled("replication") {
		app.PyPIReplication{Store: r.store, Source: r.objects, Destination: r.objects, Metrics: r.metrics}.Start(ctx, time.Minute)
	}
}

func (r backgroundRuntime) startNPMWorkers(ctx context.Context, cfg config.Config) {
	if !cfg.WorkerFormatEnabled("npm") {
		return
	}
	if cfg.WorkerKindEnabled("reclaim") {
		app.NativeNPMMaintenance{Store: r.store, Objects: r.objects, Metrics: r.metrics}.StartWorker(ctx, time.Minute)
	}
	if cfg.WorkerKindEnabled("promotion") {
		npmprotocol.NativePromotion{Store: r.store, Intelligence: r.store, Metrics: r.metrics}.Start(ctx, time.Minute)
	}
	if cfg.WorkerKindEnabled("replication") {
		app.NPMReplication{Store: r.store, Source: r.objects, Destination: r.objects, Metrics: r.metrics}.Start(ctx, time.Minute)
	}
}

func (r backgroundRuntime) startMavenWorkers(ctx context.Context, cfg config.Config) {
	if !cfg.WorkerFormatEnabled("maven") {
		return
	}
	if cfg.WorkerKindEnabled("reclaim") {
		app.NativeMavenMaintenance{Store: r.store, Objects: r.objects, Metrics: r.metrics}.StartWorker(ctx, time.Minute)
	}
	if cfg.WorkerKindEnabled("promotion") {
		app.NativeMavenPromotion{Store: r.store, Intelligence: r.store, Metrics: r.metrics}.Start(ctx, time.Minute)
	}
	if cfg.WorkerKindEnabled("replication") {
		app.MavenReplication{Store: r.store, Source: r.objects, Destination: r.objects, Metrics: r.metrics}.Start(ctx, time.Minute)
	}
}

func (r backgroundRuntime) startOCIWorkers(ctx context.Context, cfg config.Config) {
	if !cfg.WorkerFormatEnabled("oci") {
		return
	}
	if cfg.WorkerKindEnabled("reclaim") {
		app.NativeOCIMaintenance{Store: r.store, Objects: r.objects, Metrics: r.metrics}.StartWorker(ctx, time.Minute)
	}
	if cfg.WorkerKindEnabled("promotion") {
		app.NativeOCIPromotion{Store: r.store, Objects: r.objects, Intelligence: r.store, Metrics: r.metrics}.Start(ctx, time.Minute)
	}
	if cfg.WorkerKindEnabled("replication") {
		app.OCIReplication{Store: r.store, Source: r.objects, Destination: r.objects, Metrics: r.metrics}.Start(ctx, time.Minute)
	}
}

func (r backgroundRuntime) startRawWorkers(ctx context.Context, cfg config.Config) {
	if !cfg.WorkerFormatEnabled("raw") {
		return
	}
	if cfg.WorkerKindEnabled("reclaim") {
		rawmaintenance.Collector{Store: r.store, Objects: r.objects, Metrics: r.metrics}.StartWorker(ctx, time.Minute)
	}
	if cfg.WorkerKindEnabled("promotion") {
		rawprotocol.NativePromotion{Store: r.store, Intelligence: r.store, Metrics: r.metrics}.Start(ctx, time.Minute)
	}
	if cfg.WorkerKindEnabled("replication") {
		app.RawReplication{Store: r.store, Source: r.objects, Destination: r.objects, Metrics: r.metrics}.Start(ctx, time.Minute)
	}
}

func (r backgroundRuntime) startConanWorkers(ctx context.Context, cfg config.Config) {
	if !cfg.WorkerFormatEnabled("conan") {
		return
	}
	if cfg.WorkerKindEnabled("reclaim") {
		app.NativeConanMaintenance{Store: r.store, Objects: r.objects, Metrics: r.metrics}.StartWorker(ctx, time.Minute)
	}
	if cfg.WorkerKindEnabled("promotion") {
		conanprotocol.NativePromotion{Store: r.store, Intelligence: r.store, Metrics: r.metrics}.Start(ctx, time.Minute)
	}
	if cfg.WorkerKindEnabled("replication") {
		app.ConanReplication{Store: r.store, Source: r.objects, Destination: r.objects, Metrics: r.metrics}.Start(ctx, time.Minute)
	}
}
