package app

import (
	"net/http"
	"strings"
	"time"

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
		if group.AnonymousRead && anonymousReadMethod(r.Method) && anonymousAccessAllowed(r.Context(), resolver.groups) {
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
	var denied bool
	members, denied = resolver.authorizeMembers(r.Context(), principal, repository.FormatRaw, path, members)
	if denied && len(members) == 0 {
		http.Error(w, "repository read permission required", http.StatusForbidden)
		return
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
			if h.native.serveHostedRead(w, r, repo, path, principal) {
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
func (h nativeRawHandler) serveHostedRead(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, path string, principal Principal) bool {
	if rawChecksumExtension(path) != "" {
		source := rawChecksumSourcePath(path)
		if rawChecksumExtension(source) != "" {
			return false
		}
		asset, err := h.store.GetRawAsset(r.Context(), repo.ID, source)
		if err != nil {
			return false
		}
		if h.blockQuarantinedRead(w, r, repo, asset, principal) {
			return true
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
	if h.blockQuarantinedRead(w, r, repo, asset, principal) {
		return true
	}
	serveNativeRawObject(w, r, path, asset, h.objects)
	return true
}

func (h nativeRawHandler) blockQuarantinedRead(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, asset repository.RawAsset, principal Principal) bool {
	blocked, err := repository.QuarantinedArtifactReadBlocked(r.Context(), h.readPolicies, h.quarantine, repo.ID, repository.FormatRaw, asset.Path, asset.Digest)
	if err != nil {
		http.Error(w, "evaluate artifact quarantine failed", http.StatusInternalServerError)
		return true
	}
	if !blocked {
		return false
	}
	if h.audit != nil {
		_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{GroupName: repo.Name, Repository: repo.Name, Actor: principal.Actor, Outcome: repository.AuditAccessDenied, OccurredAt: time.Now().UTC(), Format: string(repository.FormatRaw), Resource: asset.Path, Representation: asset.Digest, Operation: strings.ToLower(r.Method), Status: http.StatusForbidden, CacheDisposition: "bypass", AuthorizationSource: "quarantine_read_policy", AuthorizationReason: repository.ArtifactQuarantinedReason})
	}
	http.Error(w, repository.ArtifactQuarantinedReason, http.StatusForbidden)
	return true
}
