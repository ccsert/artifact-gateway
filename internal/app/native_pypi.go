package app

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/textproto"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	xhtml "golang.org/x/net/html"
)

const (
	pypiUploadBodyLimit = 512 << 20
	pypiArchiveLimit    = 384 << 20
	pypiMetadataLimit   = 2 << 20
)

var (
	pypiProjectSeparator = regexp.MustCompile(`[-_.]+`)
	pypiProjectPattern   = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,253}[a-z0-9])?$`)
	pypiProjectPrefix    = regexp.MustCompile(`^[a-z0-9-]{0,255}$`)
	pypiVersionPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.!+_-]{0,255}$`)
)

type nativePyPIHandler struct {
	store              repository.NativePyPIStore
	repos              repository.HostedRepositoryStore
	objects            OCIObjectStore
	auth               Authenticator
	authorizer         RepositoryAuthorizer
	audit              repository.Store
	metrics            *Metrics
	proxy              PyPIClient
	publicationScanner *publicationScanScheduler
}

type pypiRoute struct {
	repository string
	kind       string
	project    string
	filename   string
}

type pypiAuditTarget struct {
	GroupName  string
	Repository string
	MemberName string
}

func newNativePyPIHandler(store GatewayStore, objects OCIObjectStore, auth Authenticator) nativePyPIHandler {
	if objects == nil {
		objects = NewMemoryOCIObjectStore()
	}
	return nativePyPIHandler{
		store: store, repos: store, objects: objects, auth: auth, audit: store, proxy: UpstreamClient{},
		authorizer: RepositoryAuthorizer{Grants: store, Legacy: auth, LegacyFallback: func(Principal, repository.HostedRepository, RepositoryOperation) AuthorizationDecision {
			return AuthorizationDecision{Allowed: true, Source: "legacy_protocol", Reason: "authenticated"}
		}},
	}
}

func (h nativePyPIHandler) withPublicationScanner(scanner publicationScanScheduler) nativePyPIHandler {
	h.publicationScanner = &scanner
	return h
}

func (h nativePyPIHandler) withProxy(client PyPIClient) nativePyPIHandler {
	if client != nil {
		h.proxy = client
	}
	return h
}

func (h nativePyPIHandler) withMetrics(metrics *Metrics) nativePyPIHandler {
	h.metrics = metrics
	return h
}

func (h nativePyPIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route, ok := parsePyPIPath(r.URL.EscapedPath())
	if !ok {
		http.NotFound(w, r)
		return
	}
	repo, err := h.repos.GetHostedRepositoryByName(r.Context(), route.repository)
	if errors.Is(err, repository.ErrNotFound) || repo.Format != repository.FormatPyPI || repo.State != repository.RepositoryActive {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "repository unavailable", http.StatusServiceUnavailable)
		return
	}
	operation := RepositoryRead
	if route.kind == "legacy" {
		operation = RepositoryWrite
	}
	principal, authenticated := h.protocolPrincipal(r)
	if !authenticated {
		if operation == RepositoryRead && anonymousHostedRepositoryReadAllowed(r.Context(), h.store, repo, r.Method) {
			principal = anonymousPrincipal()
		} else {
			h.challenge(w, http.StatusUnauthorized, "authentication required")
			return
		}
	}
	if !isAnonymous(principal) {
		resource := route.project
		if resource == "" {
			resource = route.filename
		}
		decision := h.authorizer.AuthorizeResource(r.Context(), principal, repo, operation, resource)
		if !decision.Allowed {
			h.challenge(w, http.StatusForbidden, "repository permission required")
			return
		}
	}
	switch route.kind {
	case "legacy":
		if r.Method != http.MethodPost || repo.Type != repository.RepositoryTypeHosted {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		h.upload(w, r, repo, principal.Actor)
	case "simple-root":
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		h.simpleRoot(w, r, repo, principal.Actor)
	case "simple-project":
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if repo.Type == repository.RepositoryTypeProxy {
			files, disposition, err := h.resolveProxyProject(r, repo, route.project)
			if err != nil {
				if errors.Is(err, repository.ErrNotFound) {
					http.NotFound(w, r)
					h.recordAudit(r, repo, route.project, "project", principal.Actor, repository.AuditNotFound, http.StatusNotFound, 0, disposition)
				} else {
					h.writeError(w, http.StatusBadGateway, "PyPI upstream unavailable")
					h.recordAudit(r, repo, route.project, "project", principal.Actor, repository.AuditUpstreamError, http.StatusBadGateway, 0, disposition)
				}
				return
			}
			if disposition == "stale" {
				w.Header().Set("Warning", `110 Artifact-Gateway "Response is stale"`)
			}
			h.writeSimpleProject(w, r, route.project, files)
			h.recordAudit(r, repo, route.project, "project", principal.Actor, repository.AuditResolved, http.StatusOK, 0, disposition)
		} else {
			h.simpleProject(w, r, repo, route.project, principal.Actor)
		}
	case "package":
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		h.download(w, r, repo, route.filename, principal.Actor)
	default:
		http.NotFound(w, r)
	}
}

func parsePyPIPath(escapedPath string) (pypiRoute, bool) {
	if !strings.HasPrefix(escapedPath, "/pypi/") {
		return pypiRoute{}, false
	}
	remainder := strings.TrimPrefix(escapedPath, "/pypi/")
	repositoryName, resource, found := strings.Cut(remainder, "/")
	repositoryName, err := url.PathUnescape(repositoryName)
	if err != nil || !found || repositoryName == "" || strings.Contains(repositoryName, "/") {
		return pypiRoute{}, false
	}
	resource = strings.TrimPrefix(resource, "/")
	switch {
	case resource == "legacy" || resource == "legacy/":
		return pypiRoute{repository: repositoryName, kind: "legacy"}, true
	case resource == "simple" || resource == "simple/":
		return pypiRoute{repository: repositoryName, kind: "simple-root"}, true
	case strings.HasPrefix(resource, "simple/"):
		project := strings.TrimSuffix(strings.TrimPrefix(resource, "simple/"), "/")
		project, err = url.PathUnescape(project)
		if err != nil || project == "" || strings.Contains(project, "/") {
			return pypiRoute{}, false
		}
		project = normalizePyPIProject(project)
		if !validPyPIProject(project) {
			return pypiRoute{}, false
		}
		return pypiRoute{repository: repositoryName, kind: "simple-project", project: project}, true
	case strings.HasPrefix(resource, "packages/"):
		filename := strings.TrimPrefix(resource, "packages/")
		filename, err = url.PathUnescape(filename)
		if err != nil || !validPyPIFilename(filename) {
			return pypiRoute{}, false
		}
		return pypiRoute{repository: repositoryName, kind: "package", filename: filename}, true
	default:
		return pypiRoute{}, false
	}
}

func normalizePyPIProject(name string) string {
	return pypiProjectSeparator.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
}

func validPyPIProject(project string) bool {
	return pypiProjectPattern.MatchString(project)
}

func validPyPIProjectSearchPrefix(prefix string) bool {
	return pypiProjectPrefix.MatchString(normalizePyPIProject(prefix))
}

func validPyPIFilename(filename string) bool {
	return filename != "" && len(filename) <= 512 && filepath.Base(filename) == filename && !strings.ContainsAny(filename, `/\\`) && !strings.ContainsRune(filename, 0)
}

func (h nativePyPIHandler) upload(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, publisher string) {
	r.Body = http.MaxBytesReader(w, r.Body, pypiUploadBodyLimit)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid multipart upload")
		return
	}
	if r.MultipartForm != nil {
		defer func() { _ = r.MultipartForm.RemoveAll() }()
	}
	if r.FormValue(":action") != "file_upload" {
		h.writeError(w, http.StatusBadRequest, "unsupported upload action")
		return
	}
	project := normalizePyPIProject(r.FormValue("name"))
	version := strings.TrimSpace(r.FormValue("version"))
	if !validPyPIProject(project) || !pypiVersionPattern.MatchString(version) {
		h.writeError(w, http.StatusBadRequest, "invalid project name or version")
		return
	}
	file, header, err := r.FormFile("content")
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "distribution file is required")
		return
	}
	defer func() { _ = file.Close() }()
	if !validPyPIFilename(header.Filename) {
		h.writeError(w, http.StatusBadRequest, "invalid distribution filename")
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, pypiArchiveLimit+1))
	if err != nil || len(data) == 0 || len(data) > pypiArchiveLimit {
		h.writeError(w, http.StatusRequestEntityTooLarge, "distribution file is too large")
		return
	}
	sum := sha256.Sum256(data)
	digestHex := hex.EncodeToString(sum[:])
	declaredDigest := strings.ToLower(strings.TrimSpace(r.FormValue("sha256_digest")))
	if declaredDigest != "" && declaredDigest != digestHex {
		h.writeError(w, http.StatusBadRequest, "sha256 digest does not match upload")
		return
	}
	metadataName, metadataVersion, err := inspectPyPIDistribution(header.Filename, data)
	if err != nil || normalizePyPIProject(metadataName) != project || metadataVersion != version {
		h.writeError(w, http.StatusBadRequest, "distribution metadata does not match upload")
		return
	}
	digest := "sha256:" + digestHex
	objectKey := "native/pypi/sha256/" + digestHex
	objectCtx, objectRelease, err := repository.LockObjectKeys(r.Context(), []string{objectKey}, h.store, repository.FormatPyPI, h.store.LockPyPIObject)
	if err != nil {
		h.writeError(w, http.StatusServiceUnavailable, "PyPI object coordination is unavailable")
		return
	}
	defer objectRelease()
	if err = h.objects.PutVerifiedReader(objectCtx, objectKey, bytes.NewReader(data), int64(len(data)), digest); err != nil {
		h.writeError(w, http.StatusInternalServerError, "persist distribution failed")
		return
	}
	published, err := h.store.PublishPyPIFile(objectCtx, repository.PyPIFile{
		RepositoryID: repo.ID, Project: project, Version: version, Filename: header.Filename,
		FileType: strings.TrimSpace(r.FormValue("filetype")), PythonVersion: strings.TrimSpace(r.FormValue("pyversion")),
		RequiresPython: strings.TrimSpace(r.FormValue("requires_python")), Digest: digest, ObjectKey: objectKey,
		Size: int64(len(data)), Publisher: publisher,
	})
	if repository.IsQuotaExceeded(err) {
		h.writeError(w, http.StatusInsufficientStorage, "repository capacity quota exceeded")
		return
	}
	if errors.Is(err, repository.ErrNameExists) {
		h.writeError(w, http.StatusConflict, "distribution filename already exists")
		return
	}
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "commit distribution failed")
		return
	}
	w.Header().Set("Location", "/pypi/"+url.PathEscape(repo.Name)+"/packages/"+url.PathEscape(published.Filename))
	if h.publicationScanner != nil {
		_ = h.publicationScanner.Schedule(r.Context(), repo, project+"@"+version, published.Digest, publisher)
	}
	w.WriteHeader(http.StatusCreated)
	h.recordAudit(r, repo, project+"@"+version, "distribution", publisher, repository.AuditResolved, http.StatusCreated, published.Size, "bypass")
}

func inspectPyPIDistribution(filename string, data []byte) (string, string, error) {
	var metadata []byte
	var err error
	switch {
	case strings.HasSuffix(strings.ToLower(filename), ".whl") || strings.HasSuffix(strings.ToLower(filename), ".zip"):
		metadata, err = readPyPIZipMetadata(data)
	case strings.HasSuffix(strings.ToLower(filename), ".tar.gz") || strings.HasSuffix(strings.ToLower(filename), ".tgz"):
		metadata, err = readPyPITarMetadata(data)
	default:
		return "", "", errors.New("unsupported distribution archive")
	}
	if err != nil {
		return "", "", err
	}
	// Core metadata in wheels commonly ends immediately after the last header;
	// append the MIME header terminator so the standard parser accepts EOF-only
	// metadata without weakening header validation.
	metadata = append(append([]byte(nil), metadata...), '\n')
	headers, err := textproto.NewReader(bufio.NewReader(bytes.NewReader(metadata))).ReadMIMEHeader()
	if err != nil {
		return "", "", err
	}
	name, version := strings.TrimSpace(headers.Get("Name")), strings.TrimSpace(headers.Get("Version"))
	if name == "" || version == "" {
		return "", "", errors.New("distribution metadata is missing Name or Version")
	}
	return name, version, nil
}

func readPyPIZipMetadata(data []byte) ([]byte, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var metadata []byte
	for _, file := range archive.File {
		upper := strings.ToUpper(file.Name)
		if !strings.HasSuffix(upper, ".DIST-INFO/METADATA") && !strings.HasSuffix(upper, "/PKG-INFO") {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			return nil, err
		}
		candidate, readErr := io.ReadAll(io.LimitReader(reader, pypiMetadataLimit+1))
		_ = reader.Close()
		if readErr != nil || len(candidate) > pypiMetadataLimit || metadata != nil {
			return nil, errors.New("invalid distribution metadata")
		}
		metadata = candidate
	}
	if metadata == nil {
		return nil, errors.New("distribution metadata not found")
	}
	return metadata, nil
}

func readPyPITarMetadata(data []byte) ([]byte, error) {
	gzipReader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer func() { _ = gzipReader.Close() }()
	archive := tar.NewReader(io.LimitReader(gzipReader, 1<<30))
	var metadata []byte
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag != tar.TypeReg || !strings.HasSuffix(strings.ToUpper(header.Name), "/PKG-INFO") {
			continue
		}
		if header.Size < 0 || header.Size > pypiMetadataLimit {
			return nil, errors.New("invalid distribution metadata")
		}
		if metadata != nil {
			return nil, errors.New("multiple distribution metadata files")
		}
		metadata, err = io.ReadAll(io.LimitReader(archive, pypiMetadataLimit+1))
		if err != nil || len(metadata) > pypiMetadataLimit {
			return nil, errors.New("invalid distribution metadata")
		}
	}
	if metadata == nil {
		return nil, errors.New("distribution metadata not found")
	}
	return metadata, nil
}

func (h nativePyPIHandler) simpleRoot(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, actor string) {
	projects, err := h.store.ListPyPIProjects(r.Context(), repo.ID, "", 10000, "")
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "list projects failed")
		h.recordAudit(r, repo, "", "index", actor, repository.AuditStorageError, http.StatusInternalServerError, 0, "bypass")
		return
	}
	h.simpleRootFromProjects(w, r, repo, projects)
	h.recordAudit(r, repo, "", "index", actor, repository.AuditResolved, http.StatusOK, 0, "bypass")
}

func (h nativePyPIHandler) simpleRootFromProjects(w http.ResponseWriter, r *http.Request, _ repository.HostedRepository, projects []repository.PyPIProjectSummary) {
	if wantsPyPIJSON(r) {
		items := make([]map[string]string, 0, len(projects))
		for _, project := range projects {
			items = append(items, map[string]string{"name": project.Project})
		}
		h.writePyPIJSON(w, r, map[string]any{"meta": map[string]string{"api-version": "1.0"}, "projects": items})
		return
	}
	var body strings.Builder
	body.WriteString("<!DOCTYPE html><html><body>\n")
	for _, project := range projects {
		fmt.Fprintf(&body, `<a href="%s/">%s</a>`+"\n", url.PathEscape(project.Project), html.EscapeString(project.Project))
	}
	body.WriteString("</body></html>\n")
	h.writeSimpleHTML(w, r, body.String())
}

func (h nativePyPIHandler) simpleProject(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, project, actor string) {
	files, err := h.store.ListPyPIProjectFiles(r.Context(), repo.ID, project)
	if errors.Is(err, repository.ErrNotFound) {
		http.NotFound(w, r)
		h.recordAudit(r, repo, project, "project", actor, repository.AuditNotFound, http.StatusNotFound, 0, "bypass")
		return
	}
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "list project files failed")
		h.recordAudit(r, repo, project, "project", actor, repository.AuditStorageError, http.StatusInternalServerError, 0, "bypass")
		return
	}
	h.writeSimpleProject(w, r, project, files)
	h.recordAudit(r, repo, project, "project", actor, repository.AuditResolved, http.StatusOK, 0, "bypass")
}

func (h nativePyPIHandler) writeSimpleProject(w http.ResponseWriter, r *http.Request, project string, files []repository.PyPIFile) {
	if wantsPyPIJSON(r) {
		items := make([]map[string]any, 0, len(files))
		for _, file := range files {
			gateway := map[string]any{
				"version": file.Version, "size": file.Size, "publisher": file.Publisher,
				"created-at": file.CreatedAt, "cached": file.ObjectKey != "", "file-type": file.FileType,
				"python-version": file.PythonVersion,
			}
			if !file.CachedAt.IsZero() {
				gateway["cached-at"] = file.CachedAt
			}
			item := map[string]any{"filename": file.Filename, "url": "../../packages/" + url.PathEscape(file.Filename), "hashes": map[string]string{"sha256": strings.TrimPrefix(file.Digest, "sha256:")}, "_artifact-gateway": gateway}
			if file.RequiresPython != "" {
				item["requires-python"] = file.RequiresPython
			}
			items = append(items, item)
		}
		h.writePyPIJSON(w, r, map[string]any{"meta": map[string]string{"api-version": "1.0"}, "name": project, "files": items})
		return
	}
	var body strings.Builder
	body.WriteString("<!DOCTYPE html><html><body>\n")
	for _, file := range files {
		attributes := ""
		if file.RequiresPython != "" {
			attributes = ` data-requires-python="` + html.EscapeString(file.RequiresPython) + `"`
		}
		fmt.Fprintf(&body, `<a href="../../packages/%s#sha256=%s"%s>%s</a>`+"\n", url.PathEscape(file.Filename), strings.TrimPrefix(file.Digest, "sha256:"), attributes, html.EscapeString(file.Filename))
	}
	body.WriteString("</body></html>\n")
	h.writeSimpleHTML(w, r, body.String())
}

func (h nativePyPIHandler) download(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, filename, actor string) {
	h.downloadForTarget(w, r, repo, filename, actor, pypiAuditTarget{GroupName: repo.Name, Repository: repo.Name})
}

func (h nativePyPIHandler) downloadForTarget(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, filename, actor string, target pypiAuditTarget) {
	disposition := "bypass"
	if repo.Type == repository.RepositoryTypeProxy {
		disposition = "hit"
	}
	file, err := h.store.GetPyPIFile(r.Context(), repo.ID, filename)
	if errors.Is(err, repository.ErrNotFound) {
		http.NotFound(w, r)
		h.recordAuditForTarget(r, repo, target, filename, "distribution", actor, repository.AuditNotFound, http.StatusNotFound, 0, disposition)
		return
	}
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "read distribution failed")
		h.recordAuditForTarget(r, repo, target, filename, "distribution", actor, repository.AuditStorageError, http.StatusInternalServerError, 0, disposition)
		return
	}
	if file.ObjectKey == "" {
		disposition = "miss"
		if repo.Type != repository.RepositoryTypeProxy {
			h.writeError(w, http.StatusServiceUnavailable, "distribution object unavailable")
			h.recordAuditForTarget(r, repo, target, filename, "distribution", actor, repository.AuditStorageError, http.StatusServiceUnavailable, 0, disposition)
			return
		}
		file, err = h.cacheProxyFile(r, repo, file)
		if repository.IsQuotaExceeded(err) {
			h.writeError(w, http.StatusInsufficientStorage, "repository capacity quota exceeded")
			h.recordAuditForTarget(r, repo, target, filename, "distribution", actor, repository.AuditStorageError, http.StatusInsufficientStorage, 0, disposition)
			return
		}
		if err != nil {
			h.writeError(w, http.StatusBadGateway, "cache PyPI distribution failed")
			h.recordAuditForTarget(r, repo, target, filename, "distribution", actor, repository.AuditUpstreamError, http.StatusBadGateway, 0, disposition)
			return
		}
	}
	reader, size, err := h.objects.Open(r.Context(), file.ObjectKey)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "distribution object unavailable")
		h.recordAuditForTarget(r, repo, target, file.Project+"@"+file.Version, "distribution", actor, repository.AuditStorageError, http.StatusInternalServerError, 0, disposition)
		return
	}
	defer func() { _ = reader.Close() }()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("ETag", `"`+strings.TrimPrefix(file.Digest, "sha256:")+`"`)
	w.Header().Set("X-Checksum-Sha256", strings.TrimPrefix(file.Digest, "sha256:"))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = io.Copy(w, reader)
	}
	h.recordAuditForTarget(r, repo, target, file.Project+"@"+file.Version, "distribution", actor, repository.AuditResolved, http.StatusOK, size, disposition)
}

func (h nativePyPIHandler) resolveProxyProject(r *http.Request, repo repository.HostedRepository, project string) ([]repository.PyPIFile, string, error) {
	cached, cacheErr := h.store.ListPyPIProjectFiles(r.Context(), repo.ID, project)
	disposition := "miss"
	if cacheErr == nil && len(cached) > 0 {
		disposition = "hit"
	}
	target := pyPIProjectURL(repo.Endpoint, project)
	response, err := h.proxy.FetchPyPI(r.Context(), http.MethodGet, repo, target, r.Header)
	if err != nil {
		if cacheErr == nil && len(cached) > 0 {
			return cached, "stale", nil
		}
		return nil, disposition, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotFound {
		if cacheErr == nil && len(cached) > 0 {
			return cached, "stale", nil
		}
		return nil, disposition, repository.ErrNotFound
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if cacheErr == nil && len(cached) > 0 {
			return cached, "stale", nil
		}
		return nil, disposition, fmt.Errorf("PyPI upstream returned %d", response.StatusCode)
	}
	files, err := parsePyPISimpleResponse(response, repo.ID, project)
	if err != nil {
		if cacheErr == nil && len(cached) > 0 {
			return cached, "stale", nil
		}
		return nil, disposition, err
	}
	allowed := files[:0]
	for _, file := range files {
		target, parseErr := url.Parse(file.SourceURL)
		if parseErr == nil && proxyUpstreamURLAllowed(repo, target) {
			allowed = append(allowed, file)
		}
	}
	files = allowed
	if len(files) == 0 {
		return nil, disposition, repository.ErrNotFound
	}
	if err = h.store.SyncPyPIProxyFiles(r.Context(), repo.ID, project, files); err != nil {
		return nil, disposition, err
	}
	stored, err := h.store.ListPyPIProjectFiles(r.Context(), repo.ID, project)
	return stored, disposition, err
}

func pyPIProjectURL(endpoint, project string) string {
	base := strings.TrimRight(endpoint, "/")
	if !strings.HasSuffix(strings.ToLower(base), "/simple") {
		base += "/simple"
	}
	return base + "/" + url.PathEscape(project) + "/"
}

type pypiSimpleJSON struct {
	Files []struct {
		Filename       string            `json:"filename"`
		URL            string            `json:"url"`
		Hashes         map[string]string `json:"hashes"`
		RequiresPython string            `json:"requires-python"`
	} `json:"files"`
}

func parsePyPISimpleResponse(response *http.Response, repositoryID, project string) ([]repository.PyPIFile, error) {
	data, err := io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
	if err != nil || len(data) > 8<<20 {
		return nil, errors.New("PyPI Simple response is too large")
	}
	baseURL := response.Request.URL
	files := make([]repository.PyPIFile, 0)
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "json") {
		var document pypiSimpleJSON
		if err = json.Unmarshal(data, &document); err != nil {
			return nil, err
		}
		for _, incoming := range document.Files {
			if file, ok := pypiProxyFile(repositoryID, project, incoming.Filename, incoming.URL, incoming.Hashes["sha256"], incoming.RequiresPython, baseURL); ok {
				files = append(files, file)
			}
		}
	} else {
		document, parseErr := xhtml.Parse(bytes.NewReader(data))
		if parseErr != nil {
			return nil, parseErr
		}
		var visit func(*xhtml.Node)
		visit = func(node *xhtml.Node) {
			if node.Type == xhtml.ElementNode && node.Data == "a" {
				var href, requiresPython string
				for _, attribute := range node.Attr {
					switch strings.ToLower(attribute.Key) {
					case "href":
						href = attribute.Val
					case "data-requires-python":
						requiresPython = attribute.Val
					}
				}
				if parsed, parseErr := url.Parse(href); parseErr == nil {
					filename, _ := url.PathUnescape(filepath.Base(parsed.Path))
					digest := parsed.Query().Get("sha256")
					if digest == "" {
						if fragment, fragmentErr := url.ParseQuery(strings.ReplaceAll(parsed.Fragment, ";", "&")); fragmentErr == nil {
							digest = fragment.Get("sha256")
						}
					}
					if file, ok := pypiProxyFile(repositoryID, project, filename, href, digest, requiresPython, baseURL); ok {
						files = append(files, file)
					}
				}
			}
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				visit(child)
			}
		}
		visit(document)
	}
	if len(files) == 0 {
		return nil, repository.ErrNotFound
	}
	return files, nil
}

func pypiProxyFile(repositoryID, project, filename, href, digest, requiresPython string, baseURL *url.URL) (repository.PyPIFile, bool) {
	digest = strings.ToLower(strings.TrimSpace(digest))
	if !validPyPIFilename(filename) || len(digest) != 64 {
		return repository.PyPIFile{}, false
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return repository.PyPIFile{}, false
	}
	version, ok := pypiVersionFromFilename(project, filename)
	if !ok {
		return repository.PyPIFile{}, false
	}
	parsed, err := url.Parse(href)
	if err != nil {
		return repository.PyPIFile{}, false
	}
	resolved := baseURL.ResolveReference(parsed)
	resolved.Fragment = ""
	fileType := "sdist"
	if strings.HasSuffix(strings.ToLower(filename), ".whl") {
		fileType = "bdist_wheel"
	}
	return repository.PyPIFile{RepositoryID: repositoryID, Project: project, Version: version, Filename: filename, FileType: fileType, RequiresPython: html.UnescapeString(requiresPython), Digest: "sha256:" + digest, SourceURL: resolved.String(), State: "visible", Publisher: "upstream:" + resolved.Hostname()}, true
}

func pypiVersionFromFilename(project, filename string) (string, bool) {
	lower := strings.ToLower(filename)
	if strings.HasSuffix(lower, ".whl") {
		parts := strings.Split(strings.TrimSuffix(filename, filepath.Ext(filename)), "-")
		if len(parts) >= 5 && normalizePyPIProject(parts[0]) == project && pypiVersionPattern.MatchString(parts[1]) {
			return parts[1], true
		}
		return "", false
	}
	base := filename
	for _, suffix := range []string{".tar.gz", ".tgz", ".zip"} {
		if strings.HasSuffix(strings.ToLower(base), suffix) {
			base = base[:len(base)-len(suffix)]
			break
		}
	}
	for index, char := range base {
		if char != '-' || index == 0 || index == len(base)-1 {
			continue
		}
		version := base[index+1:]
		if normalizePyPIProject(base[:index]) == project && pypiVersionPattern.MatchString(version) {
			return version, true
		}
	}
	return "", false
}

func (h nativePyPIHandler) cacheProxyFile(r *http.Request, repo repository.HostedRepository, file repository.PyPIFile) (repository.PyPIFile, error) {
	response, err := h.proxy.FetchPyPI(r.Context(), http.MethodGet, repo, file.SourceURL, r.Header)
	if err != nil {
		return repository.PyPIFile{}, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return repository.PyPIFile{}, fmt.Errorf("PyPI upstream returned %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, pypiArchiveLimit+1))
	if err != nil || len(data) == 0 || len(data) > pypiArchiveLimit {
		return repository.PyPIFile{}, errors.New("invalid PyPI distribution size")
	}
	sum := sha256.Sum256(data)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if digest != file.Digest {
		return repository.PyPIFile{}, repository.ErrUpstreamChanged
	}
	name, version, err := inspectPyPIDistribution(file.Filename, data)
	if err != nil || normalizePyPIProject(name) != file.Project || version != file.Version {
		return repository.PyPIFile{}, repository.ErrUpstreamChanged
	}
	file.ObjectKey = "native/pypi/sha256/" + strings.TrimPrefix(digest, "sha256:")
	file.Size = int64(len(data))
	file.CachedAt = time.Now().UTC()
	objectRelease, err := h.store.LockPyPIObject(r.Context(), file.ObjectKey)
	if err != nil {
		return repository.PyPIFile{}, err
	}
	defer objectRelease()
	if err = h.objects.PutVerifiedReader(r.Context(), file.ObjectKey, bytes.NewReader(data), file.Size, digest); err != nil {
		return repository.PyPIFile{}, err
	}
	return h.store.CachePyPIProxyFile(r.Context(), file)
}

func (h nativePyPIHandler) recordAudit(r *http.Request, repo repository.HostedRepository, resource, representation, actor string, outcome repository.AuditOutcome, status int, bytes int64, disposition string) {
	h.recordAuditForTarget(r, repo, pypiAuditTarget{GroupName: repo.Name, Repository: repo.Name}, resource, representation, actor, outcome, status, bytes, disposition)
}

func (h nativePyPIHandler) recordAuditForTarget(r *http.Request, repo repository.HostedRepository, target pypiAuditTarget, resource, representation, actor string, outcome repository.AuditOutcome, status int, bytes int64, disposition string) {
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
	audit := repository.AuditRecord{
		GroupName: target.GroupName, Repository: target.Repository, MemberName: target.MemberName,
		Actor: actor, Outcome: outcome, OccurredAt: time.Now().UTC(), Format: string(repository.FormatPyPI),
		Resource: resource, Representation: representation, MemberType: memberType, UpstreamHost: upstreamHost,
		Operation: strings.ToLower(r.Method), Status: status, CacheDisposition: disposition, Bytes: bytes,
	}
	if actor == anonymousActor {
		audit.AuthorizationSource = anonymousAuthorizationSource
		audit.AuthorizationReason = anonymousAuthorizationReason
	}
	_ = h.audit.RecordAudit(r.Context(), audit)
}

func (h nativePyPIHandler) protocolPrincipal(r *http.Request) (Principal, bool) {
	if principal, ok := h.auth.Authenticate(r.Header.Get("Authorization")); ok {
		return principal, true
	}
	username, password, ok := r.BasicAuth()
	if !ok {
		return Principal{}, false
	}
	return h.auth.AuthenticateBasic(username, password)
}

func (h nativePyPIHandler) challenge(w http.ResponseWriter, status int, message string) {
	w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway PyPI"`)
	h.writeError(w, status, message)
}

func (h nativePyPIHandler) writeError(w http.ResponseWriter, status int, message string) {
	http.Error(w, message, status)
}

func (h nativePyPIHandler) writeSimpleHTML(w http.ResponseWriter, r *http.Request, body string) {
	w.Header().Set("Content-Type", "text/html; charset=UTF-8")
	w.Header().Set("X-PyPI-Last-Serial", strconv.FormatInt(time.Now().UTC().Unix(), 10))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, body)
	}
}

func (h nativePyPIHandler) writePyPIJSON(w http.ResponseWriter, r *http.Request, value any) {
	w.Header().Set("Content-Type", "application/vnd.pypi.simple.v1+json")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_ = json.NewEncoder(w).Encode(value)
	}
}

func wantsPyPIJSON(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "application/vnd.pypi.simple.v1+json")
}
