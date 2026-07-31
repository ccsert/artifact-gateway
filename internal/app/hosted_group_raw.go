package app

import (
	"net/http"
	"strings"

	rawprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/raw"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// v2GroupRawHandler serves Raw reads against a V2 HostedGroup. Hosted members
// are served from the native hosted store first; proxy members fall through to
// the shared upstream fetch and read-through cache. Writes are not supported
// on groups.
type v2GroupRawHandler struct {
	native *nativeRawHandler
	auth   Authenticator
}

func (h v2GroupRawHandler) serve(w http.ResponseWriter, r *http.Request, resolver v2GroupResolver, group repository.HostedGroup) {
	if h.native == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	_, path, ok := rawprotocol.ParsePath(r.URL.EscapedPath())
	if !ok || path == "" || strings.HasSuffix(path, "/") {
		http.NotFound(w, r)
		return
	}
	principal, authenticated := h.auth.Authenticate(r.Header.Get("Authorization"))
	if !authenticated {
		if group.AnonymousRead && anonymousReadMethod(r.Method) {
			principal = anonymousPrincipal()
		} else {
			w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
	}
	members, err := resolver.resolveMembers(r.Context(), group)
	if err != nil {
		http.Error(w, "unable to resolve group members", http.StatusInternalServerError)
		return
	}
	if len(members) == 0 {
		resolver.auditResolution(r.Context(), group, repository.FormatRaw, path, strings.ToLower(r.Method), principal.Actor, repository.AuditNotFound, http.StatusNotFound)
		http.NotFound(w, r)
		return
	}
	if isAnonymous(principal) {
		members = anonymousHostedGroupMembers(group, members)
	}
	if len(members) == 0 {
		resolver.auditResolution(r.Context(), group, repository.FormatRaw, path, strings.ToLower(r.Method), principal.Actor, repository.AuditAccessDenied, http.StatusUnauthorized)
		w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway"`)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	for _, member := range members {
		if member.Type == repository.MemberHosted {
			repo, err := resolver.repos.GetHostedRepository(r.Context(), member.RepositoryID)
			if err != nil {
				continue
			}
			if h.native.serveHostedRead(w, r, repo, path) {
				return
			}
			continue
		}
		repo, err := resolver.repos.GetHostedRepository(r.Context(), member.RepositoryID)
		if err != nil {
			continue
		}
		h.native.proxyRead(w, r, repo, path, principal)
		return
	}
	resolver.auditResolution(r.Context(), group, repository.FormatRaw, path, strings.ToLower(r.Method), principal.Actor, repository.AuditNotFound, http.StatusNotFound)
	http.NotFound(w, r)
}

// serveHostedRead attempts a read against a native hosted Raw repository
// without writing a response when the asset is absent, so the caller can fall
// through to the next group member. It reports whether the request was served.
func (h nativeRawHandler) serveHostedRead(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, path string) bool {
	if rawChecksumExtension(path) != "" {
		source := rawChecksumSourcePath(path)
		if rawChecksumExtension(source) != "" {
			return false
		}
		asset, err := h.store.GetRawAsset(r.Context(), repo.ID, source)
		if err != nil {
			return false
		}
		body, err := h.rawChecksum(r.Context(), asset, rawChecksumExtension(path))
		if err != nil {
			http.Error(w, "raw object unavailable", http.StatusInternalServerError)
			return true
		}
		h.serveChecksum(w, r, body)
		return true
	}
	asset, err := h.store.GetRawAsset(r.Context(), repo.ID, path)
	if err != nil {
		return false
	}
	serveNativeRawObject(w, r, path, asset, h.objects)
	return true
}
