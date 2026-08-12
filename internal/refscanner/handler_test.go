package refscanner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/scanning"
)

func TestHandlerAcceptsGatewayMultipartAndMapsTrivyReport(t *testing.T) {
	engine := &fakeEngine{
		scan: func(_ context.Context, root string) (EngineOutput, error) {
			jar, err := os.ReadFile(filepath.Join(root, "org", "example", "widget.jar"))
			if err != nil {
				t.Fatal(err)
			}
			if string(jar) != "jar-content" {
				t.Fatalf("asset content=%q", jar)
			}
			return EngineOutput{
				Report: []byte(`{
                  "SchemaVersion": 2,
                  "Results": [{
                    "Target": "org/example/widget.jar",
                    "Packages": [{"Name":"widget","Version":"1.2.3","Identifier":{"PURL":"pkg:maven/org.example/widget@1.2.3"},"Licenses":["Apache-2.0"]}],
                    "Licenses": [{"Name":"MIT","FilePath":"META-INF/LICENSE"}],
                    "Vulnerabilities": [{
                      "VulnerabilityID":"CVE-2026-1234",
                      "PkgName":"widget",
                      "PkgPath":"org/example/widget.jar",
                      "PkgIdentifier":{"PURL":"pkg:maven/org.example/widget@1.2.3"},
                      "InstalledVersion":"1.2.3",
                      "FixedVersion":"1.2.4",
                      "Severity":"HIGH",
                      "SeveritySource":"nvd",
                      "PrimaryURL":"https://avd.aquasec.com/nvd/cve-2026-1234",
                      "Title":"Example vulnerability",
                      "Description":"Upgrade the affected component.",
                      "CVSS":{"nvd":{"V3Vector":"CVSS:3.1/AV:N/AC:L","V3Score":8.1}}
                    }]
                  }]
                }`),
				SBOM: []byte(`{"bomFormat":"CycloneDX","specVersion":"1.6"}`),
			}, nil
		},
	}
	handler, err := NewHandler(Options{
		Token: "shared-secret", Engine: engine, MaxArtifactBytes: 1024,
		SBOMDir: t.TempDir(), SBOMBaseURL: "http://127.0.0.1:18082", MaxSBOMBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	client, err := scanning.NewHTTPScanner(scanning.HTTPOptions{
		Name: "trivy-reference", Endpoint: "http://127.0.0.1/v1/scan", Token: "shared-secret", MaxArtifactBytes: 1024,
		Client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			return response.Result(), nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("jar-content")
	report, err := client.Scan(context.Background(), scanning.Artifact{
		RepositoryID: "repo-1", Format: repository.FormatMaven,
		Coordinate: "org.example:widget:1.2.3", Digest: testDigest(content),
		Assets: []scanning.Asset{{
			Path: "org/example/widget.jar", Digest: testDigest(content), Size: int64(len(content)), MediaType: "application/java-archive",
			Open: func(context.Context) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(content)), nil },
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.SBOMs) != 1 || report.SBOMs[0].MediaType != "application/vnd.cyclonedx+json" || report.SBOMs[0].Digest != testDigest(engine.lastOutput.SBOM) || report.SBOMs[0].Size != int64(len(engine.lastOutput.SBOM)) || report.SBOMs[0].URL != "http://127.0.0.1:18082/v1/sboms/"+testDigest(engine.lastOutput.SBOM) {
		t.Fatalf("sboms=%#v", report.SBOMs)
	}
	sbomRequest := httptest.NewRequest(http.MethodGet, "/v1/sboms/"+testDigest(engine.lastOutput.SBOM), nil)
	sbomRequest.Header.Set("Authorization", "Bearer shared-secret")
	sbomResponse := httptest.NewRecorder()
	handler.ServeHTTP(sbomResponse, sbomRequest)
	if sbomResponse.Code != http.StatusOK || !bytes.Equal(sbomResponse.Body.Bytes(), engine.lastOutput.SBOM) || sbomResponse.Header().Get("ETag") != `"`+testDigest(engine.lastOutput.SBOM)+`"` {
		t.Fatalf("SBOM status=%d headers=%v body=%q", sbomResponse.Code, sbomResponse.Header(), sbomResponse.Body.Bytes())
	}
	if len(report.Licenses) != 2 || report.Licenses[0].SPDXID != "Apache-2.0" || report.Licenses[1].SPDXID != "MIT" {
		t.Fatalf("licenses=%#v", report.Licenses)
	}
	if report.Vulnerability == nil || report.Vulnerability.Status != "affected" || report.Vulnerability.High != 1 || len(report.Vulnerability.Findings) != 1 {
		t.Fatalf("vulnerability=%#v", report.Vulnerability)
	}
	finding := report.Vulnerability.Findings[0]
	if finding.ID != "CVE-2026-1234" || finding.Component != "pkg:maven/org.example/widget@1.2.3" || finding.CVSSScore == nil || *finding.CVSSScore != 8.1 {
		t.Fatalf("finding=%#v", finding)
	}
}

func TestHandlerRejectsUnauthorizedAndTamperedAssets(t *testing.T) {
	handler, err := NewHandler(Options{Token: "shared-secret", Engine: &fakeEngine{}, MaxArtifactBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name     string
		token    string
		path     string
		declared []byte
		actual   []byte
		wantCode int
	}{
		{name: "unauthorized", token: "wrong", path: "asset.jar", declared: []byte("body"), actual: []byte("body"), wantCode: http.StatusUnauthorized},
		{name: "path traversal", token: "shared-secret", path: "../escape.jar", declared: []byte("body"), actual: []byte("body"), wantCode: http.StatusBadRequest},
		{name: "digest mismatch", token: "shared-secret", path: "asset.jar", declared: []byte("expected"), actual: []byte("tampered"), wantCode: http.StatusBadRequest},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := multipartScanRequest(t, testCase.path, testCase.declared, testCase.actual)
			request.Header.Set("Authorization", "Bearer "+testCase.token)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != testCase.wantCode {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestHandlerNegotiatesLegacyV1WithoutDetailedFindings(t *testing.T) {
	engine := &fakeEngine{output: EngineOutput{
		Report: []byte(`{"SchemaVersion":2,"Results":[{"Target":"asset","Vulnerabilities":[{"VulnerabilityID":"CVE-1","PkgName":"widget","InstalledVersion":"1","Severity":"CRITICAL"}]}]}`),
		SBOM:   []byte(`{"bomFormat":"CycloneDX"}`),
	}}
	handler, err := NewHandler(Options{Engine: engine, MaxArtifactBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	request := multipartScanRequest(t, "asset.jar", []byte("body"), []byte("body"))
	request.Header.Del("X-Artifact-Scanner-Accept-Schema")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body map[string]any
	if err = json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["schemaVersion"] != "v1" {
		t.Fatalf("schemaVersion=%v", body["schemaVersion"])
	}
	vulnerability := body["vulnerability"].(map[string]any)
	if _, exists := vulnerability["findings"]; exists {
		t.Fatalf("legacy response contains findings: %#v", vulnerability)
	}
}

func TestHandlerFallsBackToCompleteSummaryWhenFindingsExceedResponseBound(t *testing.T) {
	vulnerabilities := make([]map[string]any, maximumDetailedFinding)
	for index := range vulnerabilities {
		vulnerabilities[index] = map[string]any{
			"VulnerabilityID": fmt.Sprintf("CVE-2026-%04d", index),
			"PkgName":         fmt.Sprintf("component-%04d", index), "Severity": "HIGH",
			"Description": strings.Repeat("bounded evidence ", 250),
		}
	}
	native, err := json.Marshal(map[string]any{
		"SchemaVersion": 2,
		"Results":       []map[string]any{{"Target": "artifact", "Vulnerabilities": vulnerabilities}},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(Options{Engine: &fakeEngine{output: EngineOutput{Report: native}}, MaxArtifactBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, multipartScanRequest(t, "asset.jar", []byte("body"), []byte("body")))
	if response.Code != http.StatusOK || response.Body.Len() > maximumGatewayReport {
		t.Fatalf("status=%d bytes=%d body=%s", response.Code, response.Body.Len(), response.Body.String())
	}
	var body struct {
		Vulnerability struct {
			High     int             `json:"high"`
			Findings json.RawMessage `json:"findings"`
		} `json:"vulnerability"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Vulnerability.High != maximumDetailedFinding || body.Vulnerability.Findings != nil {
		t.Fatalf("vulnerability=%#v", body.Vulnerability)
	}
}

func TestHandlerAggregatesLicenseSourcesAndRejectsMoreThanOneHundredUniqueLicenses(t *testing.T) {
	packages := make([]map[string]any, maximumLicenses+1)
	for index := range packages {
		license := fmt.Sprintf("License-%03d", index)
		packages[index] = map[string]any{
			"Name": fmt.Sprintf("component-%03d", index), "Version": "1.0.0", "Licenses": []string{license},
		}
	}
	packages[0]["Licenses"] = []string{"Apache-2.0"}
	packages[1]["Licenses"] = []string{"Apache-2.0"}
	native, err := json.Marshal(map[string]any{
		"SchemaVersion": 2,
		"Results":       []map[string]any{{"Target": "artifact", "Packages": packages}},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(Options{Engine: &fakeEngine{output: EngineOutput{Report: native}}, MaxArtifactBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, multipartScanRequest(t, "asset.jar", []byte("body"), []byte("body")))
	if response.Code != http.StatusOK {
		t.Fatalf("deduplicated status=%d body=%s", response.Code, response.Body.String())
	}
	var accepted struct {
		Licenses []scanLicense `json:"licenses"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	if len(accepted.Licenses) != maximumLicenses || accepted.Licenses[0].SPDXID != "Apache-2.0" {
		t.Fatalf("licenses count=%d first=%#v", len(accepted.Licenses), accepted.Licenses[0])
	}

	packages[1]["Licenses"] = []string{"Additional-License"}
	native, err = json.Marshal(map[string]any{
		"SchemaVersion": 2,
		"Results":       []map[string]any{{"Target": "artifact", "Packages": packages}},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err = NewHandler(Options{Engine: &fakeEngine{output: EngineOutput{Report: native}}, MaxArtifactBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	rejected := httptest.NewRecorder()
	handler.ServeHTTP(rejected, multipartScanRequest(t, "asset.jar", []byte("body"), []byte("body")))
	if rejected.Code != http.StatusServiceUnavailable || !strings.Contains(rejected.Body.String(), "scanner_invalid_result") {
		t.Fatalf("overflow status=%d body=%s", rejected.Code, rejected.Body.String())
	}
}

func TestHandlerBoundsConcurrentScansAndRejectsUnsupportedOCI(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	engine := &fakeEngine{scan: func(context.Context, string) (EngineOutput, error) {
		once.Do(func() { close(started) })
		<-release
		return EngineOutput{Report: []byte(`{"SchemaVersion":2,"Results":[]}`)}, nil
	}}
	handler, err := NewHandler(Options{Engine: engine, MaxArtifactBytes: 1024, MaxConcurrent: 1})
	if err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	firstRequest := multipartScanRequest(t, "asset.jar", []byte("body"), []byte("body"))
	go func() {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, firstRequest)
		firstDone <- response
	}()
	<-started
	busy := httptest.NewRecorder()
	handler.ServeHTTP(busy, multipartScanRequest(t, "other.jar", []byte("body"), []byte("body")))
	if busy.Code != http.StatusTooManyRequests || busy.Header().Get("Retry-After") != "5" {
		t.Fatalf("busy status=%d headers=%v body=%s", busy.Code, busy.Header(), busy.Body.String())
	}
	close(release)
	if response := <-firstDone; response.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", response.Code, response.Body.String())
	}

	oci := httptest.NewRecorder()
	handler.ServeHTTP(oci, multipartScanRequestForFormat(t, "oci", "manifest", []byte("body"), []byte("body")))
	if oci.Code != http.StatusBadRequest {
		t.Fatalf("OCI status=%d body=%s", oci.Code, oci.Body.String())
	}
}

func TestHandlerKeepsExistingSBOMWhenStoreCapacityIsFull(t *testing.T) {
	var calls atomic.Int32
	firstSBOM := []byte(`{"bomFormat":"CycloneDX","serialNumber":"first"}`)
	secondSBOM := []byte(`{"bomFormat":"CycloneDX","serialNumber":"second"}`)
	engine := &fakeEngine{scan: func(context.Context, string) (EngineOutput, error) {
		sbom := firstSBOM
		if calls.Add(1) > 1 {
			sbom = secondSBOM
		}
		return EngineOutput{Report: []byte(`{"SchemaVersion":2,"Results":[]}`), SBOM: sbom}, nil
	}}
	handler, err := NewHandler(Options{
		Engine: engine, MaxArtifactBytes: 1024, SBOMDir: t.TempDir(),
		SBOMBaseURL: "http://127.0.0.1:18082", MaxSBOMBytes: int64(len(firstSBOM)),
	})
	if err != nil {
		t.Fatal(err)
	}
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, multipartScanRequest(t, "first.jar", []byte("body"), []byte("body")))
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, multipartScanRequest(t, "second.jar", []byte("body"), []byte("body")))
	if second.Code != http.StatusServiceUnavailable {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
	}
	retrieve := httptest.NewRecorder()
	handler.ServeHTTP(retrieve, httptest.NewRequest(http.MethodGet, "/v1/sboms/"+testDigest(firstSBOM), nil))
	if retrieve.Code != http.StatusOK || !bytes.Equal(retrieve.Body.Bytes(), firstSBOM) {
		t.Fatalf("retrieve status=%d body=%q", retrieve.Code, retrieve.Body.Bytes())
	}
}

func TestHandlerReportsEngineHealth(t *testing.T) {
	updatedAt := time.Date(2026, 8, 12, 2, 30, 0, 0, time.UTC)
	engine := &fakeEngine{health: EngineHealth{Version: "0.70.0", DatabaseVersion: "2", DatabaseUpdatedAt: updatedAt}}
	handler, err := NewHandler(Options{Token: "shared-secret", Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	request.Header.Set("Authorization", "Bearer shared-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status=%d content-type=%q body=%s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
	var body struct {
		SchemaVersion string `json:"schemaVersion"`
		Status        string `json:"status"`
		Version       string `json:"version"`
		Database      struct {
			Version   string    `json:"version"`
			UpdatedAt time.Time `json:"updatedAt"`
		} `json:"database"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.SchemaVersion != "v1" || body.Status != "healthy" || body.Version != "0.70.0" || body.Database.Version != "2" || body.Database.UpdatedAt != updatedAt {
		t.Fatalf("health=%#v", body)
	}
}

type fakeEngine struct {
	scan       func(context.Context, string) (EngineOutput, error)
	output     EngineOutput
	lastOutput EngineOutput
	health     EngineHealth
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func (e *fakeEngine) Scan(ctx context.Context, root string) (EngineOutput, error) {
	var output EngineOutput
	var err error
	if e.scan != nil {
		output, err = e.scan(ctx, root)
	} else {
		output = e.output
	}
	e.lastOutput = output
	return output, err
}

func (e *fakeEngine) Health(context.Context) (EngineHealth, error) {
	return e.health, nil
}

func multipartScanRequest(t *testing.T, assetPath string, declared, actual []byte) *http.Request {
	return multipartScanRequestForFormat(t, "maven", assetPath, declared, actual)
}

func multipartScanRequestForFormat(t *testing.T, format, assetPath string, declared, actual []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	metadataHeader := make(textproto.MIMEHeader)
	metadataHeader.Set("Content-Disposition", `form-data; name="metadata"`)
	metadataHeader.Set("Content-Type", "application/json")
	part, err := writer.CreatePart(metadataHeader)
	if err != nil {
		t.Fatal(err)
	}
	metadata := map[string]any{
		"schemaVersion": "v1", "repositoryId": "repo-1", "format": format,
		"coordinate": "org.example:widget:1.2.3", "digest": testDigest(declared),
		"assets": []map[string]any{{"part": "asset-0", "path": assetPath, "digest": testDigest(declared), "size": len(declared), "mediaType": "application/java-archive"}},
	}
	if err = json.NewEncoder(part).Encode(metadata); err != nil {
		t.Fatal(err)
	}
	assetHeader := make(textproto.MIMEHeader)
	assetHeader.Set("Content-Disposition", `form-data; name="asset-0"; filename="asset.jar"`)
	assetHeader.Set("Content-Type", "application/java-archive")
	part, err = writer.CreatePart(assetHeader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(actual); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/scan", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("X-Artifact-Scanner-Schema", "v1")
	request.Header.Set("X-Artifact-Scanner-Accept-Schema", "v2, v1")
	return request
}

func testDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}
