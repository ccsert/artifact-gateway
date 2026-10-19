package raw

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/requestcontext"
	"github.com/google/uuid"
)

func (h *Module) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, _ := requestcontext.WithRequest(r.Context(), r.Header.Get("X-Request-ID"))
	r = r.WithContext(ctx)
	name := strings.Split(strings.TrimPrefix(r.URL.Path, "/raw/"), "/")[0]
	if h.repos == nil {
		http.Error(w, "Raw repository store unavailable", http.StatusServiceUnavailable)
		return
	}
	if name != "" {
		repo, err := h.repos.GetHostedRepositoryByName(r.Context(), name)
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			http.Error(w, "repository lookup failed", http.StatusServiceUnavailable)
			return
		}
		if err == nil && repo.Format == repository.FormatRaw {
			if repo.State != repository.RepositoryActive {
				http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
				return
			}
			if _, ok := h.protocolPrincipal(r); !ok && !h.anonymousRepositoryRead(r.Context(), repo, r.Method) {
				w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway"`)
				http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
				return
			}
		}
		if h.groups != nil {
			group, err := h.groups.GetHostedGroupByName(r.Context(), name)
			if err != nil && !errors.Is(err, repository.ErrNotFound) {
				http.Error(w, "group lookup failed", http.StatusServiceUnavailable)
				return
			}
			if err == nil && group.Format == repository.FormatRaw {
				h.serveGroup(w, r, group)
				return
			}
		}
	}
	if h.store != nil && h.serveRepository(w, r) {
		return
	}
	h.legacy.ServeHTTP(w, r)
}

func auditRequestID(ctx context.Context) string {
	if correlation, ok := requestcontext.FromContext(ctx); ok {
		return correlation.RequestID
	}
	return uuid.NewString()
}
func auditTraceID(ctx context.Context) string {
	if correlation, ok := requestcontext.FromContext(ctx); ok {
		return correlation.TraceID
	}
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}
