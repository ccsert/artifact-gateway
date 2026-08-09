package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
	modzip "golang.org/x/mod/zip"
)

const (
	goListLimit = 8 << 20
	goInfoLimit = 1 << 20
	goModLimit  = 16 << 20
	goZipLimit  = 512 << 20
)

type nativeGoHandler struct {
	store      repository.NativeGoStore
	repos      repository.HostedRepositoryStore
	objects    OCIObjectStore
	auth       Authenticator
	authorizer RepositoryAuthorizer
	audit      repository.Store
	metrics    *Metrics
	proxy      GoClient
}

type goRoute struct {
	repository string
	module     string
	version    string
	kind       string
}

func newNativeGoHandler(store GatewayStore, objects OCIObjectStore, auth Authenticator) nativeGoHandler {
	if objects == nil {
		objects = NewMemoryOCIObjectStore()
	}
	return nativeGoHandler{
		store: store, repos: store, objects: objects, auth: auth, audit: store, proxy: UpstreamClient{},
		authorizer: RepositoryAuthorizer{Grants: store, Legacy: auth, LegacyFallback: func(Principal, repository.HostedRepository, RepositoryOperation) AuthorizationDecision {
			return AuthorizationDecision{Allowed: true, Source: "legacy_protocol", Reason: "authenticated"}
		}},
	}
}

func (h nativeGoHandler) withProxy(client GoClient) nativeGoHandler {
	if client != nil {
		h.proxy = client
	}
	return h
}

func (h nativeGoHandler) withMetrics(metrics *Metrics) nativeGoHandler {
	h.metrics = metrics
	return h
}

func (h nativeGoHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route, ok := parseGoProxyPath(r.URL.EscapedPath())
	if !ok {
		http.NotFound(w, r)
		return
	}
	repo, err := h.repos.GetHostedRepositoryByName(r.Context(), route.repository)
	if errors.Is(err, repository.ErrNotFound) || repo.Format != repository.FormatGo || repo.Type != repository.RepositoryTypeProxy || repo.State != repository.RepositoryActive {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "repository unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	principal, authenticated := h.protocolPrincipal(r)
	if !authenticated {
		if anonymousHostedRepositoryReadAllowed(r.Context(), h.store, repo, r.Method) {
			principal = anonymousPrincipal()
		} else {
			h.challenge(w, http.StatusUnauthorized, "authentication required")
			return
		}
	}
	if !isAnonymous(principal) {
		decision := h.authorizer.AuthorizeResource(r.Context(), principal, repo, RepositoryRead, route.module)
		if !decision.Allowed {
			h.challenge(w, http.StatusForbidden, "repository permission required")
			return
		}
	}
	switch route.kind {
	case "list":
		h.serveList(w, r, repo, route.module, principal.Actor)
	case "latest":
		h.serveLatest(w, r, repo, route.module, principal.Actor)
	case "info", "mod", "zip":
		h.serveAsset(w, r, repo, route, principal.Actor)
	default:
		http.NotFound(w, r)
	}
}

func parseGoProxyPath(escapedPath string) (goRoute, bool) {
	if !strings.HasPrefix(escapedPath, "/go/") {
		return goRoute{}, false
	}
	remainder := strings.TrimPrefix(escapedPath, "/go/")
	repositoryName, resource, found := strings.Cut(remainder, "/")
	repositoryName, err := url.PathUnescape(repositoryName)
	if err != nil || !found || repositoryName == "" || strings.Contains(repositoryName, "/") {
		return goRoute{}, false
	}
	marker := "/@v/"
	if strings.HasSuffix(resource, "/@latest") {
		escapedModule := strings.TrimSuffix(resource, "/@latest")
		modulePath, ok := unescapeGoModulePath(escapedModule)
		return goRoute{repository: repositoryName, module: modulePath, kind: "latest"}, ok
	}
	modulePart, assetPart, found := strings.Cut(resource, marker)
	if !found {
		return goRoute{}, false
	}
	modulePath, ok := unescapeGoModulePath(modulePart)
	if !ok {
		return goRoute{}, false
	}
	if assetPart == "list" {
		return goRoute{repository: repositoryName, module: modulePath, kind: "list"}, true
	}
	for _, kind := range []string{"info", "mod", "zip"} {
		suffix := "." + kind
		if !strings.HasSuffix(assetPart, suffix) {
			continue
		}
		escapedVersion, err := url.PathUnescape(strings.TrimSuffix(assetPart, suffix))
		if err != nil {
			return goRoute{}, false
		}
		version, err := module.UnescapeVersion(escapedVersion)
		if err != nil || module.Check(modulePath, version) != nil {
			return goRoute{}, false
		}
		return goRoute{repository: repositoryName, module: modulePath, version: version, kind: kind}, true
	}
	return goRoute{}, false
}

func unescapeGoModulePath(value string) (string, bool) {
	escaped, err := url.PathUnescape(value)
	if err != nil {
		return "", false
	}
	path, err := module.UnescapePath(escaped)
	return path, err == nil
}

func validGoModuleSearchPrefix(value string) bool {
	if len(value) > 1024 || strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\\\x00\r\n\t ?#") {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func goProxyTarget(endpoint, modulePath, suffix string) (string, error) {
	escaped, err := module.EscapePath(modulePath)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(endpoint, "/") + "/" + escaped + "/" + strings.TrimLeft(suffix, "/"), nil
}

func (h nativeGoHandler) serveList(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, modulePath, actor string) {
	versions, disposition, err := h.resolveList(r, repo, modulePath)
	if err != nil {
		status, outcome := http.StatusBadGateway, repository.AuditUpstreamError
		if errors.Is(err, repository.ErrNotFound) {
			status, outcome = http.StatusNotFound, repository.AuditNotFound
		}
		http.Error(w, "Go module list unavailable", status)
		h.recordAudit(r, repo, modulePath, "list", actor, outcome, status, 0, disposition)
		return
	}
	sort.Slice(versions, func(i, j int) bool { return semver.Compare(versions[i].Version, versions[j].Version) < 0 })
	var body strings.Builder
	for _, version := range versions {
		body.WriteString(version.Version)
		body.WriteByte('\n')
	}
	if disposition == "stale" {
		w.Header().Set("Warning", `110 Artifact-Gateway "Response is stale"`)
	}
	h.writeBytes(w, r, []byte(body.String()), "text/plain; charset=utf-8", "")
	h.recordAudit(r, repo, modulePath, "list", actor, repository.AuditResolved, http.StatusOK, int64(body.Len()), disposition)
}

func (h nativeGoHandler) resolveList(r *http.Request, repo repository.HostedRepository, modulePath string) ([]repository.GoModuleVersion, string, error) {
	cached, cacheErr := h.store.ListGoModuleVersions(r.Context(), repo.ID, modulePath)
	disposition := "miss"
	if cacheErr == nil && len(cached) > 0 {
		disposition = "hit"
	}
	target, err := goProxyTarget(repo.Endpoint, modulePath, "@v/list")
	if err != nil {
		return nil, disposition, err
	}
	response, err := h.proxy.FetchGo(r.Context(), http.MethodGet, repo, target, r.Header)
	if err != nil {
		if len(cached) > 0 {
			return cached, "stale", nil
		}
		return nil, disposition, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone {
		if len(cached) > 0 {
			return cached, "stale", nil
		}
		return nil, disposition, repository.ErrNotFound
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if len(cached) > 0 {
			return cached, "stale", nil
		}
		return nil, disposition, fmt.Errorf("go upstream returned %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, goListLimit+1))
	if err != nil || len(data) > goListLimit {
		return nil, disposition, errors.New("go version list is too large")
	}
	versions := make([]repository.GoModuleVersion, 0)
	seen := make(map[string]bool)
	for _, value := range strings.Fields(string(data)) {
		if seen[value] || semver.Canonical(value) == "" || module.Check(modulePath, value) != nil {
			continue
		}
		seen[value] = true
		versions = append(versions, repository.GoModuleVersion{Version: value, Publisher: "upstream:" + upstreamHostname(repo.Endpoint)})
	}
	if len(versions) == 0 {
		return nil, disposition, repository.ErrNotFound
	}
	if err = h.store.SyncGoProxyVersions(r.Context(), repo.ID, modulePath, versions); err != nil {
		return nil, disposition, err
	}
	stored, err := h.store.ListGoModuleVersions(r.Context(), repo.ID, modulePath)
	return stored, disposition, err
}

func (h nativeGoHandler) serveLatest(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, modulePath, actor string) {
	versions, disposition, err := h.resolveList(r, repo, modulePath)
	if err != nil || len(versions) == 0 {
		http.NotFound(w, r)
		h.recordAudit(r, repo, modulePath, "latest", actor, repository.AuditNotFound, http.StatusNotFound, 0, disposition)
		return
	}
	sort.Slice(versions, func(i, j int) bool { return semver.Compare(versions[i].Version, versions[j].Version) > 0 })
	h.serveAsset(w, r, repo, goRoute{module: modulePath, version: versions[0].Version, kind: "info"}, actor)
}

func (h nativeGoHandler) serveAsset(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, route goRoute, actor string) {
	asset, disposition, err := h.loadAsset(r, repo, route)
	if repository.IsQuotaExceeded(err) {
		http.Error(w, "repository capacity quota exceeded", http.StatusInsufficientStorage)
		h.recordAudit(r, repo, route.module+"@"+route.version, route.kind, actor, repository.AuditStorageError, http.StatusInsufficientStorage, 0, disposition)
		return
	}
	if errors.Is(err, repository.ErrNotFound) {
		http.NotFound(w, r)
		h.recordAudit(r, repo, route.module+"@"+route.version, route.kind, actor, repository.AuditNotFound, http.StatusNotFound, 0, disposition)
		return
	}
	if err != nil {
		http.Error(w, "Go module asset unavailable", http.StatusBadGateway)
		h.recordAudit(r, repo, route.module+"@"+route.version, route.kind, actor, repository.AuditUpstreamError, http.StatusBadGateway, 0, disposition)
		return
	}
	size, status, err := h.writeAsset(w, r, asset, route.kind)
	if err != nil {
		h.recordAudit(r, repo, route.module+"@"+route.version, route.kind, actor, repository.AuditStorageError, status, 0, disposition)
		return
	}
	h.recordAudit(r, repo, route.module+"@"+route.version, route.kind, actor, repository.AuditResolved, status, size, disposition)
}

func (h nativeGoHandler) loadAsset(r *http.Request, repo repository.HostedRepository, route goRoute) (repository.GoModuleAsset, string, error) {
	asset, err := h.store.GetGoModuleAsset(r.Context(), repo.ID, route.module, route.version, route.kind)
	disposition := "hit"
	if errors.Is(err, repository.ErrNotFound) {
		disposition = "miss"
		asset, err = h.cacheAsset(r, repo, route)
	}
	return asset, disposition, err
}

func (h nativeGoHandler) writeAsset(w http.ResponseWriter, r *http.Request, asset repository.GoModuleAsset, kind string) (int64, int, error) {
	reader, size, err := h.objects.Open(r.Context(), asset.ObjectKey)
	if err != nil {
		http.Error(w, "Go module object unavailable", http.StatusInternalServerError)
		return 0, http.StatusInternalServerError, err
	}
	defer func() { _ = reader.Close() }()
	if match := r.Header.Get("If-None-Match"); match != "" && match == `"`+strings.TrimPrefix(asset.Digest, "sha256:")+`"` {
		w.WriteHeader(http.StatusNotModified)
		return 0, http.StatusNotModified, nil
	}
	w.Header().Set("ETag", `"`+strings.TrimPrefix(asset.Digest, "sha256:")+`"`)
	w.Header().Set("X-Checksum-Sha256", strings.TrimPrefix(asset.Digest, "sha256:"))
	w.Header().Set("Content-Type", goAssetContentType(kind))
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = io.Copy(w, reader)
	}
	return size, http.StatusOK, nil
}

func (h nativeGoHandler) cacheAsset(r *http.Request, repo repository.HostedRepository, route goRoute) (repository.GoModuleAsset, error) {
	escapedVersion, err := module.EscapeVersion(route.version)
	if err != nil {
		return repository.GoModuleAsset{}, err
	}
	target, err := goProxyTarget(repo.Endpoint, route.module, "@v/"+escapedVersion+"."+route.kind)
	if err != nil {
		return repository.GoModuleAsset{}, err
	}
	response, err := h.proxy.FetchGo(r.Context(), http.MethodGet, repo, target, r.Header)
	if err != nil {
		return repository.GoModuleAsset{}, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone {
		return repository.GoModuleAsset{}, repository.ErrNotFound
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return repository.GoModuleAsset{}, fmt.Errorf("go upstream returned %d", response.StatusCode)
	}
	limit := goAssetLimit(route.kind)
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || len(data) == 0 || int64(len(data)) > limit {
		return repository.GoModuleAsset{}, errors.New("invalid Go module asset size")
	}
	publishedAt, err := validateGoAsset(route.module, route.version, route.kind, data)
	if err != nil {
		return repository.GoModuleAsset{}, repository.ErrUpstreamChanged
	}
	publisher := "upstream:" + upstreamHostname(repo.Endpoint)
	if _, err = h.store.PutGoModuleVersion(r.Context(), repository.GoModuleVersion{RepositoryID: repo.ID, Module: route.module, Version: route.version, PublishedAt: publishedAt, Publisher: publisher}); err != nil {
		return repository.GoModuleAsset{}, err
	}
	sum := sha256.Sum256(data)
	digestHex := hex.EncodeToString(sum[:])
	asset := repository.GoModuleAsset{
		RepositoryID: repo.ID, Module: route.module, Version: route.version, Kind: route.kind,
		Digest: "sha256:" + digestHex, ObjectKey: "native/go/sha256/" + digestHex,
		Size: int64(len(data)), SourceURL: target, CachedAt: time.Now().UTC(),
	}
	release, err := h.store.LockGoObject(r.Context(), asset.ObjectKey)
	if err != nil {
		return repository.GoModuleAsset{}, err
	}
	defer release()
	if err = h.objects.PutVerifiedReader(r.Context(), asset.ObjectKey, bytes.NewReader(data), asset.Size, asset.Digest); err != nil {
		return repository.GoModuleAsset{}, err
	}
	return h.store.CacheGoModuleAsset(r.Context(), asset)
}

func validateGoAsset(modulePath, version, kind string, data []byte) (time.Time, error) {
	switch kind {
	case "info":
		var info struct {
			Version string    `json:"Version"`
			Time    time.Time `json:"Time"`
		}
		if err := json.Unmarshal(data, &info); err != nil || info.Version != version || info.Time.IsZero() {
			return time.Time{}, errors.New("invalid Go info response")
		}
		return info.Time.UTC(), nil
	case "mod":
		parsed, err := modfile.Parse("go.mod", data, nil)
		if err != nil || parsed.Module == nil || parsed.Module.Mod.Path != modulePath {
			return time.Time{}, errors.New("go.mod module path does not match request")
		}
		return time.Time{}, nil
	case "zip":
		file, err := os.CreateTemp("", "artifact-gateway-go-*.zip")
		if err != nil {
			return time.Time{}, err
		}
		name := file.Name()
		defer func() { _ = os.Remove(name) }()
		if _, err = file.Write(data); err != nil {
			_ = file.Close()
			return time.Time{}, err
		}
		if err = file.Close(); err != nil {
			return time.Time{}, err
		}
		_, err = modzip.CheckZip(module.Version{Path: modulePath, Version: version}, name)
		return time.Time{}, err
	default:
		return time.Time{}, errors.New("unsupported Go asset kind")
	}
}

func goAssetLimit(kind string) int64 {
	switch kind {
	case "info":
		return goInfoLimit
	case "mod":
		return goModLimit
	default:
		return goZipLimit
	}
}

func goAssetContentType(kind string) string {
	switch kind {
	case "info":
		return "application/json"
	case "zip":
		return "application/zip"
	default:
		return "text/plain; charset=utf-8"
	}
}

func upstreamHostname(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func (h nativeGoHandler) writeBytes(w http.ResponseWriter, r *http.Request, data []byte, contentType, digest string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if digest != "" {
		w.Header().Set("ETag", `"`+strings.TrimPrefix(digest, "sha256:")+`"`)
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(data)
	}
}

func (h nativeGoHandler) recordAudit(r *http.Request, repo repository.HostedRepository, resource, representation, actor string, outcome repository.AuditOutcome, status int, size int64, disposition string) {
	if h.audit == nil {
		return
	}
	if actor == "" {
		actor = anonymousActor
	}
	audit := repository.AuditRecord{
		GroupName: repo.Name, Repository: repo.Name, Actor: actor, Outcome: outcome, OccurredAt: time.Now().UTC(),
		Format: string(repository.FormatGo), Resource: resource, Representation: representation, MemberType: "proxy",
		UpstreamHost: upstreamHostname(repo.Endpoint), Operation: strings.ToLower(r.Method), Status: status,
		CacheDisposition: disposition, Bytes: size,
	}
	if actor == anonymousActor {
		audit.AuthorizationSource = anonymousAuthorizationSource
		audit.AuthorizationReason = anonymousAuthorizationReason
	}
	_ = h.audit.RecordAudit(r.Context(), audit)
}

func (h nativeGoHandler) protocolPrincipal(r *http.Request) (Principal, bool) {
	if principal, ok := h.auth.Authenticate(r.Header.Get("Authorization")); ok {
		return principal, true
	}
	username, password, ok := r.BasicAuth()
	if !ok {
		return Principal{}, false
	}
	return h.auth.AuthenticateBasic(username, password)
}

func (h nativeGoHandler) challenge(w http.ResponseWriter, status int, message string) {
	w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway Go"`)
	http.Error(w, message, status)
}
