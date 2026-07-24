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

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// nativeRawHandler serves V3 Raw repositories directly from the object store.
// Unlike Raw Groups, it never consults an upstream member or cache index.
type nativeRawHandler struct {
	store   repository.NativeRawStore
	repos   repository.HostedRepositoryStore
	objects OCIObjectStore
	auth    Authenticator
}

func newNativeRawHandler(store GatewayStore, objects OCIObjectStore, auth Authenticator) nativeRawHandler {
	if objects == nil {
		objects = NewMemoryOCIObjectStore()
	}
	return nativeRawHandler{store: store, repos: store, objects: objects, auth: auth}
}

func (h nativeRawHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) bool {
	repositoryName, path, ok := parseRawPath(r.URL.EscapedPath())
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
	if _, ok := h.auth.Authenticate(r.Header.Get("Authorization")); !ok {
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
		body, err := h.objects.Get(r.Context(), asset.ObjectKey)
		if err != nil {
			http.Error(w, "raw object unavailable", http.StatusInternalServerError)
			return true
		}
		serveRaw(w, r, path, RawContent{Body: body, Digest: strings.TrimPrefix(asset.Digest, "sha256:"), ContentType: asset.ContentType})
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
		if err = h.store.StageRawObject(r.Context(), repository.RawObject{Digest: digest, ObjectKey: key, Size: int64(len(body))}); err != nil {
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
