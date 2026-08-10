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
	if _, err := store.ReplaceAnonymousAccessPolicy(context.Background(), repository.AnonymousAccessPolicy{Enabled: true}, "1"); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	digest := "sha256:" + strings.Repeat("b", 64)
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
	created := request(http.MethodPut, path, `{"signatures":[],"sboms":[],"licenses":[{"spdxId":"MIT","name":"MIT License"}],"vulnerability":{"scanner":"grype","status":"clean","critical":0,"high":0,"medium":0,"low":0,"unknown":0}}`, "admin-secret", "")
	if created.Code != http.StatusOK || created.Header().Get("ETag") != "1" {
		t.Fatalf("create=%d etag=%q body=%s", created.Code, created.Header().Get("ETag"), created.Body.String())
	}
	read := request(http.MethodGet, path, "", "", "")
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), "MIT License") {
		t.Fatalf("anonymous read=%d body=%s", read.Code, read.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(read.Body.Bytes(), &response); err != nil || response["version"] != "1" {
		t.Fatalf("response=%#v err=%v", response, err)
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
	digest := "sha256:" + strings.Repeat("c", 64)
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

func TestArtifactIntelligencePayloadValidation(t *testing.T) {
	valid := adminopenapi.ArtifactIntelligenceWritable{
		Signatures: []adminopenapi.ArtifactSignature{{KeyId: "key-1", Algorithm: "cosign", Identity: "ci@example.test", Signature: "signature", Verified: true}},
		Sboms:      []adminopenapi.ArtifactSBOM{{MediaType: "application/spdx+json", Digest: "sha256:" + strings.Repeat("a", 64)}},
		Provenance: &adminopenapi.ArtifactProvenance{Builder: "github-actions", BuildType: "https://slsa.dev/provenance/v1", SourceRepository: "https://github.com/example/project", SourceCommit: strings.Repeat("b", 40), BuildId: "run-42"},
		Licenses:   []adminopenapi.ArtifactLicense{{SpdxId: "MIT", Name: "MIT License"}},
		Vulnerability: &adminopenapi.ArtifactVulnerabilitySummary{
			Scanner: "grype", Status: adminopenapi.ArtifactVulnerabilitySummaryStatusClean,
		},
	}
	if !validArtifactIntelligencePayload(valid) {
		t.Fatal("expected valid intelligence payload")
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
				candidate.Vulnerability = &vulnerability
			}
			testCase.mutate(&candidate)
			if validArtifactIntelligencePayload(candidate) {
				t.Fatal("expected payload to be rejected")
			}
		})
	}
}
