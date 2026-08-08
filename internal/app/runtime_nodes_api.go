package app

import (
	"net/http"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
)

const (
	runtimeNodeStaleAfter   = 30 * time.Second
	runtimeNodeOfflineAfter = 2 * time.Minute
)

func runtimeNodeStatus(now, lastSeen time.Time) adminopenapi.RuntimeNodeStatus {
	age := now.Sub(lastSeen)
	if age < 0 || age <= runtimeNodeStaleAfter {
		return adminopenapi.Online
	}
	if age <= runtimeNodeOfflineAfter {
		return adminopenapi.Stale
	}
	return adminopenapi.Offline
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
	now := time.Now().UTC()
	items := make([]adminopenapi.RuntimeNode, 0, len(nodes))
	for _, node := range nodes {
		formats := make([]adminopenapi.Format, 0, len(node.WorkerFormats))
		for _, format := range node.WorkerFormats {
			formats = append(formats, adminopenapi.Format(format))
		}
		items = append(items, adminopenapi.RuntimeNode{
			InstanceId:    node.InstanceID,
			Roles:         append([]string(nil), node.Roles...),
			WorkerFormats: formats,
			WorkerKinds:   append([]string(nil), node.WorkerKinds...),
			StartedAt:     node.StartedAt,
			LastSeenAt:    node.LastSeenAt,
			Status:        runtimeNodeStatus(now, node.LastSeenAt),
		})
	}
	writeNativeMavenJSON(w, http.StatusOK, adminopenapi.RuntimeNodeList{Items: items})
}
