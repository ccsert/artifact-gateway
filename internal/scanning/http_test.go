package scanning

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestHTTPScannerStreamsVerifiedArtifactAndReturnsBoundedReport(t *testing.T) {
	assets := map[string][]byte{
		"artifact.jar": []byte("jar-content"),
		"artifact.pom": []byte("pom-content"),
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.Header.Get("Authorization") != "Bearer scanner-token" || request.Header.Get("X-Artifact-Scanner-Schema") != SchemaVersion {
			t.Fatalf("request method=%s authorization=%q schema=%q", request.Method, request.Header.Get("Authorization"), request.Header.Get("X-Artifact-Scanner-Schema"))
		}
		reader, err := request.MultipartReader()
		if err != nil {
			t.Fatal(err)
		}
		metadataPart, err := reader.NextPart()
		if err != nil || metadataPart.FormName() != "metadata" {
			t.Fatalf("metadata part=%v err=%v", metadataPart, err)
		}
		var metadata wireRequest
		if err = json.NewDecoder(metadataPart).Decode(&metadata); err != nil {
			t.Fatal(err)
		}
		if metadata.SchemaVersion != SchemaVersion || metadata.RepositoryID != "repo-1" || metadata.Format != "maven" || metadata.Coordinate != "org.example:demo:1.0.0" || len(metadata.Assets) != 2 {
			t.Fatalf("metadata=%#v", metadata)
		}
		for _, expected := range metadata.Assets {
			part, partErr := reader.NextPart()
			if partErr != nil {
				t.Fatal(partErr)
			}
			content, readErr := io.ReadAll(part)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if part.FormName() != expected.Part || !bytes.Equal(content, assets[expected.Path]) {
				t.Fatalf("part=%q path=%q content=%q", part.FormName(), expected.Path, content)
			}
		}
		if _, err = reader.NextPart(); !errors.Is(err, io.EOF) {
			t.Fatalf("multipart trailing part error=%v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"schemaVersion":"v1","sboms":[{"mediaType":"application/spdx+json","digest":"`+digestOf([]byte("sbom"))+`","url":"https://scanner.example.test/sboms/1","size":4}],"licenses":[{"spdxId":"Apache-2.0","name":"Apache License 2.0","source":"manifest"}],"vulnerability":{"status":"affected","critical":1,"high":0,"medium":0,"low":0,"unknown":0,"findings":[{"id":"CVE-2026-1234","source":"nvd","severity":"critical","component":"pkg:maven/org.example/widget","version":"1.2.3","fixedVersion":"1.2.4","location":"widget-1.2.3.jar","title":"Example remote code execution","description":"A crafted payload can execute code.","url":"https://scanner.example.test/vulnerabilities/CVE-2026-1234","cvssScore":9.8,"cvssVector":"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"}]}}`)
	}))
	defer server.Close()

	scanner, err := NewHTTPScanner(HTTPOptions{Name: "trivy", Endpoint: server.URL, Token: "scanner-token"})
	if err != nil {
		t.Fatal(err)
	}
	fixedTime := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	scanner.now = func() time.Time { return fixedTime }
	report, err := scanner.Scan(context.Background(), testArtifact(assets))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.SBOMs) != 1 || report.SBOMs[0].Size != 4 || len(report.Licenses) != 1 || report.Licenses[0].SPDXID != "Apache-2.0" {
		t.Fatalf("report=%#v", report)
	}
	if report.Vulnerability == nil || report.Vulnerability.Scanner != "trivy" || report.Vulnerability.ScannedAt != fixedTime || report.Vulnerability.Critical != 1 || report.Vulnerability.Unknown != 0 {
		t.Fatalf("vulnerability=%#v", report.Vulnerability)
	}
	if len(report.Vulnerability.Findings) != 1 {
		t.Fatalf("findings=%#v", report.Vulnerability.Findings)
	}
	finding := report.Vulnerability.Findings[0]
	if finding.ID != "CVE-2026-1234" || finding.Severity != "critical" || finding.Component != "pkg:maven/org.example/widget" || finding.FixedVersion != "1.2.4" || finding.CVSSScore == nil || *finding.CVSSScore != 9.8 {
		t.Fatalf("finding=%#v", finding)
	}
}

func TestHTTPScannerRejectsInvalidInputBeforeNetworkRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	scanner, err := NewHTTPScanner(HTTPOptions{Name: "scanner", Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	artifact := testArtifact(map[string][]byte{"artifact.jar": []byte("body")})
	artifact.Assets[0].Digest = "sha256:invalid"
	if _, err = scanner.Scan(context.Background(), artifact); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("error=%v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("requests=%d", requests.Load())
	}
}

func TestHTTPScannerDetectsStreamedAssetIntegrityFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"schemaVersion":"v1","sboms":[],"licenses":[]}`)
	}))
	defer server.Close()
	scanner, err := NewHTTPScanner(HTTPOptions{Name: "scanner", Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	declared := []byte("expected")
	artifact := testArtifact(map[string][]byte{"artifact.jar": declared})
	artifact.Assets[0].Open = func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("tampered")), nil
	}
	if _, err = scanner.Scan(context.Background(), artifact); !errors.Is(err, ErrAssetIntegrity) {
		t.Fatalf("error=%v", err)
	}
}

func TestHTTPScannerRejectsOversizedOrMalformedResponse(t *testing.T) {
	for _, testCase := range []struct {
		name string
		body string
	}{
		{name: "oversized", body: strings.Repeat("x", 2048)},
		{name: "missing required arrays", body: `{"schemaVersion":"v1"}`},
		{name: "unknown field", body: `{"schemaVersion":"v1","sboms":[],"licenses":[],"unexpected":true}`},
		{name: "negative vulnerability count", body: `{"schemaVersion":"v1","sboms":[],"licenses":[],"vulnerability":{"status":"affected","critical":-1}}`},
		{name: "affected without findings or counts", body: `{"schemaVersion":"v1","sboms":[],"licenses":[],"vulnerability":{"status":"affected","critical":0,"high":0,"medium":0,"low":0,"unknown":0}}`},
		{name: "affected with empty findings", body: `{"schemaVersion":"v1","sboms":[],"licenses":[],"vulnerability":{"status":"affected","critical":0,"high":1,"medium":0,"low":0,"unknown":0,"findings":[]}}`},
		{name: "clean with count", body: `{"schemaVersion":"v1","sboms":[],"licenses":[],"vulnerability":{"status":"clean","critical":0,"high":0,"medium":0,"low":1,"unknown":0}}`},
		{name: "finding count mismatch", body: `{"schemaVersion":"v1","sboms":[],"licenses":[],"vulnerability":{"status":"affected","critical":0,"high":1,"medium":0,"low":0,"unknown":0,"findings":[{"id":"CVE-2026-1234","severity":"critical","component":"pkg:generic/widget@1"}]}}`},
		{name: "invalid finding severity", body: `{"schemaVersion":"v1","sboms":[],"licenses":[],"vulnerability":{"status":"affected","critical":0,"high":0,"medium":0,"low":0,"unknown":1,"findings":[{"id":"CVE-2026-1234","severity":"important","component":"pkg:generic/widget@1"}]}}`},
		{name: "invalid finding URL", body: `{"schemaVersion":"v1","sboms":[],"licenses":[],"vulnerability":{"status":"affected","critical":1,"high":0,"medium":0,"low":0,"unknown":0,"findings":[{"id":"CVE-2026-1234","severity":"critical","component":"pkg:generic/widget@1","url":"file:///tmp/report"}]}}`},
		{name: "invalid CVSS score", body: `{"schemaVersion":"v1","sboms":[],"licenses":[],"vulnerability":{"status":"affected","critical":1,"high":0,"medium":0,"low":0,"unknown":0,"findings":[{"id":"CVE-2026-1234","severity":"critical","component":"pkg:generic/widget@1","cvssScore":10.1}]}}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				_, _ = io.Copy(io.Discard, request.Body)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, testCase.body)
			}))
			defer server.Close()
			scanner, err := NewHTTPScanner(HTTPOptions{Name: "scanner", Endpoint: server.URL, MaxResponseBytes: 1024})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = scanner.Scan(context.Background(), testArtifact(map[string][]byte{"artifact.jar": []byte("body")})); !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestHTTPScannerDoesNotExposeErrorBodyOrFollowRedirect(t *testing.T) {
	secret := "scanner-internal-secret"
	redirected := atomic.Bool{}
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Store(true) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
		_, _ = io.WriteString(w, secret)
	}))
	defer server.Close()
	scanner, err := NewHTTPScanner(HTTPOptions{Name: "scanner", Endpoint: server.URL, Token: secret})
	if err != nil {
		t.Fatal(err)
	}
	_, err = scanner.Scan(context.Background(), testArtifact(map[string][]byte{"artifact.jar": []byte(strings.Repeat("body", 1<<18))}))
	if !errors.Is(err, ErrScannerUnavailable) || strings.Contains(err.Error(), secret) || redirected.Load() {
		t.Fatalf("error=%v redirected=%t", err, redirected.Load())
	}
}

func TestHTTPScannerHonorsCallerCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()
	scanner, err := NewHTTPScanner(HTTPOptions{Name: "scanner", Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = scanner.Scan(ctx, testArtifact(map[string][]byte{"artifact.jar": []byte("body")})); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

func TestHTTPScannerReportsHealthAndVulnerabilityDatabaseMetadata(t *testing.T) {
	updatedAt := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	checkedAt := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.Header.Get("Authorization") != "Bearer scanner-token" || request.Header.Get("X-Artifact-Scanner-Schema") != SchemaVersion {
			t.Fatalf("request method=%s authorization=%q schema=%q", request.Method, request.Header.Get("Authorization"), request.Header.Get("X-Artifact-Scanner-Schema"))
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = io.WriteString(w, `{"schemaVersion":"v1","status":"healthy","version":"0.61.0","database":{"version":"2026-08-10","updatedAt":"2026-08-10T06:00:00Z"}}`)
	}))
	defer server.Close()

	scanner, err := NewHTTPScanner(HTTPOptions{
		Name: "trivy", Endpoint: server.URL + "/scan", HealthEndpoint: server.URL + "/health", Token: "scanner-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	scanner.now = func() time.Time { return checkedAt }
	health, err := scanner.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if health.Status != HealthHealthy || health.Version != "0.61.0" || health.CheckedAt != checkedAt || health.Database == nil || health.Database.Version != "2026-08-10" || health.Database.UpdatedAt != updatedAt {
		t.Fatalf("health=%#v", health)
	}
}

func TestHTTPScannerHealthIsOptionalAndStrictlyValidated(t *testing.T) {
	scanner, err := NewHTTPScanner(HTTPOptions{Name: "scanner", Endpoint: "https://scanner.example.test/v1/scan"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = scanner.Health(context.Background()); !errors.Is(err, ErrHealthNotConfigured) {
		t.Fatalf("Health() error=%v, want ErrHealthNotConfigured", err)
	}

	for _, body := range []string{
		`{"schemaVersion":"v1","status":"unknown"}`,
		`{"schemaVersion":"v1","status":"healthy","database":{"updatedAt":"0001-01-01T00:00:00Z"}}`,
		`{"schemaVersion":"v1","status":"healthy","unexpected":true}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, body)
		}))
		scanner, scannerErr := NewHTTPScanner(HTTPOptions{Name: "scanner", Endpoint: server.URL + "/scan", HealthEndpoint: server.URL + "/health"})
		if scannerErr != nil {
			server.Close()
			t.Fatal(scannerErr)
		}
		if _, healthErr := scanner.Health(context.Background()); !errors.Is(healthErr, ErrInvalidResponse) {
			server.Close()
			t.Fatalf("body=%s error=%v", body, healthErr)
		}
		server.Close()
	}
}

func TestHTTPScannerEnforcesConfiguredTimeout(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release
	}))
	defer func() {
		close(release)
		server.Close()
	}()
	scanner, err := NewHTTPScanner(HTTPOptions{Name: "scanner", Endpoint: server.URL, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err = scanner.Scan(context.Background(), testArtifact(map[string][]byte{"artifact.jar": []byte("body")})); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v", err)
	}
	if elapsed := time.Since(started); elapsed > 1500*time.Millisecond {
		t.Fatalf("timeout elapsed=%s", elapsed)
	}
}

func TestNewHTTPScannerRequiresHTTPSOutsideLoopback(t *testing.T) {
	for _, endpoint := range []string{
		"http://scanner.example.test/v1/scan",
		"https://user:password@scanner.example.test/v1/scan",
		"https://scanner.example.test/v1/scan?token=secret",
	} {
		if _, err := NewHTTPScanner(HTTPOptions{Name: "scanner", Endpoint: endpoint}); !errors.Is(err, ErrInvalidConfiguration) {
			t.Fatalf("endpoint=%q error=%v", endpoint, err)
		}
	}
	if _, err := NewHTTPScanner(HTTPOptions{Name: "scanner", Endpoint: "https://scanner.example.test/v1/scan"}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewHTTPScanner(HTTPOptions{Name: "scanner", Endpoint: "https://scanner.example.test/v1/scan", HealthEndpoint: "http://scanner.example.test/health"}); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("insecure health endpoint error=%v", err)
	}
}

func testArtifact(contents map[string][]byte) Artifact {
	assets := make([]Asset, 0, len(contents))
	for path, content := range contents {
		body := append([]byte(nil), content...)
		assets = append(assets, Asset{
			Path: path, Digest: digestOf(body), Size: int64(len(body)), MediaType: "application/octet-stream",
			Open: func(context.Context) (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(body)), nil
			},
		})
	}
	return Artifact{
		RepositoryID: "repo-1", Format: repository.FormatMaven,
		Coordinate: "org.example:demo:1.0.0", Digest: digestOf([]byte("artifact-identity")), Assets: assets,
	}
}

func digestOf(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

var _ Scanner = ScannerFunc(func(context.Context, Artifact) (Report, error) { return Report{}, nil })
