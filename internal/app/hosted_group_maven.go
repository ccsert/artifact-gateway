package app

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// v2GroupMavenHandler serves Maven reads against a V2 HostedGroup. Hosted
// members are served from the native hosted store first; proxy members fall
// through to the shared upstream fetch and read-through cache. Writes are not
// supported on groups.
type v2GroupMavenHandler struct {
	native *nativeMavenHandler
	proxy  *MavenHandler
	auth   Authenticator
}

func (h v2GroupMavenHandler) serve(w http.ResponseWriter, r *http.Request, resolver v2GroupResolver, group repository.HostedGroup) {
	if h.native == nil || h.proxy == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	assetPath := strings.TrimPrefix(r.URL.Path, "/maven/"+group.Name+"/")
	if assetPath == r.URL.Path || assetPath == "" {
		http.NotFound(w, r)
		return
	}
	principal, ok := h.native.protocolPrincipal(r)
	if !ok {
		if group.AnonymousRead && anonymousReadMethod(r.Method) && anonymousAccessAllowed(r.Context(), resolver.groups) {
			principal = anonymousPrincipal()
		} else {
			w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway Maven"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	}
	members, err := resolver.resolveMembers(r.Context(), group)
	if err != nil {
		http.Error(w, "unable to resolve group members", http.StatusInternalServerError)
		return
	}
	if len(members) == 0 {
		resolver.auditResolution(r.Context(), group, repository.FormatMaven, assetPath, strings.ToLower(r.Method), principal.Actor, repository.AuditNotFound, http.StatusNotFound)
		http.NotFound(w, r)
		return
	}
	if isAnonymous(principal) {
		members = anonymousHostedGroupMembers(group, members)
	}
	var denied bool
	members, denied = resolver.authorizeMembers(r.Context(), principal, repository.FormatMaven, assetPath, members)
	if denied && len(members) == 0 {
		http.Error(w, "repository read permission required", http.StatusForbidden)
		return
	}
	if len(members) == 0 {
		resolver.auditResolution(r.Context(), group, repository.FormatMaven, assetPath, strings.ToLower(r.Method), principal.Actor, repository.AuditAccessDenied, http.StatusUnauthorized)
		w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway Maven"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	for _, member := range members {
		if member.Type == repository.MemberHosted {
			repo, err := resolver.repos.GetHostedRepository(r.Context(), member.RepositoryID)
			if err != nil {
				continue
			}
			if h.native.serveHostedRead(w, r, repo, assetPath, principal.Actor) {
				return
			}
			continue
		}
		h.proxy.serveResolvedMembers(w, r, group.Name, assetPath, principal.Actor, []repository.Member{member})
		return
	}
	resolver.auditResolution(r.Context(), group, repository.FormatMaven, assetPath, strings.ToLower(r.Method), principal.Actor, repository.AuditNotFound, http.StatusNotFound)
	http.NotFound(w, r)
}

// serveHostedRead attempts a read against a native hosted Maven repository
// without writing a response when the asset is absent, so the caller can fall
// through to the next group member. It reports whether the request was served.
func (h nativeMavenHandler) serveHostedRead(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, assetPath, actor string) bool {
	// The coordinate is the Group conflict boundary. A quarantined higher
	// priority coordinate must claim every classifier/extension even when that
	// exact asset name only exists in a lower member.
	if _, _, metadataRequest := mavenMetadataRequest(assetPath); !metadataRequest && h.blockQuarantinedRead(w, r, repo, assetPath, actor) {
		return true
	}
	asset, err := h.store.GetMavenAsset(r.Context(), repo.ID, assetPath)
	if err != nil {
		if snapshotAsset, found := h.snapshotAsset(r.Context(), repo.ID, assetPath); found {
			asset = snapshotAsset
			err = nil
		}
	}
	if err != nil {
		if _, _, metadataRequest := mavenMetadataRequest(assetPath); metadataRequest {
			if h.serveHostedMetadata(w, r, repo, assetPath) {
				return true
			}
		}
		return false
	}
	body, err := h.objects.Get(r.Context(), asset.ObjectKey)
	if err != nil {
		http.Error(w, "artifact unavailable", http.StatusServiceUnavailable)
		return true
	}
	w.Header().Set("ETag", `"`+strings.TrimPrefix(asset.Digest, "sha256:")+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(asset.Size, 10))
	if r.Method == http.MethodGet {
		_, _ = w.Write(body)
	}
	return true
}

// serveHostedMetadata renders generated Maven metadata for a hosted member
// when it can answer the request, reusing the native metadata generator. It
// reports whether metadata was available.
func (h nativeMavenHandler) serveHostedMetadata(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, path string) bool {
	metadataPath, _, ok := mavenMetadataRequest(path)
	if !ok {
		return false
	}
	items, err := h.store.ListMavenArtifacts(r.Context(), repo.ID)
	if err != nil {
		http.Error(w, "metadata unavailable", http.StatusServiceUnavailable)
		return true
	}
	prefix := strings.TrimSuffix(metadataPath, "/maven-metadata.xml")
	for _, item := range items {
		base := mavenCoordinatePath(item.Coordinate)
		if base == prefix || strings.HasPrefix(base, prefix+"/") {
			h.metadata(w, r, repo, path, "")
			return true
		}
	}
	return false
}
