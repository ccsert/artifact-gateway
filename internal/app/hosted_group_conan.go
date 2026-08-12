package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// v2GroupConanHandler serves Conan reads against a V2 HostedGroup. Hosted
// members are served from the native hosted store first; proxy members fall
// through to the shared upstream fetch and read-through cache.
type v2GroupConanHandler struct {
	conan *ConanHandler
	auth  Authenticator
}

func (h v2GroupConanHandler) serve(w http.ResponseWriter, r *http.Request, resolver v2GroupResolver, group repository.HostedGroup) {
	if h.conan == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	_, path, kind, file, ok := parseConanPath(r.Method, r.URL.EscapedPath())
	if !ok || kind == "revision" {
		http.NotFound(w, r)
		return
	}
	principal, authenticated := h.conan.authenticate(r)
	if !authenticated {
		if group.AnonymousRead && anonymousReadMethod(r.Method) && anonymousAccessAllowed(r.Context(), resolver.groups) {
			principal = anonymousPrincipal()
		} else {
			w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway Conan"`)
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
		resolver.auditResolution(r.Context(), group, repository.FormatConan, path, strings.ToLower(r.Method), principal.Actor, repository.AuditNotFound, http.StatusNotFound)
		http.NotFound(w, r)
		return
	}
	if isAnonymous(principal) {
		members = anonymousHostedGroupMembers(group, members)
	}
	var denied bool
	members, denied = resolver.authorizeMembers(r.Context(), principal, repository.FormatConan, path, members)
	if denied && len(members) == 0 {
		w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway Conan"`)
		http.Error(w, "repository read permission required", http.StatusForbidden)
		return
	}
	if len(members) == 0 {
		resolver.auditResolution(r.Context(), group, repository.FormatConan, path, strings.ToLower(r.Method), principal.Actor, repository.AuditAccessDenied, http.StatusUnauthorized)
		w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway Conan"`)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	for _, member := range members {
		if member.Type == repository.MemberHosted {
			repo, err := resolver.repos.GetHostedRepository(r.Context(), member.RepositoryID)
			if err != nil {
				continue
			}
			if h.conan.serveHostedRead(w, r, group.Name, repo, path, file, principal) {
				return
			}
			continue
		}
		repo, err := resolver.repos.GetHostedRepository(r.Context(), member.RepositoryID)
		if err != nil {
			continue
		}
		h.conan.serveV2GroupProxy(w, r, group.Name, repo, path, kind, file, principal)
		return
	}
	resolver.auditResolution(r.Context(), group, repository.FormatConan, path, strings.ToLower(r.Method), principal.Actor, repository.AuditNotFound, http.StatusNotFound)
	http.NotFound(w, r)
}

// serveHostedRead attempts a read against a native hosted Conan repository
// without writing a response when the content is absent, so the caller can
// fall through to the next group member. It reports whether the request was
// served.
func (h ConanHandler) serveHostedRead(w http.ResponseWriter, r *http.Request, groupName string, repo repository.HostedRepository, path, file string, principal Principal) bool {
	if h.NativeStore == nil || h.NativeObjects == nil {
		return false
	}
	content, ok, err := h.nativeConanContent(r.Context(), repo, path, file)
	if errors.Is(err, errConanArtifactQuarantined) {
		decision := AuthorizationDecision{Source: "quarantine_read_policy", Reason: repository.ArtifactQuarantinedReason}
		h.audit(withConanAuditAuthorization(withConanAuditStatus(r.Context(), http.StatusForbidden), decision), groupName, path, repo.Name, principal.Actor, repository.AuditAccessDenied)
		http.Error(w, repository.ArtifactQuarantinedReason, http.StatusForbidden)
		return true
	}
	if err != nil {
		http.Error(w, "unable to read native Conan artifact", http.StatusInternalServerError)
		return true
	}
	if !ok || conanContentIsEmptyListing(path, file, content) && !content.policyFiltered {
		return false
	}
	w.Header().Set("Content-Type", content.contentType)
	w.Header().Set("Content-Length", fmt.Sprint(len(content.body)))
	if r.Method == http.MethodGet {
		_, _ = w.Write(content.body)
	}
	return true
}

// conanContentIsEmptyListing reports whether a native metadata response is an
// empty listing. A hosted member with no matching revision still produces an
// empty JSON document, which must not shadow the group's proxy members.
func conanContentIsEmptyListing(path, file string, content conanCacheEntry) bool {
	if file != "" {
		return false
	}
	var listing struct {
		Revisions []json.RawMessage          `json:"revisions"`
		Files     map[string]json.RawMessage `json:"files"`
		Packages  map[string]json.RawMessage `json:"packages"`
	}
	if json.Unmarshal(content.body, &listing) != nil {
		return false
	}
	switch {
	case strings.HasSuffix(path, "/revisions"):
		return len(listing.Revisions) == 0
	case strings.HasSuffix(path, "/files"):
		return len(listing.Files) == 0
	case strings.HasSuffix(path, "/search"):
		return len(listing.Packages) == 0
	}
	return false
}

// serveV2GroupProxy serves a read against a single proxy member of a V2
// HostedGroup. The group name is the cache and audit namespace; the member
// binds the repository so grant evaluation stays on the same target. It reuses
// the Group proxy resolve path, including verification and read-through cache.
func (h ConanHandler) serveV2GroupProxy(w http.ResponseWriter, r *http.Request, groupName string, repo repository.HostedRepository, path, kind, file string, principal Principal) {
	member := repository.Member{Type: repository.MemberProxy, Name: repo.Name, Endpoint: repo.Endpoint, AllowedHosts: repo.AllowedHosts, EgressProxy: repo.EgressProxy, RepositoryID: repo.ID}
	g := repository.Group{Name: groupName, Enabled: true, Members: []repository.Member{member}}
	content, status, err := h.resolve(r.Context(), g, path, kind, r.Header, principal)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	h.serveResolvedConan(w, r, groupName, g, path, file, content, principal)
}
