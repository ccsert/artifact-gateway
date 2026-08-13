package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/egress"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

const aptProxyUserAgent = "apt/2 Artifact-Gateway/1.0"

// APTClient is intentionally separate from the generic raw client. APT
// metadata and .deb bytes must be cached verbatim, including Release files
// and their signatures.
type APTClient interface {
	FetchAPT(context.Context, string, repository.HostedRepository, string, http.Header) (*http.Response, error)
}

// UpstreamClient implements the APT proxy transport with the same host and
// egress restrictions used by the other native proxies.
func (c UpstreamClient) FetchAPT(ctx context.Context, method string, repo repository.HostedRepository, target string, headers http.Header) (*http.Response, error) {
	targetURL, err := url.Parse(target)
	if err != nil || !proxyUpstreamURLAllowed(repo, targetURL) {
		return nil, fmt.Errorf("APT upstream target is not allowed")
	}
	request, err := http.NewRequestWithContext(ctx, method, targetURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create APT upstream request: %w", err)
	}
	request.Header.Set("User-Agent", aptProxyUserAgent)
	request.Header.Set("Accept", "*/*")
	for _, name := range []string{"If-Modified-Since", "If-None-Match"} {
		if value := headers.Get(name); value != "" {
			request.Header.Set(name, value)
		}
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 90 * time.Second}
	} else {
		copy := *client
		client = &copy
	}
	if targetURL.Scheme == "https" {
		client, err = egress.Apply(client, repo.EgressProxy, targetURL.String(), rawEgressHooks())
		if err != nil {
			return nil, err
		}
	}
	client.CheckRedirect = func(next *http.Request, _ []*http.Request) error {
		if !proxyUpstreamURLAllowed(repo, next.URL) {
			return fmt.Errorf("APT upstream redirect is not allowed")
		}
		return nil
	}
	response, err := tracedHTTPClient(client).Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch APT upstream content: %w", err)
	}
	return response, nil
}

type nativeAPTHandler struct {
	store        repository.NativeAPTStore
	publications repository.NativeAPTPublicationStore
	repos        repository.HostedRepositoryStore
	objects      OCIObjectStore
	auth         Authenticator
	authorizer   RepositoryAuthorizer
	audit        repository.Store
	proxy        APTClient
}

type aptRoute struct {
	repository string
	path       string
}

func newNativeAPTHandler(store GatewayStore, objects OCIObjectStore, auth Authenticator) nativeAPTHandler {
	if objects == nil {
		objects = NewMemoryOCIObjectStore()
	}
	return nativeAPTHandler{
		store: store, publications: store, repos: store, objects: objects, auth: auth, audit: store, proxy: UpstreamClient{},
		authorizer: RepositoryAuthorizer{Grants: store, Legacy: auth, LegacyFallback: func(Principal, repository.HostedRepository, RepositoryOperation) AuthorizationDecision {
			return AuthorizationDecision{Allowed: true, Source: "legacy_protocol", Reason: "authenticated"}
		}},
	}
}

func (h nativeAPTHandler) withProxy(client APTClient) nativeAPTHandler {
	if client != nil {
		h.proxy = client
	}
	return h
}

func (h nativeAPTHandler) protocolPrincipal(r *http.Request) (Principal, bool) {
	if principal, ok := h.auth.Authenticate(r.Header.Get("Authorization")); ok {
		return principal, true
	}
	username, password, ok := r.BasicAuth()
	if !ok {
		return Principal{}, false
	}
	return h.auth.AuthenticateBasic(username, password)
}

func (h nativeAPTHandler) challenge(w http.ResponseWriter, status int, message string) {
	w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway APT"`)
	http.Error(w, message, status)
}

func parseAPTPath(escapedPath string) (aptRoute, bool) {
	if !strings.HasPrefix(escapedPath, "/apt/") {
		return aptRoute{}, false
	}
	remainder := strings.TrimPrefix(escapedPath, "/apt/")
	repositoryName, resource, found := strings.Cut(remainder, "/")
	repositoryName, err := url.PathUnescape(repositoryName)
	if err != nil || !found || repositoryName == "" || strings.Contains(repositoryName, "/") {
		return aptRoute{}, false
	}
	resource, err = url.PathUnescape(strings.TrimPrefix(resource, "/"))
	if err != nil || !validAPTPath(resource) {
		return aptRoute{}, false
	}
	return aptRoute{repository: repositoryName, path: resource}, true
}

func validAPTPath(path string) bool {
	if path == "" || len(path) > 2048 || strings.ContainsRune(path, 0) || strings.ContainsAny(path, "\\\r\n\t?#") {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return strings.HasPrefix(path, "dists/") || strings.HasPrefix(path, "pool/")
}

func validAPTPathPrefix(path string) bool {
	if path == "" {
		return true
	}
	if len(path) > 2048 || strings.HasPrefix(path, "/") || strings.ContainsRune(path, 0) || strings.ContainsAny(path, "\\\r\n\t?#") {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return strings.HasPrefix("dists/", path) || strings.HasPrefix("pool/", path) || strings.HasPrefix(path, "dists/") || strings.HasPrefix(path, "pool/")
}

func aptUpstreamTarget(endpoint, path string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("invalid APT upstream endpoint")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + path
	u.RawPath = ""
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}

func (h nativeAPTHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route, ok := parseAPTPath(r.URL.EscapedPath())
	if !ok {
		http.NotFound(w, r)
		return
	}
	repo, err := h.repos.GetHostedRepositoryByName(r.Context(), route.repository)
	if errors.Is(err, repository.ErrNotFound) || (err == nil && (repo.Format != repository.FormatAPT ||
		(repo.Type != repository.RepositoryTypeProxy && repo.Type != repository.RepositoryTypeHosted) || repo.State != repository.RepositoryActive)) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "repository unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
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
		decision := h.authorizer.AuthorizeResource(r.Context(), principal, repo, RepositoryRead, route.path)
		if !decision.Allowed {
			h.challenge(w, http.StatusForbidden, "repository permission required")
			return
		}
	}
	asset, disposition, err := h.loadAsset(r, repo, route.path)
	if errors.Is(err, repository.ErrNotFound) {
		http.NotFound(w, r)
		h.recordAudit(r, repo, route.path, principal.Actor, repository.AuditNotFound, http.StatusNotFound, disposition)
		return
	}
	if errors.Is(err, repository.ErrQuotaExceeded) {
		http.Error(w, "repository capacity quota exceeded", http.StatusInsufficientStorage)
		h.recordAudit(r, repo, route.path, principal.Actor, repository.AuditStorageError, http.StatusInsufficientStorage, disposition)
		return
	}
	if err != nil {
		http.Error(w, "APT upstream unavailable", http.StatusBadGateway)
		h.recordAudit(r, repo, route.path, principal.Actor, repository.AuditUpstreamError, http.StatusBadGateway, disposition)
		return
	}
	if disposition == "stale" {
		w.Header().Set("Warning", `110 Artifact-Gateway "Response is stale"`)
	}
	status, size, err := h.writeAsset(w, r, asset)
	if err != nil {
		h.recordAudit(r, repo, route.path, principal.Actor, repository.AuditStorageError, status, disposition)
		return
	}
	h.recordAudit(r, repo, route.path, principal.Actor, repository.AuditResolved, status, disposition, size)
}

func (h nativeAPTHandler) loadAsset(r *http.Request, repo repository.HostedRepository, path string) (repository.APTAsset, string, error) {
	if repo.Type == repository.RepositoryTypeHosted {
		if h.publications == nil {
			return repository.APTAsset{}, "hosted", errors.New("APT Hosted publication store is unavailable")
		}
		asset, err := h.publications.GetVisibleAPTSnapshotAsset(r.Context(), repo.ID, path)
		if err != nil {
			return repository.APTAsset{}, "hosted", err
		}
		return repository.APTAsset{
			RepositoryID: asset.RepositoryID, Path: asset.Path, Digest: asset.Digest, ObjectKey: asset.ObjectKey,
			Size: asset.Size, ContentType: asset.ContentType,
		}, "hosted", nil
	}
	asset, err := h.store.GetAPTAsset(r.Context(), repo.ID, path)
	if err == nil {
		if !repository.APTAssetMutable(path) {
			return asset, "hit", nil
		}
	} else if !errors.Is(err, repository.ErrNotFound) {
		return repository.APTAsset{}, "miss", err
	}
	unlock, lockErr := h.store.LockAPTObject(r.Context(), repo.ID+"\x00"+path)
	if lockErr != nil {
		return repository.APTAsset{}, "miss", lockErr
	}
	defer unlock()
	if current, getErr := h.store.GetAPTAsset(r.Context(), repo.ID, path); getErr == nil {
		asset = current
		if !repository.APTAssetMutable(path) {
			return asset, "hit", nil
		}
	} else if !errors.Is(getErr, repository.ErrNotFound) {
		return repository.APTAsset{}, "miss", getErr
	}
	target, err := aptUpstreamTarget(repo.Endpoint, path)
	if err != nil {
		return repository.APTAsset{}, "miss", err
	}
	upstreamHeaders := make(http.Header)
	if asset.UpstreamETag != "" {
		upstreamHeaders.Set("If-None-Match", asset.UpstreamETag)
	}
	if asset.UpstreamModified != "" {
		upstreamHeaders.Set("If-Modified-Since", asset.UpstreamModified)
	}
	response, err := h.proxy.FetchAPT(r.Context(), http.MethodGet, repo, target, upstreamHeaders)
	if err != nil {
		if asset.ObjectKey != "" {
			return asset, "stale", nil
		}
		return repository.APTAsset{}, "miss", err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotModified && asset.ObjectKey != "" {
		return asset, "revalidated", nil
	}
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone {
		return repository.APTAsset{}, "miss", repository.ErrNotFound
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if asset.ObjectKey != "" && (response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500) {
			return asset, "stale", nil
		}
		return repository.APTAsset{}, "miss", fmt.Errorf("APT upstream returned %d", response.StatusCode)
	}
	const maxAPTAssetBytes = int64(1 << 30)
	spool, err := spoolUpload(response.Body, maxAPTAssetBytes)
	if err != nil {
		if asset.ObjectKey != "" && !errors.Is(err, errUploadTooLarge) {
			return asset, "stale", nil
		}
		if errors.Is(err, errUploadTooLarge) {
			return repository.APTAsset{}, "miss", errors.New("APT asset is too large")
		}
		return repository.APTAsset{}, "miss", fmt.Errorf("read APT upstream content: %w", err)
	}
	defer func() { _ = spool.Close() }()
	digest := spool.Digest()
	pathDigest := sha256.Sum256([]byte(path))
	objectKey := "apt/" + repo.ID + "/" + hex.EncodeToString(pathDigest[:]) + "/" + strings.TrimPrefix(digest, "sha256:")
	if asset.Digest != digest {
		if err = h.objects.PutVerifiedReader(r.Context(), objectKey, spool.Reader(), spool.Size(), digest); err != nil {
			return repository.APTAsset{}, "miss", err
		}
	}
	updated := repository.APTAsset{RepositoryID: repo.ID, Path: path, Digest: digest, ObjectKey: objectKey, Size: spool.Size(), ContentType: aptContentType(path, response.Header.Get("Content-Type")), SourceURL: target, UpstreamETag: response.Header.Get("ETag"), UpstreamModified: response.Header.Get("Last-Modified")}
	previousObjectKey := asset.ObjectKey
	asset, err = h.store.CacheAPTAsset(r.Context(), updated)
	if err != nil {
		if updated.ObjectKey != previousObjectKey {
			_ = h.objects.Delete(r.Context(), objectKey)
		}
		return repository.APTAsset{}, "miss", err
	}
	if previousObjectKey != "" && previousObjectKey != asset.ObjectKey {
		_ = h.objects.Delete(r.Context(), previousObjectKey)
	}
	if previousObjectKey != "" && previousObjectKey == asset.ObjectKey {
		return asset, "revalidated", nil
	}
	return asset, "miss", nil
}

func aptContentType(path, upstream string) string {
	if upstream != "" {
		return upstream
	}
	if strings.HasSuffix(path, ".deb") {
		return "application/vnd.debian.binary-package"
	}
	if strings.HasSuffix(path, ".gz") {
		return "application/gzip"
	}
	if strings.HasSuffix(path, ".xz") {
		return "application/x-xz"
	}
	if strings.HasSuffix(path, ".zst") {
		return "application/zstd"
	}
	return "text/plain; charset=utf-8"
}

func (h nativeAPTHandler) writeAsset(w http.ResponseWriter, r *http.Request, asset repository.APTAsset) (int, int64, error) {
	etag := `"` + strings.TrimPrefix(asset.Digest, "sha256:") + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Type", asset.ContentType)
	w.Header().Set("Accept-Ranges", "bytes")
	if asset.UpstreamModified != "" {
		w.Header().Set("Last-Modified", asset.UpstreamModified)
	}
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return http.StatusNotModified, 0, nil
	}
	if r.Header.Get("If-None-Match") == "" && asset.UpstreamModified != "" {
		lastModified, modifiedErr := http.ParseTime(asset.UpstreamModified)
		modifiedSince, requestErr := http.ParseTime(r.Header.Get("If-Modified-Since"))
		if modifiedErr == nil && requestErr == nil && !lastModified.After(modifiedSince) {
			w.WriteHeader(http.StatusNotModified)
			return http.StatusNotModified, 0, nil
		}
	}
	if r.Method == http.MethodGet && r.Header.Get("Range") != "" {
		start, end, ok := parseOCIRange(w, r, asset.Size)
		if !ok {
			return http.StatusRequestedRangeNotSatisfiable, 0, nil
		}
		length := end - start + 1
		reader, _, err := h.objects.OpenRange(r.Context(), asset.ObjectKey, start, length)
		if err != nil {
			http.Error(w, "APT object unavailable", http.StatusInternalServerError)
			return http.StatusInternalServerError, 0, err
		}
		defer func() { _ = reader.Close() }()
		w.Header().Set("Content-Range", "bytes "+utoa(uint64(start))+"-"+utoa(uint64(end))+"/"+utoa(uint64(asset.Size)))
		w.Header().Set("Content-Length", utoa(uint64(length)))
		w.WriteHeader(http.StatusPartialContent)
		_, err = io.CopyN(w, reader, length)
		return http.StatusPartialContent, length, err
	}
	reader, size, err := h.objects.Open(r.Context(), asset.ObjectKey)
	if err != nil {
		http.Error(w, "APT object unavailable", http.StatusInternalServerError)
		return http.StatusInternalServerError, 0, err
	}
	defer func() { _ = reader.Close() }()
	if asset.Size > 0 {
		size = asset.Size
	}
	w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, err = io.Copy(w, reader)
	}
	return http.StatusOK, size, err
}

func (h nativeAPTHandler) recordAudit(r *http.Request, repo repository.HostedRepository, path, actor string, outcome repository.AuditOutcome, status int, disposition string, size ...int64) {
	if h.audit == nil {
		return
	}
	var bytes int64
	if len(size) > 0 {
		bytes = size[0]
	}
	_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{Repository: repo.Name, Actor: actor, Outcome: outcome, OccurredAt: time.Now().UTC(), Format: string(repository.FormatAPT), Resource: path, Operation: strings.ToLower(r.Method), Status: status, Bytes: bytes, CacheDisposition: disposition})
}
