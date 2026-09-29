package app

import (
	"net/http"
	"sort"
	"strings"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

const (
	runtimeNodeStaleAfter   = 30 * time.Second
	runtimeNodeOfflineAfter = 2 * time.Minute
)

func runtimeNodeStatus(now, lastSeen time.Time) adminopenapi.RuntimeNodeStatus {
	age := now.Sub(lastSeen)
	if age < 0 || age < runtimeNodeStaleAfter {
		return adminopenapi.RuntimeNodeStatusOnline
	}
	if age <= runtimeNodeOfflineAfter {
		return adminopenapi.RuntimeNodeStatusStale
	}
	return adminopenapi.RuntimeNodeStatusOffline
}

func runtimeNodeHealth(items []adminopenapi.RuntimeNode) adminopenapi.RuntimeNodeHealth {
	health := adminopenapi.RuntimeNodeHealth{Issues: make([]adminopenapi.RuntimeNodeHealthIssue, 0)}
	activeInstanceIDs := make(map[string]int)
	hasAPI, hasScheduler, hasWorker := false, false, false
	for _, node := range items {
		switch node.Status {
		case adminopenapi.RuntimeNodeStatusOnline:
			health.Online++
		case adminopenapi.RuntimeNodeStatusStale:
			health.Stale++
		case adminopenapi.RuntimeNodeStatusOffline:
			health.Offline++
		}
		if node.Status == adminopenapi.RuntimeNodeStatusOffline {
			continue
		}
		activeInstanceIDs[node.InstanceId]++
		if node.Status != adminopenapi.RuntimeNodeStatusOnline {
			continue
		}
		for _, role := range node.Roles {
			switch role {
			case "standalone":
				hasAPI, hasScheduler, hasWorker = true, true, true
			case "api":
				hasAPI = true
			case "scheduler":
				hasScheduler = true
			case "worker":
				hasWorker = true
			}
		}
	}
	addIssue := func(code, severity, message string) {
		issue := adminopenapi.RuntimeNodeHealthIssue{Code: code, Message: message}
		if severity == "error" {
			issue.Severity = adminopenapi.RuntimeNodeHealthIssueSeverityError
		} else {
			issue.Severity = adminopenapi.RuntimeNodeHealthIssueSeverityWarning
		}
		health.Issues = append(health.Issues, issue)
	}
	if !hasAPI {
		addIssue("api_unavailable", "error", "没有在线 API 节点")
	}
	if !hasScheduler {
		addIssue("scheduler_unavailable", "warning", "没有在线 Scheduler 节点，后台调度不会运行")
	}
	if !hasWorker {
		addIssue("worker_unavailable", "warning", "没有在线 Worker 节点，后台任务不会被处理")
	}
	duplicateCount := 0
	for _, count := range activeInstanceIDs {
		if count > 1 {
			duplicateCount += count - 1
		}
	}
	if duplicateCount > 0 {
		addIssue("duplicate_instance_id", "warning", "存在重复的实例 ID，会话已分开记录")
	}
	if health.Stale > 0 {
		addIssue("stale_nodes", "warning", "存在心跳过期的运行节点")
	}
	builds := make(map[string][]string)
	unknownBuild := make([]string, 0)
	for _, node := range items {
		if node.Status == adminopenapi.RuntimeNodeStatusOffline {
			continue
		}
		version, revision := "", ""
		if node.Version != nil {
			version = strings.TrimSpace(*node.Version)
		}
		if node.Revision != nil {
			revision = strings.TrimSpace(*node.Revision)
		}
		if version == "" || revision == "" || revision == "unknown" {
			unknownBuild = append(unknownBuild, node.SessionId)
			continue
		}
		builds[version+"\x00"+revision] = append(builds[version+"\x00"+revision], node.SessionId)
	}
	if len(unknownBuild) > 0 {
		sort.Strings(unknownBuild)
		affected := unknownBuild
		health.Issues = append(health.Issues, adminopenapi.RuntimeNodeHealthIssue{
			Code: "build_identity_unknown", Severity: adminopenapi.RuntimeNodeHealthIssueSeverityWarning,
			Message: "部分在线节点未报告完整构建身份，无法确认版本一致", AffectedNodes: &affected,
		})
	}
	if len(builds) > 1 {
		affected := make([]string, 0)
		for _, sessions := range builds {
			affected = append(affected, sessions...)
		}
		sort.Strings(affected)
		health.Issues = append(health.Issues, adminopenapi.RuntimeNodeHealthIssue{
			Code: "mixed_build", Severity: adminopenapi.RuntimeNodeHealthIssueSeverityWarning,
			Message: "在线节点运行不同的版本或修订号，可能正在滚动升级", AffectedNodes: &affected,
		})
	}
	health.Status = adminopenapi.RuntimeNodeHealthStatusHealthy
	for _, issue := range health.Issues {
		if issue.Severity == adminopenapi.RuntimeNodeHealthIssueSeverityError {
			health.Status = adminopenapi.RuntimeNodeHealthStatusCritical
			break
		}
		health.Status = adminopenapi.RuntimeNodeHealthStatusDegraded
	}
	return health
}

func (h generatedRepositoryAPIAdapter) ListRuntimeNodes(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	nodes, err := h.runtimeNodes.ListRuntimeNodes(r.Context())
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list runtime nodes failed")
		return
	}
	items := runtimeNodeResponses(nodes, time.Now().UTC())
	response := adminopenapi.RuntimeNodeList{Items: items, Health: runtimeNodeHealth(items), ReleaseSource: adminopenapi.RuntimeNodeListReleaseSourceNotConfigured}
	if sessionID := h.diagnostics.Runtime.SessionID; sessionID != "" {
		response.CurrentSessionId = &sessionID
	}
	writeNativeMavenJSON(w, http.StatusOK, response)
}

func runtimeNodeResponses(nodes []repository.RuntimeNode, now time.Time) []adminopenapi.RuntimeNode {
	items := make([]adminopenapi.RuntimeNode, 0, len(nodes))
	for _, node := range nodes {
		formats := make([]adminopenapi.Format, 0, len(node.WorkerFormats))
		for _, format := range node.WorkerFormats {
			formats = append(formats, adminopenapi.Format(format))
		}
		status := runtimeNodeStatus(now, node.LastSeenAt)
		if !node.StoppedAt.IsZero() {
			status = adminopenapi.RuntimeNodeStatusOffline
		}
		item := adminopenapi.RuntimeNode{
			InstanceId:    node.InstanceID,
			SessionId:     node.SessionID,
			Roles:         append([]string{}, node.Roles...),
			WorkerFormats: formats,
			WorkerKinds:   append([]string{}, node.WorkerKinds...),
			StartedAt:     node.StartedAt,
			LastSeenAt:    node.LastSeenAt,
			Status:        status,
		}
		if node.BuildVersion != "" {
			version := node.BuildVersion
			item.Version = &version
		}
		if node.BuildRevision != "" {
			revision := node.BuildRevision
			item.Revision = &revision
		}
		if !node.StoppedAt.IsZero() {
			stoppedAt := node.StoppedAt
			item.StoppedAt = &stoppedAt
		}
		items = append(items, item)
	}
	return items
}
