package raw

import (
	"net/http"
	"strings"
	"time"

	"github.com/ccsert/artifact-gateway/internal/artifacthttp"
	"github.com/ccsert/artifact-gateway/internal/authorization"
	rawprotocol "github.com/ccsert/artifact-gateway/internal/protocol/raw"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

func (h *Module) serveGroup(w http.ResponseWriter, r *http.Request, group repository.HostedGroup) {
	if h.groupRuntime == nil {
		http.Error(w, "group runtime unavailable", http.StatusServiceUnavailable)
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
		if group.AnonymousRead && (r.Method == http.MethodGet || r.Method == http.MethodHead) && h.groupRuntime.AnonymousAllowed(r.Context()) {
			principal = anonymousPrincipal()
		} else {
			w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
	}
	members, err := h.groupRuntime.ResolveMembers(r.Context(), group)
	if err != nil {
		http.Error(w, "unable to resolve group members", http.StatusInternalServerError)
		return
	}
	if len(members) == 0 {
		h.groupRuntime.AuditResolution(r.Context(), group, repository.FormatRaw, path, strings.ToLower(r.Method), principal.Actor, repository.AuditNotFound, http.StatusNotFound)
		http.NotFound(w, r)
		return
	}
	if isAnonymous(principal) {
		members = anonymousMembers(group, members)
	}
	var denied bool
	members, denied = h.groupRuntime.AuthorizeMembers(r.Context(), principal, repository.FormatRaw, path, members)
	if denied && len(members) == 0 {
		http.Error(w, "repository read permission required", http.StatusForbidden)
		return
	}
	if len(members) == 0 {
		h.groupRuntime.AuditResolution(r.Context(), group, repository.FormatRaw, path, strings.ToLower(r.Method), principal.Actor, repository.AuditAccessDenied, http.StatusUnauthorized)
		w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway"`)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	for _, member := range members {
		if member.Type == repository.MemberHosted {
			repo, err := h.repos.GetHostedRepository(r.Context(), member.RepositoryID)
			if err != nil {
				continue
			}
			if h.serveHostedRead(w, r, repo, path, principal) {
				return
			}
			continue
		}
		repo, err := h.repos.GetHostedRepository(r.Context(), member.RepositoryID)
		if err != nil {
			continue
		}
		if h.tryProxyRead(w, r, repo, path, principal) {
			return
		}
	}
	h.groupRuntime.AuditResolution(r.Context(), group, repository.FormatRaw, path, strings.ToLower(r.Method), principal.Actor, repository.AuditNotFound, http.StatusNotFound)
	http.NotFound(w, r)
}

// serveHostedRead attempts a read against a native hosted Raw repository
// without writing a response when the asset is absent, so the caller can fall
// through to the next group member. It reports whether the request was served.
func (h *Module) serveHostedRead(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, path string, principal authorization.Principal) bool {
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
		h.serveChecksum(w, r, path, body)
		return true
	}
	asset, err := h.store.GetRawAsset(r.Context(), repo.ID, path)
	if err != nil {
		return false
	}
	if h.blockQuarantinedRead(w, r, repo, asset, principal) {
		return true
	}
	artifacthttp.ServeObject(w, r, path, artifacthttp.Object{Digest: asset.Digest, ObjectKey: asset.ObjectKey, Size: asset.Size, ContentType: asset.ContentType}, h.objects)
	return true
}

func (h *Module) blockQuarantinedRead(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, asset repository.RawAsset, principal authorization.Principal) bool {
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

func anonymousMembers(group repository.HostedGroup, members []repository.Member) []repository.Member {
	if !group.AnonymousRead {
		return nil
	}
	allowed := make([]repository.Member, 0, len(members))
	for _, member := range members {
		if member.Anonymous {
			allowed = append(allowed, member)
		}
	}
	return allowed
}
