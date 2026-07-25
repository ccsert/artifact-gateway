package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	rawprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/raw"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// nativeRawHandler serves V3 Raw repositories directly from the object store.
// Unlike Raw Groups, it never consults an upstream member or cache index.
type nativeRawHandler struct {
	store      repository.NativeRawStore
	repos      repository.HostedRepositoryStore
	objects    OCIObjectStore
	auth       Authenticator
	authorizer RepositoryAuthorizer
	audit      repository.Store
	metrics    *Metrics
}

func (h nativeRawHandler) withMetrics(metrics *Metrics) nativeRawHandler {
	h.metrics = metrics
	return h
}

func newNativeRawHandler(store GatewayStore, objects OCIObjectStore, auth Authenticator) nativeRawHandler {
	if objects == nil {
		objects = NewMemoryOCIObjectStore()
	}
	return nativeRawHandler{store: store, repos: store, objects: objects, auth: auth, audit: store, authorizer: RepositoryAuthorizer{
		Grants: store,
		Legacy: auth,
		LegacyFallback: func(Principal, repository.HostedRepository, RepositoryOperation) AuthorizationDecision {
			// Native Raw historically admitted every authenticated principal.
			return AuthorizationDecision{Allowed: true, Source: "legacy_protocol", Reason: "authenticated"}
		},
	}}
}

func (h nativeRawHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) bool {
	repositoryName, path, ok := rawprotocol.ParsePath(r.URL.EscapedPath())
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
	principal, ok := h.auth.Authenticate(r.Header.Get("Authorization"))
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway"`)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return true
	}
	operation := RepositoryWrite
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		operation = RepositoryRead
	}
	if decision := h.authorizer.Authorize(r.Context(), principal, repo, operation); !decision.Allowed {
		h.recordAuthorizationDenial(r, principal, repo, operation, decision)
		w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway"`)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return true
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		asset, err := h.store.GetRawAsset(r.Context(), repo.ID, path)
		if err != nil {
			http.NotFound(w, r)
			return true
		}
		serveNativeRawObject(w, r, path, asset, h.objects)
	case http.MethodPut:
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<30))
		if err != nil {
			http.Error(w, "raw object is too large", http.StatusRequestEntityTooLarge)
			return true
		}
		sum := sha256.Sum256(body)
		digest := "sha256:" + hex.EncodeToString(sum[:])
		if requested := r.Header.Get("Digest"); requested != "" && requested != "sha-256="+base64.StdEncoding.EncodeToString(sum[:]) {
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
		if err = h.store.StageRawObject(r.Context(), repository.RawObject{RepositoryID: repo.ID, Digest: digest, ObjectKey: key, Size: int64(len(body))}); err != nil {
			http.Error(w, "stage raw object failed", http.StatusInternalServerError)
			return true
		}
		if err = h.objects.PutVerifiedReader(r.Context(), key, bytes.NewReader(body), int64(len(body)), digest); err != nil {
			http.Error(w, "persist raw object failed", http.StatusInternalServerError)
			return true
		}
		contentType := r.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		if _, err = h.store.PutRawAsset(r.Context(), repository.RawAsset{RepositoryID: repo.ID, Path: path, Digest: digest, ObjectKey: key, Size: int64(len(body)), ContentType: contentType}); err != nil {
			http.Error(w, "publish raw object failed", http.StatusInternalServerError)
			return true
		}
		w.Header().Set("ETag", `"`+digest+`"`)
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
