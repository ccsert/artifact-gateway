package scanning

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

const (
	defaultScanTimeout      = 2 * time.Minute
	defaultHealthTimeout    = 2 * time.Second
	defaultMaxResponseBytes = 512 << 10
	defaultMaxArtifactBytes = 20 << 30
	maxHealthResponseBytes  = 64 << 10
)

var (
	ErrInvalidConfiguration = errors.New("artifact scanner configuration is invalid")
	ErrInvalidArtifact      = errors.New("artifact scan input is invalid")
	ErrAssetIntegrity       = errors.New("artifact scan asset failed integrity validation")
	ErrScannerUnavailable   = errors.New("artifact scanner is unavailable")
	ErrHealthNotConfigured  = errors.New("artifact scanner health endpoint is not configured")
	ErrInvalidResponse      = errors.New("artifact scanner response is invalid")
)

type HTTPOptions struct {
	Name             string
	Endpoint         string
	HealthEndpoint   string
	Token            string
	Client           *http.Client
	Timeout          time.Duration
	HealthTimeout    time.Duration
	MaxResponseBytes int64
	MaxArtifactBytes int64
}

// HTTPScanner streams a logical artifact to a controlled remote scanner. It
// never buffers artifact bytes in memory and never executes local commands.
type HTTPScanner struct {
	name             string
	endpoint         string
	healthEndpoint   string
	token            string
	client           *http.Client
	timeout          time.Duration
	healthTimeout    time.Duration
	maxResponseBytes int64
	maxArtifactBytes int64
	now              func() time.Time
}

func NewHTTPScanner(options HTTPOptions) (*HTTPScanner, error) {
	name := strings.TrimSpace(options.Name)
	endpoint, err := validateEndpoint(options.Endpoint)
	if err != nil || !validText(name, 128) || strings.ContainsAny(options.Token, "\r\n") {
		return nil, ErrInvalidConfiguration
	}
	healthEndpoint := ""
	if strings.TrimSpace(options.HealthEndpoint) != "" {
		parsed, healthErr := validateEndpoint(options.HealthEndpoint)
		if healthErr != nil {
			return nil, ErrInvalidConfiguration
		}
		healthEndpoint = parsed.String()
	}
	timeout := options.Timeout
	if timeout == 0 {
		timeout = defaultScanTimeout
	}
	healthTimeout := options.HealthTimeout
	if healthTimeout == 0 {
		healthTimeout = defaultHealthTimeout
	}
	maxResponseBytes := options.MaxResponseBytes
	if maxResponseBytes == 0 {
		maxResponseBytes = defaultMaxResponseBytes
	}
	maxArtifactBytes := options.MaxArtifactBytes
	if maxArtifactBytes == 0 {
		maxArtifactBytes = defaultMaxArtifactBytes
	}
	if timeout < time.Second || timeout > 30*time.Minute || healthTimeout < time.Second || healthTimeout > 30*time.Second || maxResponseBytes < 1024 || maxResponseBytes > 8<<20 || maxArtifactBytes < 1 || maxArtifactBytes > 1<<40 {
		return nil, ErrInvalidConfiguration
	}
	client := options.Client
	if client == nil {
		client = &http.Client{}
	}
	clientCopy := *client
	// Scanner credentials and request bodies must never be replayed to a
	// redirected endpoint.
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &HTTPScanner{
		name: name, endpoint: endpoint.String(), healthEndpoint: healthEndpoint,
		token: options.Token, client: &clientCopy, timeout: timeout, healthTimeout: healthTimeout,
		maxResponseBytes: maxResponseBytes, maxArtifactBytes: maxArtifactBytes,
		now: func() time.Time { return time.Now().UTC() },
	}, nil
}

// Health reads a bounded, read-only scanner status document. It uses a
// separate endpoint so probing can never be mistaken for a scan request.
func (s *HTTPScanner) Health(ctx context.Context) (Health, error) {
	if s.healthEndpoint == "" {
		return Health{}, ErrHealthNotConfigured
	}
	ctx, cancel := context.WithTimeout(ctx, s.healthTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.healthEndpoint, nil)
	if err != nil {
		return Health{}, ErrInvalidConfiguration
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Artifact-Scanner-Schema", SchemaVersion)
	if s.token != "" {
		request.Header.Set("Authorization", "Bearer "+s.token)
	}
	response, err := s.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return Health{}, ctx.Err()
		}
		return Health{}, ErrScannerUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return Health{}, fmt.Errorf("%w: HTTP %d", ErrScannerUnavailable, response.StatusCode)
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	if mediaType != "application/json" {
		return Health{}, ErrInvalidResponse
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxHealthResponseBytes+1))
	if err != nil || len(body) > maxHealthResponseBytes {
		return Health{}, ErrInvalidResponse
	}
	return s.decodeHealth(body)
}

func (s *HTTPScanner) Scan(ctx context.Context, artifact Artifact) (Report, error) {
	if err := validateArtifact(artifact, s.maxArtifactBytes); err != nil {
		return Report{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, reader)
	if err != nil {
		_ = reader.Close()
		_ = writer.Close()
		return Report{}, ErrInvalidConfiguration
	}
	request.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Artifact-Scanner-Schema", SchemaVersion)
	request.Header.Set("X-Artifact-Scanner-Accept-Schema", AcceptedReportSchemaValue)
	if s.token != "" {
		request.Header.Set("Authorization", "Bearer "+s.token)
	}

	writeResult := make(chan error, 1)
	go func() {
		streamErr := writeArtifact(ctx, multipartWriter, artifact)
		if closeErr := multipartWriter.Close(); streamErr == nil {
			streamErr = closeErr
		}
		_ = writer.CloseWithError(streamErr)
		writeResult <- streamErr
	}()

	response, requestErr := s.client.Do(request)
	if requestErr != nil {
		_ = reader.CloseWithError(requestErr)
		streamErr := <-writeResult
		if ctx.Err() != nil {
			return Report{}, ctx.Err()
		}
		if streamErr != nil && !errors.Is(streamErr, ErrScannerUnavailable) {
			return Report{}, streamErr
		}
		return Report{}, ErrScannerUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_ = reader.CloseWithError(ErrScannerUnavailable)
		<-writeResult
		return Report{}, fmt.Errorf("%w: HTTP %d", ErrScannerUnavailable, response.StatusCode)
	}
	if streamErr := <-writeResult; streamErr != nil {
		return Report{}, streamErr
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	if mediaType != "application/json" {
		return Report{}, ErrInvalidResponse
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, s.maxResponseBytes+1))
	if err != nil || int64(len(body)) > s.maxResponseBytes {
		return Report{}, ErrInvalidResponse
	}
	report, err := s.decodeReport(body)
	if err != nil {
		return Report{}, err
	}
	return report, nil
}

type wireResponse struct {
	SchemaVersion string             `json:"schemaVersion"`
	SBOMs         []wireSBOM         `json:"sboms"`
	Licenses      []wireLicense      `json:"licenses"`
	Vulnerability *wireVulnerability `json:"vulnerability,omitempty"`
}

type wireSBOM struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	URL       string `json:"url,omitempty"`
	Size      int64  `json:"size,omitempty"`
}

type wireLicense struct {
	SPDXID string `json:"spdxId"`
	Name   string `json:"name"`
	Source string `json:"source,omitempty"`
}

type wireVulnerability struct {
	Status   string          `json:"status"`
	Critical int             `json:"critical"`
	High     int             `json:"high"`
	Medium   int             `json:"medium"`
	Low      int             `json:"low"`
	Unknown  int             `json:"unknown"`
	Findings json.RawMessage `json:"findings,omitempty"`
}

type wireVulnerabilityFinding struct {
	ID           string   `json:"id"`
	Source       string   `json:"source,omitempty"`
	Severity     string   `json:"severity"`
	Component    string   `json:"component"`
	Version      string   `json:"version,omitempty"`
	FixedVersion string   `json:"fixedVersion,omitempty"`
	Location     string   `json:"location,omitempty"`
	Title        string   `json:"title,omitempty"`
	Description  string   `json:"description,omitempty"`
	URL          string   `json:"url,omitempty"`
	CVSSScore    *float64 `json:"cvssScore,omitempty"`
	CVSSVector   string   `json:"cvssVector,omitempty"`
}

type wireHealthResponse struct {
	SchemaVersion string              `json:"schemaVersion"`
	Status        HealthStatus        `json:"status"`
	Version       string              `json:"version,omitempty"`
	Database      *wireHealthDatabase `json:"database,omitempty"`
}

type wireHealthDatabase struct {
	Version   string    `json:"version,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (s *HTTPScanner) decodeHealth(body []byte) (Health, error) {
	var response wireHealthResponse
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil || response.SchemaVersion != SchemaVersion {
		return Health{}, ErrInvalidResponse
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Health{}, ErrInvalidResponse
	}
	switch response.Status {
	case HealthHealthy, HealthDegraded, HealthUnhealthy:
	default:
		return Health{}, ErrInvalidResponse
	}
	if !validOptionalText(response.Version, 128) {
		return Health{}, ErrInvalidResponse
	}
	checkedAt := s.now()
	health := Health{Status: response.Status, Version: response.Version, CheckedAt: checkedAt}
	if response.Database != nil {
		if response.Database.UpdatedAt.IsZero() || response.Database.UpdatedAt.After(checkedAt.Add(5*time.Minute)) || !validOptionalText(response.Database.Version, 256) {
			return Health{}, ErrInvalidResponse
		}
		health.Database = &DatabaseHealth{Version: response.Database.Version, UpdatedAt: response.Database.UpdatedAt}
	}
	return health, nil
}

func (s *HTTPScanner) decodeReport(body []byte) (Report, error) {
	var response wireResponse
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil || response.SBOMs == nil || response.Licenses == nil {
		return Report{}, ErrInvalidResponse
	}
	switch response.SchemaVersion {
	case SchemaVersion:
		if response.Vulnerability != nil && response.Vulnerability.Findings != nil {
			return Report{}, ErrInvalidResponse
		}
	case ReportSchemaVersion:
	default:
		return Report{}, ErrInvalidResponse
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Report{}, ErrInvalidResponse
	}
	var findings []wireVulnerabilityFinding
	if response.Vulnerability != nil && response.Vulnerability.Findings != nil {
		var err error
		findings, err = decodeWireVulnerabilityFindings(response.Vulnerability.Findings)
		if err != nil {
			return Report{}, ErrInvalidResponse
		}
	}
	report := Report{
		SBOMs:    make([]repository.ArtifactSBOM, 0, len(response.SBOMs)),
		Licenses: make([]repository.ArtifactLicense, 0, len(response.Licenses)),
	}
	for _, value := range response.SBOMs {
		report.SBOMs = append(report.SBOMs, repository.ArtifactSBOM{
			MediaType: value.MediaType, Digest: value.Digest, URL: value.URL, Size: value.Size,
		})
	}
	for _, value := range response.Licenses {
		report.Licenses = append(report.Licenses, repository.ArtifactLicense{
			SPDXID: value.SPDXID, Name: value.Name, Source: value.Source,
		})
	}
	if response.Vulnerability != nil {
		report.Vulnerability = &repository.ArtifactVulnerabilitySummary{
			Scanner: s.name, ScannedAt: s.now(), Status: response.Vulnerability.Status,
			Critical: response.Vulnerability.Critical, High: response.Vulnerability.High,
			Medium: response.Vulnerability.Medium, Low: response.Vulnerability.Low,
			Unknown: response.Vulnerability.Unknown,
		}
		if response.Vulnerability.Findings != nil {
			report.Vulnerability.Findings = make([]repository.ArtifactVulnerabilityFinding, 0, len(findings))
			for _, value := range findings {
				report.Vulnerability.Findings = append(report.Vulnerability.Findings, repository.ArtifactVulnerabilityFinding{
					ID: value.ID, Source: value.Source, Severity: repository.ArtifactVulnerabilitySeverity(value.Severity), Component: value.Component,
					Version: value.Version, FixedVersion: value.FixedVersion, Location: value.Location,
					Title: value.Title, Description: value.Description, URL: value.URL,
					CVSSScore: value.CVSSScore, CVSSVector: value.CVSSVector,
				})
			}
		}
	}
	if !validReport(report) {
		return Report{}, ErrInvalidResponse
	}
	return report, nil
}

func decodeWireVulnerabilityFindings(raw json.RawMessage) ([]wireVulnerabilityFinding, error) {
	var findings []wireVulnerabilityFinding
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&findings); err != nil || findings == nil {
		return nil, ErrInvalidResponse
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, ErrInvalidResponse
	}
	return findings, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrInvalidResponse
	}
	return nil
}
