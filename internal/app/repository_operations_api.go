package app

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type cacheOperationsHandler struct {
	maintenance   *CacheMaintenance
	authenticator Authenticator
}

type cacheCollectionHandler struct {
	maintenance   *CacheMaintenance
	authenticator Authenticator
}

func (h cacheCollectionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok || !principal.Admin {
		http.Error(w, "administrator access required", http.StatusForbidden)
		return
	}
	if err := h.maintenance.Run(r.Context()); err != nil {
		http.Error(w, "cache collection failed", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h cacheOperationsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
		return
	}
	if !principal.Admin {
		writeError(w, http.StatusForbidden, "forbidden", "administrator permission required")
		return
	}
	status, err := h.maintenance.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "unable to inspect cache")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

type RepositoryOperationsStatus struct {
	Repository   string                 `json:"repository"`
	Metrics      RepositoryMetrics      `json:"metrics"`
	HitRate      float64                `json:"hit_rate"`
	GatewayCache CacheMaintenanceStatus `json:"gateway_cache"`
}

type repositoryOperationsHandler struct {
	maintenance   *CacheMaintenance
	metrics       *Metrics
	authenticator Authenticator
}

func (h repositoryOperationsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
		return
	}
	if !principal.Admin {
		writeError(w, http.StatusForbidden, "forbidden", "administrator permission required")
		return
	}
	repositoryName := strings.TrimSpace(r.URL.Query().Get("repository"))
	if repositoryName == "" {
		writeError(w, http.StatusBadRequest, "invalid_repository", "repository is required")
		return
	}
	cache, err := h.maintenance.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "unable to inspect cache")
		return
	}
	metrics := h.metrics.repository(repositoryName)
	denominator := metrics.CacheHits + metrics.CacheMisses
	status := RepositoryOperationsStatus{Repository: repositoryName, Metrics: metrics, GatewayCache: cache}
	if denominator > 0 {
		status.HitRate = float64(metrics.CacheHits) / float64(denominator)
	}
	writeJSON(w, http.StatusOK, status)
}

type auditAPIHandler struct {
	store         repository.AuditStore
	authenticator Authenticator
}

func (a auditAPIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
		return
	}
	if !principal.Admin {
		writeError(w, http.StatusForbidden, "forbidden", "administrator permission required")
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 500")
			return
		}
		limit = parsed
	}
	audits, err := a.store.ListAudits(r.Context(), repository.AuditQuery{
		GroupName: r.URL.Query().Get("group"), Repository: r.URL.Query().Get("repository"), Limit: limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "unable to query audits")
		return
	}
	writeJSON(w, http.StatusOK, audits)
}
