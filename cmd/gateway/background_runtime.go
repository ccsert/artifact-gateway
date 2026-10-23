package main

import (
	"context"
	"time"

	"github.com/ccsert/artifact-gateway/internal/app"
	"github.com/ccsert/artifact-gateway/internal/aptpublication"
	"github.com/ccsert/artifact-gateway/internal/config"
	"github.com/ccsert/artifact-gateway/internal/emailnotification"
	formatmodule "github.com/ccsert/artifact-gateway/internal/format"
	helmformat "github.com/ccsert/artifact-gateway/internal/format/helm"
	nugetformat "github.com/ccsert/artifact-gateway/internal/format/nuget"
	rawformat "github.com/ccsert/artifact-gateway/internal/format/raw"
	rubygemsformat "github.com/ccsert/artifact-gateway/internal/format/rubygems"
	helmdist "github.com/ccsert/artifact-gateway/internal/helmdistribution"
	"github.com/ccsert/artifact-gateway/internal/nexusmigration"
	dist "github.com/ccsert/artifact-gateway/internal/nugetdistribution"
	conanprotocol "github.com/ccsert/artifact-gateway/internal/protocol/conan"
	npmprotocol "github.com/ccsert/artifact-gateway/internal/protocol/npm"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/scanning"
)

type backgroundRuntime struct {
	migration           *nexusmigration.Service
	migrationSchedules  *nexusmigration.ScheduleService
	store               *repository.PostgresStore
	objects             app.ObjectStore
	taskQueue           *app.PostgresCacheTaskQueue
	maintenance         *app.CacheMaintenance
	metrics             *app.Metrics
	scanner             scanning.Scanner
	scanResolver        app.ArtifactScanResolver
	scannerFormats      []repository.Format
	workerSessionID     string
	aptPublisher        *aptpublication.Publisher
	helmMaintenance     *helmformat.Maintenance
	helmReplication     *helmdist.Worker
	nugetMaintenance    *nugetformat.Maintenance
	nugetReplication    *dist.Worker
	rubyGemsMaintenance *rubygemsformat.Maintenance
}

func (r backgroundRuntime) Start(ctx context.Context, cfg config.Config) {
	if cfg.HasRole(config.NodeRoleScheduler) {
		r.startSchedulers(ctx, cfg, app.NativeRepositoryRetention{Store: r.store, Metrics: r.metrics})
	}
	if cfg.HasRole(config.NodeRoleWorker) {
		r.startWorkers(ctx, cfg, app.NativeRepositoryRetention{Store: r.store, Metrics: r.metrics, WorkerFormats: cfg.WorkerFormats})
		if r.helmMaintenance != nil && cfg.WorkerEnabled("helm", "retention") {
			r.helmMaintenance.Start(ctx, time.Minute)
		}
		if r.helmReplication != nil && cfg.HelmPreviewEnabled && cfg.WorkerEnabled("helm", "replication") {
			r.helmReplication.Start(ctx, 5*time.Second)
		}
		if r.nugetReplication != nil && cfg.WorkerEnabled("nuget", "replication") {
			r.nugetReplication.Start(ctx, 5*time.Second)
		}
		if r.nugetMaintenance != nil && cfg.WorkerEnabled("nuget", "retention") {
			// This preview only reclaims expired, never-visible uploads. It
			// does not advertise NuGet lifecycle/retention capability.
			r.nugetMaintenance.Start(ctx, time.Minute)
		}
		if r.rubyGemsMaintenance != nil && cfg.RubyGemsPreviewEnabled && cfg.WorkerEnabled("rubygems", "retention") {
			r.rubyGemsMaintenance.Start(ctx, time.Minute)
		}
	}
	app.BackgroundOperationQueueObserver{Store: r.store, Metrics: r.metrics}.Start(ctx, time.Minute)
}

func (r backgroundRuntime) startSchedulers(ctx context.Context, cfg config.Config, retention app.NativeRepositoryRetention) {
	if r.migrationSchedules != nil {
		r.migrationSchedules.StartScheduler(ctx, 5*time.Second)
	}
	app.QuotaAlertEvaluator{Store: r.store, Mail: repository.QuotaAlertMailConfig{Enabled: cfg.Email.Enabled, From: cfg.Email.From, ConsoleOrigin: cfg.Email.ConsoleOrigin}}.Start(ctx, 15*time.Second)
	app.ScheduledTaskScheduler{Store: r.store}.Start(ctx, time.Minute)
	app.UserSessionJanitor{Store: r.store}.Start(ctx, time.Hour)
	r.taskQueue.StartCacheScheduler(ctx, 5*time.Minute)
	app.NativeMavenMaintenance{Store: r.store, Objects: r.objects, Metrics: r.metrics}.StartScheduler(ctx, time.Hour)
	app.NativeOCIMaintenance{Store: r.store, Objects: r.objects, Metrics: r.metrics}.StartScheduler(ctx, time.Hour)
	modules := r.formatModules()
	if background, ok := formatmodule.Capability[formatmodule.Background](modules, repository.FormatRaw); ok {
		background.StartScheduler(ctx, time.Hour)
	}
	app.NativeConanMaintenance{Store: r.store, Collections: r.store, Objects: r.objects, Metrics: r.metrics}.StartScheduler(ctx, time.Hour)
	app.NativeNPMMaintenance{Store: r.store, Objects: r.objects, Metrics: r.metrics}.StartScheduler(ctx, time.Hour)
	app.NativePyPIMaintenance{Store: r.store, Objects: r.objects, Metrics: r.metrics}.StartScheduler(ctx, time.Hour)
	app.NativeGoMaintenance{Store: r.store, Objects: r.objects, Metrics: r.metrics}.StartScheduler(ctx, time.Hour)
	app.NativeCargoMaintenance{Store: r.store, Objects: r.objects, Metrics: r.metrics}.StartScheduler(ctx, time.Hour)
	aptpublication.Maintenance{Store: r.store, Objects: r.objects, Metrics: r.metrics}.StartScheduler(ctx, time.Hour)
	retention.StartScheduler(ctx, time.Hour)
}

func (r backgroundRuntime) startWorkers(ctx context.Context, cfg config.Config, retention app.NativeRepositoryRetention) {
	if r.migration != nil && cfg.WorkerKindEnabled("migration") {
		r.migration.StartWorker(ctx, 5*time.Second, migrationWorkerFormats(cfg))
	}
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
	if cfg.WorkerKindEnabled("email") {
		app.EmailNotificationWorker{Store: r.store, Sender: emailnotification.Sender{Config: cfg.Email}, InstanceID: cfg.InstanceID + "/" + r.workerSessionID}.Start(ctx, 5*time.Second)
	}
	r.startMavenWorkers(ctx, cfg)
	r.startOCIWorkers(ctx, cfg)
	r.startRawWorkers(ctx, cfg)
	r.startConanWorkers(ctx, cfg)
	r.startNPMWorkers(ctx, cfg)
	r.startPyPIWorkers(ctx, cfg)
	r.startGoWorkers(ctx, cfg)
	if cfg.WorkerEnabled("cargo", "reclaim") {
		app.NativeCargoMaintenance{Store: r.store, Objects: r.objects, Metrics: r.metrics}.StartWorker(ctx, time.Minute)
	}
	if cfg.WorkerEnabled("cargo", "promotion") {
		app.NativeCargoPromotion{Store: r.store, Objects: r.objects, Intelligence: r.store, Metrics: r.metrics}.Start(ctx, time.Minute)
	}
	if cfg.WorkerEnabled("cargo", "replication") {
		app.CargoReplication{Store: r.store, Source: r.objects, Destination: r.objects, Metrics: r.metrics}.Start(ctx, time.Minute)
	}
	r.startAPTWorkers(ctx, cfg)
}

func (r backgroundRuntime) startGoWorkers(ctx context.Context, cfg config.Config) {
	if goReclaimWorkerEnabled(cfg) {
		app.NativeGoMaintenance{Store: r.store, Objects: r.objects, Metrics: r.metrics}.StartWorker(ctx, time.Minute)
	}
	if cfg.WorkerEnabled("go", "promotion") {
		app.NativeGoPromotion{Store: r.store, Intelligence: r.store, Metrics: r.metrics}.Start(ctx, time.Minute)
	}
	if cfg.WorkerEnabled("go", "replication") {
		app.GoReplication{Store: r.store, Source: r.objects, Destination: r.objects, Metrics: r.metrics}.Start(ctx, time.Minute)
	}
}

func goReclaimWorkerEnabled(cfg config.Config) bool {
	return cfg.WorkerEnabled("go", "reclaim")
}

func (r backgroundRuntime) startAPTWorkers(ctx context.Context, cfg config.Config) {
	if r.aptPublisher != nil {
		d := aptpublication.Distribution{Store: r.store, Source: r.objects, Destination: r.objects, Publisher: r.aptPublisher, Intelligence: r.store, Metrics: r.metrics}
		if cfg.WorkerEnabled("apt", "promotion") {
			d.StartPromotion(ctx, time.Minute)
		}
		if cfg.WorkerEnabled("apt", "replication") {
			d.StartReplication(ctx, time.Minute)
		}
	}
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
	background, ok := formatmodule.Capability[formatmodule.Background](r.formatModules(), repository.FormatRaw)
	if !ok {
		return
	}
	if !cfg.WorkerFormatEnabled("raw") {
		return
	}
	if cfg.WorkerKindEnabled("reclaim") {
		background.StartReclaimWorker(ctx, time.Minute)
	}
	if cfg.WorkerKindEnabled("promotion") {
		background.StartPromotionWorker(ctx, time.Minute)
	}
	if cfg.WorkerKindEnabled("replication") {
		background.StartReplicationWorker(ctx, time.Minute)
	}
}

func (r backgroundRuntime) startConanWorkers(ctx context.Context, cfg config.Config) {
	if !cfg.WorkerFormatEnabled("conan") {
		return
	}
	if cfg.WorkerKindEnabled("reclaim") {
		app.NativeConanMaintenance{Store: r.store, Collections: r.store, Objects: r.objects, Metrics: r.metrics}.StartWorker(ctx, time.Minute)
	}
	if cfg.WorkerKindEnabled("promotion") {
		conanprotocol.NativePromotion{Store: r.store, Intelligence: r.store, Metrics: r.metrics}.Start(ctx, time.Minute)
	}
	if cfg.WorkerKindEnabled("replication") {
		app.ConanReplication{Store: r.store, Source: r.objects, Destination: r.objects, Metrics: r.metrics}.Start(ctx, time.Minute)
	}
}

func migrationWorkerFormats(cfg config.Config) []string {
	formats := []string{}
	for _, format := range []string{"maven", "raw", "oci", "conan", "npm"} {
		if cfg.WorkerFormatEnabled(format) {
			formats = append(formats, format)
		}
	}
	return formats
}

func (r backgroundRuntime) formatModules() *formatmodule.Registry {
	modules, err := formatmodule.New(rawformat.New(rawformat.Options{Store: r.store, Jobs: r.store, Replications: r.store, Intelligence: r.store, Objects: r.objects, BackgroundMetrics: r.metrics}))
	if err != nil {
		panic(err)
	} // fixed built-in module registration, independent of deployment input
	return modules
}
