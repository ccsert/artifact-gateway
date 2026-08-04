// Package evidence collects a redacted release record from existing Gateway
// endpoints. It never changes Gateway state.
package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Options struct {
	GatewayURL string
	Token      string
	OutputDir  string
	Revision   string
	Image      string
}

type Manifest struct {
	SchemaVersion string    `json:"schema_version"`
	CollectedAt   time.Time `json:"collected_at"`
	TargetSHA256  string    `json:"target_sha256"`
	Revision      string    `json:"revision,omitempty"`
	Image         string    `json:"image,omitempty"`
	Endpoints     []Record  `json:"endpoints"`
}

type Record struct {
	Name       string `json:"name"`
	StatusCode int    `json:"status_code"`
	File       string `json:"file"`
}

type Collector struct{ Client *http.Client }

func (c Collector) Collect(ctx context.Context, options Options) (Manifest, error) {
	base, err := validateOptions(options)
	if err != nil {
		return Manifest{}, err
	}
	if err := prepareOutput(options.OutputDir); err != nil {
		return Manifest{}, err
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	manifest := Manifest{SchemaVersion: "v1", CollectedAt: time.Now().UTC(), TargetSHA256: hash(base.String()), Revision: options.Revision, Image: options.Image}
	for _, endpoint := range []struct {
		name, path string
		redact     func([]byte) ([]byte, error)
	}{
		{"readyz", "/readyz", redactReady},
		{"metrics", "/metrics", redactMetrics},
		{"audits", "/api/v1/audits?limit=500", redactAudits},
		{"cache_operations", "/api/v1/operations/cache", redactCache},
	} {
		status, body, err := fetch(ctx, client, base, endpoint.path, options.Token)
		if err != nil {
			return Manifest{}, fmt.Errorf("collect %s: %w", endpoint.name, err)
		}
		if status < 200 || status >= 300 {
			return Manifest{}, fmt.Errorf("collect %s: endpoint returned HTTP %d", endpoint.name, status)
		}
		redacted, err := endpoint.redact(body)
		if err != nil {
			return Manifest{}, fmt.Errorf("redact %s: %w", endpoint.name, err)
		}
		file := endpoint.name + ".json"
		if err := writeJSON(filepath.Join(options.OutputDir, file), json.RawMessage(redacted)); err != nil {
			return Manifest{}, err
		}
		manifest.Endpoints = append(manifest.Endpoints, Record{Name: endpoint.name, StatusCode: status, File: file})
	}
	if err := writeJSON(filepath.Join(options.OutputDir, "manifest.json"), manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func validateOptions(options Options) (*url.URL, error) {
	if strings.TrimSpace(options.GatewayURL) == "" || strings.TrimSpace(options.Token) == "" || strings.TrimSpace(options.OutputDir) == "" {
		return nil, fmt.Errorf("gateway URL, administrator token, and output directory are required")
	}
	base, err := url.Parse(options.GatewayURL)
	if err != nil || base.Scheme == "" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") {
		return nil, fmt.Errorf("gateway URL must be a root absolute URL without credentials, query, or fragment")
	}
	base.Path = strings.TrimRight(base.Path, "/")
	return base, nil
}

func prepareOutput(dir string) error {
	entries, err := os.ReadDir(dir)
	if err == nil && len(entries) > 0 {
		return fmt.Errorf("evidence output directory must be empty")
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.MkdirAll(dir, 0o750)
}

func fetch(ctx context.Context, client *http.Client, base *url.URL, path, token string) (int, []byte, error) {
	endpoint, err := base.Parse(path)
	if err != nil {
		return 0, nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	return response.StatusCode, body, err
}

func redactReady(_ []byte) ([]byte, error) { return json.Marshal(map[string]bool{"ready": true}) }

func redactMetrics(body []byte) ([]byte, error) {
	allowed := map[string]bool{
		"artifact_gateway_cache_quota_rejections_total":           true,
		"artifact_gateway_anonymous_reads_total":                  true,
		"artifact_gateway_repository_authorization_denials_total": true,
		"artifact_gateway_resolver_requests_total":                true,
		"artifact_gateway_oci_cache_requests_total":               true,
		"artifact_gateway_maven_cache_requests_total":             true,
		"artifact_gateway_raw_cache_requests_total":               true,
		"artifact_gateway_conan_cache_requests_total":             true,
	}
	values := make(map[string]float64)
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		name := strings.Split(fields[0], "{")[0]
		if !allowed[name] {
			continue
		}
		value, err := strconv.ParseFloat(fields[1], 64)
		if err == nil {
			values[name] += value
		}
	}
	return json.Marshal(map[string]any{"metrics": values})
}

func redactAudits(body []byte) ([]byte, error) {
	var records []map[string]json.RawMessage
	if err := json.Unmarshal(body, &records); err != nil {
		return nil, err
	}
	counts := make(map[string]int)
	for _, record := range records {
		outcome := stringField(record, "Outcome", "outcome")
		format := stringField(record, "Format", "format")
		if outcome != "" {
			counts["outcome:"+outcome]++
		}
		if format != "" {
			counts["format:"+format]++
		}
	}
	return json.Marshal(map[string]any{"record_count": len(records), "counts": counts})
}

func stringField(record map[string]json.RawMessage, names ...string) string {
	for _, name := range names {
		var value string
		if json.Unmarshal(record[name], &value) == nil {
			return value
		}
	}
	return ""
}

func redactCache(body []byte) ([]byte, error) {
	var status struct {
		ObjectCount       int64     `json:"object_count"`
		Bytes             int64     `json:"bytes"`
		PendingCandidates int64     `json:"pending_candidates"`
		LastStartedAt     time.Time `json:"last_started_at"`
		LastCompletedAt   time.Time `json:"last_completed_at"`
		SuccessfulRuns    uint64    `json:"successful_runs"`
		FailedRuns        uint64    `json:"failed_runs"`
	}
	if err := json.Unmarshal(body, &status); err != nil {
		return nil, err
	}
	return json.Marshal(status)
}

func writeJSON(path string, value any) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
