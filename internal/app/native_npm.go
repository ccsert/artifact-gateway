package app

import (
	"bytes"
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
)

type nativeNPMHandler struct {
	store      repository.NativeNPMStore
	repos      repository.HostedRepositoryStore
	objects    OCIObjectStore
	auth       Authenticator
	authorizer RepositoryAuthorizer
	audit      repository.Store
	metrics    *Metrics
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

func newNativeNPMHandler(store GatewayStore, objects OCIObjectStore, auth Authenticator) nativeNPMHandler {
	if objects == nil {
		objects = NewMemoryOCIObjectStore()
	}
	return nativeNPMHandler{
		store: store, repos: store, objects: objects, auth: auth, audit: store,
		authorizer: RepositoryAuthorizer{Grants: store, Legacy: auth, LegacyFallback: func(Principal, repository.HostedRepository, RepositoryOperation) AuthorizationDecision {
			return AuthorizationDecision{Allowed: true, Source: "legacy_protocol", Reason: "authenticated"}
		}},
	}
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
	if err != nil || repo.State != repository.RepositoryActive || repo.Type != repository.RepositoryTypeHosted {
		h.writeError(w, http.StatusNotFound, "repository is unavailable")
		return true
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
			h.packument(w, r, repo, route.Package, principal.Actor)
		case http.MethodPut:
			h.publish(w, r, repo, route.Package, principal.Actor)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	case npmprotocol.RouteTarball:
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return true
		}
		h.tarball(w, r, repo, route.Package, route.Tarball, principal.Actor)
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

func (h nativeNPMHandler) packument(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, packageName, actor string) {
	pkg, err := h.store.GetNPMPackage(r.Context(), repo.ID, packageName)
	if errors.Is(err, repository.ErrNotFound) {
		h.writeError(w, http.StatusNotFound, "package not found")
		return
	}
	if err != nil {
		h.writeError(w, http.StatusServiceUnavailable, "package metadata unavailable")
		return
	}
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
			"tarball":   h.tarballURL(r, repo.Name, packageName, version.TarballName),
			"integrity": version.Integrity,
			"shasum":    version.Shasum,
		}
		document["_artifactGateway"] = map[string]any{
			"digest": version.Digest, "publisher": version.Publisher, "size": version.Size,
		}
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
	h.recordAudit(r, repo, packageName, actor, repository.AuditResolved, http.StatusOK, int64(len(encoded)))
}

func (h nativeNPMHandler) tarball(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, packageName, tarballName, actor string) {
	version, err := h.store.GetNPMVersionByTarball(r.Context(), repo.ID, packageName, tarballName)
	if errors.Is(err, repository.ErrNotFound) {
		h.writeError(w, http.StatusNotFound, "package tarball not found")
		return
	}
	if err != nil {
		h.writeError(w, http.StatusServiceUnavailable, "package tarball unavailable")
		return
	}
	result := serveNativeRawObject(w, r, tarballName, repository.RawAsset{
		Digest: version.Digest, ObjectKey: version.ObjectKey, Size: version.Size,
		ContentType: "application/octet-stream",
	}, h.objects)
	h.recordAudit(r, repo, packageName+"@"+version.Version, actor, repository.AuditResolved, result.Status, result.Bytes)
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
	if h.audit == nil {
		return
	}
	if actor == "" {
		actor = anonymousActor
	}
	_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{
		GroupName: repo.Name, Repository: repo.Name, Actor: actor, Outcome: outcome, OccurredAt: time.Now().UTC(),
		Format: "npm", Resource: resource, Representation: "package", Operation: strings.ToLower(r.Method),
		Status: status, CacheDisposition: "bypass", Bytes: bytes,
	})
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
