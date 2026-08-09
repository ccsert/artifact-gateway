package app

import (
	"context"
	"net/http"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
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
	responseNodes := runtimeNodeResponses(nodes, time.Now().UTC())
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
	writeNativeMavenJSON(w, http.StatusOK, adminopenapi.Diagnostics{
		GeneratedAt: time.Now().UTC(),
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
		Queues:       queues,
		Nodes:        runtimeNodeHealth(responseNodes),
	})
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
		status, detail := adminopenapi.Reachable, "reachable"
		checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		if err := checker.Check(checkCtx); err != nil {
			status, detail = adminopenapi.Unreachable, "health check failed"
		}
		cancel()
		items = append(items, adminopenapi.DiagnosticDependency{Name: name, Status: status, Detail: &detail})
	}
	if len(items) == 0 {
		detail := "no dependency checks configured"
		items = append(items, adminopenapi.DiagnosticDependency{Name: "runtime", Status: adminopenapi.NotConfigured, Detail: &detail})
	}
	return items
}
