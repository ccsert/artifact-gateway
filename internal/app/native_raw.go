package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	rawprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/raw"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

// nativeRawHandler serves V3 Raw repositories directly from the object store.
// Unlike Raw Groups, it never consults an upstream member or cache index.
type nativeRawHandler struct {
	store              repository.NativeRawStore
	repos              repository.HostedRepositoryStore
	objects            OCIObjectStore
	auth               Authenticator
	authorizer         RepositoryAuthorizer
	audit              repository.Store
	metrics            *Metrics
	proxyClient        RawClient
	proxyCache         *RawCache
	publicationScanner *publicationScanScheduler
	readPolicies       repository.RepositoryQuarantineReadPolicyStore
	quarantine         repository.ArtifactQuarantineStore
}

func (h nativeRawHandler) withMetrics(metrics *Metrics) nativeRawHandler {
	h.metrics = metrics
	return h
}

func (h nativeRawHandler) withPublicationScanner(scanner publicationScanScheduler) nativeRawHandler {
	h.publicationScanner = &scanner
	return h
}

// withProxy wires the legacy Group proxy fetch path so a native proxy
// repository is served through the same upstream client and cache.
func (h nativeRawHandler) withProxy(client RawClient, cache *RawCache) nativeRawHandler {
	h.proxyClient = client
	h.proxyCache = cache
	return h
}

func newNativeRawHandler(store GatewayStore, objects OCIObjectStore, auth Authenticator) nativeRawHandler {
	if objects == nil {
		objects = NewMemoryOCIObjectStore()
	}
	return nativeRawHandler{store: store, repos: store, objects: objects, auth: auth, audit: store, readPolicies: store, quarantine: store, authorizer: RepositoryAuthorizer{
		Grants: store,
		Legacy: auth,
		LegacyFallback: func(Principal, repository.HostedRepository, RepositoryOperation) AuthorizationDecision {
			// Native Raw historically admitted every authenticated principal.
			return AuthorizationDecision{Allowed: true, Source: "legacy_protocol", Reason: "authenticated"}
		},
	}}
}

func (h nativeRawHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) bool {
	listRepositoryName, prefix, listing := rawprotocol.ParseDirectoryPath(r.URL.EscapedPath())
	repositoryName, path, ok := rawprotocol.ParsePath(r.URL.EscapedPath())
	if listing {
		repositoryName = listRepositoryName
		ok = true
	}
	if !ok {
		return false
	}
	repo, err := h.repos.GetHostedRepositoryByName(r.Context(), repositoryName)
	if errors.Is(err, repository.ErrNotFound) || repo.Format != repository.FormatRaw {
		return false
	}
	if err != nil || repo.State != repository.RepositoryActive {
		http.NotFound(w, r)
		return true
	}
	principal, ok := h.protocolPrincipal(r)
	if !ok {
		if anonymousHostedRepositoryReadAllowed(r.Context(), h.store, repo, r.Method) {
			principal = anonymousPrincipal()
		} else {
			w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return true
		}
	}
	operation := RepositoryWrite
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		operation = RepositoryRead
	}
	if !isAnonymous(principal) {
		if decision := h.authorizer.AuthorizeResource(r.Context(), principal, repo, operation, strings.TrimSuffix(path, "/")); !decision.Allowed {
			h.recordAuthorizationDenial(r, principal, repo, operation, decision)
			w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return true
		}
	}
	if repo.Type == repository.RepositoryTypeProxy {
		h.proxyRead(w, r, repo, path, principal)
		return true
	}
	if listing {
		h.list(w, r, repo, prefix)
		return true
	}
	if id := r.URL.Query().Get("uploadId"); id != "" {
		h.upload(w, r, repo, path, id, principal.Actor)
		return true
	}
	if r.Method == http.MethodPost && r.URL.Query().Get("resumable") == "1" {
		h.startUpload(w, r, repo, path)
		return true
	}
	if rawChecksumExtension(path) != "" {
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			h.checksum(w, r, repo, path, principal)
		case http.MethodPut:
			h.verifyChecksumUpload(w, r, repo, path)
		case http.MethodDelete:
			w.WriteHeader(http.StatusMethodNotAllowed)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return true
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		asset, err := h.store.GetRawAsset(r.Context(), repo.ID, path)
		if err != nil {
			http.NotFound(w, r)
			return true
		}
		if h.blockQuarantinedRead(w, r, repo, asset, principal) {
			return true
		}
		serveNativeRawObject(w, r, path, asset, h.objects)
	case http.MethodPut:
		spool, err := spoolUpload(r.Body, 1<<30)
		if err != nil {
			if errors.Is(err, errUploadTooLarge) {
				http.Error(w, "raw object is too large", http.StatusRequestEntityTooLarge)
			} else {
				http.Error(w, "read raw object failed", http.StatusBadRequest)
			}
			return true
		}
		defer func() { _ = spool.Close() }()
		digest := spool.Digest()
		if requested := r.Header.Get("Digest"); requested != "" && requested != "sha-256="+base64.StdEncoding.EncodeToString(spool.DigestBytes()) {
			http.Error(w, "digest mismatch", http.StatusUnprocessableEntity)
			return true
		}
		key := "native/raw/sha256/" + strings.TrimPrefix(digest, "sha256:")
		release, lockErr := h.store.LockRawObject(r.Context(), digest)
		if lockErr != nil {
			http.Error(w, "raw object coordination is unavailable", http.StatusServiceUnavailable)
			return true
		}
		defer release()
		if err = h.store.StageRawObject(r.Context(), repository.RawObject{RepositoryID: repo.ID, Digest: digest, ObjectKey: key, Size: spool.Size()}); err != nil {
			http.Error(w, "stage raw object failed", http.StatusInternalServerError)
			return true
		}
		if err = h.objects.PutVerifiedReader(r.Context(), key, spool.Reader(), spool.Size(), digest); err != nil {
			http.Error(w, "persist raw object failed", http.StatusInternalServerError)
			return true
		}
		contentType := r.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		if _, err = h.store.PutRawAsset(r.Context(), repository.RawAsset{RepositoryID: repo.ID, Path: path, Digest: digest, ObjectKey: key, Size: spool.Size(), ContentType: contentType}); err != nil {
			if repository.IsQuotaExceeded(err) {
				http.Error(w, "repository capacity quota exceeded", http.StatusInsufficientStorage)
				return true
			}
			http.Error(w, "publish raw object failed", http.StatusInternalServerError)
			return true
		}
		w.Header().Set("ETag", `"`+digest+`"`)
		w.Header().Set("Digest", "sha-256="+base64.StdEncoding.EncodeToString(spool.DigestBytes()))
		if h.publicationScanner != nil {
			_ = h.publicationScanner.Schedule(r.Context(), repo, path, digest, principal.Actor)
		}
		w.WriteHeader(http.StatusCreated)
	case http.MethodDelete:
		if err := h.store.DeleteRawAsset(r.Context(), repo.ID, path); err != nil {
			http.NotFound(w, r)
			return true
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
	return true
}

func (h nativeRawHandler) protocolPrincipal(r *http.Request) (Principal, bool) {
	if principal, ok := h.auth.Authenticate(r.Header.Get("Authorization")); ok {
		return principal, true
	}
	username, password, ok := r.BasicAuth()
	if !ok {
		return Principal{}, false
	}
	return h.auth.AuthenticateBasic(username, password)
}

func (h nativeRawHandler) startUpload(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, path string) {
	id := uuid.NewString()
	upload := repository.RawUpload{ID: id, RepositoryID: repo.ID, Path: path, ObjectKey: "native/raw/uploads/" + id, State: "open", ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if _, err := h.store.CreateRawUpload(r.Context(), upload); err != nil {
		http.Error(w, "create raw upload failed", 500)
		return
	}
	h.uploadHeaders(r.Context(), w, repo.Name, upload)
	w.WriteHeader(http.StatusCreated)
}

func (h nativeRawHandler) upload(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, path, id, actor string) {
	release, err := h.store.LockRawUpload(r.Context(), id)
	if err != nil {
		http.Error(w, "raw upload coordination is unavailable", http.StatusServiceUnavailable)
		return
	}
	defer release()
	upload, err := h.store.GetRawUpload(r.Context(), id)
	if err != nil || upload.RepositoryID != repo.ID || upload.Path != path || upload.State != "open" || time.Now().After(upload.ExpiresAt) {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.uploadHeaders(r.Context(), w, repo.Name, upload)
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		upload, err = h.store.CancelRawUpload(r.Context(), id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if err := deleteUploadObjects(r.Context(), h.objects, upload.ObjectKey); err == nil {
			_ = h.store.MarkRawUploadCollected(r.Context(), upload.ID)
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPatch:
		upload, err = h.appendUpload(r.Context(), r, upload)
		if err != nil {
			http.Error(w, "upload offset is invalid", http.StatusConflict)
			return
		}
		h.uploadHeaders(r.Context(), w, repo.Name, upload)
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPut:
		if r.URL.Query().Get("complete") != "1" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.ContentLength != 0 {
			upload, err = h.appendUpload(r.Context(), r, upload)
			if err != nil {
				http.Error(w, "upload offset is invalid", http.StatusConflict)
				return
			}
		}
		spool, err := spoolStoredUpload(r.Context(), h.objects, upload.ObjectKey, upload.Offset)
		if err != nil {
			http.Error(w, "upload bytes are unavailable", 500)
			return
		}
		defer func() { _ = spool.Close() }()
		digest, size := spool.Digest(), spool.Size()
		expected := "sha-256=" + base64.StdEncoding.EncodeToString(spool.DigestBytes())
		if r.Header.Get("Digest") != expected {
			http.Error(w, "digest mismatch", http.StatusUnprocessableEntity)
			return
		}
		key := "native/raw/sha256/" + strings.TrimPrefix(digest, "sha256:")
		releaseObject, err := h.store.LockRawObject(r.Context(), digest)
		if err != nil {
			http.Error(w, "raw object coordination is unavailable", http.StatusServiceUnavailable)
			return
		}
		defer releaseObject()
		if err = h.objects.PutVerifiedReader(r.Context(), key, spool.Reader(), size, digest); err != nil {
			http.Error(w, "persist raw object failed", 500)
			return
		}
		contentType := r.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		asset, err := h.store.CompleteRawUpload(r.Context(), id, repository.RawAsset{RepositoryID: repo.ID, Path: path, Digest: digest, ObjectKey: key, Size: size, ContentType: contentType})
		if err != nil {
			http.Error(w, "raw upload cannot be completed", http.StatusConflict)
			return
		}
		if err := deleteUploadObjects(r.Context(), h.objects, upload.ObjectKey); err == nil {
			_ = h.store.MarkRawUploadCollected(r.Context(), upload.ID)
		}
		w.Header().Set("ETag", `"`+asset.Digest+`"`)
		w.Header().Set("Digest", expected)
		if h.publicationScanner != nil {
			_ = h.publicationScanner.Schedule(r.Context(), repo, asset.Path, asset.Digest, actor)
		}
		w.WriteHeader(http.StatusCreated)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h nativeRawHandler) appendUpload(ctx context.Context, r *http.Request, upload repository.RawUpload) (repository.RawUpload, error) {
	if raw := r.Header.Get("Upload-Offset"); raw != "" {
		offset, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || offset != upload.Offset {
			return upload, errors.New("offset mismatch")
		}
	}
	spool, chunkSize, err := spoolObjectAppend(ctx, h.objects, upload.ObjectKey, upload.Offset, r.Body, 1<<30)
	if err != nil {
		return upload, err
	}
	defer func() { _ = spool.Close() }()
	partKey := uploadPartKey(upload.ObjectKey, upload.Offset)
	if err = h.objects.PutReader(ctx, partKey, spool.Reader(), spool.Size()); err != nil {
		return upload, err
	}
	updated, err := h.store.UpdateRawUpload(ctx, upload.ID, upload.Offset+chunkSize)
	if err != nil {
		// The database result can be ambiguous after a timeout. Keep the
		// offset-addressed part: a retry can overwrite it, and upload reclaim
		// removes it if the session never advances.
		return upload, err
	}
	return updated, nil
}
func (h nativeRawHandler) uploadHeaders(ctx context.Context, w http.ResponseWriter, repositoryName string, upload repository.RawUpload) {
	location := "/raw/" + repositoryName + "/" + upload.Path
	if externalPath, ok := nexusRepositoryCompatibilityExternalEscapedPath(ctx, repositoryName); ok {
		location = externalPath
	}
	w.Header().Set("Location", location+"?uploadId="+upload.ID)
	w.Header().Set("Upload-Offset", strconv.FormatInt(upload.Offset, 10))
}

func rawChecksumExtension(path string) string {
	if strings.HasSuffix(path, ".sha256") {
		return ".sha256"
	}
	if strings.HasSuffix(path, ".sha512") {
		return ".sha512"
	}
	return ""
}

func rawChecksumSourcePath(path string) string {
	return strings.TrimSuffix(path, rawChecksumExtension(path))
}

func (h nativeRawHandler) checksum(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, path string, principal Principal) {
	source := rawChecksumSourcePath(path)
	if rawChecksumExtension(source) != "" {
		http.NotFound(w, r)
		return
	}
	asset, err := h.store.GetRawAsset(r.Context(), repo.ID, source)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if h.blockQuarantinedRead(w, r, repo, asset, principal) {
		return
	}
	body, err := h.rawChecksum(r.Context(), asset, rawChecksumExtension(path))
	if err != nil {
		http.Error(w, "raw object unavailable", http.StatusInternalServerError)
		return
	}
	h.serveChecksum(w, r, body)
}

func (h nativeRawHandler) verifyChecksumUpload(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, path string) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "checksum sidecar is too large", http.StatusRequestEntityTooLarge)
		return
	}
	if !rawprotocol.ValidChecksum(path, body) {
		http.Error(w, "invalid checksum sidecar", http.StatusUnprocessableEntity)
		return
	}
	source := rawChecksumSourcePath(path)
	if rawChecksumExtension(source) != "" {
		http.Error(w, "checksum sidecar target is invalid", http.StatusBadRequest)
		return
	}
	asset, err := h.store.GetRawAsset(r.Context(), repo.ID, source)
	if err != nil {
		http.Error(w, "checksum sidecar target is unavailable", http.StatusConflict)
		return
	}
	expected, err := h.rawChecksum(r.Context(), asset, rawChecksumExtension(path))
	if err != nil {
		http.Error(w, "raw object unavailable", http.StatusInternalServerError)
		return
	}
	if !bytes.Equal(body, expected) {
		http.Error(w, "checksum mismatch", http.StatusUnprocessableEntity)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h nativeRawHandler) rawChecksum(ctx context.Context, asset repository.RawAsset, extension string) ([]byte, error) {
	if extension == ".sha256" {
		return []byte(strings.TrimPrefix(asset.Digest, "sha256:") + "\n"), nil
	}
	reader, _, err := h.objects.Open(ctx, asset.ObjectKey)
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()
	sum := sha512.New()
	if _, err = io.Copy(sum, reader); err != nil {
		return nil, err
	}
	return []byte(hex.EncodeToString(sum.Sum(nil)) + "\n"), nil
}

func (h nativeRawHandler) serveChecksum(w http.ResponseWriter, r *http.Request, body []byte) {
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	etag := `"sha256-` + digest + `"`
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("ETag", etag)
	w.Header().Set("Digest", "sha-256="+base64.StdEncoding.EncodeToString(sum[:]))
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(body)
	}
}

func (h nativeRawHandler) list(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, prefix string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("n"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 1000 {
			http.Error(w, "list page size must be between 1 and 1000", http.StatusBadRequest)
			return
		}
		limit = n
	}
	after := r.URL.Query().Get("last")
	if !validRawListCursor(repo.Name, prefix, after) {
		http.Error(w, "list cursor is invalid", http.StatusBadRequest)
		return
	}
	assets := make([]repository.RawAsset, 0, limit+1)
	cursor := after
	for len(assets) < limit+1 {
		batch, err := h.store.ListRawAssets(r.Context(), repo.ID, prefix, limit+1, cursor)
		if err != nil {
			http.Error(w, "list raw objects failed", http.StatusInternalServerError)
			return
		}
		if len(batch) == 0 {
			break
		}
		for _, asset := range batch {
			blocked, blockErr := repository.QuarantinedArtifactReadBlocked(r.Context(), h.readPolicies, h.quarantine, repo.ID, repository.FormatRaw, asset.Path, asset.Digest)
			if blockErr != nil {
				http.Error(w, "list raw objects failed", http.StatusInternalServerError)
				return
			}
			if !blocked {
				assets = append(assets, asset)
				if len(assets) == limit+1 {
					break
				}
			}
		}
		cursor = batch[len(batch)-1].Path
		if len(batch) < limit+1 {
			break
		}
	}
	if len(assets) > limit {
		assets = assets[:limit]
		nextPath := r.URL.EscapedPath()
		if externalPath, ok := nexusRepositoryCompatibilityExternalEscapedPath(r.Context(), repo.Name); ok {
			nextPath = externalPath
		}
		next := nextPath + "?n=" + strconv.Itoa(limit) + "&last=" + url.QueryEscape(assets[len(assets)-1].Path)
		w.Header().Set("Link", "<"+next+">; rel=\"next\"")
	}
	type item struct {
		Path        string `json:"path"`
		Digest      string `json:"digest"`
		Size        int64  `json:"size"`
		ContentType string `json:"contentType"`
	}
	items := make([]item, 0, len(assets))
	for _, asset := range assets {
		items = append(items, item{Path: asset.Path, Digest: asset.Digest, Size: asset.Size, ContentType: asset.ContentType})
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"path": prefix, "items": items})
}

func validRawListCursor(repositoryName, prefix, cursor string) bool {
	if cursor == "" {
		return true
	}
	_, canonical, ok := rawprotocol.ParsePath("/raw/" + repositoryName + "/" + cursor)
	return ok && canonical == cursor && strings.HasPrefix(cursor, prefix)
}

// proxyRead serves a read against a native proxy repository through the legacy
// Group proxy fetch and cache path. The repository name is the cache and audit
// namespace (the group slot); the upstream object path is the remainder.
// Proxy repositories are read-only.
func (h nativeRawHandler) proxyRead(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, path string, principal Principal) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.proxyClient == nil {
		http.NotFound(w, r)
		return
	}
	member := repository.Member{Type: repository.MemberProxy, Name: repo.Name, Endpoint: repo.Endpoint, AllowedHosts: repo.AllowedHosts, EgressProxy: repo.EgressProxy}
	if !rawprotocol.MemberProxyAllowed(member) {
		h.proxyAudit(r, repo, path, member, principal.Actor, repository.AuditProxyDenied, http.StatusForbidden, "bypass", 0)
		http.Error(w, "upstream repository is not allowed", http.StatusForbidden)
		return
	}
	key := ""
	if h.proxyCache != nil {
		key = h.proxyCache.Key(repo.Name, path, member.Name, member.Endpoint)
		if content, err := h.proxyCache.Load(r.Context(), key); err == nil {
			served := rawprotocol.ServeContent(w, r, path, rawprotocol.Content{Digest: content.Digest, ContentType: content.ContentType, Size: content.Size, Source: content})
			h.proxyAudit(r, repo, path, member, principal.Actor, repository.AuditResolved, served.Status, "hit", served.Bytes)
			return
		} else if errors.Is(err, errRawCacheNegative) {
			h.proxyAudit(r, repo, path, member, principal.Actor, repository.AuditNotFound, http.StatusNotFound, "hit", 0)
			http.NotFound(w, r)
			return
		}
	}
	if r.Method == http.MethodHead {
		response, err := h.proxyClient.FetchRaw(r.Context(), http.MethodHead, member, path, nil)
		if err != nil {
			h.proxyAudit(r, repo, path, member, principal.Actor, repository.AuditUpstreamError, http.StatusBadGateway, "bypass", 0)
			http.Error(w, "upstream repository unavailable", http.StatusBadGateway)
			return
		}
		_ = response.Body.Close()
		if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone {
			if h.proxyCache != nil {
				_ = h.proxyCache.StoreNegative(r.Context(), key, member)
			}
			h.proxyAudit(r, repo, path, member, principal.Actor, repository.AuditNotFound, http.StatusNotFound, "miss", 0)
			http.NotFound(w, r)
			return
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			h.proxyAudit(r, repo, path, member, principal.Actor, repository.AuditUpstreamError, http.StatusBadGateway, "bypass", 0)
			http.Error(w, "upstream repository unavailable", http.StatusBadGateway)
			return
		}
		copyRawHeadHeaders(w.Header(), response.Header)
		w.WriteHeader(http.StatusOK)
		h.proxyAudit(r, repo, path, member, principal.Actor, repository.AuditResolved, http.StatusOK, "miss", 0)
		return
	}
	workCtx := r.Context()
	release := func() error { return nil }
	if h.proxyCache != nil {
		var lockErr error
		workCtx, release, lockErr = h.proxyCache.AcquireRequestLock(r.Context(), key)
		if lockErr != nil {
			http.Error(w, "unable to coordinate Raw cache fetch", http.StatusServiceUnavailable)
			return
		}
		if content, loadErr := h.proxyCache.Load(workCtx, key); loadErr == nil {
			if releaseErr := release(); releaseErr != nil {
				http.Error(w, "unable to coordinate Raw cache fetch", http.StatusServiceUnavailable)
				return
			}
			served := rawprotocol.ServeContent(w, r, path, rawprotocol.Content{Digest: content.Digest, ContentType: content.ContentType, Size: content.Size, Source: content})
			h.proxyAudit(r, repo, path, member, principal.Actor, repository.AuditResolved, served.Status, "hit", served.Bytes)
			return
		} else if errors.Is(loadErr, errRawCacheNegative) {
			if releaseErr := release(); releaseErr != nil {
				http.Error(w, "unable to coordinate Raw cache fetch", http.StatusServiceUnavailable)
				return
			}
			h.proxyAudit(r, repo, path, member, principal.Actor, repository.AuditNotFound, http.StatusNotFound, "hit", 0)
			http.NotFound(w, r)
			return
		}
	}
	releaseSpool := func() {}
	if h.proxyCache != nil {
		var spoolErr error
		releaseSpool, spoolErr = h.proxyCache.AcquireSpool()
		if spoolErr != nil {
			if releaseErr := release(); releaseErr != nil {
				http.Error(w, "unable to coordinate Raw cache fetch", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Retry-After", "1")
			if h.metrics != nil {
				h.metrics.recordRawSpoolRejected()
			}
			h.proxyAudit(r, repo, path, member, principal.Actor, repository.AuditStorageError, http.StatusServiceUnavailable, "miss", 0)
			http.Error(w, "Raw cache staging capacity is full", http.StatusServiceUnavailable)
			return
		}
	}
	releaseAll := func() error {
		releaseSpool()
		return release()
	}
	response, err := h.proxyClient.FetchRaw(workCtx, http.MethodGet, member, path, nil)
	if err != nil {
		if releaseErr := releaseAll(); releaseErr != nil {
			http.Error(w, "unable to coordinate Raw cache fetch", http.StatusServiceUnavailable)
			return
		}
		h.proxyAudit(r, repo, path, member, principal.Actor, repository.AuditUpstreamError, http.StatusBadGateway, "bypass", 0)
		http.Error(w, "upstream repository unavailable", http.StatusBadGateway)
		return
	}
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone {
		_ = response.Body.Close()
		if h.proxyCache != nil {
			_ = h.proxyCache.StoreNegative(workCtx, key, member)
		}
		if releaseErr := releaseAll(); releaseErr != nil {
			http.Error(w, "unable to coordinate Raw cache fetch", http.StatusServiceUnavailable)
			return
		}
		h.proxyAudit(r, repo, path, member, principal.Actor, repository.AuditNotFound, http.StatusNotFound, "miss", 0)
		http.NotFound(w, r)
		return
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_ = response.Body.Close()
		if releaseErr := releaseAll(); releaseErr != nil {
			http.Error(w, "unable to coordinate Raw cache fetch", http.StatusServiceUnavailable)
			return
		}
		h.proxyAudit(r, repo, path, member, principal.Actor, repository.AuditUpstreamError, http.StatusBadGateway, "bypass", 0)
		http.Error(w, "upstream repository unavailable", http.StatusBadGateway)
		return
	}
	limit := defaultRawMaxObjectBytes
	if h.proxyCache != nil && h.proxyCache.MaxObjectBytes() > 0 {
		limit = h.proxyCache.MaxObjectBytes()
	}
	content, readErr := rawprotocol.StageContent(response.Body, limit)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		content.Cleanup()
		if releaseErr := releaseAll(); releaseErr != nil {
			http.Error(w, "unable to coordinate Raw cache fetch", http.StatusServiceUnavailable)
			return
		}
		h.proxyAudit(r, repo, path, member, principal.Actor, repository.AuditUpstreamError, http.StatusBadGateway, "bypass", 0)
		http.Error(w, "upstream repository unavailable", http.StatusBadGateway)
		return
	}
	contentType := response.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if strings.HasSuffix(path, ".sha256") || strings.HasSuffix(path, ".sha512") {
		if !validRawProxyChecksum(workCtx, path, content) {
			content.Cleanup()
			if releaseErr := releaseAll(); releaseErr != nil {
				http.Error(w, "unable to coordinate Raw cache fetch", http.StatusServiceUnavailable)
				return
			}
			h.proxyAudit(r, repo, path, member, principal.Actor, repository.AuditUpstreamError, http.StatusBadGateway, "bypass", 0)
			http.Error(w, "invalid checksum sidecar", http.StatusBadGateway)
			return
		}
	}
	content.ContentType, content.Member, content.Endpoint = contentType, member.Name, member.Endpoint
	content.Repository, content.Path = repo.Name, path
	if h.proxyCache != nil {
		if err := h.proxyCache.Store(workCtx, key, content); err != nil && !errors.Is(err, ErrCacheQuotaExceeded) {
			content.Cleanup()
			if releaseErr := releaseAll(); releaseErr != nil {
				http.Error(w, "unable to coordinate Raw cache fetch", http.StatusServiceUnavailable)
				return
			}
			h.proxyAudit(r, repo, path, member, principal.Actor, repository.AuditUpstreamError, http.StatusInternalServerError, "bypass", 0)
			http.Error(w, "unable to cache Raw content", http.StatusInternalServerError)
			return
		}
	}
	if releaseErr := release(); releaseErr != nil {
		content.Cleanup()
		releaseSpool()
		http.Error(w, "unable to coordinate Raw cache fetch", http.StatusServiceUnavailable)
		return
	}
	served := rawprotocol.ServeContent(w, r, path, rawprotocol.Content{Digest: content.Digest, ContentType: content.ContentType, Size: content.Size, Source: content})
	content.Cleanup()
	releaseSpool()
	h.proxyAudit(r, repo, path, member, principal.Actor, repository.AuditResolved, served.Status, "miss", served.Bytes)
}

func validRawProxyChecksum(ctx context.Context, path string, content RawContent) bool {
	if content.Size > 129 {
		return false
	}
	reader, _, err := content.Open(ctx)
	if err != nil {
		return false
	}
	body, readErr := io.ReadAll(io.LimitReader(reader, 130))
	closeErr := reader.Close()
	return readErr == nil && closeErr == nil && rawprotocol.ValidChecksum(path, body)
}

func copyRawHeadHeaders(target, source http.Header) {
	for _, name := range []string{"Accept-Ranges", "Content-Length", "Content-Type", "Digest", "ETag", "Last-Modified"} {
		if value := source.Get(name); value != "" {
			target.Set(name, value)
		}
	}
}

func (h nativeRawHandler) proxyAudit(r *http.Request, repo repository.HostedRepository, path string, member repository.Member, actor string, outcome repository.AuditOutcome, status int, disposition string, bytes int64) {
	if h.audit == nil {
		return
	}
	upstreamHost := ""
	if endpoint, err := url.Parse(member.Endpoint); err == nil {
		upstreamHost = endpoint.Hostname()
	}
	audit := repository.AuditRecord{
		GroupName: repo.Name, Repository: repo.Name, MemberName: member.Name, Actor: actor, Outcome: outcome, OccurredAt: time.Now().UTC(),
		Format: "raw", Resource: path, Representation: "body", MemberType: string(member.Type), UpstreamHost: upstreamHost,
		Operation: strings.ToLower(r.Method), Status: status, CacheDisposition: disposition, Bytes: bytes,
		RequestID: rawAuditRequestID(r.Context()), TraceID: rawAuditTraceID(r.Context()),
	}
	if actor == anonymousActor {
		audit.AuthorizationSource, audit.AuthorizationReason = anonymousAuthorizationSource, anonymousAuthorizationReason
	}
	_ = h.audit.RecordAudit(r.Context(), audit)
}

func (h nativeRawHandler) recordAuthorizationDenial(r *http.Request, principal Principal, repo repository.HostedRepository, operation RepositoryOperation, decision AuthorizationDecision) {
	if h.metrics != nil {
		h.metrics.recordRepositoryAuthorizationDenied("raw", decision.Source, decision.Reason)
	}
	if h.audit == nil {
		return
	}
	_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{
		GroupName: repo.Name, Repository: repo.Name, Actor: principal.Actor, Outcome: repository.AuditAccessDenied, OccurredAt: time.Now().UTC(),
		Format: "raw", Resource: strings.TrimPrefix(r.URL.Path, "/raw/"+repo.Name+"/"), Operation: string(operation), Status: http.StatusUnauthorized, CacheDisposition: "bypass",
		AuthorizationSource: decision.Source, AuthorizationReason: decision.Reason,
	})
}

func serveNativeRawObject(w http.ResponseWriter, r *http.Request, name string, asset repository.RawAsset, objects OCIObjectStore) rawprotocol.ServeResult {
	statusWriter := &nativeRawStatusWriter{ResponseWriter: w}
	digest := strings.TrimPrefix(asset.Digest, "sha256:")
	w.Header().Set("Content-Type", asset.ContentType)
	etag := `"sha256-` + digest + `"`
	w.Header().Set("ETag", etag)
	if decoded, err := hex.DecodeString(digest); err == nil {
		w.Header().Set("Digest", "sha-256="+base64.StdEncoding.EncodeToString(decoded))
	}
	if r.Header.Get("If-None-Match") == etag {
		statusWriter.WriteHeader(http.StatusNotModified)
		return statusWriter.result()
	}
	if r.Method == http.MethodGet && r.Header.Get("Range") != "" {
		start, end, ok := parseOCIRange(statusWriter, r, asset.Size)
		if !ok {
			return statusWriter.result()
		}
		length := end - start + 1
		reader, _, err := objects.OpenRange(r.Context(), asset.ObjectKey, start, length)
		if err != nil {
			http.Error(statusWriter, "raw object unavailable", http.StatusInternalServerError)
			return statusWriter.result()
		}
		defer func() { _ = reader.Close() }()
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Range", "bytes "+utoa(uint64(start))+"-"+utoa(uint64(end))+"/"+utoa(uint64(asset.Size)))
		w.Header().Set("Content-Length", utoa(uint64(length)))
		statusWriter.WriteHeader(http.StatusPartialContent)
		_, _ = io.CopyN(statusWriter, reader, length)
		return statusWriter.result()
	}
	reader, size, err := objects.Open(r.Context(), asset.ObjectKey)
	if err != nil {
		http.Error(statusWriter, "raw object unavailable", http.StatusInternalServerError)
		return statusWriter.result()
	}
	defer func() { _ = reader.Close() }()
	if asset.Size > 0 {
		size = asset.Size
	}
	w.Header().Set("Content-Length", utoa(uint64(size)))
	statusWriter.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = io.Copy(statusWriter, reader)
	}
	return statusWriter.result()
}

type nativeRawStatusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *nativeRawStatusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *nativeRawStatusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(body)
	w.bytes += int64(n)
	return n, err
}

func (w *nativeRawStatusWriter) result() rawprotocol.ServeResult {
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return rawprotocol.ServeResult{Status: status}
	}
	return rawprotocol.ServeResult{Status: status, Bytes: w.bytes}
}
