// Package refscanner implements the bundled HTTP-to-Trivy reference adapter.
// It is intentionally separate from the Gateway process: the adapter receives
// only immutable bytes, cannot mutate repository state, and executes a fixed
// scanner command line.
package refscanner

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

const (
	requestSchemaVersion = "v1"
	reportSchemaVersion  = "v2"
	defaultMaxArtifact   = int64(20 << 30)
	maximumArtifact      = int64(1 << 40)
	maximumAssets        = 256
	maximumMetadataBytes = int64(1 << 20)
	multipartOverhead    = int64(2 << 20)
	defaultScanTimeout   = 10 * time.Minute
	defaultHealthTimeout = 10 * time.Second
	defaultMaxSBOMBytes  = int64(2 << 30)
)

var (
	ErrInvalidOptions = errors.New("reference scanner options are invalid")
	ErrEngineFailed   = errors.New("reference scanner engine failed")
)

// EngineOutput contains Trivy's native JSON report and its converted CycloneDX
// document. The handler stores only a content-addressed SBOM reference in the
// Gateway report; the potentially large document never crosses that boundary.
type EngineOutput struct {
	Report []byte
	SBOM   []byte
}

// EngineHealth is sanitized version and vulnerability-database metadata.
type EngineHealth struct {
	Version           string
	DatabaseVersion   string
	DatabaseUpdatedAt time.Time
}

// Engine is the narrow execution seam used by the HTTP boundary.
type Engine interface {
	Scan(context.Context, string) (EngineOutput, error)
	Health(context.Context) (EngineHealth, error)
}

type Options struct {
	Token            string
	Engine           Engine
	TempDir          string
	MaxArtifactBytes int64
	ScanTimeout      time.Duration
	HealthTimeout    time.Duration
	MaxConcurrent    int
	SBOMDir          string
	SBOMBaseURL      string
	MaxSBOMBytes     int64
}

type handler struct {
	token            string
	engine           Engine
	tempDir          string
	maxArtifactBytes int64
	scanTimeout      time.Duration
	healthTimeout    time.Duration
	slots            chan struct{}
	sboms            *sbomStore
}

func NewHandler(options Options) (http.Handler, error) {
	maxArtifactBytes := options.MaxArtifactBytes
	if maxArtifactBytes == 0 {
		maxArtifactBytes = defaultMaxArtifact
	}
	scanTimeout := options.ScanTimeout
	if scanTimeout == 0 {
		scanTimeout = defaultScanTimeout
	}
	healthTimeout := options.HealthTimeout
	if healthTimeout == 0 {
		healthTimeout = defaultHealthTimeout
	}
	maxConcurrent := options.MaxConcurrent
	if maxConcurrent == 0 {
		maxConcurrent = 1
	}
	maxSBOMBytes := options.MaxSBOMBytes
	if maxSBOMBytes == 0 {
		maxSBOMBytes = defaultMaxSBOMBytes
	}
	if options.Engine == nil || strings.ContainsAny(options.Token, "\r\n") || len(options.Token) > 4096 || maxArtifactBytes < 1 || maxArtifactBytes > maximumArtifact || scanTimeout < time.Second || scanTimeout > 30*time.Minute || healthTimeout < time.Second || healthTimeout > 30*time.Second || maxConcurrent < 1 || maxConcurrent > 32 || maxSBOMBytes < 1 || maxSBOMBytes > 1<<40 {
		return nil, ErrInvalidOptions
	}
	var sboms *sbomStore
	if options.SBOMDir != "" || options.SBOMBaseURL != "" {
		baseURL, err := url.Parse(strings.TrimRight(options.SBOMBaseURL, "/"))
		if err != nil || options.SBOMDir == "" || baseURL.Scheme != "http" && baseURL.Scheme != "https" || baseURL.Host == "" || baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" {
			return nil, ErrInvalidOptions
		}
		if err = os.MkdirAll(options.SBOMDir, 0o700); err != nil {
			return nil, ErrInvalidOptions
		}
		sboms = &sbomStore{dir: options.SBOMDir, baseURL: strings.TrimRight(baseURL.String(), "/"), maxBytes: maxSBOMBytes}
	}
	value := &handler{
		token: options.Token, engine: options.Engine, tempDir: options.TempDir,
		maxArtifactBytes: maxArtifactBytes, scanTimeout: scanTimeout, healthTimeout: healthTimeout,
		slots: make(chan struct{}, maxConcurrent), sboms: sboms,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/livez", value.serveLive)
	mux.HandleFunc("/v1/health", value.serveHealth)
	mux.HandleFunc("/v1/scan", value.serveScan)
	mux.HandleFunc("/v1/sboms/", value.serveSBOM)
	return mux, nil
}

func (h *handler) serveLive(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		methodNotAllowed(response, http.MethodGet, http.MethodHead)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (h *handler) serveHealth(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(response, http.MethodGet)
		return
	}
	if !h.authorized(request) {
		unauthorized(response)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.healthTimeout)
	defer cancel()
	health, err := h.engine.Health(ctx)
	if err != nil {
		writeJSON(response, http.StatusOK, healthResponse{SchemaVersion: requestSchemaVersion, Status: "unhealthy"})
		return
	}
	status := "healthy"
	if health.DatabaseUpdatedAt.IsZero() {
		status = "degraded"
	}
	value := healthResponse{SchemaVersion: requestSchemaVersion, Status: status, Version: boundedLine(health.Version, 128)}
	if !health.DatabaseUpdatedAt.IsZero() {
		value.Database = &healthDatabase{
			Version:   boundedLine(health.DatabaseVersion, 256),
			UpdatedAt: health.DatabaseUpdatedAt.UTC(),
		}
	}
	writeJSON(response, http.StatusOK, value)
}

func (h *handler) serveScan(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(response, http.MethodPost)
		return
	}
	if !h.authorized(request) {
		unauthorized(response)
		return
	}
	if request.Header.Get("X-Artifact-Scanner-Schema") != requestSchemaVersion {
		writeError(response, http.StatusBadRequest, "unsupported_schema")
		return
	}
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		response.Header().Set("Retry-After", "5")
		writeError(response, http.StatusTooManyRequests, "scanner_busy")
		return
	}
	mediaType, parameters, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || parameters["boundary"] == "" {
		writeError(response, http.StatusUnsupportedMediaType, "unsupported_media_type")
		return
	}
	maximumBody := h.maxArtifactBytes + multipartOverhead
	request.Body = http.MaxBytesReader(response, request.Body, maximumBody)
	reader := multipart.NewReader(request.Body, parameters["boundary"])

	root, err := os.MkdirTemp(h.tempDir, "artifact-gateway-scan-")
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "scanner_unavailable")
		return
	}
	defer func() { _ = os.RemoveAll(root) }()
	if err = receiveArtifact(reader, root, h.maxArtifactBytes); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_artifact")
		return
	}

	ctx, cancel := context.WithTimeout(request.Context(), h.scanTimeout)
	defer cancel()
	output, err := h.engine.Scan(ctx, root)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "scanner_unavailable")
		return
	}
	sbomURL := ""
	if len(output.SBOM) > 0 {
		if h.sboms == nil {
			output.SBOM = nil
		} else if sbomURL, err = h.sboms.put(output.SBOM); err != nil {
			writeError(response, http.StatusServiceUnavailable, "scanner_unavailable")
			return
		}
	}
	includeFindings := acceptsSchema(request.Header.Get("X-Artifact-Scanner-Accept-Schema"), reportSchemaVersion)
	report, err := mapTrivyReport(output, includeFindings)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "scanner_invalid_result")
		return
	}
	if !includeFindings {
		report.SchemaVersion = requestSchemaVersion
	}
	if len(report.SBOMs) == 1 {
		report.SBOMs[0].URL = sbomURL
	}
	writeJSON(response, http.StatusOK, report)
}

func (h *handler) serveSBOM(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		methodNotAllowed(response, http.MethodGet, http.MethodHead)
		return
	}
	if !h.authorized(request) {
		unauthorized(response)
		return
	}
	digest := strings.TrimPrefix(request.URL.Path, "/v1/sboms/")
	if h.sboms == nil || !validDigest(digest) {
		writeError(response, http.StatusNotFound, "sbom_not_found")
		return
	}
	content, err := h.sboms.get(digest)
	if err != nil {
		writeError(response, http.StatusNotFound, "sbom_not_found")
		return
	}
	response.Header().Set("Content-Type", "application/vnd.cyclonedx+json")
	response.Header().Set("Cache-Control", "private, immutable, max-age=31536000")
	response.Header().Set("ETag", `"`+digest+`"`)
	response.Header().Set("Content-Length", strconv.FormatInt(int64(len(content)), 10))
	response.WriteHeader(http.StatusOK)
	if request.Method == http.MethodGet {
		_, _ = response.Write(content)
	}
}

func (h *handler) authorized(request *http.Request) bool {
	if h.token == "" {
		return true
	}
	expected := []byte("Bearer " + h.token)
	actual := []byte(request.Header.Get("Authorization"))
	return len(actual) == len(expected) && subtle.ConstantTimeCompare(actual, expected) == 1
}

type wireRequest struct {
	SchemaVersion string      `json:"schemaVersion"`
	RepositoryID  string      `json:"repositoryId"`
	Format        string      `json:"format"`
	Coordinate    string      `json:"coordinate"`
	Digest        string      `json:"digest"`
	Assets        []wireAsset `json:"assets"`
}

type wireAsset struct {
	Part      string `json:"part"`
	Path      string `json:"path"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	MediaType string `json:"mediaType,omitempty"`
}

func receiveArtifact(reader *multipart.Reader, root string, maximumBytes int64) error {
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "metadata" || part.FileName() != "" {
		return errors.New("metadata part is missing")
	}
	metadataType, _, typeErr := mime.ParseMediaType(part.Header.Get("Content-Type"))
	if typeErr != nil || metadataType != "application/json" {
		return errors.New("metadata content type is invalid")
	}
	metadataBody, err := io.ReadAll(io.LimitReader(part, maximumMetadataBytes+1))
	if err != nil || int64(len(metadataBody)) > maximumMetadataBytes {
		return errors.New("metadata is too large")
	}
	var metadata wireRequest
	decoder := json.NewDecoder(strings.NewReader(string(metadataBody)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&metadata); err != nil || !jsonEOF(decoder) || !validMetadata(metadata, maximumBytes) {
		return errors.New("metadata is invalid")
	}
	for _, asset := range metadata.Assets {
		part, err = reader.NextPart()
		if err != nil || part.FormName() != asset.Part {
			return errors.New("asset part is missing")
		}
		destination := filepath.Join(root, filepath.FromSlash(asset.Path))
		if err = os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return errors.New("asset directory cannot be created")
		}
		file, createErr := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if createErr != nil {
			return errors.New("asset cannot be created")
		}
		hash := sha256.New()
		written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(part, asset.Size+1))
		closeErr := file.Close()
		actualDigest := "sha256:" + hex.EncodeToString(hash.Sum(nil))
		if copyErr != nil || closeErr != nil || written != asset.Size || actualDigest != asset.Digest {
			return errors.New("asset integrity check failed")
		}
	}
	if _, err = reader.NextPart(); !errors.Is(err, io.EOF) {
		return errors.New("unexpected multipart data")
	}
	return nil
}

func validMetadata(metadata wireRequest, maximumBytes int64) bool {
	if metadata.SchemaVersion != requestSchemaVersion || !validLine(metadata.RepositoryID, 128) || !referenceFormat(repository.Format(metadata.Format)) || !validLine(metadata.Coordinate, 1024) || !validDigest(metadata.Digest) || len(metadata.Assets) == 0 || len(metadata.Assets) > maximumAssets {
		return false
	}
	seenParts := make(map[string]struct{}, len(metadata.Assets))
	seenPaths := make(map[string]struct{}, len(metadata.Assets))
	var total int64
	for index, asset := range metadata.Assets {
		if asset.Part != "asset-"+strconv.Itoa(index) || !safeAssetPath(asset.Path) || !validDigest(asset.Digest) || asset.Size < 0 || asset.Size > maximumBytes || len(asset.MediaType) > 255 {
			return false
		}
		if asset.MediaType != "" {
			if _, _, err := mime.ParseMediaType(asset.MediaType); err != nil {
				return false
			}
		}
		if _, exists := seenParts[asset.Part]; exists {
			return false
		}
		if _, exists := seenPaths[asset.Path]; exists {
			return false
		}
		seenParts[asset.Part] = struct{}{}
		seenPaths[asset.Path] = struct{}{}
		if total > maximumBytes-asset.Size {
			return false
		}
		total += asset.Size
	}
	return true
}

func referenceFormat(format repository.Format) bool {
	switch format {
	case repository.FormatMaven, repository.FormatRaw, repository.FormatNPM, repository.FormatPyPI, repository.FormatGo, repository.FormatConan:
		return true
	default:
		return false
	}
}

func safeAssetPath(value string) bool {
	if !validLine(value, 2048) || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") {
		return false
	}
	clean := path.Clean(value)
	return clean == value && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(value[len("sha256:"):])
	return err == nil && strings.ToLower(value) == value
}

func validLine(value string, maximum int) bool {
	return strings.TrimSpace(value) != "" && len(value) <= maximum && !strings.ContainsAny(value, "\x00\r\n")
}

func acceptsSchema(header, schema string) bool {
	for _, value := range strings.Split(header, ",") {
		if strings.TrimSpace(value) == schema {
			return true
		}
	}
	return false
}

func jsonEOF(decoder *json.Decoder) bool {
	var extra json.RawMessage
	return errors.Is(decoder.Decode(&extra), io.EOF)
}

type healthResponse struct {
	SchemaVersion string          `json:"schemaVersion"`
	Status        string          `json:"status"`
	Version       string          `json:"version,omitempty"`
	Database      *healthDatabase `json:"database,omitempty"`
}

type healthDatabase struct {
	Version   string    `json:"version,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func boundedLine(value string, maximum int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > maximum {
		value = value[:maximum]
	}
	return value
}

func methodNotAllowed(response http.ResponseWriter, methods ...string) {
	response.Header().Set("Allow", strings.Join(methods, ", "))
	writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
}

func unauthorized(response http.ResponseWriter) {
	response.Header().Set("WWW-Authenticate", `Bearer realm="artifact-reference-scanner"`)
	writeError(response, http.StatusUnauthorized, "unauthorized")
}

func writeError(response http.ResponseWriter, status int, code string) {
	writeJSON(response, status, map[string]string{"error": code})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
