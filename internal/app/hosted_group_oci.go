package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	ociprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/oci"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// v2GroupOCIHandler serves Registry V2 reads against a V2 HostedGroup. Hosted
// members are served from the native hosted store first; proxy members fall
// through to the shared upstream fetch and read-through cache. Writes are not
// supported on groups.
type v2GroupOCIHandler struct {
	native *nativeOCIHandler
	proxy  *OCIHandler
	auth   Authenticator
}

func (h v2GroupOCIHandler) serve(w http.ResponseWriter, r *http.Request, resolver v2GroupResolver, group repository.HostedGroup) {
	if h.native == nil || h.proxy == nil {
		writeOCIError(w, http.StatusNotFound, "NAME_UNKNOWN", "repository name not known to registry")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeOCIError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "method not allowed")
		return
	}
	name, resource, reference, _, ok := parseNativeOCIPath(r.URL.Path)
	if !ok || (resource != "manifest" && resource != "blob" && resource != "tags") {
		writeOCIError(w, http.StatusNotFound, "NAME_UNKNOWN", "repository name not known to registry")
		return
	}
	_, imageName, found := strings.Cut(name, "/")
	if !found || imageName == "" {
		writeOCIError(w, http.StatusNotFound, "NAME_UNKNOWN", "repository name not known to registry")
		return
	}
	principal, authenticated := h.auth.Authenticate(r.Header.Get("Authorization"))
	if !authenticated {
		if group.AnonymousRead && anonymousReadMethod(r.Method) && anonymousAccessAllowed(r.Context(), resolver.groups) {
			principal = anonymousPrincipal()
		} else {
			writeOCIChallenge(w, r)
			return
		}
	}
	members, err := resolver.resolveMembers(r.Context(), group)
	if err != nil {
		writeOCIError(w, http.StatusInternalServerError, "UNKNOWN", "unable to resolve group members")
		return
	}
	if len(members) == 0 {
		resolver.auditResolution(r.Context(), group, repository.FormatOCI, r.URL.Path, strings.ToLower(r.Method), principal.Actor, repository.AuditNotFound, http.StatusNotFound)
		writeOCIError(w, http.StatusNotFound, "NAME_UNKNOWN", "repository name not known to registry")
		return
	}
	if isAnonymous(principal) {
		members = anonymousHostedGroupMembers(group, members)
	}
	var denied bool
	members, denied = resolver.authorizeMembers(r.Context(), principal, repository.FormatOCI, imageName, members)
	if denied && len(members) == 0 {
		writeOCIError(w, http.StatusForbidden, "DENIED", "requested access to the resource is denied")
		return
	}
	if len(members) == 0 {
		resolver.auditResolution(r.Context(), group, repository.FormatOCI, r.URL.Path, strings.ToLower(r.Method), principal.Actor, repository.AuditAccessDenied, http.StatusUnauthorized)
		writeOCIChallenge(w, r)
		return
	}
	if resource == "tags" {
		h.tags(w, r, resolver, group, imageName, principal, members)
		return
	}
	upstreamResource := map[string]string{"manifest": ociManifest, "blob": ociBlob}[resource]
	for _, member := range members {
		if member.Type == repository.MemberHosted {
			repo, err := resolver.repos.GetHostedRepository(r.Context(), member.RepositoryID)
			if err != nil {
				continue
			}
			served, status := h.native.serveHostedRead(w, r, repo, imageName, resource, reference, principal.Actor)
			if served {
				if status == http.StatusForbidden {
					_ = resolver.auditGroupPolicyDenied(r.Context(), group.Name, repository.FormatOCI, r.URL.Path, repo.Name, principal.Actor, strings.ToLower(r.Method))
				} else {
					resolver.auditResolution(r.Context(), group, repository.FormatOCI, r.URL.Path, strings.ToLower(r.Method), principal.Actor, repository.AuditResolved, status)
				}
				return
			}
			continue
		}
		h.proxy.serveV2GroupProxy(w, r, group.Name, member, imageName, upstreamResource, reference, principal.Actor)
		return
	}
	resolver.auditResolution(r.Context(), group, repository.FormatOCI, r.URL.Path, strings.ToLower(r.Method), principal.Actor, repository.AuditNotFound, http.StatusNotFound)
	writeOCIError(w, http.StatusNotFound, map[string]string{"manifest": "MANIFEST_UNKNOWN", "blob": "BLOB_UNKNOWN"}[resource], "resource unknown to registry")
}

func (h v2GroupOCIHandler) tags(w http.ResponseWriter, r *http.Request, resolver v2GroupResolver, group repository.HostedGroup, imageName string, principal Principal, members []repository.Member) {
	limit := 100
	if raw := r.URL.Query().Get("n"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 1000 {
			writeOCIError(w, http.StatusBadRequest, "NAME_INVALID", "tag page size must be between 1 and 1000")
			return
		}
		limit = parsed
	}
	seen := make(map[string]struct{})
	claimed := make(map[string]struct{})
	for _, member := range members {
		if member.Type != repository.MemberHosted {
			continue
		}
		repo, err := resolver.repos.GetHostedRepository(r.Context(), member.RepositoryID)
		if err != nil {
			continue
		}
		blockedDigests, err := h.native.ociQuarantinedDigestClosure(r.Context(), repo, imageName)
		if err != nil {
			writeOCIError(w, http.StatusInternalServerError, "UNKNOWN", "unable to list group tags")
			return
		}
		memberVisible := 0
		cursor := r.URL.Query().Get("last")
		for memberVisible < limit+1 {
			batch, listErr := h.native.store.ListOCITags(r.Context(), member.RepositoryID, imageName, limit+1, cursor)
			if listErr != nil {
				writeOCIError(w, http.StatusInternalServerError, "UNKNOWN", "unable to list group tags")
				return
			}
			if len(batch) == 0 {
				break
			}
			for _, tag := range batch {
				if _, exists := claimed[tag]; exists {
					continue
				}
				claimed[tag] = struct{}{}
				manifest, lookupErr := h.native.store.GetOCIManifest(r.Context(), repo.ID, imageName, tag)
				if lookupErr != nil {
					continue
				}
				if _, blocked := blockedDigests[manifest.Digest]; blocked {
					continue
				}
				seen[tag] = struct{}{}
				memberVisible++
				if memberVisible == limit+1 {
					break
				}
			}
			if memberVisible == limit+1 || len(batch) < limit+1 {
				break
			}
			next := batch[len(batch)-1]
			if next == cursor {
				break
			}
			cursor = next
		}
	}
	tags := make([]string, 0, len(seen))
	for tag := range seen {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	if len(tags) > limit {
		tags = tags[:limit]
		next := "/v2/" + group.Name + "/" + imageName + "/tags/list?n=" + strconv.Itoa(limit) + "&last=" + url.QueryEscape(tags[len(tags)-1])
		w.Header().Set("Link", "<"+next+">; rel=\"next\"")
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	w.WriteHeader(http.StatusOK)
	resolver.auditResolution(r.Context(), group, repository.FormatOCI, r.URL.Path, strings.ToLower(r.Method), principal.Actor, repository.AuditResolved, http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"name": group.Name + "/" + imageName, "tags": tags})
}

// serveHostedRead attempts a read against a native hosted repository without
// writing a response when the content is absent, so the caller can fall
// through to the next group member. It reports whether the request was served
// and the status that was written.
func (h nativeOCIHandler) serveHostedRead(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, imageName, resource, reference, actor string) (bool, int) {
	switch resource {
	case "blob":
		if !validOCIDigest(reference) {
			writeOCIError(w, http.StatusBadRequest, "DIGEST_INVALID", "valid sha256 digest is required")
			return true, http.StatusBadRequest
		}
		blocked, err := h.ociDigestReadBlocked(r.Context(), repo, imageName, reference)
		if err != nil {
			writeOCIError(w, http.StatusInternalServerError, "UNKNOWN", "unable to evaluate artifact quarantine")
			return true, http.StatusInternalServerError
		}
		if blocked {
			writeOCIError(w, http.StatusForbidden, "DENIED", repository.ArtifactQuarantinedReason)
			return true, http.StatusForbidden
		}
		blob, err := h.store.GetOCIBlob(r.Context(), repo.ID, reference)
		if errors.Is(err, repository.ErrNotFound) {
			return false, 0
		}
		if err != nil {
			writeOCIError(w, http.StatusInternalServerError, "UNKNOWN", "unable to read blob")
			return true, http.StatusInternalServerError
		}
		serveCachedOCIContent(w, r, reference, ociprotocol.NewStoredContent(blob.Digest, "application/octet-stream", blob.ObjectKey, blob.Size, h.objects))
		return true, http.StatusOK
	case "manifest":
		if validOCIDigest(reference) {
			blocked, blockErr := h.ociDigestReadBlocked(r.Context(), repo, imageName, reference)
			if blockErr != nil {
				writeOCIError(w, http.StatusInternalServerError, "UNKNOWN", "unable to evaluate artifact quarantine")
				return true, http.StatusInternalServerError
			}
			if blocked {
				writeOCIError(w, http.StatusForbidden, "DENIED", repository.ArtifactQuarantinedReason)
				return true, http.StatusForbidden
			}
		}
		manifest, err := h.store.GetOCIManifest(r.Context(), repo.ID, imageName, reference)
		if errors.Is(err, repository.ErrNotFound) {
			return false, 0
		}
		if err != nil {
			writeOCIError(w, http.StatusInternalServerError, "UNKNOWN", "unable to read manifest")
			return true, http.StatusInternalServerError
		}
		blocked, blockErr := h.ociManifestReadBlocked(r.Context(), repo, manifest)
		if blockErr != nil {
			writeOCIError(w, http.StatusInternalServerError, "UNKNOWN", "unable to evaluate artifact quarantine")
			return true, http.StatusInternalServerError
		}
		if blocked {
			writeOCIError(w, http.StatusForbidden, "DENIED", repository.ArtifactQuarantinedReason)
			return true, http.StatusForbidden
		}
		if !ociAcceptsManifest(r.Header.Get("Accept"), manifest.MediaType) {
			writeOCIError(w, http.StatusNotAcceptable, "MANIFEST_UNKNOWN", "manifest media type is not acceptable")
			return true, http.StatusNotAcceptable
		}
		serveCachedOCIContent(w, r, reference, ociprotocol.NewStoredContent(manifest.Digest, manifest.MediaType, manifest.ObjectKey, manifest.Size, h.objects))
		return true, http.StatusOK
	}
	return false, 0
}
