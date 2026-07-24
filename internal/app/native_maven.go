package app

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

// nativeMavenHandler implements the V3 Maven session API. Its metadata store
// only gains asset rows at commit, making incomplete uploads unaddressable.
type nativeMavenHandler struct {
	store         GatewayStore
	objects       OCIObjectStore
	authenticator Authenticator
	authorizer    RepositoryAuthorizer
	management    hostedRepositoryAPIHandler
	metrics       *Metrics
}

func (h nativeMavenHandler) withMetrics(metrics *Metrics) nativeMavenHandler {
	h.metrics = metrics
	return h
}

func (h nativeMavenHandler) recordAuthorizationDenial(decision AuthorizationDecision) {
	if h.metrics != nil {
		h.metrics.recordRepositoryAuthorizationDenied("maven", decision.Source, decision.Reason)
	}
}

func newNativeMavenHandler(store GatewayStore, objects OCIObjectStore, auth Authenticator) nativeMavenHandler {
	return nativeMavenHandler{store: store, objects: objects, authenticator: auth, authorizer: RepositoryAuthorizer{
		Grants: store,
		Legacy: auth,
		LegacyFallback: func(principal Principal, target repository.HostedRepository, operation RepositoryOperation) AuthorizationDecision {
			switch operation {
			case RepositoryRead:
				if auth.CanReadMavenRepository(principal, target.Name) {
					return AuthorizationDecision{Allowed: true, Source: "legacy_static", Reason: "read_pattern_granted"}
				}
			case RepositoryWrite:
				if auth.CanWriteMavenRepository(principal, target.Name) {
					return AuthorizationDecision{Allowed: true, Source: "legacy_static", Reason: "write_pattern_granted"}
				}
			}
			return AuthorizationDecision{Source: "legacy_static", Reason: "scope_not_granted"}
		},
	}, management: hostedRepositoryAPIHandler{store: store, authenticator: auth}}
}

type nativeMavenSessionRequest struct {
	Format, Coordinate, PomObject string
	Objects                       []repository.MavenDeclaredObject
}

type nativeMavenCoordinateCommitRequest struct {
	ExpectedAssetNames []string `json:"expectedAssetNames"`
}

func (h nativeMavenHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/repository/maven/") {
		if repositoryName, coordinate, ok := mavenCoordinateCommitPath(r.URL.Path); ok && r.Method == http.MethodPost {
			h.coordinateCommit(w, r, repositoryName, coordinate)
			return
		}
		h.read(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/v2/publish-sessions/") {
		h.session(w, r)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/v2/repositories/") {
		h.management.ServeHTTP(w, r)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v2/repositories/"), "/")
	if len(parts) == 2 && parts[1] == "publish-sessions" && r.Method == http.MethodPost {
		h.create(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "artifacts" && r.Method == http.MethodGet {
		h.artifacts(w, r, parts[0])
		return
	}
	// Session endpoints are globally addressed, so keep them under the V3 API
	// namespace without making them collide with repository resource routes.
	h.management.ServeHTTP(w, r)
}

func (h nativeMavenHandler) artifacts(w http.ResponseWriter, r *http.Request, repoID string) {
	if _, ok := h.admin(r); !ok {
		writeHostedProblem(w, http.StatusUnauthorized, "access_denied", "administrator authentication is required")
		return
	}
	h.listArtifacts(w, r, repoID)
}

func (h nativeMavenHandler) listArtifacts(w http.ResponseWriter, r *http.Request, repoID string) {
	repo, err := h.store.GetHostedRepository(r.Context(), repoID)
	if err != nil || repo.Format != repository.FormatMaven {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "Maven repository not found")
		return
	}
	items, err := h.store.ListMavenArtifacts(r.Context(), repo.ID)
	if err != nil {
		writeHostedProblem(w, 500, "internal_error", "list Maven artifacts failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h nativeMavenHandler) getArtifact(w http.ResponseWriter, r *http.Request, repoID, artifactID string) {
	repo, err := h.store.GetHostedRepository(r.Context(), repoID)
	if err != nil || repo.Format != repository.FormatMaven {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "Maven repository not found")
		return
	}
	artifact, err := h.store.GetMavenArtifact(r.Context(), repo.ID, artifactID)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "Maven artifact not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get Maven artifact failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusOK, artifact)
}

func (h nativeMavenHandler) deleteArtifact(w http.ResponseWriter, r *http.Request, repoID, artifactID string) {
	repo, err := h.store.GetHostedRepository(r.Context(), repoID)
	if err != nil || repo.Format != repository.FormatMaven {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "Maven repository not found")
		return
	}
	if _, err = h.store.TombstoneMavenArtifact(r.Context(), repo.ID, artifactID); errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "Maven artifact not found")
		return
	} else if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "delete Maven artifact failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusAccepted, map[string]string{"id": artifactID, "state": "pending"})
}

func (h nativeMavenHandler) admin(r *http.Request) (Principal, bool) {
	p, ok := h.authenticator.Authenticate(r.Header.Get("Authorization"))
	return p, ok && p.Admin
}
func (h nativeMavenHandler) create(w http.ResponseWriter, r *http.Request, repoID string) {
	principal, ok := h.admin(r)
	if !ok {
		writeHostedProblem(w, http.StatusUnauthorized, "access_denied", "administrator authentication is required")
		return
	}
	h.createWithIdempotencyKey(w, r, principal, repoID, r.Header.Get("Idempotency-Key"))
}

func (h nativeMavenHandler) createWithIdempotencyKey(w http.ResponseWriter, r *http.Request, principal Principal, repoID, key string) {
	repo, err := h.store.GetHostedRepository(r.Context(), repoID)
	if err != nil || repo.Format != repository.FormatMaven || repo.State != repository.RepositoryActive {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "Maven repository not found")
		return
	}
	var body nativeMavenSessionRequest
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20))
	d.DisallowUnknownFields()
	if d.Decode(&body) != nil || body.Format != "maven" || !validMavenCoordinate(body.Coordinate) || body.PomObject == "" || len(body.Objects) == 0 || !validDeclaredObjects(body.Objects) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "valid Maven coordinate, POM object, and objects are required")
		return
	}
	foundPom := false
	for _, o := range body.Objects {
		if o.Name == body.PomObject {
			foundPom = true
		}
	}
	if !foundPom {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "pomObject must be declared")
		return
	}
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 128 {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "Idempotency-Key is required and must be at most 128 characters")
		return
	}
	payload, _ := json.Marshal(body)
	digest := sha256.Sum256(payload)
	s := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: body.Coordinate, Publisher: principal.Actor, PomObject: body.PomObject, State: "open", Objects: body.Objects, ExpiresAt: time.Now().UTC().Add(time.Hour)}
	s, _, err = h.store.CreateMavenPublishSessionIdempotently(r.Context(), s, principal.Actor, "repositories/"+repo.ID+"/publish-sessions", key, hex.EncodeToString(digest[:]))
	if errors.Is(err, repository.ErrIdempotencyConflict) {
		writeHostedProblem(w, http.StatusConflict, "idempotency_conflict", "Idempotency-Key was already used with a different request")
		return
	}
	if err != nil {
		writeHostedProblem(w, 500, "internal_error", "create publish session failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusCreated, s)
}
func (h nativeMavenHandler) session(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.admin(r); !ok {
		writeHostedProblem(w, http.StatusUnauthorized, "access_denied", "administrator authentication is required")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v2/publish-sessions/")
	commit := strings.HasSuffix(path, ":commit")
	path = strings.TrimSuffix(path, ":commit")
	parts := strings.Split(path, "/")
	id := parts[0]
	if commit && r.Method == http.MethodPost {
		h.commit(w, r, id)
		return
	}
	if len(parts) == 3 && parts[1] == "objects" && r.Method == http.MethodPut {
		h.upload(w, r, id, parts[2])
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		h.getSession(w, r, id)
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
}

func (h nativeMavenHandler) getSession(w http.ResponseWriter, r *http.Request, id string) {
	s, err := h.store.GetMavenPublishSession(r.Context(), id)
	if err != nil {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "publish session not found")
		return
	}
	writeNativeMavenJSON(w, http.StatusOK, s)
}
func (h nativeMavenHandler) upload(w http.ResponseWriter, r *http.Request, id, name string) {
	s, err := h.store.GetMavenPublishSession(r.Context(), id)
	if err != nil || s.State != "open" || time.Now().After(s.ExpiresAt) {
		writeHostedProblem(w, 409, "session_closed", "publish session is closed")
		return
	}
	var declared *repository.MavenDeclaredObject
	for i := range s.Objects {
		if s.Objects[i].Name == name {
			declared = &s.Objects[i]
		}
	}
	if declared == nil {
		writeHostedProblem(w, 404, "not_found", "declared object not found")
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, declared.Size+1))
	if err != nil || int64(len(data)) != declared.Size {
		writeHostedProblem(w, 422, "digest_mismatch", "uploaded object size does not match declaration")
		return
	}
	sum := sha256.Sum256(data)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if digest != declared.Digest {
		writeHostedProblem(w, 422, "digest_mismatch", "uploaded object digest does not match declaration")
		return
	}
	key := "native/maven/sha256/" + strings.TrimPrefix(digest, "sha256:")
	// Persist the staging intent before object bytes. A failed write is then
	// invisible to readers and remains discoverable for recovery/collection.
	if err := h.store.MarkMavenPublishObject(r.Context(), id, name, key); err != nil {
		writeHostedProblem(w, 500, "internal_error", "stage Maven object failed")
		return
	}
	if err := h.objects.PutVerifiedReader(r.Context(), key, bytes.NewReader(data), int64(len(data)), digest); err != nil {
		writeHostedProblem(w, 500, "internal_error", "stage Maven object failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h nativeMavenHandler) commit(w http.ResponseWriter, r *http.Request, id string) {
	s, err := h.store.GetMavenPublishSession(r.Context(), id)
	if err != nil {
		writeHostedProblem(w, 404, "not_found", "publish session not found")
		return
	}
	if s.State != "open" || time.Now().After(s.ExpiresAt) {
		writeHostedProblem(w, 409, "session_closed", "publish session is closed")
		return
	}
	a, err := h.promote(r.Context(), s)
	if errors.Is(err, repository.ErrNameExists) {
		writeHostedProblem(w, 409, "coordinate_exists", "Maven coordinate already exists")
		return
	}
	if err != nil {
		writeHostedProblem(w, 422, "invalid_publish_session", err.Error())
		return
	}
	writeNativeMavenJSON(w, 200, a)
}

func (h nativeMavenHandler) promote(ctx context.Context, s repository.MavenPublishSession) (repository.MavenArtifact, error) {
	if err := h.validatePOM(ctx, s); err != nil {
		return repository.MavenArtifact{}, err
	}
	base := mavenCoordinatePath(s.Coordinate)
	assets := make([]repository.MavenAsset, 0, len(s.Objects)*4)
	for _, o := range s.Objects {
		key := "native/maven/sha256/" + strings.TrimPrefix(o.Digest, "sha256:")
		info, statErr := h.objects.Stat(ctx, key)
		if statErr != nil || info.Size != o.Size || info.Digest != o.Digest {
			return repository.MavenArtifact{}, errors.New("staged Maven object is unavailable or has changed")
		}
		assets = append(assets, repository.MavenAsset{RepositoryID: s.RepositoryID, Path: base + "/" + o.Name, ObjectKey: "native/maven/sha256/" + strings.TrimPrefix(o.Digest, "sha256:"), Digest: o.Digest, Size: o.Size})
		body, err := h.objects.Get(ctx, "native/maven/sha256/"+strings.TrimPrefix(o.Digest, "sha256:"))
		if err != nil {
			return repository.MavenArtifact{}, errors.New("staged Maven object is unavailable")
		}
		for _, checksum := range generatedMavenChecksums(body) {
			key := "native/maven/sha256/" + checksum.digest
			if err := h.objects.PutVerifiedReader(ctx, key, strings.NewReader(checksum.body), int64(len(checksum.body)), "sha256:"+checksum.digest); err != nil {
				return repository.MavenArtifact{}, err
			}
			assets = append(assets, repository.MavenAsset{RepositoryID: s.RepositoryID, Path: base + "/" + o.Name + checksum.extension, ObjectKey: key, Digest: "sha256:" + checksum.digest, Size: int64(len(checksum.body))})
		}
	}
	return h.store.CommitMavenPublishSession(ctx, s.ID, assets)
}
func (h nativeMavenHandler) read(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut {
		h.deploy(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	principal, ok := h.protocolPrincipal(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway Maven"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	user := principal.Actor
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/repository/maven/"), "/")
	if len(parts) < 2 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	repo, err := h.store.GetHostedRepositoryByName(r.Context(), parts[0])
	if err != nil || repo.Format != repository.FormatMaven || repo.State != repository.RepositoryActive {
		http.NotFound(w, r)
		return
	}
	if decision := h.authorizer.Authorize(r.Context(), principal, repo, RepositoryRead); !decision.Allowed {
		h.recordAuthorizationDenial(decision)
		_ = h.store.RecordAudit(r.Context(), repository.AuditRecord{Repository: repo.Name, GroupName: repo.Name, Actor: user, Outcome: repository.AuditAccessDenied, OccurredAt: time.Now().UTC(), Format: "maven", Resource: strings.Join(parts[1:], "/"), Operation: strings.ToLower(r.Method), Status: http.StatusForbidden, AuthorizationSource: decision.Source, AuthorizationReason: decision.Reason})
		http.Error(w, "repository read permission required", http.StatusForbidden)
		return
	}
	asset, err := h.store.GetMavenAsset(r.Context(), repo.ID, strings.Join(parts[1:], "/"))
	if err != nil {
		if snapshotAsset, found := h.snapshotAsset(r.Context(), repo.ID, strings.Join(parts[1:], "/")); found {
			asset = snapshotAsset
			err = nil
		}
	}
	if err != nil {
		if strings.HasSuffix(strings.Join(parts[1:], "/"), "maven-metadata.xml") {
			h.metadata(w, r, repo, strings.Join(parts[1:], "/"), user)
			return
		}
		http.NotFound(w, r)
		return
	}
	body, err := h.objects.Get(r.Context(), asset.ObjectKey)
	if err != nil {
		http.Error(w, "artifact unavailable", 503)
		return
	}
	w.Header().Set("ETag", `"`+strings.TrimPrefix(asset.Digest, "sha256:")+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(asset.Size, 10))
	if r.Method == http.MethodGet {
		_, _ = w.Write(body)
	}
	_ = h.store.RecordAudit(r.Context(), repository.AuditRecord{Repository: repo.Name, GroupName: repo.Name, Actor: user, Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(), Format: "maven", Resource: asset.Path, Operation: strings.ToLower(r.Method), Status: 200, Bytes: asset.Size})
}

// snapshotAsset resolves Maven's timestamped SNAPSHOT filenames to the
// immutable SNAPSHOT coordinate that produced the generated metadata.
func (h nativeMavenHandler) snapshotAsset(ctx context.Context, repositoryID, path string) (repository.MavenAsset, bool) {
	parts := strings.Split(path, "/")
	if len(parts) < 4 {
		return repository.MavenAsset{}, false
	}
	version, artifact, name := parts[len(parts)-2], parts[len(parts)-3], parts[len(parts)-1]
	if !strings.HasSuffix(version, "-SNAPSHOT") {
		return repository.MavenAsset{}, false
	}
	group := strings.Join(parts[:len(parts)-3], ".")
	coordinate := group + ":" + artifact + ":" + version
	items, err := h.store.ListMavenArtifacts(ctx, repositoryID)
	if err != nil {
		return repository.MavenAsset{}, false
	}
	for _, item := range items {
		if item.Coordinate != coordinate {
			continue
		}
		timestamped := strings.TrimSuffix(version, "-SNAPSHOT") + "-" + item.CreatedAt.UTC().Format("20060102.150405") + "-1"
		prefix := artifact + "-" + timestamped
		if !strings.HasPrefix(name, prefix) {
			return repository.MavenAsset{}, false
		}
		logicalPath := strings.Join(append(parts[:len(parts)-1], artifact+"-"+version+strings.TrimPrefix(name, prefix)), "/")
		asset, err := h.store.GetMavenAsset(ctx, repositoryID, logicalPath)
		return asset, err == nil
	}
	return repository.MavenAsset{}, false
}

func mavenCoordinateCommitPath(path string) (repositoryName, coordinate string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/repository/maven/"), "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] != "coordinates" || !strings.HasSuffix(parts[2], ":commit") {
		return "", "", false
	}
	coordinate = strings.TrimSuffix(parts[2], ":commit")
	return parts[0], coordinate, validMavenCoordinate(coordinate)
}

func (h nativeMavenHandler) coordinateCommit(w http.ResponseWriter, r *http.Request, repositoryName, coordinate string) {
	principal, ok := h.protocolPrincipal(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway Maven"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "Idempotency-Key is required")
		return
	}
	repo, err := h.store.GetHostedRepositoryByName(r.Context(), repositoryName)
	if err != nil || repo.Format != repository.FormatMaven || repo.State != repository.RepositoryActive {
		http.NotFound(w, r)
		return
	}
	if decision := h.authorizer.Authorize(r.Context(), principal, repo, RepositoryWrite); !decision.Allowed {
		h.recordAuthorizationDenial(decision)
		_ = h.store.RecordAudit(r.Context(), repository.AuditRecord{Repository: repo.Name, GroupName: repo.Name, Actor: principal.Actor, Outcome: repository.AuditAccessDenied, OccurredAt: time.Now().UTC(), Format: "maven", Resource: coordinate, Operation: "commit", Status: http.StatusForbidden, AuthorizationSource: decision.Source, AuthorizationReason: decision.Reason})
		http.Error(w, "repository write permission required", http.StatusForbidden)
		return
	}
	var body nativeMavenCoordinateCommitRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || !validExpectedMavenAssetNames(body.ExpectedAssetNames) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "expectedAssetNames must be a non-empty unique set of filenames")
		return
	}
	var s repository.MavenPublishSession
	if principal.Admin {
		s, err = h.store.FindAnyMavenPublishSession(r.Context(), repo.ID, coordinate)
	} else {
		s, err = h.store.FindMavenPublishSession(r.Context(), repo.ID, coordinate, principal.Actor)
	}
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "Maven coordinate has no staged publish session")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "load Maven publish session failed")
		return
	}
	if s.Publisher != principal.Actor && !principal.Admin {
		writeHostedProblem(w, http.StatusForbidden, "publisher_required", "only the session publisher may commit this Maven coordinate")
		return
	}
	if !sameMavenAssetNames(body.ExpectedAssetNames, s.Objects) {
		writeHostedProblem(w, http.StatusConflict, "expected_assets_conflict", "expectedAssetNames do not match staged Maven assets")
		return
	}
	if s.State == "committed" {
		artifacts, listErr := h.store.ListMavenArtifacts(r.Context(), repo.ID)
		if listErr != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "load committed Maven coordinate failed")
			return
		}
		for _, artifact := range artifacts {
			if artifact.Coordinate == coordinate {
				writeNativeMavenJSON(w, http.StatusOK, artifact)
				return
			}
		}
		writeHostedProblem(w, http.StatusConflict, "coordinate_exists", "Maven coordinate is already committed")
		return
	}
	if s.State != "open" || time.Now().After(s.ExpiresAt) {
		writeHostedProblem(w, http.StatusConflict, "session_closed", "Maven publish session is closed")
		return
	}
	artifact, err := h.promote(r.Context(), s)
	if errors.Is(err, repository.ErrNameExists) {
		writeHostedProblem(w, http.StatusConflict, "coordinate_exists", "Maven coordinate already exists")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusUnprocessableEntity, "invalid_publish_session", err.Error())
		return
	}
	writeNativeMavenJSON(w, http.StatusOK, artifact)
}

func validExpectedMavenAssetNames(names []string) bool {
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name == "" || strings.Contains(name, "/") {
			return false
		}
		if _, exists := seen[name]; exists {
			return false
		}
		seen[name] = struct{}{}
	}
	return len(names) > 0
}

func sameMavenAssetNames(expected []string, objects []repository.MavenDeclaredObject) bool {
	if len(expected) != len(objects) {
		return false
	}
	actual := make(map[string]struct{}, len(objects))
	for _, object := range objects {
		actual[object.Name] = struct{}{}
	}
	for _, name := range expected {
		if _, exists := actual[name]; !exists {
			return false
		}
	}
	return true
}

// deploy accepts Maven's ordinary HTTP PUT layout and only stages server-derived
// object facts. A coordinate is visible exclusively through coordinateCommit.
func (h nativeMavenHandler) deploy(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.protocolPrincipal(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway Maven"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/repository/maven/"), "/")
	if len(parts) < 5 {
		http.NotFound(w, r)
		return
	}
	repo, err := h.store.GetHostedRepositoryByName(r.Context(), parts[0])
	if err != nil || repo.Format != repository.FormatMaven || repo.State != repository.RepositoryActive {
		http.NotFound(w, r)
		return
	}
	if decision := h.authorizer.Authorize(r.Context(), principal, repo, RepositoryWrite); !decision.Allowed {
		h.recordAuthorizationDenial(decision)
		_ = h.store.RecordAudit(r.Context(), repository.AuditRecord{Repository: repo.Name, GroupName: repo.Name, Actor: principal.Actor, Outcome: repository.AuditAccessDenied, OccurredAt: time.Now().UTC(), Format: "maven", Resource: strings.Join(parts[1:], "/"), Operation: "put", Status: http.StatusForbidden, AuthorizationSource: decision.Source, AuthorizationReason: decision.Reason})
		http.Error(w, "repository write permission required", http.StatusForbidden)
		return
	}
	path := parts[1:]
	version, artifact := path[len(path)-2], path[len(path)-3]
	group := strings.Join(path[:len(path)-3], ".")
	name := path[len(path)-1]
	if group == "" || artifact == "" || version == "" {
		http.Error(w, "invalid Maven asset path", 400)
		return
	}
	// Maven and Gradle upload repository metadata and checksum sidecars as part
	// of their normal deploy protocol. They never decide coordinate visibility,
	// so accept them for client compatibility without making them authoritative.
	if name == "maven-metadata.xml" || strings.HasSuffix(name, ".sha1") || strings.HasSuffix(name, ".sha256") || strings.HasSuffix(name, ".sha512") || strings.HasSuffix(name, ".md5") {
		w.WriteHeader(http.StatusCreated)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<20))
	if err != nil {
		http.Error(w, "read Maven asset", 400)
		return
	}
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	coordinate := group + ":" + artifact + ":" + version
	name = canonicalMavenAssetName(artifact, version, name)
	declared := repository.MavenDeclaredObject{Name: name, Digest: digest, Size: int64(len(body))}
	s, err := h.store.FindOpenMavenPublishSession(r.Context(), repo.ID, coordinate, principal.Actor)
	if errors.Is(err, repository.ErrNotFound) {
		pomObject := ""
		if strings.HasSuffix(name, ".pom") {
			pomObject = name
		}
		s = repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: coordinate, Publisher: principal.Actor, PomObject: pomObject, State: "open", Objects: []repository.MavenDeclaredObject{declared}, ExpiresAt: time.Now().Add(time.Hour)}
		_, err = h.store.CreateMavenPublishSession(r.Context(), s)
	} else if err == nil {
		err = h.store.AppendMavenPublishObject(r.Context(), s.ID, declared)
	}
	if err != nil {
		if errors.Is(err, repository.ErrNameExists) {
			writeHostedProblem(w, http.StatusConflict, "asset_conflict", "Maven asset was already staged with different bytes")
			return
		}
		http.Error(w, "create Maven publish session", 500)
		return
	}
	if strings.HasSuffix(name, ".pom") {
		if err = h.store.SetMavenPublishPom(r.Context(), s.ID, name); err != nil {
			http.Error(w, "stage Maven POM", http.StatusInternalServerError)
			return
		}
	}
	key := "native/maven/sha256/" + strings.TrimPrefix(digest, "sha256:")
	if err = h.store.MarkMavenPublishObject(r.Context(), s.ID, name, key); err != nil {
		http.Error(w, "stage Maven asset", 500)
		return
	}
	if err = h.objects.PutVerifiedReader(r.Context(), key, bytes.NewReader(body), int64(len(body)), digest); err != nil {
		http.Error(w, "stage Maven asset", 500)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (h nativeMavenHandler) validatePOM(ctx context.Context, s repository.MavenPublishSession) error {
	parts := strings.Split(s.Coordinate, ":")
	if len(parts) != 3 || s.PomObject != parts[1]+"-"+parts[2]+".pom" {
		return errors.New("staged POM does not match Maven coordinate")
	}
	var pom repository.MavenDeclaredObject
	found := false
	for _, object := range s.Objects {
		if object.Name == s.PomObject {
			pom, found = object, true
			break
		}
	}
	if !found {
		return errors.New("staged Maven coordinate has no POM")
	}
	body, err := h.objects.Get(ctx, "native/maven/sha256/"+strings.TrimPrefix(pom.Digest, "sha256:"))
	if err != nil {
		return errors.New("staged POM is unavailable")
	}
	var project struct {
		GroupID    string `xml:"groupId"`
		ArtifactID string `xml:"artifactId"`
		Version    string `xml:"version"`
		Parent     struct {
			GroupID string `xml:"groupId"`
			Version string `xml:"version"`
		} `xml:"parent"`
	}
	if err := xml.Unmarshal(body, &project); err != nil {
		return errors.New("staged POM is invalid XML")
	}
	if project.GroupID == "" {
		project.GroupID = project.Parent.GroupID
	}
	if project.Version == "" {
		project.Version = project.Parent.Version
	}
	if project.GroupID != parts[0] || project.ArtifactID != parts[1] || project.Version != parts[2] {
		return errors.New("staged POM identity does not match Maven coordinate")
	}
	return nil
}

func (h nativeMavenHandler) protocolPrincipal(r *http.Request) (Principal, bool) {
	if principal, ok := h.authenticator.Authenticate(r.Header.Get("Authorization")); ok {
		return principal, true
	}
	user, pass, ok := r.BasicAuth()
	if !ok || user == "" || !tokenMatches(pass, h.authenticator.ResolverToken) {
		return Principal{}, false
	}
	return h.authenticator.principal(user), true
}

func (h nativeMavenHandler) metadata(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, path, actor string) {
	items, err := h.store.ListMavenArtifacts(r.Context(), repo.ID)
	if err != nil {
		http.Error(w, "metadata unavailable", 503)
		return
	}
	prefix := strings.TrimSuffix(path, "/maven-metadata.xml")
	parts := strings.Split(prefix, "/")
	// Maven asks for a distinct version-level metadata document when resolving
	// -SNAPSHOT. It contains the timestamp/build mapping rather than the list
	// of artifact versions.
	if len(parts) >= 3 && strings.HasSuffix(parts[len(parts)-1], "-SNAPSHOT") {
		group := strings.Join(parts[:len(parts)-2], ".")
		artifact, version := parts[len(parts)-2], parts[len(parts)-1]
		coordinate := group + ":" + artifact + ":" + version
		for _, item := range items {
			if item.Coordinate != coordinate {
				continue
			}
			timestamp := item.CreatedAt.UTC().Format("20060102.150405")
			base := strings.TrimSuffix(version, "-SNAPSHOT") + "-" + timestamp + "-1"
			body := []byte("<metadata><groupId>" + group + "</groupId><artifactId>" + artifact + "</artifactId><version>" + version + "</version><versioning><snapshot><timestamp>" + timestamp + "</timestamp><buildNumber>1</buildNumber></snapshot><snapshotVersions><snapshotVersion><extension>pom</extension><value>" + base + "</value><updated>" + strings.ReplaceAll(timestamp, ".", "") + "</updated></snapshotVersion><snapshotVersion><extension>jar</extension><value>" + base + "</value><updated>" + strings.ReplaceAll(timestamp, ".", "") + "</updated></snapshotVersion></snapshotVersions></versioning></metadata>")
			w.Header().Set("Content-Type", "application/xml")
			if r.Method == http.MethodGet {
				_, _ = w.Write(body)
			}
			_ = h.store.RecordAudit(r.Context(), repository.AuditRecord{Repository: repo.Name, GroupName: repo.Name, Actor: actor, Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(), Format: "maven", Resource: path, Operation: strings.ToLower(r.Method), Status: 200, Bytes: int64(len(body))})
			return
		}
		http.NotFound(w, r)
		return
	}
	versions := []string{}
	for _, a := range items {
		base := mavenCoordinatePath(a.Coordinate)
		if strings.HasPrefix(base, prefix) {
			p := strings.Split(a.Coordinate, ":")
			if len(p) >= 3 {
				versions = append(versions, p[2])
			}
		}
	}
	if len(versions) == 0 {
		http.NotFound(w, r)
		return
	}
	sort.Strings(versions)
	p := strings.Split(prefix, "/")
	group := strings.Join(p[:len(p)-1], ".")
	artifact := p[len(p)-1]
	body := []byte("<metadata><groupId>" + group + "</groupId><artifactId>" + artifact + "</artifactId><versioning><latest>" + versions[len(versions)-1] + "</latest><release>" + versions[len(versions)-1] + "</release><versions>" + strings.Join(func() []string {
		v := make([]string, len(versions))
		for i, x := range versions {
			v[i] = "<version>" + x + "</version>"
		}
		return v
	}(), "") + "</versions></versioning></metadata>")
	w.Header().Set("Content-Type", "application/xml")
	if r.Method == http.MethodGet {
		_, _ = w.Write(body)
	}
	_ = h.store.RecordAudit(r.Context(), repository.AuditRecord{Repository: repo.Name, GroupName: repo.Name, Actor: actor, Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(), Format: "maven", Resource: path, Operation: strings.ToLower(r.Method), Status: 200, Bytes: int64(len(body))})
}
func validMavenCoordinate(s string) bool {
	p := strings.Split(s, ":")
	return len(p) >= 3 && len(p) <= 5 && p[0] != "" && p[1] != "" && p[2] != ""
}

// Gradle publishes SNAPSHOT components under Maven's timestamped filename
// convention. The native coordinate remains the logical -SNAPSHOT version, so
// store those uploads under their canonical filename and let generated metadata
// map client timestamp requests back to this immutable coordinate.
func canonicalMavenAssetName(artifact, version, name string) string {
	if !strings.HasSuffix(version, "-SNAPSHOT") {
		return name
	}
	prefix := artifact + "-" + strings.TrimSuffix(version, "-SNAPSHOT") + "-"
	if !strings.HasPrefix(name, prefix) {
		return name
	}
	remainder := strings.TrimPrefix(name, prefix)
	dash := strings.IndexByte(remainder, '-')
	if dash < 0 {
		return name
	}
	buildAndSuffix := remainder[dash+1:]
	buildLength := 0
	for buildLength < len(buildAndSuffix) && buildAndSuffix[buildLength] >= '0' && buildAndSuffix[buildLength] <= '9' {
		buildLength++
	}
	if buildLength == 0 || buildLength == len(buildAndSuffix) {
		return name
	}
	return artifact + "-" + version + buildAndSuffix[buildLength:]
}
func validDeclaredObjects(objects []repository.MavenDeclaredObject) bool {
	seen := map[string]bool{}
	for _, o := range objects {
		if o.Name == "" || strings.Contains(o.Name, "/") || o.Size < 0 || len(o.Digest) != 71 || !strings.HasPrefix(o.Digest, "sha256:") || seen[o.Name] {
			return false
		}
		seen[o.Name] = true
	}
	return true
}
func mavenCoordinatePath(c string) string {
	p := strings.Split(c, ":")
	return strings.ReplaceAll(p[0], ".", "/") + "/" + p[1] + "/" + p[2]
}

type mavenChecksum struct{ extension, digest, body string }

func generatedMavenChecksums(content []byte) []mavenChecksum {
	sha256Sum := sha256.Sum256(content)
	sha1Sum := sha1.Sum(content)
	md5Sum := md5.Sum(content)
	checksums := []struct{ extension, value string }{{".sha256", hex.EncodeToString(sha256Sum[:])}, {".sha1", hex.EncodeToString(sha1Sum[:])}, {".md5", hex.EncodeToString(md5Sum[:])}}
	out := make([]mavenChecksum, 0, len(checksums))
	for _, v := range checksums {
		body := v.value + "\n"
		sum := sha256.Sum256([]byte(body))
		out = append(out, mavenChecksum{extension: v.extension, digest: hex.EncodeToString(sum[:]), body: body})
	}
	return out
}
func writeNativeMavenJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
