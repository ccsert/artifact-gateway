package app

import (
	"errors"
	"net/http"
	"strings"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// hostedRepositoryGuard prevents a Native Hosted Repository from falling back
// into legacy Group resolution after it leaves the active state. Native
// repositories are private by default; anonymous protocol reads are admitted
// only when the addressed Hosted Repository explicitly enables anonymousRead.
type hostedRepositoryGuard struct {
	store         repository.HostedRepositoryStore
	authenticator Authenticator
	format        repository.Format
	next          http.Handler
}

func (h hostedRepositoryGuard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := nativeRepositoryName(h.format, r.URL.Path)
	if name == "" {
		h.next.ServeHTTP(w, r)
		return
	}
	repo, err := h.store.GetHostedRepositoryByName(r.Context(), name)
	if errors.Is(err, repository.ErrNotFound) {
		h.next.ServeHTTP(w, r)
		return
	}
	if err != nil {
		http.Error(w, "repository lookup failed", http.StatusServiceUnavailable)
		return
	}
	if repo.Format != h.format {
		h.next.ServeHTTP(w, r)
		return
	}
	if repo.State != repository.RepositoryActive {
		h.reject(w, r, http.StatusForbidden)
		return
	}
	if !h.authenticated(r) && !anonymousHostedRepositoryReadAllowed(r.Context(), h.store, repo, r.Method) {
		h.reject(w, r, http.StatusUnauthorized)
		return
	}
	// Native bytes are introduced by the publish-session slice. Until then the
	// legacy handler returns its normal not-found response for an active native
	// repository, but never authorizes anonymous access or a disabled one.
	h.next.ServeHTTP(w, r)
}

func (h hostedRepositoryGuard) authenticated(r *http.Request) bool {
	if _, ok := h.authenticator.Authenticate(r.Header.Get("Authorization")); ok {
		return true
	}
	username, password, ok := r.BasicAuth()
	return ok && username != "" && h.authenticator.ResolverPasswordMatches(password)
}

func (h hostedRepositoryGuard) reject(w http.ResponseWriter, r *http.Request, status int) {
	if h.format == repository.FormatOCI {
		if status == http.StatusUnauthorized {
			writeOCIChallenge(w, r)
		} else {
			writeOCIError(w, status, "DENIED", "requested access to the resource is denied")
		}
		return
	}
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway"`)
	}
	http.Error(w, http.StatusText(status), status)
}

func nativeRepositoryName(format repository.Format, path string) string {
	var prefix string
	switch format {
	case repository.FormatOCI:
		prefix = "/v2/"
	case repository.FormatRaw:
		prefix = "/raw/"
	case repository.FormatMaven:
		prefix = "/maven/"
	case repository.FormatNPM:
		prefix = "/npm/"
	default:
		return ""
	}
	rest := strings.TrimPrefix(path, prefix)
	if rest == path || rest == "" {
		return ""
	}
	if format == repository.FormatOCI && rest == "_catalog" {
		return ""
	}
	return strings.Split(rest, "/")[0]
}
