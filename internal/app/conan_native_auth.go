package app

import (
	"net/http"
	"strings"

	"github.com/ccsert/artifact-gateway/internal/repository"
)

func conanRemoteName(raw string) string {
	parts := strings.Split(strings.Trim(raw, "/"), "/")
	if len(parts) < 2 || parts[0] != "conan" {
		return ""
	}
	return parts[1]
}

func conanHandshakeKind(r *http.Request) string {
	if r.Method != http.MethodGet {
		return ""
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) == 4 && parts[0] == "conan" && parts[2] == "v1" && parts[3] == "ping" {
		return "ping"
	}
	if len(parts) == 5 && parts[0] == "conan" && parts[2] == "v2" && parts[3] == "users" && (parts[4] == "authenticate" || parts[4] == "check_credentials") {
		return parts[4]
	}
	return ""
}

type conanHandshakeScope struct {
	ID, Name      string
	AnonymousRead bool
}

func (h ConanHandler) serveNativeConanHandshake(w http.ResponseWriter, r *http.Request) bool {
	if conanHandshakeKind(r) == "" || h.Repositories == nil {
		return false
	}
	repo, err := h.Repositories.GetHostedRepositoryByName(r.Context(), conanRemoteName(r.URL.Path))
	if err != nil || repo.Format != repository.FormatConan || repo.State != repository.RepositoryActive {
		return false
	}
	principal, authenticated := h.authenticate(r)
	h.serveConanHandshake(w, r, conanHandshakeScope{ID: repo.ID, Name: repo.Name, AnonymousRead: h.anonymousConanAllowed(r.Context(), repo.Name)}, principal, authenticated)
	return true
}

func (h ConanHandler) serveConanHandshake(w http.ResponseWriter, r *http.Request, scope conanHandshakeScope, principal Principal, authenticated bool) {
	kind := conanHandshakeKind(r)
	w.Header().Set("Cache-Control", "no-store")
	if !authenticated && kind == "ping" && r.Header.Get("Authorization") == "" && scope.AnonymousRead {
		w.Header().Set("X-Conan-Server-Capabilities", "revisions")
		w.WriteHeader(http.StatusOK)
		return
	}
	if !authenticated || principal.RepositoryAccessBlocked() {
		w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway Conan"`)
		h.audit(withConanAuditStatus(r.Context(), http.StatusUnauthorized), scope.Name, "users", "native", "anonymous", repository.AuditAccessDenied)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if kind == "ping" {
		w.Header().Set("X-Conan-Server-Capabilities", "revisions")
		w.WriteHeader(http.StatusOK)
		return
	}
	if kind == "authenticate" {
		if len(h.Authenticator.ConanTokenSecret) < 32 {
			writeHostedProblem(w, http.StatusNotImplemented, "not_supported", "Conan protocol authentication is not configured")
			return
		}
		username, password, basic := r.BasicAuth()
		token, ok := h.Authenticator.IssueConanToken(username, password, scope.ID)
		if !basic || !ok {
			http.Error(w, "Basic local credential required", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(token))
		return
	}
	w.WriteHeader(http.StatusOK)
}
