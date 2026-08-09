package app

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	npmprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/npm"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

const (
	npmPublishBodyLimit = 384 << 20
	npmTarballLimit     = 256 << 20
	npmPackumentLimit   = 16 << 20
	npmMetadataTTL      = 15 * time.Minute
)

type nativeNPMHandler struct {
	store       repository.NativeNPMStore
	repos       repository.HostedRepositoryStore
	objects     OCIObjectStore
	auth        Authenticator
	authorizer  RepositoryAuthorizer
	audit       repository.Store
	metrics     *Metrics
	proxy       NPMClient
	protection  *npmProxyProtection
	metadataTTL time.Duration
	negativeTTL time.Duration
}

type npmPublishDocument struct {
	ID          string                          `json:"_id"`
	Name        string                          `json:"name"`
	DistTags    map[string]string               `json:"dist-tags"`
	Versions    map[string]json.RawMessage      `json:"versions"`
	Attachments map[string]npmPublishAttachment `json:"_attachments"`
}

type npmPublishAttachment struct {
	ContentType string `json:"content_type"`
	Data        string `json:"data"`
	Length      int64  `json:"length"`
}

type npmVersionIdentity struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Dist    struct {
		Tarball   string `json:"tarball"`
		Integrity string `json:"integrity"`
		Shasum    string `json:"shasum"`
	} `json:"dist"`
}

type npmProxyPackumentDocument struct {
	ID       string                     `json:"_id"`
	Name     string                     `json:"name"`
	DistTags map[string]string          `json:"dist-tags"`
	Versions map[string]json.RawMessage `json:"versions"`
	Time     map[string]string          `json:"time"`
}

type npmProxyPackageResolution struct {
	Package     repository.NPMPackage
	Disposition string
	Stale       bool
}

type npmProxyPackageError struct {
	Status  int
	Message string
}

func (e *npmProxyPackageError) Error() string { return e.Message }

type npmAuditTarget struct {
	GroupName  string
	Repository string
	MemberName string
}

func newNativeNPMHandler(store GatewayStore, objects OCIObjectStore, auth Authenticator) nativeNPMHandler {
	if objects == nil {
		objects = NewMemoryOCIObjectStore()
	}
	return nativeNPMHandler{
		store: store, repos: store, objects: objects, auth: auth, audit: store,
		proxy: UpstreamClient{}, protection: newNPMProxyProtection(nil, 0), metadataTTL: npmMetadataTTL, negativeTTL: 10 * time.Minute,
		authorizer: RepositoryAuthorizer{Grants: store, Legacy: auth, LegacyFallback: func(Principal, repository.HostedRepository, RepositoryOperation) AuthorizationDecision {
			return AuthorizationDecision{Allowed: true, Source: "legacy_protocol", Reason: "authenticated"}
		}},
	}
}

func (h nativeNPMHandler) withProxy(client NPMClient) nativeNPMHandler {
	if client != nil {
		h.proxy = client
	}
	return h
}

func (h nativeNPMHandler) withMetrics(metrics *Metrics) nativeNPMHandler {
	h.metrics = metrics
	return h
}

func (h nativeNPMHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) bool {
	route, ok := npmprotocol.ParsePath(r.URL.EscapedPath())
	if !ok {
		return false
	}
	repo, err := h.repos.GetHostedRepositoryByName(r.Context(), route.Repository)
	if errors.Is(err, repository.ErrNotFound) || repo.Format != repository.FormatNPM {
		return false
	}
	if err != nil || repo.State != repository.RepositoryActive || (repo.Type != repository.RepositoryTypeHosted && repo.Type != repository.RepositoryTypeProxy) {
		h.writeError(w, http.StatusNotFound, "repository is unavailable")
		return true
	}
	if h.metrics != nil {
		h.metrics.recordNPMRequest(r.Method)
	}

	operation := RepositoryRead
	if r.Method == http.MethodPut || route.Kind == npmprotocol.RoutePing && r.URL.Query().Get("write") == "true" {
		operation = RepositoryWrite
	}
	principal, authenticated := h.protocolPrincipal(r)
	if !authenticated {
		anonymousMethod := r.Method
		if route.Kind == npmprotocol.RouteAuditBulk || route.Kind == npmprotocol.RouteAuditQuick {
			// npm install sends audit reads as POST requests. They do not mutate
			// repository state and follow the repository's anonymous-read policy.
			anonymousMethod = http.MethodGet
		}
		if operation == RepositoryRead && anonymousHostedRepositoryReadAllowed(r.Context(), h.store, repo, anonymousMethod) {
			principal = anonymousPrincipal()
		} else {
			h.challenge(w, http.StatusUnauthorized, "authentication required")
			return true
		}
	}
	if !isAnonymous(principal) {
		decision := h.authorizer.AuthorizeResource(r.Context(), principal, repo, operation, route.Package)
		if !decision.Allowed {
			if h.metrics != nil {
				h.metrics.recordRepositoryAuthorizationDenied("npm", decision.Source, decision.Reason)
				h.metrics.recordNPMAudit(repository.AuditAccessDenied, 0, false)
			}
			h.challenge(w, http.StatusForbidden, "repository permission required")
			return true
		}
	}

	switch route.Kind {
	case npmprotocol.RoutePing:
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return true
		}
		h.writeJSON(w, r, http.StatusOK, map[string]any{"ok": true})
	case npmprotocol.RouteAuditBulk:
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return true
		}
		h.writeJSON(w, r, http.StatusOK, map[string]any{})
	case npmprotocol.RouteAuditQuick:
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return true
		}
		h.writeJSON(w, r, http.StatusOK, emptyNPMAuditReport())
	case npmprotocol.RoutePackage:
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			if repo.Type == repository.RepositoryTypeProxy {
				h.proxyPackument(w, r, repo, route.Package, principal.Actor)
			} else {
				h.packument(w, r, repo, route.Package, principal.Actor)
			}
		case http.MethodPut:
			if repo.Type == repository.RepositoryTypeProxy {
				w.WriteHeader(http.StatusMethodNotAllowed)
			} else {
				h.publish(w, r, repo, route.Package, principal.Actor)
			}
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	case npmprotocol.RouteTarball:
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return true
		}
		if repo.Type == repository.RepositoryTypeProxy {
			h.proxyTarball(w, r, repo, route.Package, route.Tarball, principal.Actor)
		} else {
			h.tarball(w, r, repo, route.Package, route.Tarball, principal.Actor)
		}
	}
	return true
}

func (h nativeNPMHandler) publish(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, packageName, publisher string) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, npmPublishBodyLimit))
	var document npmPublishDocument
	if err := decoder.Decode(&document); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid npm publication document")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		h.writeError(w, http.StatusBadRequest, "invalid npm publication document")
		return
	}
	if (document.Name != "" && document.Name != packageName) || (document.ID != "" && document.ID != packageName) || len(document.Versions) != 1 || len(document.Attachments) != 1 {
		h.writeError(w, http.StatusBadRequest, "publication must contain one matching package version and tarball")
		return
	}
	var version string
	var manifest json.RawMessage
	for version, manifest = range document.Versions {
	}
	if !npmprotocol.ValidVersion(version) {
		h.writeError(w, http.StatusBadRequest, "package version must be valid SemVer")
		return
	}
	var identity npmVersionIdentity
	if json.Unmarshal(manifest, &identity) != nil || identity.Name != packageName || identity.Version != version {
		h.writeError(w, http.StatusBadRequest, "version manifest identity does not match the request")
		return
	}
	var attachmentName string
	var attachment npmPublishAttachment
	for attachmentName, attachment = range document.Attachments {
	}
	tarballName := path.Base(attachmentName)
	if !npmprotocol.ValidPublishAttachmentName(attachmentName, packageName, version) || !npmprotocol.ValidTarballName(tarballName) || identity.Dist.Tarball != "" && path.Base(identity.Dist.Tarball) != tarballName {
		h.writeError(w, http.StatusBadRequest, "version tarball does not match the attachment")
		return
	}
	if base64.StdEncoding.DecodedLen(len(attachment.Data)) > npmTarballLimit {
		h.writeError(w, http.StatusRequestEntityTooLarge, "npm tarball is too large")
		return
	}
	body, err := base64.StdEncoding.DecodeString(attachment.Data)
	if err != nil || len(body) == 0 || len(body) > npmTarballLimit || attachment.Length > 0 && attachment.Length != int64(len(body)) {
		h.writeError(w, http.StatusBadRequest, "npm tarball attachment is invalid")
		return
	}
	if err = npmprotocol.ValidateTarball(body, packageName, version); err != nil {
		h.writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	sha256Sum := sha256.Sum256(body)
	sha512Sum := sha512.Sum512(body)
	sha1Sum := sha1.Sum(body)
	digest := "sha256:" + hex.EncodeToString(sha256Sum[:])
	integrity := "sha512-" + base64.StdEncoding.EncodeToString(sha512Sum[:])
	shasum := hex.EncodeToString(sha1Sum[:])
	if identity.Dist.Integrity != "" && identity.Dist.Integrity != integrity || identity.Dist.Shasum != "" && identity.Dist.Shasum != shasum {
		h.writeError(w, http.StatusUnprocessableEntity, "published tarball integrity does not match the manifest")
		return
	}
	for tag, target := range document.DistTags {
		if tag == "" || len(tag) > 128 || strings.ContainsAny(tag, "/\\\x00") || !npmprotocol.ValidVersion(target) {
			h.writeError(w, http.StatusBadRequest, "distribution tags are invalid")
			return
		}
	}
	if len(document.DistTags) == 0 {
		document.DistTags = map[string]string{"latest": version}
	}
	objectKey := "native/npm/sha256/" + strings.TrimPrefix(digest, "sha256:")
	objectRelease, err := h.store.LockNPMObject(r.Context(), objectKey)
	if err != nil {
		h.writeError(w, http.StatusServiceUnavailable, "npm object coordination is unavailable")
		return
	}
	defer objectRelease()
	if err = h.objects.PutVerifiedReader(r.Context(), objectKey, bytes.NewReader(body), int64(len(body)), digest); err != nil {
		h.writeError(w, http.StatusInternalServerError, "persist npm tarball failed")
		return
	}
	published, err := h.store.PublishNPMVersion(r.Context(), repository.NPMVersion{
		RepositoryID: repo.ID, PackageName: packageName, Version: version, Digest: digest,
		Integrity: integrity, Shasum: shasum, TarballName: tarballName, ObjectKey: objectKey,
		Size: int64(len(body)), Manifest: append([]byte(nil), manifest...), Publisher: publisher,
	}, document.DistTags)
	if errors.Is(err, repository.ErrNameExists) {
		h.writeError(w, http.StatusConflict, "package version already exists")
		return
	}
	if errors.Is(err, repository.ErrNotFound) {
		h.writeError(w, http.StatusBadRequest, "distribution tag references an unknown version")
		return
	}
	if repository.IsQuotaExceeded(err) {
		h.writeError(w, http.StatusInsufficientStorage, "repository capacity quota exceeded")
		return
	}
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "publish npm package failed")
		return
	}
	h.recordAudit(r, repo, packageName+"@"+version, publisher, repository.AuditResolved, http.StatusCreated, published.Size)
	h.writeJSON(w, r, http.StatusCreated, map[string]any{"ok": true, "id": packageName, "rev": published.Digest})
}

func (h nativeNPMHandler) proxyPackument(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, packageName, actor string) {
	resolved, err := h.resolveProxyPackage(r, repo, packageName, actor)
	if err != nil {
		var responseError *npmProxyPackageError
		if errors.As(err, &responseError) {
			h.writeError(w, responseError.Status, responseError.Message)
			return
		}
		h.writeError(w, http.StatusServiceUnavailable, "package metadata unavailable")
		return
	}
	if resolved.Stale {
		w.Header().Set("Warning", `110 Artifact-Gateway "Response is stale"`)
	}
	h.writePackument(w, r, repo, repo.Name, resolved.Package, actor, resolved.Disposition)
}

// resolveProxyPackage makes a Proxy package available without committing an
// HTTP response. Direct Proxy reads and Group aggregation therefore share the
// same locking, revalidation, stale fallback, negative cache, retry, and
// circuit-breaker behavior.
func (h nativeNPMHandler) resolveProxyPackage(r *http.Request, repo repository.HostedRepository, packageName, actor string) (npmProxyPackageResolution, error) {
	return h.resolveProxyPackageWithAudit(r, repo, packageName, actor, npmAuditTarget{GroupName: repo.Name, Repository: repo.Name})
}

func (h nativeNPMHandler) resolveProxyPackageWithAudit(r *http.Request, repo repository.HostedRepository, packageName, actor string, target npmAuditTarget) (npmProxyPackageResolution, error) {
	pkg, err := h.store.GetNPMPackage(r.Context(), repo.ID, packageName)
	now := time.Now().UTC()
	if err == nil {
		if resolved, cacheErr, ok := h.resolveFromNPMProxyCache(r, repo, pkg, packageName, actor, now, target); ok {
			return resolved, cacheErr
		}
	}
	release, err := h.store.LockNPMProxy(r.Context(), "metadata:"+repo.ID+":"+packageName)
	if err != nil {
		return npmProxyPackageResolution{}, &npmProxyPackageError{Status: http.StatusServiceUnavailable, Message: "package metadata cache is unavailable"}
	}
	defer release()
	pkg, err = h.store.GetNPMPackage(r.Context(), repo.ID, packageName)
	now = time.Now().UTC()
	if err == nil {
		if resolved, cacheErr, ok := h.resolveFromNPMProxyCache(r, repo, pkg, packageName, actor, now, target); ok {
			return resolved, cacheErr
		}
	}
	if h.proxy == nil {
		return npmProxyPackageResolution{}, &npmProxyPackageError{Status: http.StatusBadGateway, Message: "npm upstream client is unavailable"}
	}
	if h.metrics != nil {
		h.metrics.recordCache(repo.Name, false)
		h.metrics.recordNPMCacheMiss()
	}
	upstreamTarget := strings.TrimRight(repo.Endpoint, "/") + "/" + url.PathEscape(packageName)
	upstreamHeaders := make(http.Header)
	if accept := r.Header.Get("Accept"); accept != "" {
		upstreamHeaders.Set("Accept", accept)
	}
	if err == nil && pkg.SourceEndpoint == repo.Endpoint && !pkg.Negative {
		if pkg.UpstreamETag != "" {
			upstreamHeaders.Set("If-None-Match", pkg.UpstreamETag)
		}
		if pkg.UpstreamModified != "" {
			upstreamHeaders.Set("If-Modified-Since", pkg.UpstreamModified)
		}
	}
	response, fetchErr := h.fetchNPMProxy(r.Context(), http.MethodGet, repo, upstreamTarget, upstreamHeaders)
	if fetchErr != nil {
		h.recordNPMUpstreamFailureForTarget(r, repo, target, packageName, actor, false)
		if err == nil && len(pkg.Versions) > 0 && !pkg.Negative {
			return npmProxyPackageResolution{Package: pkg, Disposition: "stale", Stale: true}, nil
		}
		if errors.Is(fetchErr, errNPMUpstreamCircuitOpen) {
			return npmProxyPackageResolution{}, &npmProxyPackageError{Status: http.StatusServiceUnavailable, Message: "npm upstream circuit is open"}
		}
		return npmProxyPackageResolution{}, &npmProxyPackageError{Status: http.StatusBadGateway, Message: "npm upstream is unavailable"}
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotFound {
		_ = h.store.StoreNPMProxyNegative(r.Context(), repository.NPMPackage{
			RepositoryID: repo.ID, Name: packageName, SourceEndpoint: repo.Endpoint,
			NegativeExpiresAt: time.Now().UTC().Add(h.negativeTTL), Negative: true,
		})
		return npmProxyPackageResolution{}, &npmProxyPackageError{Status: http.StatusNotFound, Message: "package not found"}
	}
	if response.StatusCode == http.StatusNotModified && err == nil && len(pkg.Versions) > 0 && !pkg.Negative {
		pkg.MetadataExpiresAt = time.Now().UTC().Add(h.metadataTTL)
		if value := response.Header.Get("ETag"); value != "" {
			pkg.UpstreamETag = value
		}
		if value := response.Header.Get("Last-Modified"); value != "" {
			pkg.UpstreamModified = value
		}
		if _, syncErr := h.store.SyncNPMProxyPackage(r.Context(), pkg); syncErr != nil {
			return npmProxyPackageResolution{}, &npmProxyPackageError{Status: http.StatusServiceUnavailable, Message: "refresh npm proxy metadata failed"}
		}
		return npmProxyPackageResolution{Package: pkg, Disposition: "hit"}, nil
	}
	if response.StatusCode != http.StatusOK {
		if retryableNPMStatus(response.StatusCode) {
			h.recordNPMUpstreamFailureForTarget(r, repo, target, packageName, actor, false)
		}
		if err == nil && len(pkg.Versions) > 0 && !pkg.Negative && response.StatusCode >= http.StatusInternalServerError {
			return npmProxyPackageResolution{Package: pkg, Disposition: "stale", Stale: true}, nil
		}
		return npmProxyPackageResolution{}, &npmProxyPackageError{Status: http.StatusBadGateway, Message: "npm upstream returned an unexpected response"}
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, npmPackumentLimit+1))
	if readErr != nil || len(body) > npmPackumentLimit {
		return npmProxyPackageResolution{}, &npmProxyPackageError{Status: http.StatusBadGateway, Message: "npm upstream package metadata is invalid"}
	}
	incoming, normalizeErr := npmProxyPackage(repo, packageName, body, response.Header, h.metadataTTL)
	if normalizeErr != nil {
		return npmProxyPackageResolution{}, &npmProxyPackageError{Status: http.StatusBadGateway, Message: normalizeErr.Error()}
	}
	_, err = h.store.SyncNPMProxyPackage(r.Context(), incoming)
	if err != nil {
		if errors.Is(err, repository.ErrUpstreamChanged) {
			return npmProxyPackageResolution{}, &npmProxyPackageError{Status: http.StatusBadGateway, Message: "npm upstream changed immutable package metadata"}
		}
		return npmProxyPackageResolution{}, &npmProxyPackageError{Status: http.StatusServiceUnavailable, Message: "persist npm proxy metadata failed"}
	}
	stored, err := h.store.GetNPMPackage(r.Context(), repo.ID, packageName)
	if err != nil {
		return npmProxyPackageResolution{}, &npmProxyPackageError{Status: http.StatusServiceUnavailable, Message: "package metadata unavailable after refresh"}
	}
	return npmProxyPackageResolution{Package: stored, Disposition: "miss"}, nil
}

// resolveFromNPMProxyCache returns ok when a fresh packument or negative lookup
// can answer the request. It is used before and after the per-package lock.
func (h nativeNPMHandler) resolveFromNPMProxyCache(r *http.Request, repo repository.HostedRepository, pkg repository.NPMPackage, packageName, actor string, now time.Time, target npmAuditTarget) (npmProxyPackageResolution, error, bool) {
	if pkg.SourceEndpoint != repo.Endpoint {
		return npmProxyPackageResolution{}, nil, false
	}
	if pkg.Negative && now.Before(pkg.NegativeExpiresAt) {
		h.recordNPMNegativeHitForTarget(r, repo, target, packageName, actor)
		return npmProxyPackageResolution{}, &npmProxyPackageError{Status: http.StatusNotFound, Message: "package not found"}, true
	}
	if !pkg.Negative && now.Before(pkg.MetadataExpiresAt) {
		if h.metrics != nil {
			h.metrics.recordCache(repo.Name, true)
			h.metrics.recordNPMCacheHit()
		}
		return npmProxyPackageResolution{Package: pkg, Disposition: "hit"}, nil, true
	}
	return npmProxyPackageResolution{}, nil, false
}

func (h nativeNPMHandler) fetchNPMProxy(ctx context.Context, method string, repo repository.HostedRepository, target string, headers http.Header) (*http.Response, error) {
	if h.protection != nil && !h.protection.allowed(ctx, repo.Endpoint) {
		if h.metrics != nil {
			h.metrics.recordNPMCircuitOpen()
		}
		return nil, errNPMUpstreamCircuitOpen
	}
	var response *http.Response
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		response, err = h.proxy.FetchNPM(ctx, method, repo, target, headers)
		if err == nil && response == nil {
			err = errors.New("npm upstream returned no response")
		}
		if err == nil && !retryableNPMStatus(response.StatusCode) {
			h.protection.success(ctx, repo.Endpoint)
			return response, nil
		}
		if attempt == 0 && response != nil {
			_ = response.Body.Close()
		}
		if attempt == 0 && h.metrics != nil {
			h.metrics.recordNPMRetry()
		}
	}
	h.protection.failure(ctx, repo.Endpoint)
	return response, err
}

func retryableNPMStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
}

func npmProxyPackage(repo repository.HostedRepository, packageName string, body []byte, headers http.Header, ttl time.Duration) (repository.NPMPackage, error) {
	var document npmProxyPackumentDocument
	if json.Unmarshal(body, &document) != nil || document.Name != packageName || document.ID != "" && document.ID != packageName || len(document.Versions) == 0 {
		return repository.NPMPackage{}, errors.New("npm upstream package metadata is invalid")
	}
	endpoint, err := url.Parse(repo.Endpoint)
	if err != nil {
		return repository.NPMPackage{}, errors.New("npm upstream endpoint is invalid")
	}
	now := time.Now().UTC()
	pkg := repository.NPMPackage{
		RepositoryID: repo.ID, Name: packageName, DistTags: document.DistTags,
		SourceEndpoint: repo.Endpoint, UpstreamETag: headers.Get("ETag"),
		UpstreamModified: headers.Get("Last-Modified"), MetadataExpiresAt: now.Add(ttl),
	}
	for version, manifest := range document.Versions {
		if !npmprotocol.ValidVersion(version) {
			continue
		}
		var identity npmVersionIdentity
		if json.Unmarshal(manifest, &identity) != nil || identity.Name != packageName || identity.Version != version || !validNPMProxyIntegrity(identity.Dist.Integrity, identity.Dist.Shasum) {
			return repository.NPMPackage{}, errors.New("npm upstream version metadata is invalid")
		}
		tarballURL, parseErr := url.Parse(identity.Dist.Tarball)
		if parseErr != nil || !npmUpstreamURLAllowed(repo, tarballURL) {
			return repository.NPMPackage{}, errors.New("npm upstream tarball URL is not allowed")
		}
		createdAt := now
		if published := document.Time[version]; published != "" {
			if parsed, parseErr := time.Parse(time.RFC3339, published); parseErr == nil {
				createdAt = parsed.UTC()
			}
		}
		pkg.Versions = append(pkg.Versions, repository.NPMVersion{
			RepositoryID: repo.ID, PackageName: packageName, Version: version,
			Integrity: identity.Dist.Integrity, Shasum: strings.ToLower(identity.Dist.Shasum),
			TarballName:     path.Base(packageName) + "-" + version + ".tgz",
			UpstreamTarball: tarballURL.String(), Manifest: append([]byte(nil), manifest...),
			Publisher: "upstream:" + endpoint.Hostname(), CreatedAt: createdAt,
		})
	}
	if len(pkg.Versions) == 0 {
		return repository.NPMPackage{}, errors.New("npm upstream package has no supported versions")
	}
	versions := make(map[string]bool, len(pkg.Versions))
	for _, version := range pkg.Versions {
		versions[version.Version] = true
	}
	for tag, target := range pkg.DistTags {
		if tag == "" || len(tag) > 128 || strings.ContainsAny(tag, "/\\\x00") || !versions[target] {
			return repository.NPMPackage{}, errors.New("npm upstream distribution tags are invalid")
		}
	}
	return pkg, nil
}

func validNPMProxyIntegrity(integrity, shasum string) bool {
	if !strings.HasPrefix(integrity, "sha512-") || len(shasum) != 40 {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(integrity, "sha512-"))
	if err != nil || len(decoded) != sha512.Size {
		return false
	}
	decodedSHA1, err := hex.DecodeString(shasum)
	return err == nil && len(decodedSHA1) == sha1.Size
}

func (h nativeNPMHandler) packument(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, packageName, actor string) {
	h.packumentWithDisposition(w, r, repo, packageName, actor, "bypass")
}

func (h nativeNPMHandler) packumentWithDisposition(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, packageName, actor, disposition string) {
	pkg, err := h.store.GetNPMPackage(r.Context(), repo.ID, packageName)
	if errors.Is(err, repository.ErrNotFound) {
		h.writeError(w, http.StatusNotFound, "package not found")
		return
	}
	if err != nil {
		h.writeError(w, http.StatusServiceUnavailable, "package metadata unavailable")
		return
	}
	h.writePackument(w, r, repo, repo.Name, pkg, actor, disposition)
}

func (h nativeNPMHandler) writePackument(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, registryName string, pkg repository.NPMPackage, actor, disposition string) {
	packageName := pkg.Name
	versions := make(map[string]any, len(pkg.Versions))
	times := map[string]string{"created": pkg.CreatedAt.Format(time.RFC3339Nano), "modified": pkg.UpdatedAt.Format(time.RFC3339Nano)}
	for _, version := range pkg.Versions {
		var document map[string]any
		if json.Unmarshal(version.Manifest, &document) != nil {
			continue
		}
		document["name"] = packageName
		document["version"] = version.Version
		document["dist"] = map[string]any{
			"tarball":   h.tarballURL(r, registryName, packageName, version.TarballName),
			"integrity": version.Integrity,
			"shasum":    version.Shasum,
		}
		metadata := map[string]any{
			"source": "hosted", "cacheStatus": "cached", "publisher": version.Publisher,
		}
		if version.UpstreamTarball != "" {
			metadata["source"] = "proxy"
			metadata["cacheStatus"] = "metadata"
		}
		if version.ObjectKey != "" {
			metadata["cacheStatus"] = "cached"
			metadata["digest"] = version.Digest
			metadata["size"] = version.Size
			if !version.CachedAt.IsZero() {
				metadata["cachedAt"] = version.CachedAt.Format(time.RFC3339Nano)
			}
		}
		document["_artifactGateway"] = metadata
		versions[version.Version] = document
		times[version.Version] = version.CreatedAt.Format(time.RFC3339Nano)
	}
	payload := map[string]any{
		"_id": packageName, "name": packageName, "dist-tags": pkg.DistTags,
		"versions": versions, "time": times,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "encode package metadata failed")
		return
	}
	etagSum := sha256.Sum256(encoded)
	etag := `"sha256-` + hex.EncodeToString(etagSum[:]) + `"`
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Length", utoa(uint64(len(encoded))))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(encoded)
	}
	h.recordAuditWithDisposition(r, repo, packageName, actor, repository.AuditResolved, http.StatusOK, int64(len(encoded)), disposition)
}

func (h nativeNPMHandler) tarball(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, packageName, tarballName, actor string) {
	h.tarballWithDisposition(w, r, repo, packageName, tarballName, actor, "bypass")
}

func (h nativeNPMHandler) tarballWithDisposition(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, packageName, tarballName, actor, disposition string) {
	h.tarballWithAudit(w, r, repo, packageName, tarballName, actor, disposition, npmAuditTarget{GroupName: repo.Name, Repository: repo.Name})
}

func (h nativeNPMHandler) tarballWithAudit(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, packageName, tarballName, actor, disposition string, target npmAuditTarget) {
	version, err := h.store.GetNPMVersionByTarball(r.Context(), repo.ID, packageName, tarballName)
	if errors.Is(err, repository.ErrNotFound) {
		h.writeError(w, http.StatusNotFound, "package tarball not found")
		h.recordAuditForTarget(r, repo, target, packageName+"/-/"+tarballName, actor, repository.AuditNotFound, http.StatusNotFound, 0, disposition)
		return
	}
	if err != nil {
		h.writeError(w, http.StatusServiceUnavailable, "package tarball unavailable")
		h.recordAuditForTarget(r, repo, target, packageName+"/-/"+tarballName, actor, repository.AuditStorageError, http.StatusServiceUnavailable, 0, disposition)
		return
	}
	result := serveNativeRawObject(w, r, tarballName, repository.RawAsset{
		Digest: version.Digest, ObjectKey: version.ObjectKey, Size: version.Size,
		ContentType: "application/octet-stream",
	}, h.objects)
	outcome := repository.AuditResolved
	if result.Status >= http.StatusInternalServerError {
		outcome = repository.AuditStorageError
	}
	h.recordAuditForTarget(r, repo, target, packageName+"@"+version.Version, actor, outcome, result.Status, result.Bytes, disposition)
}

func (h nativeNPMHandler) proxyTarball(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, packageName, tarballName, actor string) {
	h.proxyTarballWithAudit(w, r, repo, packageName, tarballName, actor, npmAuditTarget{GroupName: repo.Name, Repository: repo.Name})
}

func (h nativeNPMHandler) proxyTarballWithAudit(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, packageName, tarballName, actor string, target npmAuditTarget) {
	version, err := h.store.GetNPMVersionByTarball(r.Context(), repo.ID, packageName, tarballName)
	if errors.Is(err, repository.ErrNotFound) {
		h.writeError(w, http.StatusNotFound, "package tarball not found")
		h.recordAuditForTarget(r, repo, target, packageName+"/-/"+tarballName, actor, repository.AuditNotFound, http.StatusNotFound, 0, "bypass")
		return
	}
	if err != nil {
		h.writeError(w, http.StatusServiceUnavailable, "package tarball unavailable")
		h.recordAuditForTarget(r, repo, target, packageName+"/-/"+tarballName, actor, repository.AuditStorageError, http.StatusServiceUnavailable, 0, "bypass")
		return
	}
	if version.ObjectKey != "" {
		if h.metrics != nil {
			h.metrics.recordCache(repo.Name, true)
			h.metrics.recordNPMCacheHit()
		}
		h.tarballWithAudit(w, r, repo, packageName, tarballName, actor, "hit", target)
		return
	}
	release, err := h.store.LockNPMProxy(r.Context(), "tarball:"+repo.ID+":"+packageName+":"+version.Version)
	if err != nil {
		h.writeError(w, http.StatusServiceUnavailable, "package tarball cache is unavailable")
		h.recordAuditForTarget(r, repo, target, packageName+"@"+version.Version, actor, repository.AuditStorageError, http.StatusServiceUnavailable, 0, "bypass")
		return
	}
	defer release()
	version, err = h.store.GetNPMVersionByTarball(r.Context(), repo.ID, packageName, tarballName)
	if err != nil {
		h.writeError(w, http.StatusNotFound, "package tarball not found")
		h.recordAuditForTarget(r, repo, target, packageName+"/-/"+tarballName, actor, repository.AuditNotFound, http.StatusNotFound, 0, "bypass")
		return
	}
	if version.ObjectKey != "" {
		if h.metrics != nil {
			h.metrics.recordCache(repo.Name, true)
			h.metrics.recordNPMCacheHit()
		}
		h.tarballWithAudit(w, r, repo, packageName, tarballName, actor, "hit", target)
		return
	}
	if h.proxy == nil || version.UpstreamTarball == "" {
		h.writeError(w, http.StatusBadGateway, "npm upstream tarball is unavailable")
		h.recordNPMUpstreamFailureForTarget(r, repo, target, packageName+"@"+version.Version, actor, false)
		return
	}
	if h.metrics != nil {
		h.metrics.recordCache(repo.Name, false)
		h.metrics.recordNPMCacheMiss()
	}
	response, fetchErr := h.fetchNPMProxy(r.Context(), http.MethodGet, repo, version.UpstreamTarball, nil)
	if fetchErr != nil {
		h.recordNPMUpstreamFailureForTarget(r, repo, target, packageName+"@"+version.Version, actor, false)
		if errors.Is(fetchErr, errNPMUpstreamCircuitOpen) {
			h.writeError(w, http.StatusServiceUnavailable, "npm upstream circuit is open")
			return
		}
		h.writeError(w, http.StatusBadGateway, "npm upstream is unavailable")
		return
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotFound {
		h.writeError(w, http.StatusNotFound, "package tarball not found")
		h.recordAuditForTarget(r, repo, target, packageName+"@"+version.Version, actor, repository.AuditNotFound, http.StatusNotFound, 0, "miss")
		return
	}
	if response.StatusCode != http.StatusOK || response.ContentLength > npmTarballLimit {
		h.writeError(w, http.StatusBadGateway, "npm upstream tarball response is invalid")
		h.recordNPMUpstreamFailureForTarget(r, repo, target, packageName+"@"+version.Version, actor, false)
		return
	}
	spool, spoolErr := spoolUpload(response.Body, npmTarballLimit)
	if spoolErr != nil {
		h.writeError(w, http.StatusBadGateway, "npm upstream tarball is invalid")
		h.recordNPMUpstreamFailureForTarget(r, repo, target, packageName+"@"+version.Version, actor, false)
		return
	}
	defer func() { _ = spool.Close() }()
	sha512Hash, sha1Hash := sha512.New(), sha1.New()
	if _, err = io.Copy(io.MultiWriter(sha512Hash, sha1Hash), spool.Reader()); err != nil {
		h.writeError(w, http.StatusBadGateway, "npm upstream tarball is invalid")
		h.recordNPMUpstreamFailureForTarget(r, repo, target, packageName+"@"+version.Version, actor, false)
		return
	}
	integrity := "sha512-" + base64.StdEncoding.EncodeToString(sha512Hash.Sum(nil))
	shasum := hex.EncodeToString(sha1Hash.Sum(nil))
	if integrity != version.Integrity || shasum != version.Shasum {
		h.recordNPMUpstreamFailureForTarget(r, repo, target, packageName+"@"+version.Version, actor, true)
		h.writeError(w, http.StatusBadGateway, "npm upstream tarball integrity verification failed")
		return
	}
	if err = spool.Rewind(); err != nil || npmprotocol.ValidateTarballReader(spool.Reader(), packageName, version.Version) != nil {
		h.writeError(w, http.StatusBadGateway, "npm upstream tarball package identity is invalid")
		h.recordNPMUpstreamFailureForTarget(r, repo, target, packageName+"@"+version.Version, actor, false)
		return
	}
	if err = spool.Rewind(); err != nil {
		h.writeError(w, http.StatusInternalServerError, "prepare npm proxy tarball failed")
		h.recordAuditForTarget(r, repo, target, packageName+"@"+version.Version, actor, repository.AuditStorageError, http.StatusInternalServerError, 0, "miss")
		return
	}
	digest := spool.Digest()
	objectKey := "native/npm/sha256/" + strings.TrimPrefix(digest, "sha256:")
	objectRelease, lockErr := h.store.LockNPMObject(r.Context(), objectKey)
	if lockErr != nil {
		h.writeError(w, http.StatusServiceUnavailable, "npm object coordination is unavailable")
		h.recordAuditForTarget(r, repo, target, packageName+"@"+version.Version, actor, repository.AuditStorageError, http.StatusServiceUnavailable, 0, "miss")
		return
	}
	defer objectRelease()
	if err = h.objects.PutVerifiedReader(r.Context(), objectKey, spool.Reader(), spool.Size(), digest); err != nil {
		h.writeError(w, http.StatusInternalServerError, "persist npm proxy tarball failed")
		h.recordAuditForTarget(r, repo, target, packageName+"@"+version.Version, actor, repository.AuditStorageError, http.StatusInternalServerError, 0, "miss")
		return
	}
	version.Digest = digest
	version.ObjectKey = objectKey
	version.Size = spool.Size()
	version.CachedAt = time.Now().UTC()
	if _, err = h.store.CacheNPMProxyTarball(r.Context(), version); repository.IsQuotaExceeded(err) {
		h.writeError(w, http.StatusInsufficientStorage, "repository capacity quota exceeded")
		h.recordAuditForTarget(r, repo, target, packageName+"@"+version.Version, actor, repository.AuditStorageError, http.StatusInsufficientStorage, 0, "miss")
		return
	} else if err != nil {
		h.writeError(w, http.StatusInternalServerError, "commit npm proxy tarball failed")
		h.recordAuditForTarget(r, repo, target, packageName+"@"+version.Version, actor, repository.AuditStorageError, http.StatusInternalServerError, 0, "miss")
		return
	}
	h.tarballWithAudit(w, r, repo, packageName, tarballName, actor, "miss", target)
}

func (h nativeNPMHandler) protocolPrincipal(r *http.Request) (Principal, bool) {
	if principal, ok := h.auth.Authenticate(r.Header.Get("Authorization")); ok {
		return principal, true
	}
	username, password, ok := r.BasicAuth()
	if !ok {
		return Principal{}, false
	}
	return h.auth.AuthenticateBasic(username, password)
}

func (h nativeNPMHandler) tarballURL(r *http.Request, repositoryName, packageName, tarballName string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]); forwarded == "http" || forwarded == "https" {
		scheme = forwarded
	}
	return (&url.URL{Scheme: scheme, Host: r.Host, Path: "/npm/" + repositoryName + "/" + npmprotocol.PackagePath(packageName) + "/-/" + tarballName}).String()
}

func (h nativeNPMHandler) challenge(w http.ResponseWriter, status int, message string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="Artifact Gateway npm"`)
	h.writeError(w, status, message)
}

func (h nativeNPMHandler) writeError(w http.ResponseWriter, status int, message string) {
	h.writeJSON(w, nil, status, map[string]string{"error": message, "reason": message})
}

func (h nativeNPMHandler) writeJSON(w http.ResponseWriter, r *http.Request, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if r == nil || r.Method != http.MethodHead {
		_ = json.NewEncoder(w).Encode(value)
	}
}

func (h nativeNPMHandler) recordAudit(r *http.Request, repo repository.HostedRepository, resource, actor string, outcome repository.AuditOutcome, status int, bytes int64) {
	h.recordAuditWithDisposition(r, repo, resource, actor, outcome, status, bytes, "bypass")
}

func (h nativeNPMHandler) recordAuditWithDisposition(r *http.Request, repo repository.HostedRepository, resource, actor string, outcome repository.AuditOutcome, status int, bytes int64, disposition string) {
	h.recordAuditForTarget(r, repo, npmAuditTarget{GroupName: repo.Name, Repository: repo.Name}, resource, actor, outcome, status, bytes, disposition)
}

func (h nativeNPMHandler) recordAuditForTarget(r *http.Request, repo repository.HostedRepository, target npmAuditTarget, resource, actor string, outcome repository.AuditOutcome, status int, bytes int64, disposition string) {
	if h.audit == nil {
		return
	}
	if actor == "" {
		actor = anonymousActor
	}
	memberType := "hosted"
	upstreamHost := ""
	if repo.Type == repository.RepositoryTypeProxy {
		memberType = "proxy"
		if endpoint, err := url.Parse(repo.Endpoint); err == nil {
			upstreamHost = endpoint.Hostname()
		}
	}
	_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{
		GroupName: target.GroupName, Repository: target.Repository, MemberName: target.MemberName, Actor: actor, Outcome: outcome, OccurredAt: time.Now().UTC(),
		Format: "npm", Resource: resource, Representation: "package", MemberType: memberType, UpstreamHost: upstreamHost, Operation: strings.ToLower(r.Method),
		Status: status, CacheDisposition: disposition, Bytes: bytes,
	})
	if h.metrics != nil {
		h.metrics.recordNPMAudit(outcome, bytes, false)
	}
}

func (h nativeNPMHandler) recordNPMNegativeHitForTarget(r *http.Request, repo repository.HostedRepository, target npmAuditTarget, resource, actor string) {
	if h.metrics != nil {
		h.metrics.recordNPMNegativeCacheHit()
	}
	h.recordAuditForTarget(r, repo, target, resource, actor, repository.AuditNotFound, http.StatusNotFound, 0, "negative")
}

func (h nativeNPMHandler) recordNPMUpstreamFailureForTarget(r *http.Request, repo repository.HostedRepository, target npmAuditTarget, resource, actor string, integrityFailure bool) {
	h.recordAuditForTarget(r, repo, target, resource, actor, repository.AuditUpstreamError, http.StatusBadGateway, 0, "miss")
	if integrityFailure && h.metrics != nil {
		// recordAuditWithDisposition accounts for the upstream failure; move it
		// into the more specific integrity counter as well.
		h.metrics.npmIntegrityFailure.Add(1)
	}
}

func emptyNPMAuditReport() map[string]any {
	return map[string]any{
		"actions": []any{}, "advisories": map[string]any{}, "muted": []any{},
		"metadata": map[string]any{
			"vulnerabilities": map[string]int{"info": 0, "low": 0, "moderate": 0, "high": 0, "critical": 0, "total": 0},
			"dependencies":    0, "devDependencies": 0, "optionalDependencies": 0, "totalDependencies": 0,
		},
	}
}
