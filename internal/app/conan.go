package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// ConanClient is deliberately narrower than a generic HTTP client: client
// credentials are never forwarded to an upstream member.
type ConanClient interface {
	FetchConan(context.Context, string, repository.Member, string, http.Header) (*http.Response, error)
}

func (c UpstreamClient) FetchConan(ctx context.Context, method string, member repository.Member, path string, headers http.Header) (*http.Response, error) {
	if member.Type == repository.MemberProxy {
		proxyURL, ips, err := resolveRawProxyEndpoint(ctx, member.Endpoint)
		if err != nil {
			return nil, err
		}
		client, err := rawProxyHTTPClient(c.HTTPClient, proxyURL.Hostname(), proxyURL.Port(), ips)
		if err != nil {
			return nil, err
		}
		return c.fetchConan(ctx, client, method, member, path, headers)
	}
	return c.fetchConan(ctx, c.HTTPClient, method, member, path, headers)
}

func (c UpstreamClient) fetchConan(ctx context.Context, client *http.Client, method string, member repository.Member, path string, headers http.Header) (*http.Response, error) {
	u, err := url.Parse(strings.TrimRight(member.Endpoint, "/") + "/conans/" + path)
	if err != nil {
		return nil, fmt.Errorf("parse Conan endpoint: %w", err)
	}
	r, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if accept := headers.Get("Accept"); accept != "" {
		r.Header.Set("Accept", accept)
	}
	client = tracedHTTPClient(client)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client.Do(r)
}

type conanCacheEntry struct {
	body                          []byte
	contentType, member, endpoint string
	cacheDisposition              string
	status                        int
}

type ConanHandler struct {
	Store         repository.ConanStore
	NativeStore   repository.NativeConanStore
	Repositories  repository.HostedRepositoryStore
	Authorizer    RepositoryAuthorizer
	Authenticator Authenticator
	Client        ConanClient
	Cache         *ConanCache
	NativeObjects OCIObjectStore
	Metrics       *Metrics
}

func (h ConanHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r = r.WithContext(withRawAuditCorrelation(r.Context(), r.Header.Get("X-Request-ID")))
	r = r.WithContext(withConanAuditState(r.Context(), r.Method, conanRepresentation(r.Header), conanCacheDisposition(h.Cache)))
	if h.Metrics != nil {
		h.Metrics.recordConanRequest(r.Method)
	}
	restorePath := r.Method == http.MethodPost && strings.HasSuffix(r.URL.EscapedPath(), ":restore")
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodDelete && !(r.Method == http.MethodPost && restorePath) {
		if group, path, ok := conanReadGroupAndPath(r.URL.EscapedPath()); ok {
			h.audit(withConanAuditStatus(r.Context(), http.StatusNotFound), group, path, "", "anonymous", repository.AuditNotFound)
			http.NotFound(w, r)
			return
		}
	}
	if group, ok := parseConanPing(r.Method, r.URL.Path); ok {
		if h.anonymousConanAllowed(r.Context(), group) {
			w.Header().Set("X-Conan-Server-Capabilities", "revisions")
			h.audit(withConanAuditDisposition(withConanAuditStatus(r.Context(), http.StatusOK), "bypass"), group, "", "", "anonymous", repository.AuditResolved)
			w.WriteHeader(http.StatusOK)
			return
		}
		principal, authenticated := h.authenticate(r)
		configured, err := h.Store.GetConanGroup(r.Context(), group)
		if err != nil || !configured.Enabled {
			h.audit(withConanAuditStatus(r.Context(), http.StatusNotFound), group, "", "", principal.Actor, auditOutcome(err))
			http.NotFound(w, r)
			return
		}
		if authenticated && h.canReadConanGroup(r.Context(), principal, configured) {
			w.Header().Set("X-Conan-Server-Capabilities", "revisions")
			h.audit(withConanAuditDisposition(withConanAuditStatus(r.Context(), http.StatusOK), "bypass"), group, "", "", principal.Actor, repository.AuditResolved)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway Conan"`)
		actor := "anonymous"
		status := http.StatusUnauthorized
		if authenticated {
			actor = principal.Actor
			status = http.StatusForbidden
		}
		h.audit(withConanAuditStatus(r.Context(), status), group, "", "", actor, repository.AuditAccessDenied)
		if authenticated {
			http.Error(w, "repository read permission required", status)
			return
		}
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if group, ok := parseConanAuthenticate(r.Method, r.URL.Path); ok {
		principal, authenticated := h.authenticate(r)
		configured, err := h.Store.GetConanGroup(r.Context(), group)
		if err != nil || !configured.Enabled {
			h.audit(withConanAuditStatus(r.Context(), http.StatusNotFound), group, "", "", principal.Actor, auditOutcome(err))
			http.NotFound(w, r)
			return
		}
		if !authenticated {
			w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway Conan"`)
			h.audit(withConanAuditStatus(r.Context(), http.StatusUnauthorized), group, "", "", "anonymous", repository.AuditAccessDenied)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		if !h.canReadConanGroup(r.Context(), principal, configured) {
			h.audit(withConanAuditStatus(r.Context(), http.StatusForbidden), group, "", "", principal.Actor, repository.AuditAccessDenied)
			http.Error(w, "repository read permission required", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		h.audit(withConanAuditDisposition(withConanAuditStatus(r.Context(), http.StatusOK), "bypass"), group, "", "", principal.Actor, repository.AuditResolved)
		_, _ = w.Write([]byte(`{"user_name":` + strconv.Quote(principal.Actor) + `}`))
		return
	}
	group, path, kind, file, ok := parseConanPath(r.Method, r.URL.EscapedPath())
	if !ok {
		if malformedConanPath(r.URL.EscapedPath()) {
			h.audit(withConanAuditStatus(r.Context(), http.StatusBadRequest), "", "", "", "anonymous", repository.AuditUpstreamError)
			http.Error(w, "invalid Conan path", http.StatusBadRequest)
			return
		}
		h.audit(withConanAuditStatus(r.Context(), http.StatusNotFound), "", "", "", "anonymous", repository.AuditNotFound)
		http.NotFound(w, r)
		return
	}
	p, authenticated := h.authenticate(r)
	if !authenticated && h.anonymousConanAllowed(r.Context(), group) {
		p = Principal{Actor: "anonymous"}
		authenticated = true
	}
	if !authenticated {
		w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway Conan"`)
		h.audit(withConanAuditStatus(r.Context(), http.StatusUnauthorized), group, path, "", "anonymous", repository.AuditAccessDenied)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodDelete {
		if h.deleteNativeConan(w, r, group, path, p) {
			return
		}
		h.audit(r.Context(), group, path, "", p.Actor, repository.AuditNotFound)
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodPost {
		if h.restoreNativeConan(w, r, group, path, p) {
			return
		}
		h.audit(r.Context(), group, path, "", p.Actor, repository.AuditNotFound)
		http.NotFound(w, r)
		return
	}
	if h.serveNativeConan(w, r, group, path, file, p) {
		return
	}
	g, err := h.Store.GetConanGroup(r.Context(), group)
	if err != nil || !g.Enabled {
		h.audit(r.Context(), group, path, "", p.Actor, auditOutcome(err))
		http.NotFound(w, r)
		return
	}
	if p.Actor != "anonymous" && !h.canReadConanGroup(r.Context(), p, g) {
		h.audit(r.Context(), group, path, "", p.Actor, repository.AuditAccessDenied)
		http.Error(w, "repository read permission required", http.StatusForbidden)
		return
	}
	if h.Metrics != nil {
		h.Metrics.recordRequest(group)
		if p.Actor == "anonymous" {
			h.Metrics.recordAnonymousRead()
		}
	}
	content, status, err := h.resolve(r.Context(), g, path, kind, r.Header, p)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	if file != "" && !h.verifyFile(r.Context(), g, path, file, content, p) {
		h.audit(withConanAuditChecksum(r.Context()), group, path, content.member, p.Actor, repository.AuditUpstreamError)
		http.Error(w, "Conan file checksum mismatch", http.StatusBadGateway)
		return
	}
	if file != "" && h.Cache != nil {
		representation := conanRepresentation(r.Header)
		if err := h.Cache.store(r.Context(), h.Cache.key(group, path, repository.Member{Name: content.member, Endpoint: content.endpoint}, representation), content, group, g.CacheQuotaBytes, 15*time.Minute, group, path, representation); errors.Is(err, ErrCacheQuotaExceeded) {
			if h.Metrics != nil {
				h.Metrics.cacheQuotaDenied.Add(1)
				h.Metrics.conanCacheQuotaDenied.Add(1)
			}
		} else if err != nil {
			h.audit(r.Context(), group, path, content.member, p.Actor, repository.AuditUpstreamError)
			http.Error(w, "unable to cache Conan content", http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", content.contentType)
	w.Header().Set("Content-Length", fmt.Sprint(len(content.body)))
	servedBytes := int64(0)
	if r.Method == http.MethodGet {
		n, _ := w.Write(content.body)
		servedBytes = int64(n)
	}
	h.audit(withConanAuditBytes(withConanAuditStatus(withConanAuditDisposition(r.Context(), content.cacheDisposition), content.status), servedBytes), group, path, content.member, p.Actor, repository.AuditResolved)
}

func (h ConanHandler) restoreNativeConan(w http.ResponseWriter, r *http.Request, name, path string, principal Principal) bool {
	if h.NativeStore == nil || h.Repositories == nil {
		return false
	}
	repo, err := h.Repositories.GetHostedRepositoryByName(r.Context(), name)
	if err != nil || repo.Format != repository.FormatConan || repo.State != repository.RepositoryActive {
		return false
	}
	decision := h.Authorizer.AuthorizeResource(r.Context(), principal, repo, RepositoryWrite, strings.TrimSuffix(path, ":restore"))
	if !decision.Allowed {
		h.audit(withConanAuditAuthorization(r.Context(), decision), name, path, "native", principal.Actor, repository.AuditAccessDenied)
		http.Error(w, "repository write permission required", http.StatusForbidden)
		return true
	}
	parts := strings.Split(path, "/")
	if len(parts) == 6 && parts[4] == "revisions" && strings.HasSuffix(parts[5], ":restore") {
		_, err = h.NativeStore.RestoreConanRecipeRevision(r.Context(), repo.ID, strings.Join(parts[:4], "/"), strings.TrimSuffix(parts[5], ":restore"))
	} else if len(parts) == 10 && parts[4] == "revisions" && parts[6] == "packages" && parts[8] == "revisions" && strings.HasSuffix(parts[9], ":restore") {
		_, err = h.NativeStore.RestoreConanPackageRevision(r.Context(), repo.ID, strings.Join(parts[:4], "/"), parts[5], parts[7], strings.TrimSuffix(parts[9], ":restore"))
	} else {
		return false
	}
	if errors.Is(err, repository.ErrNotFound) || errors.Is(err, repository.ErrDisabled) {
		h.audit(r.Context(), name, path, "native", principal.Actor, repository.AuditNotFound)
		http.Error(w, "Conan revision cannot be restored", http.StatusConflict)
		return true
	}
	if err != nil {
		h.audit(r.Context(), name, path, "native", principal.Actor, repository.AuditUpstreamError)
		http.Error(w, "unable to restore native Conan artifact", http.StatusInternalServerError)
		return true
	}
	h.audit(withConanAuditStatus(r.Context(), http.StatusNoContent), name, path, "native", principal.Actor, repository.AuditResolved)
	w.WriteHeader(http.StatusNoContent)
	return true
}

// deleteNativeConan only accepts a fully qualified revision. Deleting a file
// would make a visible revision internally inconsistent, and deleting a
// reference would bypass the revision-level tombstone lifecycle.
func (h ConanHandler) deleteNativeConan(w http.ResponseWriter, r *http.Request, name, path string, principal Principal) bool {
	if h.NativeStore == nil || h.Repositories == nil {
		return false
	}
	repo, err := h.Repositories.GetHostedRepositoryByName(r.Context(), name)
	if err != nil || repo.Format != repository.FormatConan || repo.State != repository.RepositoryActive {
		return false
	}
	decision := h.Authorizer.AuthorizeResource(r.Context(), principal, repo, RepositoryWrite, path)
	if !decision.Allowed {
		h.audit(withConanAuditAuthorization(r.Context(), decision), name, path, "native", principal.Actor, repository.AuditAccessDenied)
		http.Error(w, "repository write permission required", http.StatusForbidden)
		return true
	}
	parts := strings.Split(path, "/")
	if len(parts) == 6 && parts[4] == "revisions" {
		_, err = h.NativeStore.TombstoneConanRecipeRevision(r.Context(), repo.ID, strings.Join(parts[:4], "/"), parts[5])
	} else if len(parts) == 10 && parts[4] == "revisions" && parts[6] == "packages" && parts[8] == "revisions" {
		_, err = h.NativeStore.TombstoneConanPackageRevision(r.Context(), repo.ID, strings.Join(parts[:4], "/"), parts[5], parts[7], parts[9])
	} else {
		h.audit(r.Context(), name, path, "native", principal.Actor, repository.AuditNotFound)
		http.NotFound(w, r)
		return true
	}
	if errors.Is(err, repository.ErrNotFound) {
		h.audit(r.Context(), name, path, "native", principal.Actor, repository.AuditNotFound)
		http.NotFound(w, r)
		return true
	}
	if err != nil {
		h.audit(r.Context(), name, path, "native", principal.Actor, repository.AuditUpstreamError)
		http.Error(w, "unable to delete native Conan artifact", http.StatusInternalServerError)
		return true
	}
	h.audit(withConanAuditStatus(r.Context(), http.StatusNoContent), name, path, "native", principal.Actor, repository.AuditResolved)
	w.WriteHeader(http.StatusNoContent)
	return true
}

func (h ConanHandler) serveNativeConan(w http.ResponseWriter, r *http.Request, name, path, file string, principal Principal) bool {
	if h.NativeStore == nil || h.NativeObjects == nil || h.Repositories == nil {
		return false
	}
	repo, err := h.Repositories.GetHostedRepositoryByName(r.Context(), name)
	if err != nil || repo.Format != repository.FormatConan || repo.State != repository.RepositoryActive {
		return false
	}
	decision := h.Authorizer.AuthorizeResource(r.Context(), principal, repo, RepositoryRead, path)
	if !decision.Allowed {
		h.audit(withConanAuditAuthorization(r.Context(), decision), name, path, "native", principal.Actor, repository.AuditAccessDenied)
		http.Error(w, "repository read permission required", http.StatusForbidden)
		return true
	}
	content, ok, err := h.nativeConanContent(r.Context(), repo, path, file)
	if err != nil {
		h.audit(r.Context(), name, path, "native", principal.Actor, repository.AuditUpstreamError)
		http.Error(w, "unable to read native Conan artifact", http.StatusInternalServerError)
		return true
	}
	if !ok {
		h.audit(r.Context(), name, path, "native", principal.Actor, repository.AuditNotFound)
		http.NotFound(w, r)
		return true
	}
	w.Header().Set("Content-Type", content.contentType)
	w.Header().Set("Content-Length", fmt.Sprint(len(content.body)))
	servedBytes := int64(0)
	if r.Method == http.MethodGet {
		n, _ := w.Write(content.body)
		servedBytes = int64(n)
	}
	h.audit(withConanAuditBytes(withConanAuditStatus(withConanAuditDisposition(r.Context(), "bypass"), http.StatusOK), servedBytes), name, path, "native", principal.Actor, repository.AuditResolved)
	return true
}

func (h ConanHandler) nativeConanContent(ctx context.Context, repo repository.HostedRepository, path, file string) (conanCacheEntry, bool, error) {
	parts := strings.Split(path, "/")
	if len(parts) < 5 {
		return conanCacheEntry{}, false, nil
	}
	reference := strings.Join(parts[:4], "/")
	if len(parts) == 5 && parts[4] == "revisions" {
		revisions, err := h.NativeStore.ListConanRecipeRevisions(ctx, repo.ID, reference)
		if err != nil {
			return conanCacheEntry{}, false, err
		}
		body, _ := json.Marshal(struct {
			Revisions []conanRevisionJSON `json:"revisions"`
		}{Revisions: conanRecipeRevisionJSON(revisions)})
		return conanCacheEntry{body: body, contentType: "application/json", member: "native", endpoint: repo.Name, status: http.StatusOK, cacheDisposition: "bypass"}, true, nil
	}
	if len(parts) >= 7 && parts[4] == "revisions" {
		recipeRevision := parts[5]
		visibleRecipe, err := h.nativeConanVisibleRecipe(ctx, repo.ID, reference, recipeRevision)
		if err != nil || !visibleRecipe {
			return conanCacheEntry{}, visibleRecipe, err
		}
		if len(parts) == 7 && parts[6] == "files" {
			assets, err := h.NativeStore.ListConanRecipeAssets(ctx, repo.ID, reference, recipeRevision)
			if err != nil {
				return conanCacheEntry{}, false, err
			}
			body, _ := json.Marshal(nativeConanFilesJSON(assets))
			return conanCacheEntry{body: body, contentType: "application/json", member: "native", endpoint: repo.Name, status: http.StatusOK, cacheDisposition: "bypass"}, true, nil
		}
		if len(parts) == 7 && parts[6] == "search" {
			ids, err := h.NativeStore.ListConanPackageIDs(ctx, repo.ID, reference, recipeRevision)
			if err != nil {
				return conanCacheEntry{}, false, err
			}
			packages := make(map[string]map[string]any, len(ids))
			for _, id := range ids {
				packages[id] = map[string]any{}
			}
			body, _ := json.Marshal(struct {
				Packages map[string]map[string]any `json:"packages"`
			}{Packages: packages})
			return conanCacheEntry{body: body, contentType: "application/json", member: "native", endpoint: repo.Name, status: http.StatusOK, cacheDisposition: "bypass"}, true, nil
		}
		if len(parts) == 8 && parts[6] == "files" {
			asset, err := h.NativeStore.GetConanRecipeAsset(ctx, repo.ID, reference, recipeRevision, parts[7])
			return h.nativeConanFile(ctx, asset, err)
		}
		if len(parts) == 9 && parts[6] == "packages" && parts[8] == "revisions" {
			revisions, err := h.NativeStore.ListConanPackageRevisions(ctx, repo.ID, reference, recipeRevision, parts[7])
			if err != nil {
				return conanCacheEntry{}, false, err
			}
			body, _ := json.Marshal(struct {
				Revisions []conanRevisionJSON `json:"revisions"`
			}{Revisions: conanPackageRevisionJSON(revisions)})
			return conanCacheEntry{body: body, contentType: "application/json", member: "native", endpoint: repo.Name, status: http.StatusOK, cacheDisposition: "bypass"}, true, nil
		}
		if len(parts) == 11 && parts[6] == "packages" && parts[8] == "revisions" && parts[10] == "files" {
			visiblePackage, err := h.nativeConanVisiblePackage(ctx, repo.ID, reference, recipeRevision, parts[7], parts[9])
			if err != nil || !visiblePackage {
				return conanCacheEntry{}, visiblePackage, err
			}
			assets, err := h.NativeStore.ListConanPackageAssets(ctx, repo.ID, reference, recipeRevision, parts[7], parts[9])
			if err != nil {
				return conanCacheEntry{}, false, err
			}
			body, _ := json.Marshal(nativeConanFilesJSON(assets))
			return conanCacheEntry{body: body, contentType: "application/json", member: "native", endpoint: repo.Name, status: http.StatusOK, cacheDisposition: "bypass"}, true, nil
		}
		if len(parts) == 12 && parts[6] == "packages" && parts[8] == "revisions" && parts[10] == "files" {
			visiblePackage, err := h.nativeConanVisiblePackage(ctx, repo.ID, reference, recipeRevision, parts[7], parts[9])
			if err != nil || !visiblePackage {
				return conanCacheEntry{}, visiblePackage, err
			}
			asset, err := h.NativeStore.GetConanPackageAsset(ctx, repo.ID, reference, recipeRevision, parts[7], parts[9], parts[11])
			return h.nativeConanFile(ctx, asset, err)
		}
	}
	return conanCacheEntry{}, false, nil
}

func (h ConanHandler) nativeConanVisibleRecipe(ctx context.Context, repositoryID, reference, revision string) (bool, error) {
	recipe, err := h.NativeStore.GetConanRecipeRevision(ctx, repositoryID, reference, revision)
	if errors.Is(err, repository.ErrNotFound) {
		return false, nil
	}
	return err == nil && recipe.State == "visible", err
}

func (h ConanHandler) nativeConanVisiblePackage(ctx context.Context, repositoryID, reference, recipeRevision, packageID, revision string) (bool, error) {
	pkg, err := h.NativeStore.GetConanPackageRevision(ctx, repositoryID, reference, recipeRevision, packageID, revision)
	if errors.Is(err, repository.ErrNotFound) {
		return false, nil
	}
	return err == nil && pkg.State == "visible", err
}

func (h ConanHandler) nativeConanFile(ctx context.Context, asset repository.ConanAsset, err error) (conanCacheEntry, bool, error) {
	if errors.Is(err, repository.ErrNotFound) {
		return conanCacheEntry{}, false, nil
	}
	if err != nil {
		return conanCacheEntry{}, false, err
	}
	reader, size, err := h.NativeObjects.Open(ctx, asset.ObjectKey)
	if err != nil {
		return conanCacheEntry{}, false, err
	}
	defer reader.Close()
	body, err := io.ReadAll(io.LimitReader(reader, size+1))
	if err != nil || int64(len(body)) != size {
		return conanCacheEntry{}, false, errors.New("native Conan object size mismatch")
	}
	return conanCacheEntry{body: body, contentType: "application/octet-stream", member: "native", status: http.StatusOK, cacheDisposition: "bypass"}, true, nil
}

type conanRevisionJSON struct {
	Revision string `json:"revision"`
	Time     string `json:"time"`
}

func conanRecipeRevisionJSON(revisions []repository.ConanRecipeRevision) []conanRevisionJSON {
	out := make([]conanRevisionJSON, 0, len(revisions))
	for _, revision := range revisions {
		out = append(out, conanRevisionJSON{Revision: revision.Revision, Time: revision.CreatedAt.UTC().Format(time.RFC3339)})
	}
	return out
}

func conanPackageRevisionJSON(revisions []repository.ConanPackageRevision) []conanRevisionJSON {
	out := make([]conanRevisionJSON, 0, len(revisions))
	for _, revision := range revisions {
		out = append(out, conanRevisionJSON{Revision: revision.Revision, Time: revision.CreatedAt.UTC().Format(time.RFC3339)})
	}
	return out
}

func nativeConanFilesJSON(assets []repository.ConanAsset) struct {
	Files map[string]struct {
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	} `json:"files"`
} {
	files := map[string]struct {
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	}{}
	for _, asset := range assets {
		files[asset.Path] = struct {
			SHA256 string `json:"sha256"`
			Size   int64  `json:"size"`
		}{SHA256: strings.TrimPrefix(asset.Digest, "sha256:"), Size: asset.Size}
	}
	return struct {
		Files map[string]struct {
			SHA256 string `json:"sha256"`
			Size   int64  `json:"size"`
		} `json:"files"`
	}{Files: files}
}

func (h ConanHandler) authenticate(request *http.Request) (Principal, bool) {
	if principal, ok := h.Authenticator.Authenticate(request.Header.Get("Authorization")); ok {
		return principal, true
	}
	username, password, ok := request.BasicAuth()
	if !ok {
		return Principal{}, false
	}
	return h.Authenticator.AuthenticateBasic(username, password)
}

func (h ConanHandler) resolve(ctx context.Context, group repository.Group, path, kind string, headers http.Header, principal Principal) (conanCacheEntry, int, error) {
	actor := principal.Actor
	metadata := kind == "metadata"
	hadFailure, denied, accessDenied := false, false, false
	members := group.Members
	if actor == "anonymous" {
		members = make([]repository.Member, 0, len(group.Members))
		for _, member := range group.Members {
			if member.Anonymous {
				members = append(members, member)
			}
		}
	}
	for _, member := range prioritizeHosted(members) {
		if actor != "anonymous" {
			if decision, managed := h.managedConanMemberDecision(ctx, principal, member, path); managed {
				if !decision.Allowed {
					h.audit(withConanAuditAuthorization(ctx, decision), group.Name, path, member.Name, actor, repository.AuditAccessDenied)
					accessDenied = true
					continue
				}
			} else if !h.Authenticator.CanReadRepository(principal, member.Name) {
				h.audit(ctx, group.Name, path, member.Name, actor, repository.AuditAccessDenied)
				return conanCacheEntry{}, http.StatusForbidden, errors.New("repository read permission required")
			}
		}
		key := ""
		representation := conanRepresentation(headers)
		if h.Cache != nil {
			key = h.Cache.key(group.Name, path, member, representation)
			if e, ok := h.Cache.load(ctx, key); ok {
				if !conanCacheSourceMatches(e, member) {
					h.Cache.Invalidate(ctx, group.Name, path, member)
				} else if e.status == http.StatusNotFound {
					if h.Metrics != nil {
						h.Metrics.recordConanNegativeCacheHit()
					}
					h.audit(withConanAuditDisposition(ctx, "hit"), group.Name, path, member.Name, actor, repository.AuditNotFound)
					continue
				} else {
					if h.Metrics != nil {
						h.Metrics.recordConanCacheHit()
					}
					e.cacheDisposition = "hit"
					return e, http.StatusOK, nil
				}
			}
			if h.Metrics != nil {
				h.Metrics.recordConanCacheMiss()
			}
		}
		if member.Type == repository.MemberProxy && (h.Cache == nil || !h.Cache.proxyAllowed(member)) {
			h.audit(ctx, group.Name, path, member.Name, actor, repository.AuditProxyDenied)
			denied = true
			continue
		}
		if h.Cache != nil && kind != "file" {
			release, lockErr := h.Cache.AcquireRequestLock(ctx, key)
			if lockErr != nil {
				return conanCacheEntry{}, http.StatusServiceUnavailable, errors.New("unable to coordinate Conan cache fetch")
			}
			defer release()
			if e, ok := h.Cache.load(ctx, key); ok {
				if !conanCacheSourceMatches(e, member) {
					h.Cache.Invalidate(ctx, group.Name, path, member)
				} else if e.status == http.StatusNotFound {
					if h.Metrics != nil {
						h.Metrics.recordConanNegativeCacheHit()
					}
					h.audit(withConanAuditDisposition(ctx, "hit"), group.Name, path, member.Name, actor, repository.AuditNotFound)
					continue
				} else {
					if h.Metrics != nil {
						h.Metrics.recordConanCacheHit()
					}
					e.cacheDisposition = "hit"
					return e, http.StatusOK, nil
				}
			}
		}
		response, err := h.Client.FetchConan(ctx, http.MethodGet, member, path, headers)
		if err != nil {
			h.audit(ctx, group.Name, path, member.Name, actor, repository.AuditUpstreamError)
			hadFailure = true
			continue
		}
		limit := defaultRawMaxObjectBytes
		if h.Cache != nil && h.Cache.MaxObjectBytes() > 0 {
			limit = h.Cache.MaxObjectBytes()
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
		_ = response.Body.Close()
		if readErr != nil || int64(len(body)) > limit || response.StatusCode >= 500 || response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests {
			h.audit(withConanAuditStatus(ctx, response.StatusCode), group.Name, path, member.Name, actor, repository.AuditUpstreamError)
			hadFailure = true
			continue
		}
		if response.StatusCode == http.StatusNotFound {
			if h.Cache != nil {
				_ = h.Cache.store(ctx, key, conanCacheEntry{status: http.StatusNotFound, member: member.Name, endpoint: member.Endpoint}, group.Name, group.CacheQuotaBytes, time.Minute, group.Name, path, representation)
			}
			h.audit(ctx, group.Name, path, member.Name, actor, repository.AuditNotFound)
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			h.audit(withConanAuditStatus(ctx, response.StatusCode), group.Name, path, member.Name, actor, repository.AuditUpstreamError)
			hadFailure = true
			continue
		}
		if metadata && !validConanMetadata(path, body) {
			h.audit(withConanAuditStatus(ctx, response.StatusCode), group.Name, path, member.Name, actor, repository.AuditUpstreamError)
			return conanCacheEntry{}, http.StatusBadRequest, errors.New("invalid Conan metadata")
		}
		e := conanCacheEntry{body: body, contentType: response.Header.Get("Content-Type"), member: member.Name, endpoint: member.Endpoint, status: response.StatusCode, cacheDisposition: conanCacheDisposition(h.Cache)}
		if e.contentType == "" {
			if metadata {
				e.contentType = "application/json"
			} else {
				e.contentType = "application/octet-stream"
			}
		}
		if h.Cache != nil && kind != "file" {
			ttl := 15 * time.Minute
			if metadata {
				ttl = time.Minute
			}
			if err := h.Cache.store(ctx, key, e, group.Name, group.CacheQuotaBytes, ttl, group.Name, path, representation); errors.Is(err, ErrCacheQuotaExceeded) {
				if h.Metrics != nil {
					h.Metrics.cacheQuotaDenied.Add(1)
					h.Metrics.conanCacheQuotaDenied.Add(1)
				}
			} else if err != nil {
				h.audit(ctx, group.Name, path, member.Name, actor, repository.AuditUpstreamError)
				return conanCacheEntry{}, http.StatusInternalServerError, errors.New("unable to cache Conan content")
			}
		}
		return e, http.StatusOK, nil
	}
	if hadFailure {
		return conanCacheEntry{}, http.StatusBadGateway, errors.New("upstream repository unavailable")
	}
	if accessDenied {
		return conanCacheEntry{}, http.StatusForbidden, errors.New("repository read permission required")
	}
	if denied {
		return conanCacheEntry{}, http.StatusForbidden, errors.New("upstream repository is not allowed")
	}
	return conanCacheEntry{}, http.StatusNotFound, errors.New("conan resource not found")
}

func (h ConanHandler) canReadConanGroup(ctx context.Context, principal Principal, group repository.Group) bool {
	if h.Authenticator.CanReadMavenRepository(principal, group.Name) {
		return true
	}
	for _, member := range group.Members {
		if decision, managed := h.managedConanMemberDecision(ctx, principal, member, ""); managed && decision.Allowed {
			return true
		}
	}
	return false
}

func (h ConanHandler) managedConanMemberDecision(ctx context.Context, principal Principal, member repository.Member, resource string) (AuthorizationDecision, bool) {
	access := groupMemberAccess{Repositories: h.Repositories, Authorizer: h.Authorizer, Format: repository.FormatConan}
	return access.managedDecision(ctx, principal, member, resource)
}

func conanCacheSourceMatches(entry conanCacheEntry, member repository.Member) bool {
	return entry.member == member.Name && entry.endpoint == member.Endpoint
}
func conanRepresentation(headers http.Header) string { return strings.TrimSpace(headers.Get("Accept")) }

func (h ConanHandler) verifyFile(ctx context.Context, group repository.Group, path, file string, content conanCacheEntry, principal Principal) bool {
	metadataPath := path[:strings.LastIndex(path, "/files/")] + "/files"
	metadata, status, err := h.resolve(ctx, group, metadataPath, "metadata", http.Header{}, principal)
	if err != nil || status != http.StatusOK {
		return false
	}
	var value struct {
		Files map[string]struct {
			SHA256 string `json:"sha256"`
			Size   int64  `json:"size"`
		} `json:"files"`
	}
	if json.Unmarshal(metadata.body, &value) != nil {
		return false
	}
	want, found := value.Files[file]
	sum := sha256.Sum256(content.body)
	return found && want.Size == int64(len(content.body)) && want.SHA256 == hex.EncodeToString(sum[:])
}

type conanAuditState struct {
	method, representation, cacheDisposition string
	authorizationSource, authorizationReason string
	bytes                                    int64
	checksumFailure                          bool
	status                                   int
}
type conanAuditStateKey struct{}

func withConanAuditState(ctx context.Context, method, representation, disposition string) context.Context {
	return context.WithValue(ctx, conanAuditStateKey{}, conanAuditState{method: strings.ToLower(method), representation: representation, cacheDisposition: disposition})
}
func withConanAuditDisposition(ctx context.Context, disposition string) context.Context {
	state, _ := ctx.Value(conanAuditStateKey{}).(conanAuditState)
	state.cacheDisposition = disposition
	return context.WithValue(ctx, conanAuditStateKey{}, state)
}
func withConanAuditBytes(ctx context.Context, bytes int64) context.Context {
	state, _ := ctx.Value(conanAuditStateKey{}).(conanAuditState)
	state.bytes = bytes
	return context.WithValue(ctx, conanAuditStateKey{}, state)
}
func withConanAuditChecksum(ctx context.Context) context.Context {
	state, _ := ctx.Value(conanAuditStateKey{}).(conanAuditState)
	state.checksumFailure = true
	return context.WithValue(ctx, conanAuditStateKey{}, state)
}
func withConanAuditStatus(ctx context.Context, status int) context.Context {
	state, _ := ctx.Value(conanAuditStateKey{}).(conanAuditState)
	state.status = status
	return context.WithValue(ctx, conanAuditStateKey{}, state)
}
func withConanAuditAuthorization(ctx context.Context, decision AuthorizationDecision) context.Context {
	state, _ := ctx.Value(conanAuditStateKey{}).(conanAuditState)
	state.authorizationSource = decision.Source
	state.authorizationReason = decision.Reason
	return context.WithValue(ctx, conanAuditStateKey{}, state)
}
func conanCacheDisposition(cache *ConanCache) string {
	if cache == nil {
		return "bypass"
	}
	return "miss"
}

func (h ConanHandler) audit(ctx context.Context, group, path, member, actor string, outcome repository.AuditOutcome) {
	if actor == "" {
		actor = "anonymous"
	}
	state, _ := ctx.Value(conanAuditStateKey{}).(conanAuditState)
	selected := repository.Member{Name: member}
	if configured, err := h.Store.GetConanGroup(ctx, group); err == nil {
		for _, candidate := range configured.Members {
			if candidate.Name == member {
				selected = candidate
				break
			}
		}
	}
	upstreamHost := ""
	if endpoint, err := url.Parse(selected.Endpoint); err == nil {
		upstreamHost = endpoint.Hostname()
	}
	status := http.StatusNotFound
	switch outcome {
	case repository.AuditResolved:
		status = http.StatusOK
	case repository.AuditAccessDenied, repository.AuditProxyDenied, repository.AuditGroupDisabled:
		status = http.StatusForbidden
	case repository.AuditUpstreamError:
		status = http.StatusBadGateway
	}
	if state.status != 0 {
		status = state.status
	}
	bytes := state.bytes
	_ = h.Store.RecordAudit(ctx, repository.AuditRecord{GroupName: group, Repository: path, MemberName: member, Actor: actor, Outcome: outcome, OccurredAt: time.Now().UTC(), Format: "conan", Resource: path, Representation: state.representation, MemberType: string(selected.Type), UpstreamHost: upstreamHost, Operation: state.method, Status: status, CacheDisposition: state.cacheDisposition, Bytes: bytes, AuthorizationSource: state.authorizationSource, AuthorizationReason: state.authorizationReason, RequestID: rawAuditRequestID(ctx), TraceID: rawAuditTraceID(ctx)})
	if h.Metrics != nil {
		h.Metrics.recordConanAudit(outcome, state.bytes, state.checksumFailure)
		if outcome == repository.AuditAccessDenied {
			h.Metrics.recordRepositoryAuthorizationDenied("conan", state.authorizationSource, state.authorizationReason)
		}
	}
}

func parseConanPath(method, raw string) (group, path, kind, file string, ok bool) {
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodDelete && method != http.MethodPost {
		return
	}
	parts := strings.Split(strings.TrimPrefix(raw, "/"), "/")
	if len(parts) < 8 || parts[0] != "conan" {
		return
	}
	var rest []string
	if parts[1] == "v2" && parts[3] == "conans" {
		group, rest = parts[2], parts[4:]
	} else if parts[2] == "v2" && parts[3] == "conans" {
		group, rest = parts[1], parts[4:]
	} else {
		return
	}
	for _, part := range append([]string{group}, rest...) {
		if !validConanSegment(part) {
			return
		}
	}
	if len(rest) < 5 {
		return
	}
	// recipe: n/v/u/c/revisions; package appends rrev/packages/id/revisions.
	if len(rest) == 5 && rest[4] == "revisions" {
		path = strings.Join(rest, "/")
		kind = "metadata"
		ok = true
		return
	}
	if len(rest) >= 6 && rest[4] == "revisions" {
		if method == http.MethodPost && len(rest) == 6 && strings.HasSuffix(rest[5], ":restore") && strings.TrimSuffix(rest[5], ":restore") != "" {
			path = strings.Join(rest, "/")
			kind = "revision"
			ok = true
			return
		}
		if method == http.MethodDelete && len(rest) == 6 {
			path = strings.Join(rest, "/")
			kind = "revision"
			ok = true
			return
		}
		if len(rest) == 7 && rest[6] == "search" {
			path = strings.Join(rest, "/")
			kind = "metadata"
			ok = true
			return
		}
		if len(rest) == 7 && rest[6] == "files" {
			path = strings.Join(rest, "/")
			kind = "metadata"
			ok = true
			return
		}
		if len(rest) == 8 && rest[6] == "files" {
			path = strings.Join(rest, "/")
			kind = "file"
			file = rest[7]
			ok = true
			return
		}
		if len(rest) == 9 && rest[6] == "packages" && rest[8] == "revisions" {
			path = strings.Join(rest, "/")
			kind = "metadata"
			ok = true
			return
		}
		if len(rest) == 9 && rest[6] == "packages" && rest[8] == "latest" {
			path = strings.Join(rest, "/")
			// Conan 2 uses this to select an omitted package revision. It is
			// metadata, so it must be validated and use the metadata TTL.
			kind = "metadata"
			ok = true
			return
		}
		if method == http.MethodDelete && len(rest) == 10 && rest[6] == "packages" && rest[8] == "revisions" {
			path = strings.Join(rest, "/")
			kind = "revision"
			ok = true
			return
		}
		if method == http.MethodPost && len(rest) == 10 && rest[6] == "packages" && rest[8] == "revisions" && strings.HasSuffix(rest[9], ":restore") && strings.TrimSuffix(rest[9], ":restore") != "" {
			path = strings.Join(rest, "/")
			kind = "revision"
			ok = true
			return
		}
		if len(rest) == 11 && rest[6] == "packages" && rest[8] == "revisions" && rest[10] == "files" {
			path = strings.Join(rest, "/")
			kind = "metadata"
			ok = true
			return
		}
		if len(rest) == 12 && rest[6] == "packages" && rest[8] == "revisions" && rest[10] == "files" {
			path = strings.Join(rest, "/")
			kind = "file"
			file = rest[11]
			ok = true
			return
		}
	}
	return
}

func conanReadGroupAndPath(raw string) (group, path string, ok bool) {
	parts := strings.Split(strings.Trim(raw, "/"), "/")
	if len(parts) < 5 || parts[0] != "conan" {
		return "", "", false
	}
	if parts[1] == "v2" && parts[3] == "conans" {
		return parts[2], strings.Join(parts[4:], "/"), validConanSegment(parts[2])
	}
	if parts[2] == "v2" && parts[3] == "conans" {
		return parts[1], strings.Join(parts[4:], "/"), validConanSegment(parts[1])
	}
	return "", "", false
}
func malformedConanPath(raw string) bool {
	parts := strings.Split(strings.Trim(raw, "/"), "/")
	if len(parts) < 2 || parts[0] != "conan" {
		return false
	}
	for _, part := range parts[1:] {
		if !validConanSegment(part) {
			return true
		}
	}
	return false
}
func parseConanPing(method, path string) (string, bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	return func() (string, bool) {
		if method == http.MethodGet && len(parts) == 4 && parts[0] == "conan" && parts[2] == "v1" && parts[3] == "ping" && validConanSegment(parts[1]) {
			return parts[1], true
		}
		return "", false
	}()
}
func parseConanAuthenticate(method, path string) (string, bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	return func() (string, bool) {
		if method == http.MethodGet && len(parts) == 5 && parts[0] == "conan" && parts[2] == "v2" && parts[3] == "users" && parts[4] == "authenticate" && validConanSegment(parts[1]) {
			return parts[1], true
		}
		return "", false
	}()
}
func validConanSegment(s string) bool {
	decoded, err := url.PathUnescape(s)
	return err == nil && decoded != "" && decoded != "." && decoded != ".." && !strings.ContainsAny(decoded, "/\\\x00#") && !strings.Contains(strings.ToLower(s), "%2f") && !strings.Contains(strings.ToLower(s), "%23")
}
func validConanMetadata(path string, body []byte) bool {
	if strings.HasSuffix(path, "/latest") {
		var latest struct {
			Revision string `json:"revision"`
			Time     string `json:"time"`
		}
		if json.Unmarshal(body, &latest) != nil || !validConanSegment(latest.Revision) {
			return false
		}
		_, err := time.Parse(time.RFC3339, latest.Time)
		return err == nil
	}
	if strings.HasSuffix(path, "/search") {
		var search struct {
			Packages map[string]json.RawMessage `json:"packages"`
		}
		return json.Unmarshal(body, &search) == nil && search.Packages != nil
	}
	var data struct {
		Revisions json.RawMessage `json:"revisions"`
		Files     map[string]struct {
			SHA256 string `json:"sha256"`
			Size   int64  `json:"size"`
		} `json:"files"`
	}
	if json.Unmarshal(body, &data) != nil {
		return false
	}
	if strings.HasSuffix(path, "/files") {
		if data.Files == nil {
			return false
		}
		for name, entry := range data.Files {
			if !validConanSegment(name) || len(entry.SHA256) != 64 || entry.Size < 0 {
				return false
			}
			if _, err := hex.DecodeString(entry.SHA256); err != nil || entry.SHA256 != strings.ToLower(entry.SHA256) {
				return false
			}
		}
		return true
	}
	var revisions []struct {
		Revision string          `json:"revision"`
		Time     json.RawMessage `json:"time"`
	}
	if json.Unmarshal(data.Revisions, &revisions) != nil {
		return false
	}
	for _, revision := range revisions {
		if !validConanSegment(revision.Revision) || len(revision.Time) == 0 {
			return false
		}
		if revision.Time[0] == '"' {
			var timestamp string
			if json.Unmarshal(revision.Time, &timestamp) != nil || timestamp == "" {
				return false
			}
			if _, err := time.Parse(time.RFC3339, timestamp); err != nil {
				return false
			}
			continue
		}
		var numeric json.Number
		if json.Unmarshal(revision.Time, &numeric) != nil {
			return false
		}
		if _, err := numeric.Float64(); err != nil {
			return false
		}
	}
	return true
}
