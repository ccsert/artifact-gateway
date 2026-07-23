package app

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	management    hostedRepositoryAPIHandler
}

func newNativeMavenHandler(store GatewayStore, objects OCIObjectStore, auth Authenticator) nativeMavenHandler {
	return nativeMavenHandler{store: store, objects: objects, authenticator: auth, management: hostedRepositoryAPIHandler{store: store, authenticator: auth}}
}

type nativeMavenSessionRequest struct {
	Format, Coordinate, PomObject string
	Objects                       []repository.MavenDeclaredObject
}

func (h nativeMavenHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/repository/maven/") {
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
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 128 {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "Idempotency-Key is required and must be at most 128 characters")
		return
	}
	payload, _ := json.Marshal(body)
	digest := sha256.Sum256(payload)
	s := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: body.Coordinate, PomObject: body.PomObject, State: "open", Objects: body.Objects, ExpiresAt: time.Now().UTC().Add(time.Hour)}
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
		s, err := h.store.GetMavenPublishSession(r.Context(), id)
		if err != nil {
			writeHostedProblem(w, 404, "not_found", "publish session not found")
			return
		}
		writeNativeMavenJSON(w, 200, s)
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
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
	if err := h.objects.Put(r.Context(), key, data); err != nil {
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
	base := mavenCoordinatePath(s.Coordinate)
	assets := make([]repository.MavenAsset, 0, len(s.Objects)*4)
	for _, o := range s.Objects {
		key := "native/maven/sha256/" + strings.TrimPrefix(o.Digest, "sha256:")
		info, statErr := h.objects.Stat(r.Context(), key)
		if statErr != nil || info.Size != o.Size || info.Digest != o.Digest {
			writeHostedProblem(w, 422, "digest_mismatch", "staged Maven object is unavailable or has changed")
			return
		}
		assets = append(assets, repository.MavenAsset{RepositoryID: s.RepositoryID, Path: base + "/" + o.Name, ObjectKey: "native/maven/sha256/" + strings.TrimPrefix(o.Digest, "sha256:"), Digest: o.Digest, Size: o.Size})
		body, err := h.objects.Get(r.Context(), "native/maven/sha256/"+strings.TrimPrefix(o.Digest, "sha256:"))
		if err != nil {
			writeHostedProblem(w, 500, "internal_error", "staged Maven object is unavailable")
			return
		}
		for _, checksum := range generatedMavenChecksums(body) {
			key := "native/maven/sha256/" + checksum.digest
			if err := h.objects.Put(r.Context(), key, []byte(checksum.body)); err != nil {
				writeHostedProblem(w, 500, "internal_error", "generate Maven checksum failed")
				return
			}
			assets = append(assets, repository.MavenAsset{RepositoryID: s.RepositoryID, Path: base + "/" + o.Name + checksum.extension, ObjectKey: key, Digest: "sha256:" + checksum.digest, Size: int64(len(checksum.body))})
		}
	}
	a, err := h.store.CommitMavenPublishSession(r.Context(), id, assets)
	if errors.Is(err, repository.ErrNameExists) {
		writeHostedProblem(w, 409, "coordinate_exists", "Maven coordinate already exists")
		return
	}
	if err != nil {
		writeHostedProblem(w, 422, "session_closed", "all declared objects must be uploaded before commit")
		return
	}
	writeNativeMavenJSON(w, 200, a)
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
		// A nil reader policy is the explicit default-open repository policy.
		// Once grants are configured, anonymous clients receive a Basic challenge.
		if h.authenticator.RepositoryReaders == nil {
			principal = Principal{Actor: "anonymous"}
		} else {
			w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway Maven"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
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
	if !h.authenticator.CanReadMavenRepository(principal, repo.Name) {
		_ = h.store.RecordAudit(r.Context(), repository.AuditRecord{Repository: repo.Name, GroupName: repo.Name, Actor: user, Outcome: repository.AuditAccessDenied, OccurredAt: time.Now().UTC(), Format: "maven", Resource: strings.Join(parts[1:], "/"), Operation: strings.ToLower(r.Method), Status: http.StatusForbidden})
		http.Error(w, "repository read permission required", http.StatusForbidden)
		return
	}
	asset, err := h.store.GetMavenAsset(r.Context(), repo.ID, strings.Join(parts[1:], "/"))
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

// deploy accepts Maven's ordinary HTTP PUT layout. Each asset is independently
// committed, while the shared coordinate becomes visible only after its bytes
// and generated checksum sidecars have been validated.
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
	if !h.authenticator.CanReadMavenRepository(principal, repo.Name) {
		_ = h.store.RecordAudit(r.Context(), repository.AuditRecord{Repository: repo.Name, GroupName: repo.Name, Actor: principal.Actor, Outcome: repository.AuditAccessDenied, OccurredAt: time.Now().UTC(), Format: "maven", Resource: strings.Join(parts[1:], "/"), Operation: "put", Status: http.StatusForbidden})
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
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<20))
	if err != nil {
		http.Error(w, "read Maven asset", 400)
		return
	}
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	s := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: group + ":" + artifact + ":" + version, PomObject: name, State: "open", Objects: []repository.MavenDeclaredObject{{Name: name, Digest: digest, Size: int64(len(body))}}, ExpiresAt: time.Now().Add(time.Hour)}
	if _, err = h.store.CreateMavenPublishSession(r.Context(), s); err != nil {
		http.Error(w, "create Maven publish session", 500)
		return
	}
	key := "native/maven/sha256/" + strings.TrimPrefix(digest, "sha256:")
	if err = h.objects.Put(r.Context(), key, body); err != nil || h.store.MarkMavenPublishObject(r.Context(), s.ID, name, key) != nil {
		http.Error(w, "stage Maven asset", 500)
		return
	}
	base := mavenCoordinatePath(s.Coordinate)
	assets := []repository.MavenAsset{{RepositoryID: repo.ID, Path: base + "/" + name, ObjectKey: key, Digest: digest, Size: int64(len(body))}}
	for _, c := range generatedMavenChecksums(body) {
		ck := "native/maven/sha256/" + c.digest
		if err = h.objects.Put(r.Context(), ck, []byte(c.body)); err != nil {
			http.Error(w, "generate Maven checksum", 500)
			return
		}
		assets = append(assets, repository.MavenAsset{RepositoryID: repo.ID, Path: base + "/" + name + c.extension, ObjectKey: ck, Digest: "sha256:" + c.digest, Size: int64(len(c.body))})
	}
	if _, err = h.store.CommitMavenPublishSession(r.Context(), s.ID, assets); err != nil && !errors.Is(err, repository.ErrNameExists) {
		http.Error(w, "commit Maven asset", 422)
		return
	}
	_ = h.store.RecordAudit(r.Context(), repository.AuditRecord{Repository: repo.Name, GroupName: repo.Name, Actor: principal.Actor, Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(), Format: "maven", Resource: strings.Join(path, "/"), Operation: "put", Status: http.StatusCreated, Bytes: int64(len(body))})
	w.WriteHeader(http.StatusCreated)
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
