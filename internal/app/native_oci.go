package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	ociprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/oci"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

// nativeOCIHandler is the write-capable Registry V2 implementation. It only
// claims paths whose first component is a V3 OCI hosted repository; all other
// V2 paths retain the legacy group/proxy behaviour while it is being removed.
type nativeOCIHandler struct {
	store              repository.NativeOCIStore
	repos              repository.HostedRepositoryStore
	objects            OCIObjectStore
	auth               Authenticator
	authorizer         RepositoryAuthorizer
	audit              repository.Store
	metrics            *Metrics
	proxy              *OCIHandler
	publicationScanner *publicationScanScheduler
}

func (h nativeOCIHandler) withMetrics(metrics *Metrics) nativeOCIHandler {
	h.metrics = metrics
	return h
}

func (h nativeOCIHandler) withPublicationScanner(scanner publicationScanScheduler) nativeOCIHandler {
	h.publicationScanner = &scanner
	return h
}

// withProxy wires the legacy Group proxy fetch path so a native proxy
// repository is served through the same upstream client and cache.
func (h nativeOCIHandler) withProxy(proxy OCIHandler) nativeOCIHandler {
	h.proxy = &proxy
	return h
}

func newNativeOCIHandler(store GatewayStore, objects OCIObjectStore, auth Authenticator) nativeOCIHandler {
	if objects == nil {
		objects = NewMemoryOCIObjectStore()
	}
	return nativeOCIHandler{store: store, repos: store, objects: objects, auth: auth, audit: store, authorizer: RepositoryAuthorizer{
		Grants: store,
		Legacy: auth,
		LegacyFallback: func(Principal, repository.HostedRepository, RepositoryOperation) AuthorizationDecision {
			// Native OCI historically admitted every authenticated principal.
			return AuthorizationDecision{Allowed: true, Source: "legacy_protocol", Reason: "authenticated"}
		},
	}}
}

func (h nativeOCIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path == "/v2/_catalog" {
		h.catalog(w, r)
		return true
	}
	name, resource, reference, uploadID, ok := parseNativeOCIPath(r.URL.Path)
	if !ok {
		return false
	}
	root, imageName, found := strings.Cut(name, "/")
	if !found || imageName == "" {
		return false
	}
	repo, err := h.repos.GetHostedRepositoryByName(r.Context(), root)
	if errors.Is(err, repository.ErrNotFound) || repo.Format != repository.FormatOCI {
		return false
	}
	if err != nil || repo.State != repository.RepositoryActive {
		writeOCIError(w, http.StatusNotFound, "NAME_UNKNOWN", "repository name not known to registry")
		return true
	}
	p, authenticated := h.auth.Authenticate(r.Header.Get("Authorization"))
	if !authenticated {
		if anonymousHostedRepositoryReadAllowed(r.Context(), h.store, repo, r.Method) {
			p = anonymousPrincipal()
		} else {
			writeOCIChallenge(w, r)
			return true
		}
	}
	operation := nativeOCIOperation(resource, r.Method)
	if !isAnonymous(p) {
		decision := h.authorizer.AuthorizeResource(r.Context(), p, repo, operation, imageName)
		if !decision.Allowed {
			h.recordAuthorizationDenial(r, p, repo, operation, decision)
			writeOCIChallenge(w, r)
			return true
		}
	}
	if repo.Type == repository.RepositoryTypeProxy {
		h.proxyRead(w, r, repo, resource, imageName, reference, p.Actor)
		return true
	}
	switch resource {
	case "blob":
		h.blob(w, r, repo, imageName, reference)
	case "upload":
		h.upload(w, r, repo, imageName, uploadID)
	case "uploads":
		h.startUpload(w, r, repo, imageName)
	case "manifest":
		h.manifest(w, r, repo, imageName, reference, p.Actor)
	case "tags":
		h.tags(w, r, repo, imageName)
	case "referrers":
		h.referrers(w, r, repo, imageName, reference)
	default:
		return false
	}
	return true
}

func (h nativeOCIHandler) catalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	p, authenticated := h.auth.Authenticate(r.Header.Get("Authorization"))
	if !authenticated {
		writeOCIChallenge(w, r)
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("n"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 1000 {
			writeOCIError(w, http.StatusBadRequest, "NAME_INVALID", "catalog page size must be between 1 and 1000")
			return
		}
		limit = n
	}
	last := r.URL.Query().Get("last")
	var names []string
	afterRepository := ""
	for {
		repos, next, err := h.repos.ListHostedRepositories(r.Context(), 200, afterRepository)
		if err != nil {
			writeOCIError(w, http.StatusInternalServerError, "UNKNOWN", "unable to list repositories")
			return
		}
		for _, repo := range repos {
			if repo.Format != repository.FormatOCI || repo.State != repository.RepositoryActive {
				continue
			}
			localAfter, include := ociCatalogAfter(repo.Name, last)
			if !include {
				continue
			}
			items, err := h.store.ListOCIManifestNames(r.Context(), repo.ID, limit+1, localAfter)
			if err != nil {
				writeOCIError(w, http.StatusInternalServerError, "UNKNOWN", "unable to list catalog")
				return
			}
			for _, item := range items {
				decision, managed := h.authorizer.ManagedResourceDecision(r.Context(), p, repo, RepositoryRead, item)
				if !managed || !decision.Allowed {
					continue
				}
				names = append(names, repo.Name+"/"+item)
			}
		}
		if next == "" {
			break
		}
		afterRepository = next
	}
	sort.Strings(names)
	if len(names) > limit {
		names = names[:limit]
		w.Header().Set("Link", "</v2/_catalog?n="+strconv.Itoa(limit)+"&last="+url.QueryEscape(names[len(names)-1])+">; rel=\"next\"")
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if names == nil {
		names = []string{}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"repositories": names})
}

func ociCatalogAfter(repositoryName, last string) (string, bool) {
	prefix := repositoryName + "/"
	if last == "" || last < prefix {
		return "", true
	}
	if strings.HasPrefix(last, prefix) {
		return strings.TrimPrefix(last, prefix), true
	}
	return "", false
}

func (h nativeOCIHandler) recordAuthorizationDenial(r *http.Request, principal Principal, repo repository.HostedRepository, operation RepositoryOperation, decision AuthorizationDecision) {
	if h.metrics != nil {
		h.metrics.recordRepositoryAuthorizationDenied("oci", decision.Source, decision.Reason)
	}
	if h.audit == nil {
		return
	}
	_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{
		GroupName: repo.Name, Repository: repo.Name, Actor: principal.Actor, Outcome: repository.AuditAccessDenied, OccurredAt: time.Now().UTC(),
		Format: "oci", Resource: r.URL.Path, Operation: string(operation), Status: http.StatusUnauthorized, CacheDisposition: "bypass",
		AuthorizationSource: decision.Source, AuthorizationReason: decision.Reason,
	})
}

func nativeOCIOperation(resource, method string) RepositoryOperation {
	switch resource {
	case "blob", "tags", "referrers":
		return RepositoryRead
	case "manifest":
		if method == http.MethodGet || method == http.MethodHead {
			return RepositoryRead
		}
	}
	return RepositoryWrite
}

// proxyRead serves a read against a native proxy repository through the legacy
// Group proxy fetch and cache path. Proxy repositories are read-only; only
// manifest and blob reads are proxied, matching the Registry V2 pull flow.
func (h nativeOCIHandler) proxyRead(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, resource, imageName, reference, actor string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeOCIError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "method not allowed")
		return
	}
	if resource != "manifest" && resource != "blob" {
		writeOCIError(w, http.StatusNotFound, "NAME_UNKNOWN", "repository name not known to registry")
		return
	}
	if h.proxy == nil {
		writeOCIError(w, http.StatusNotFound, "NAME_UNKNOWN", "repository name not known to registry")
		return
	}
	// The upstream Registry V2 path uses the plural resource segment, while the
	// native parser reports the singular resource kind.
	upstreamResource := map[string]string{"manifest": ociManifest, "blob": ociBlob}[resource]
	h.proxy.serveNativeProxy(w, r, repo, imageName, upstreamResource, reference, actor)
}

func parseNativeOCIPath(path string) (name, resource, reference, uploadID string, ok bool) {
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(path, "/v2/"), "/"), "/")
	for i, part := range parts {
		switch part {
		case "tags":
			if i > 0 && i+2 == len(parts) && parts[i+1] == "list" {
				return strings.Join(parts[:i], "/"), "tags", "", "", true
			}
		case "manifests":
			if i > 0 && i+2 == len(parts) {
				return strings.Join(parts[:i], "/"), "manifest", parts[i+1], "", true
			}
		case "referrers":
			if i > 0 && i+2 == len(parts) {
				return strings.Join(parts[:i], "/"), "referrers", parts[i+1], "", true
			}
		case "blobs":
			if i > 0 && i+2 == len(parts) && parts[i+1] != "uploads" {
				return strings.Join(parts[:i], "/"), "blob", parts[i+1], "", true
			}
			if i > 0 && i+1 == len(parts) {
				return strings.Join(parts[:i], "/"), "uploads", "", "", true
			}
			if i > 0 && i+2 == len(parts) && parts[i+1] == "uploads" {
				return strings.Join(parts[:i], "/"), "uploads", "", "", true
			}
			if i > 0 && i+3 == len(parts) && parts[i+1] == "uploads" {
				return strings.Join(parts[:i], "/"), "upload", "", parts[i+2], true
			}
		}
	}
	return "", "", "", "", false
}

func (h nativeOCIHandler) referrers(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, name, subject string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !validOCIDigest(subject) {
		writeOCIError(w, http.StatusBadRequest, "DIGEST_INVALID", "valid sha256 digest is required")
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("n"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 1000 {
			writeOCIError(w, http.StatusBadRequest, "NAME_INVALID", "referrer page size must be between 1 and 1000")
			return
		}
		limit = n
	}
	items, err := h.store.ListOCIReferrers(r.Context(), repo.ID, name, subject, limit+1, r.URL.Query().Get("last"))
	if err != nil {
		writeOCIError(w, 500, "UNKNOWN", "unable to list referrers")
		return
	}
	if len(items) > limit {
		items = items[:limit]
		w.Header().Set("Link", "</v2/"+repo.Name+"/"+name+"/referrers/"+subject+"?n="+strconv.Itoa(limit)+"&last="+url.QueryEscape(items[len(items)-1].Digest)+">; rel=\"next\"")
	}
	type descriptor struct {
		MediaType    string `json:"mediaType"`
		Digest       string `json:"digest"`
		Size         int64  `json:"size"`
		ArtifactType string `json:"artifactType,omitempty"`
	}
	out := make([]descriptor, 0, len(items))
	for _, item := range items {
		out = append(out, descriptor{item.MediaType, item.Digest, item.Size, item.ArtifactType})
	}
	w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"schemaVersion": 2, "manifests": out})
}

func (h nativeOCIHandler) startUpload(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, name string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if digest := r.URL.Query().Get("mount"); digest != "" {
		if !validOCIDigest(digest) {
			writeOCIError(w, http.StatusBadRequest, "DIGEST_INVALID", "valid sha256 digest is required")
			return
		}
		if sourceRoot, sourceName, found := strings.Cut(r.URL.Query().Get("from"), "/"); found && sourceRoot != "" && sourceName != "" {
			source, err := h.repos.GetHostedRepositoryByName(r.Context(), sourceRoot)
			principal, authenticated := h.auth.Authenticate(r.Header.Get("Authorization"))
			if err == nil && authenticated && source.Format == repository.FormatOCI && source.State == repository.RepositoryActive && h.authorizer.AuthorizeResource(r.Context(), principal, source, RepositoryRead, sourceName).Allowed {
				blob, err := h.store.MountOCIBlobFrom(r.Context(), repo.ID, source.ID, digest)
				if err == nil {
					w.Header().Set("Location", "/v2/"+repo.Name+"/"+name+"/blobs/"+blob.Digest)
					w.Header().Set("Docker-Content-Digest", blob.Digest)
					w.WriteHeader(http.StatusCreated)
					return
				}
			}
		}
	}
	id := uuid.NewString()
	upload := repository.OCIUpload{ID: id, RepositoryID: repo.ID, Name: name, ObjectKey: "native/oci/uploads/" + id, State: "open", ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if _, err := h.store.CreateOCIUpload(r.Context(), upload); err != nil {
		writeOCIError(w, 500, "UNKNOWN", "create upload failed")
		return
	}
	h.uploadHeaders(w, repo.Name, name, upload)
	w.WriteHeader(http.StatusAccepted)
}

func (h nativeOCIHandler) upload(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, name, id string) {
	release, err := h.store.LockOCIUpload(r.Context(), id)
	if err != nil {
		writeOCIError(w, http.StatusServiceUnavailable, "UNKNOWN", "upload coordination is unavailable")
		return
	}
	defer release()
	upload, err := h.store.GetOCIUpload(r.Context(), id)
	if err != nil || upload.RepositoryID != repo.ID || upload.Name != name || upload.State != "open" || time.Now().After(upload.ExpiresAt) {
		writeOCIError(w, 404, "BLOB_UPLOAD_UNKNOWN", "blob upload unknown to registry")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.uploadHeaders(w, repo.Name, name, upload)
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		upload, err = h.store.CancelOCIUpload(r.Context(), id)
		if err != nil {
			writeOCIError(w, 404, "BLOB_UPLOAD_UNKNOWN", "blob upload unknown to registry")
			return
		}
		_ = h.objects.Delete(r.Context(), upload.ObjectKey)
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPatch:
		upload, err = h.appendUpload(r.Context(), r, upload)
		if err != nil {
			writeOCIError(w, 416, "RANGE_INVALID", "upload range is invalid")
			return
		}
		h.uploadHeaders(w, repo.Name, name, upload)
		w.WriteHeader(http.StatusAccepted)
	case http.MethodPut:
		if r.ContentLength != 0 {
			upload, err = h.appendUpload(r.Context(), r, upload)
			if err != nil {
				writeOCIError(w, 416, "RANGE_INVALID", "upload range is invalid")
				return
			}
		}
		digest := r.URL.Query().Get("digest")
		if !validOCIDigest(digest) {
			writeOCIError(w, 400, "DIGEST_INVALID", "valid sha256 digest is required")
			return
		}
		spool, err := spoolStoredObject(r.Context(), h.objects, upload.ObjectKey)
		if err != nil {
			writeOCIError(w, 500, "UNKNOWN", "upload bytes are unavailable")
			return
		}
		defer func() { _ = spool.Close() }()
		actualDigest, size := spool.Digest(), spool.Size()
		if digest != actualDigest {
			writeOCIError(w, 400, "DIGEST_INVALID", "provided digest did not match uploaded content")
			return
		}
		key := "native/oci/blobs/sha256/" + strings.TrimPrefix(digest, "sha256:")
		releaseObject, err := h.store.LockOCIObject(r.Context(), key)
		if err != nil {
			writeOCIError(w, http.StatusServiceUnavailable, "UNKNOWN", "blob coordination is unavailable")
			return
		}
		defer releaseObject()
		if err = h.store.StageOCIObjectIntent(r.Context(), repository.OCIObjectIntent{RepositoryID: repo.ID, ObjectKey: key, Digest: digest, Size: size}); err != nil {
			writeOCIError(w, 500, "UNKNOWN", "stage blob intent failed")
			return
		}
		if err = h.objects.PutVerifiedReader(r.Context(), key, spool.Reader(), size, digest); err != nil {
			writeOCIError(w, 500, "UNKNOWN", "persist blob failed")
			return
		}
		blob, err := h.store.CompleteOCIUpload(r.Context(), id, repository.OCIBlob{Digest: digest, ObjectKey: key, Size: size})
		if err != nil {
			if repository.IsQuotaExceeded(err) {
				writeOCIError(w, http.StatusInsufficientStorage, "DENIED", "repository capacity quota exceeded")
				return
			}
			writeOCIError(w, 409, "BLOB_UPLOAD_INVALID", "blob upload cannot be completed")
			return
		}
		_ = h.objects.Delete(r.Context(), upload.ObjectKey)
		w.Header().Set("Location", "/v2/"+repo.Name+"/"+name+"/blobs/"+blob.Digest)
		w.Header().Set("Docker-Content-Digest", blob.Digest)
		w.WriteHeader(http.StatusCreated)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h nativeOCIHandler) appendUpload(ctx context.Context, r *http.Request, upload repository.OCIUpload) (repository.OCIUpload, error) {
	if start, ok := uploadRangeStart(r.Header.Get("Content-Range")); ok && start != upload.Offset {
		return upload, errors.New("offset mismatch")
	}
	spool, chunkSize, err := spoolObjectAppend(ctx, h.objects, upload.ObjectKey, upload.Offset, r.Body, 1<<30)
	if err != nil {
		return upload, err
	}
	defer func() { _ = spool.Close() }()
	if err = h.objects.PutReader(ctx, upload.ObjectKey, spool.Reader(), spool.Size()); err != nil {
		return upload, err
	}
	return h.store.UpdateOCIUpload(ctx, upload.ID, upload.Offset+chunkSize)
}

func uploadRangeStart(value string) (int64, bool) {
	if value == "" {
		return 0, false
	}
	value = strings.TrimPrefix(value, "bytes ")
	left, _, ok := strings.Cut(value, "-")
	if !ok {
		return 0, false
	}
	var n int64
	for _, c := range left {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int64(c-'0')
	}
	return n, true
}
func (h nativeOCIHandler) uploadHeaders(w http.ResponseWriter, root, name string, upload repository.OCIUpload) {
	w.Header().Set("Docker-Upload-UUID", upload.ID)
	w.Header().Set("Location", "/v2/"+root+"/"+name+"/blobs/uploads/"+upload.ID)
	if upload.Offset > 0 {
		w.Header().Set("Range", "0-"+utoa(uint64(upload.Offset-1)))
	}
}

func (h nativeOCIHandler) blob(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, _ string, digest string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !validOCIDigest(digest) {
		writeOCIError(w, 400, "DIGEST_INVALID", "valid sha256 digest is required")
		return
	}
	blob, err := h.store.GetOCIBlob(r.Context(), repo.ID, digest)
	if err != nil {
		writeOCIError(w, 404, "BLOB_UNKNOWN", "blob unknown to registry")
		return
	}
	serveCachedOCIContent(w, r, digest, ociprotocol.NewStoredContent(blob.Digest, "application/octet-stream", blob.ObjectKey, blob.Size, h.objects))
}

func (h nativeOCIHandler) manifest(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, name, reference, actor string) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		manifest, err := h.store.GetOCIManifest(r.Context(), repo.ID, name, reference)
		if err != nil {
			writeOCIError(w, 404, "MANIFEST_UNKNOWN", "manifest unknown to registry")
			return
		}
		if !ociAcceptsManifest(r.Header.Get("Accept"), manifest.MediaType) {
			writeOCIError(w, http.StatusNotAcceptable, "MANIFEST_UNKNOWN", "manifest media type is not acceptable")
			return
		}
		serveCachedOCIContent(w, r, reference, ociprotocol.NewStoredContent(manifest.Digest, manifest.MediaType, manifest.ObjectKey, manifest.Size, h.objects))
	case http.MethodPut:
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<20))
		if err != nil {
			writeOCIError(w, 413, "MANIFEST_INVALID", "manifest is too large")
			return
		}
		if !validOCIManifest(data) {
			writeOCIError(w, 400, "MANIFEST_INVALID", "manifest must be a JSON object")
			return
		}
		if code, message := h.validateManifestDescriptors(r.Context(), repo, name, data); code != "" {
			writeOCIError(w, http.StatusBadRequest, code, message)
			return
		}
		sum := sha256.Sum256(data)
		digest := "sha256:" + hex.EncodeToString(sum[:])
		if validOCIDigest(reference) && reference != digest {
			writeOCIError(w, 400, "DIGEST_INVALID", "manifest digest did not match content")
			return
		}
		mediaType := r.Header.Get("Content-Type")
		if mediaType == "" {
			mediaType = "application/vnd.oci.image.manifest.v1+json"
		}
		key := "native/oci/manifests/" + repo.ID + "/" + url.PathEscape(name) + "/" + strings.TrimPrefix(digest, "sha256:")
		releaseObject, err := h.store.LockOCIObject(r.Context(), key)
		if err != nil {
			writeOCIError(w, http.StatusServiceUnavailable, "UNKNOWN", "manifest coordination is unavailable")
			return
		}
		defer releaseObject()
		if err = h.store.StageOCIObjectIntent(r.Context(), repository.OCIObjectIntent{RepositoryID: repo.ID, ObjectKey: key, Digest: digest, Size: int64(len(data))}); err != nil {
			writeOCIError(w, 500, "UNKNOWN", "stage manifest intent failed")
			return
		}
		if err = h.objects.PutVerifiedReader(r.Context(), key, bytes.NewReader(data), int64(len(data)), digest); err != nil {
			writeOCIError(w, 500, "UNKNOWN", "persist manifest failed")
			return
		}
		var envelope struct {
			Subject struct {
				Digest string `json:"digest"`
			} `json:"subject"`
			ArtifactType string `json:"artifactType"`
		}
		_ = json.Unmarshal(data, &envelope)
		if envelope.Subject.Digest != "" && !validOCIDigest(envelope.Subject.Digest) {
			writeOCIError(w, http.StatusBadRequest, "MANIFEST_INVALID", "subject digest must be sha256")
			return
		}
		manifest, err := h.store.PutOCIManifest(r.Context(), repository.OCIManifest{RepositoryID: repo.ID, Name: name, Digest: digest, ObjectKey: key, MediaType: mediaType, SubjectDigest: envelope.Subject.Digest, ArtifactType: envelope.ArtifactType, Size: int64(len(data))}, reference)
		if err != nil {
			if repository.IsQuotaExceeded(err) {
				writeOCIError(w, http.StatusInsufficientStorage, "DENIED", "repository capacity quota exceeded")
				return
			}
			writeOCIError(w, 500, "UNKNOWN", "publish manifest failed")
			return
		}
		w.Header().Set("Docker-Content-Digest", manifest.Digest)
		w.Header().Set("Location", "/v2/"+repo.Name+"/"+name+"/manifests/"+reference)
		if h.publicationScanner != nil {
			_ = h.publicationScanner.Schedule(r.Context(), repo, name, manifest.Digest, actor)
		}
		w.WriteHeader(http.StatusCreated)
	case http.MethodDelete:
		if !validOCIDigest(reference) {
			if err := h.store.DeleteOCITag(r.Context(), repo.ID, name, reference); err != nil {
				writeOCIError(w, 404, "MANIFEST_UNKNOWN", "tag unknown to registry")
				return
			}
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if err := h.store.DeleteOCIManifest(r.Context(), repo.ID, name, reference); err != nil {
			writeOCIError(w, 404, "MANIFEST_UNKNOWN", "manifest unknown to registry")
			return
		}
		w.WriteHeader(http.StatusAccepted)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h nativeOCIHandler) tags(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, name string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("n"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 1000 {
			writeOCIError(w, http.StatusBadRequest, "NAME_INVALID", "tag page size must be between 1 and 1000")
			return
		}
		limit = parsed
	}
	tags, err := h.store.ListOCITags(r.Context(), repo.ID, name, limit+1, r.URL.Query().Get("last"))
	if err != nil {
		writeOCIError(w, http.StatusInternalServerError, "UNKNOWN", "unable to list tags")
		return
	}
	if len(tags) > limit {
		tags = tags[:limit]
		next := "/v2/" + repo.Name + "/" + name + "/tags/list?n=" + strconv.Itoa(limit) + "&last=" + url.QueryEscape(tags[len(tags)-1])
		w.Header().Set("Link", "<"+next+">; rel=\"next\"")
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if tags == nil {
		tags = []string{}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"name": repo.Name + "/" + name, "tags": tags})
}

func validOCIDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != 71 {
		return false
	}
	for _, c := range value[7:] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
func validOCIManifest(data []byte) bool {
	var v map[string]json.RawMessage
	return json.Unmarshal(data, &v) == nil && v != nil
}

func ociAcceptsManifest(accept, mediaType string) bool {
	if accept == "" || mediaType == "" {
		return true
	}
	mediaType = strings.TrimSpace(strings.Split(mediaType, ";")[0])
	for _, entry := range strings.Split(accept, ",") {
		value := strings.TrimSpace(strings.Split(entry, ";")[0])
		if value == "*/*" || value == mediaType {
			return true
		}
		if prefix, _, ok := strings.Cut(value, "/"); ok && strings.HasSuffix(value, "/*") && strings.HasPrefix(mediaType, prefix+"/") {
			return true
		}
	}
	return false
}

type ociDescriptor struct {
	Digest string `json:"digest"`
	Size   *int64 `json:"size"`
}

type ociManifestDescriptors struct {
	Config    *ociDescriptor  `json:"config"`
	Layers    []ociDescriptor `json:"layers"`
	Manifests []ociDescriptor `json:"manifests"`
}

func (h nativeOCIHandler) validateManifestDescriptors(ctx context.Context, repo repository.HostedRepository, name string, data []byte) (code, message string) {
	var document ociManifestDescriptors
	if err := json.Unmarshal(data, &document); err != nil {
		return "MANIFEST_INVALID", "manifest must be a JSON object"
	}
	blobs := make([]ociDescriptor, 0, len(document.Layers)+1)
	if document.Config != nil {
		blobs = append(blobs, *document.Config)
	}
	blobs = append(blobs, document.Layers...)
	for _, descriptor := range blobs {
		if !validOCIDigest(descriptor.Digest) || descriptor.Size != nil && *descriptor.Size < 0 {
			return "MANIFEST_INVALID", "manifest contains an invalid blob descriptor"
		}
		blob, err := h.store.GetOCIBlob(ctx, repo.ID, descriptor.Digest)
		if err != nil {
			return "MANIFEST_BLOB_UNKNOWN", "manifest references a blob unknown to repository"
		}
		if descriptor.Size != nil && *descriptor.Size != blob.Size {
			return "MANIFEST_INVALID", "manifest blob descriptor size does not match stored content"
		}
	}
	for _, descriptor := range document.Manifests {
		if !validOCIDigest(descriptor.Digest) || descriptor.Size != nil && *descriptor.Size < 0 {
			return "MANIFEST_INVALID", "manifest contains an invalid manifest descriptor"
		}
		manifest, err := h.store.GetOCIManifest(ctx, repo.ID, name, descriptor.Digest)
		if err != nil {
			return "MANIFEST_UNKNOWN", "manifest references a manifest unknown to repository"
		}
		if descriptor.Size != nil && *descriptor.Size != manifest.Size {
			return "MANIFEST_INVALID", "manifest descriptor size does not match stored content"
		}
	}
	return "", ""
}
