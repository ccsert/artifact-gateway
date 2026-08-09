package app

import (
	"net/http"
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
		return adminopenapi.Online
	}
	if age <= runtimeNodeOfflineAfter {
		return adminopenapi.Stale
	}
	return adminopenapi.Offline
}

func runtimeNodeHealth(items []adminopenapi.RuntimeNode) adminopenapi.RuntimeNodeHealth {
	health := adminopenapi.RuntimeNodeHealth{Issues: make([]adminopenapi.RuntimeNodeHealthIssue, 0)}
	activeInstanceIDs := make(map[string]int)
	hasAPI, hasScheduler, hasWorker := false, false, false
	for _, node := range items {
		switch node.Status {
		case adminopenapi.Online:
			health.Online++
		case adminopenapi.Stale:
			health.Stale++
		case adminopenapi.Offline:
			health.Offline++
		}
		if node.Status == adminopenapi.Offline {
			continue
		}
		activeInstanceIDs[node.InstanceId]++
		if node.Status != adminopenapi.Online {
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
			issue.Severity = adminopenapi.Error
		} else {
			issue.Severity = adminopenapi.Warning
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
	health.Status = adminopenapi.Healthy
	for _, issue := range health.Issues {
		if issue.Severity == adminopenapi.Error {
			health.Status = adminopenapi.Critical
			break
		}
		health.Status = adminopenapi.Degraded
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
	writeNativeMavenJSON(w, http.StatusOK, adminopenapi.RuntimeNodeList{Items: items, Health: runtimeNodeHealth(items)})
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
			status = adminopenapi.Offline
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
		if !node.StoppedAt.IsZero() {
			stoppedAt := node.StoppedAt
			item.StoppedAt = &stoppedAt
		}
		items = append(items, item)
	}
	return items
}
