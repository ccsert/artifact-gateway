package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/authorization"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestArtifactIntelligenceManagementHTTP(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "22222222-2222-2222-2222-222222222222", Name: "public-oci", Format: repository.FormatOCI, AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("b", 64)
	if _, err := store.PutOCIManifest(context.Background(), repository.OCIManifest{
		RepositoryID: repo.ID, Name: "library/widget", Digest: digest,
		ObjectKey: "oci/library/widget", MediaType: "application/vnd.oci.image.manifest.v1+json", Size: 128,
	}, "latest"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceAnonymousAccessPolicy(context.Background(), repository.AnonymousAccessPolicy{Enabled: true}, "1"); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := func(method, path, body, token, ifMatch string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			authorize(r, token)
		}
		if ifMatch != "" {
			r.Header.Set("If-Match", ifMatch)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	path := "/api/v2/repositories/" + repo.ID + "/artifact-intelligence?coordinate=library/widget&digest=" + digest
	created := request(http.MethodPut, path, `{"signatures":[],"sboms":[],"licenses":[{"spdxId":"MIT","name":"MIT License"}],"vulnerability":{"scanner":"grype","status":"affected","critical":1,"high":0,"medium":0,"low":0,"unknown":0,"findings":[{"id":"CVE-2026-1234","source":"nvd","severity":"critical","component":"pkg:oci/library/widget","version":"1.0.0","fixedVersion":"1.0.1","location":"usr/lib/widget.so","title":"Example remote code execution","description":"A crafted payload can execute code.","url":"https://security.example.test/CVE-2026-1234","cvssScore":9.8,"cvssVector":"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"}]}}`, "admin-secret", "")
	if created.Code != http.StatusOK || created.Header().Get("ETag") != "1" {
		t.Fatalf("create=%d etag=%q body=%s", created.Code, created.Header().Get("ETag"), created.Body.String())
	}
	read := request(http.MethodGet, path, "", "", "")
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), "MIT License") || !strings.Contains(read.Body.String(), "CVE-2026-1234") || !strings.Contains(read.Body.String(), `"fixedVersion":"1.0.1"`) {
		t.Fatalf("anonymous read=%d body=%s", read.Code, read.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(read.Body.Bytes(), &response); err != nil || response["version"] != "1" {
		t.Fatalf("response=%#v err=%v", response, err)
	}
	audits, err := store.ListAudits(context.Background(), repository.AuditQuery{Repository: repo.Name, Operation: "artifact.intelligence.replace"})
	if err != nil || len(audits) != 1 {
		t.Fatalf("intelligence audits=%#v err=%v", audits, err)
	}
	if audit := audits[0]; audit.Actor != "alice" || audit.Format != "oci" || audit.Resource != "library/widget" || audit.Representation != digest || audit.Outcome != repository.AuditResolved || audit.Status != http.StatusOK {
		t.Fatalf("intelligence audit=%#v", audit)
	}
	stale := request(http.MethodPut, path, `{"signatures":[],"sboms":[],"licenses":[],"vulnerability":{"scanner":"grype","status":"affected","critical":1,"high":0,"medium":0,"low":0,"unknown":0}}`, "admin-secret", "0")
	if stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale=%d body=%s", stale.Code, stale.Body.String())
	}
	invalid := request(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/artifact-intelligence?coordinate=x&digest=bad", "", "", "")
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid=%d body=%s", invalid.Code, invalid.Body.String())
	}
}

func TestArtifactIntelligenceScopeIsNarrowerThanRepositoryWrite(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "33333333-3333-3333-3333-333333333333", Name: "scoped-oci", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("c", 64)
	if _, err := store.PutOCIManifest(context.Background(), repository.OCIManifest{
		RepositoryID: repo.ID, Name: "library/scoped", Digest: digest,
		ObjectKey: "oci/library/scoped", MediaType: "application/vnd.oci.image.manifest.v1+json", Size: 128,
	}, "latest"); err != nil {
		t.Fatal(err)
	}
	keyToken := "scanner-key"
	key, err := store.CreateAPIKey(context.Background(), repository.APIKey{ID: "44444444-4444-4444-4444-444444444444", Name: "scanner", SecretHash: authorization.HashAPIKey(keyToken), Roles: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(context.Background(), repo.ID, []repository.RepositoryGrant{{Principal: "api-key:" + key.ID, Scopes: []string{"repositories:intelligence"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	auth := testAuthenticator()
	auth.APIKeys = store
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, auth)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		authorize(r, keyToken)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	path := "/api/v2/repositories/" + repo.ID + "/artifact-intelligence?coordinate=library/scoped&digest=" + digest
	if response := request(http.MethodPut, path, `{"signatures":[],"sboms":[],"licenses":[]}`); response.Code != http.StatusOK {
		t.Fatalf("intelligence write=%d body=%s", response.Code, response.Body.String())
	}
	artifacts := request(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/artifacts", "")
	if artifacts.Code != http.StatusForbidden {
		t.Fatalf("artifact browse=%d body=%s", artifacts.Code, artifacts.Body.String())
	}
}

func TestArtifactIntelligenceRejectsUnknownArtifact(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "55555555-5555-5555-5555-555555555555", Name: "unknown-oci", Format: repository.FormatOCI,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	digest := "sha256:" + strings.Repeat("d", 64)
	request := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/artifact-intelligence?coordinate=library/missing&digest="+digest, strings.NewReader(`{"signatures":[],"sboms":[],"licenses":[]}`))
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "artifact for intelligence metadata not found") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestArtifactIntelligenceManagementHTTPRejectsInvalidPayloads(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "66666666-6666-6666-6666-666666666666", Name: "validation-oci", Format: repository.FormatOCI,
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("e", 64)
	if _, err := store.PutOCIManifest(context.Background(), repository.OCIManifest{
		RepositoryID: repo.ID, Name: "library/validation", Digest: digest,
		ObjectKey: "oci/library/validation", MediaType: "application/vnd.oci.image.manifest.v1+json", Size: 128,
	}, "latest"); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	path := "/api/v2/repositories/" + repo.ID + "/artifact-intelligence?coordinate=library/validation&digest=" + digest
	request := func(payload adminopenapi.ArtifactIntelligenceWritable) *httptest.ResponseRecorder {
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		httpRequest := httptest.NewRequest(http.MethodPut, path, strings.NewReader(string(body)))
		authorize(httpRequest, "admin-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httpRequest)
		return response
	}

	source := "nvd"
	findingURL := "https://security.example.test/CVE-2026-1234"
	score := 9.8
	findings := []adminopenapi.ArtifactVulnerabilityFinding{{
		Id: "CVE-2026-1234", Source: &source, Severity: adminopenapi.ArtifactVulnerabilityFindingSeverityCritical,
		Component: "pkg:oci/library/widget@1.0.0", Url: &findingURL, CvssScore: &score,
	}}
	valid := adminopenapi.ArtifactIntelligenceWritable{
		Signatures: []adminopenapi.ArtifactSignature{{KeyId: "key-1", Algorithm: "cosign", Identity: "ci@example.test", Signature: "signature", Verified: true}},
		Sboms:      []adminopenapi.ArtifactSBOM{{MediaType: "application/spdx+json", Digest: "sha256:" + strings.Repeat("a", 64)}},
		Provenance: &adminopenapi.ArtifactProvenance{Builder: "github-actions", BuildType: "https://slsa.dev/provenance/v1", SourceRepository: "https://github.com/example/project", SourceCommit: strings.Repeat("b", 40), BuildId: "run-42"},
		Licenses:   []adminopenapi.ArtifactLicense{{SpdxId: "MIT", Name: "MIT License"}},
		Vulnerability: &adminopenapi.ArtifactVulnerabilitySummary{
			Scanner: "grype", Status: adminopenapi.ArtifactVulnerabilitySummaryStatusAffected, Critical: 1, Findings: &findings,
		},
	}
	cases := []struct {
		name   string
		mutate func(*adminopenapi.ArtifactIntelligenceWritable)
	}{
		{name: "empty signature key", mutate: func(value *adminopenapi.ArtifactIntelligenceWritable) { value.Signatures[0].KeyId = " " }},
		{name: "invalid sbom digest", mutate: func(value *adminopenapi.ArtifactIntelligenceWritable) {
			value.Sboms[0].Digest = "sha1:" + strings.Repeat("a", 40)
		}},
		{name: "negative sbom size", mutate: func(value *adminopenapi.ArtifactIntelligenceWritable) { size := int64(-1); value.Sboms[0].Size = &size }},
		{name: "malformed sbom url", mutate: func(value *adminopenapi.ArtifactIntelligenceWritable) { url := "not a url"; value.Sboms[0].Url = &url }},
		{name: "empty provenance builder", mutate: func(value *adminopenapi.ArtifactIntelligenceWritable) { value.Provenance.Builder = "" }},
		{name: "empty scanner", mutate: func(value *adminopenapi.ArtifactIntelligenceWritable) { value.Vulnerability.Scanner = "" }},
		{name: "finding count mismatch", mutate: func(value *adminopenapi.ArtifactIntelligenceWritable) { value.Vulnerability.High = 1 }},
		{name: "affected with empty findings", mutate: func(value *adminopenapi.ArtifactIntelligenceWritable) {
			empty := []adminopenapi.ArtifactVulnerabilityFinding{}
			value.Vulnerability.Findings = &empty
		}},
		{name: "blank finding component", mutate: func(value *adminopenapi.ArtifactIntelligenceWritable) {
			(*value.Vulnerability.Findings)[0].Component = " "
		}},
		{name: "newline finding id", mutate: func(value *adminopenapi.ArtifactIntelligenceWritable) {
			(*value.Vulnerability.Findings)[0].Id = "CVE-2026-1234\nforged"
		}},
		{name: "invalid finding url", mutate: func(value *adminopenapi.ArtifactIntelligenceWritable) {
			invalid := "file:///tmp/report"
			(*value.Vulnerability.Findings)[0].Url = &invalid
		}},
		{name: "invalid cvss score", mutate: func(value *adminopenapi.ArtifactIntelligenceWritable) {
			invalid := 10.1
			(*value.Vulnerability.Findings)[0].CvssScore = &invalid
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			candidate := valid
			candidate.Signatures = append([]adminopenapi.ArtifactSignature{}, valid.Signatures...)
			candidate.Sboms = append([]adminopenapi.ArtifactSBOM{}, valid.Sboms...)
			candidate.Licenses = append([]adminopenapi.ArtifactLicense{}, valid.Licenses...)
			if valid.Provenance != nil {
				provenance := *valid.Provenance
				candidate.Provenance = &provenance
			}
			if valid.Vulnerability != nil {
				vulnerability := *valid.Vulnerability
				if valid.Vulnerability.Findings != nil {
					findings := append([]adminopenapi.ArtifactVulnerabilityFinding(nil), (*valid.Vulnerability.Findings)...)
					vulnerability.Findings = &findings
				}
				candidate.Vulnerability = &vulnerability
			}
			testCase.mutate(&candidate)
			response := request(candidate)
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_request"`) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	if response := request(valid); response.Code != http.StatusOK {
		t.Fatalf("valid status=%d body=%s", response.Code, response.Body.String())
	}
}
