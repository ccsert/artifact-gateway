package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/scanning"
)

func (h generatedRepositoryAPIAdapter) GetDiagnostics(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	nodes, err := h.runtimeNodes.ListRuntimeNodes(r.Context())
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list runtime nodes failed")
		return
	}
	queueStats, err := h.queueStats.BackgroundOperationQueueStats(r.Context())
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "read queue diagnostics failed")
		return
	}
	now := time.Now().UTC()
	responseNodes := runtimeNodeResponses(nodes, now)
	workerFormats := make([]adminopenapi.Format, 0, len(h.diagnostics.Runtime.WorkerFormats))
	for _, format := range h.diagnostics.Runtime.WorkerFormats {
		workerFormats = append(workerFormats, adminopenapi.Format(format))
	}
	queues := make([]adminopenapi.DiagnosticQueueStat, 0, len(queueStats))
	for _, stat := range queueStats {
		item := adminopenapi.DiagnosticQueueStat{
			Kind: adminopenapi.DiagnosticQueueStatKind(stat.Kind), Format: adminopenapi.Format(stat.Format),
			State: adminopenapi.DiagnosticQueueStatState(stat.State), Count: int(stat.Count),
		}
		if !stat.OldestCreatedAt.IsZero() {
			oldest := stat.OldestCreatedAt
			item.OldestCreatedAt = &oldest
		}
		queues = append(queues, item)
	}
	scanner := diagnosticScanner(
		r.Context(), h.diagnostics.ArtifactScanner, h.diagnostics.ArtifactScannerName,
		h.diagnostics.ArtifactScannerFormats, h.diagnostics.ArtifactScannerHealthTimeout,
		h.diagnostics.ArtifactScannerDatabaseMaxAge, now,
	)
	writeNativeMavenJSON(w, http.StatusOK, adminopenapi.Diagnostics{
		GeneratedAt: now,
		Build: adminopenapi.DiagnosticBuild{
			Version: h.diagnostics.BuildVersion, Revision: h.diagnostics.BuildRevision,
			Modified: &h.diagnostics.BuildModified, GoVersion: h.diagnostics.BuildGoVersion,
		},
		Runtime: adminopenapi.DiagnosticRuntime{
			InstanceId:    h.diagnostics.Runtime.InstanceID,
			Roles:         append([]string(nil), h.diagnostics.Runtime.Roles...),
			WorkerFormats: workerFormats,
			WorkerKinds:   append([]string(nil), h.diagnostics.Runtime.WorkerKinds...),
		},
		Dependencies: diagnosticDependencies(r.Context(), h.diagnostics.checkers),
		Scanner:      &scanner,
		Queues:       queues,
		Nodes:        runtimeNodeHealth(responseNodes),
	})
}

const defaultScannerDatabaseMaxAge = 24 * time.Hour

func diagnosticScanner(ctx context.Context, scanner scanning.Scanner, name string, formats []repository.Format, healthTimeout, databaseMaxAge time.Duration, now time.Time) adminopenapi.DiagnosticScanner {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "artifact-scanner"
	}
	if databaseMaxAge <= 0 {
		databaseMaxAge = defaultScannerDatabaseMaxAge
	}
	if healthTimeout <= 0 || healthTimeout > 30*time.Second {
		healthTimeout = 2 * time.Second
	}
	responseFormats := make([]adminopenapi.Format, 0, len(formats))
	for _, format := range formats {
		responseFormats = append(responseFormats, adminopenapi.Format(format))
	}
	result := adminopenapi.DiagnosticScanner{
		Name: name, Formats: responseFormats, CheckedAt: now,
		Status:                adminopenapi.DiagnosticScannerStatusNotConfigured,
		Detail:                "artifact scanner is not configured",
		DatabaseFreshness:     adminopenapi.DiagnosticScannerDatabaseFreshnessUnknown,
		DatabaseMaxAgeSeconds: int(databaseMaxAge / time.Second),
	}
	if scanner == nil {
		return result
	}
	healthChecker, ok := scanner.(scanning.HealthChecker)
	if !ok {
		result.Status = adminopenapi.DiagnosticScannerStatusUnknown
		result.Detail = "scanner health reporting is not supported"
		return result
	}
	checkCtx, cancel := context.WithTimeout(ctx, healthTimeout)
	health, err := healthChecker.Health(checkCtx)
	cancel()
	if errors.Is(err, scanning.ErrHealthNotConfigured) {
		result.Status = adminopenapi.DiagnosticScannerStatusUnknown
		result.Detail = "scanner health endpoint is not configured"
		return result
	}
	if err != nil {
		result.Status = adminopenapi.DiagnosticScannerStatusUnreachable
		result.Detail = "scanner health check failed"
		return result
	}
	if !health.CheckedAt.IsZero() {
		result.CheckedAt = health.CheckedAt
	}
	if health.Version != "" {
		version := health.Version
		result.Version = &version
	}
	switch health.Status {
	case scanning.HealthHealthy:
		result.Status = adminopenapi.DiagnosticScannerStatusHealthy
		result.Detail = "scanner health check passed"
	case scanning.HealthDegraded:
		result.Status = adminopenapi.DiagnosticScannerStatusDegraded
		result.Detail = "scanner reported degraded health"
	case scanning.HealthUnhealthy:
		result.Status = adminopenapi.DiagnosticScannerStatusUnhealthy
		result.Detail = "scanner reported unhealthy health"
	default:
		result.Status = adminopenapi.DiagnosticScannerStatusUnknown
		result.Detail = "scanner returned an unknown health status"
	}
	if health.Database == nil {
		return result
	}
	updatedAt := health.Database.UpdatedAt
	result.DatabaseUpdatedAt = &updatedAt
	if health.Database.Version != "" {
		version := health.Database.Version
		result.DatabaseVersion = &version
	}
	age := now.Sub(updatedAt)
	if age < 0 {
		age = 0
	}
	if age <= databaseMaxAge {
		result.DatabaseFreshness = adminopenapi.DiagnosticScannerDatabaseFreshnessFresh
		return result
	}
	result.DatabaseFreshness = adminopenapi.DiagnosticScannerDatabaseFreshnessStale
	if result.Status == adminopenapi.DiagnosticScannerStatusHealthy {
		result.Status = adminopenapi.DiagnosticScannerStatusDegraded
		result.Detail = "vulnerability database is stale"
	}
	return result
}

func diagnosticDependencies(ctx context.Context, checkers []Checker) []adminopenapi.DiagnosticDependency {
	items := make([]adminopenapi.DiagnosticDependency, 0, len(checkers))
	for _, checker := range checkers {
		name := "dependency"
		switch checker.(type) {
		case postgresChecker, postgresPoolChecker:
			name = "postgresql"
		case httpChecker:
			name = "object-storage"
		}
		status, detail := adminopenapi.DiagnosticDependencyStatusReachable, "reachable"
		checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		if err := checker.Check(checkCtx); err != nil {
			status, detail = adminopenapi.DiagnosticDependencyStatusUnreachable, "health check failed"
		}
		cancel()
		items = append(items, adminopenapi.DiagnosticDependency{Name: name, Status: status, Detail: &detail})
	}
	if len(items) == 0 {
		detail := "no dependency checks configured"
		items = append(items, adminopenapi.DiagnosticDependency{Name: "runtime", Status: adminopenapi.DiagnosticDependencyStatusNotConfigured, Detail: &detail})
	}
	return items
}
