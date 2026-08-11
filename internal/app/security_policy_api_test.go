package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestRepositorySecurityPolicyManagementAndEvaluationHTTP(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	source, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "security-source", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "security-target", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	coordinate := "releases/widget.txt"
	if _, err = store.PutRawAsset(ctx, repository.RawAsset{RepositoryID: source.ID, Path: coordinate, Digest: digest, ObjectKey: "raw/security-widget", Size: 7}); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := func(method, path, body, ifMatch, token string) *httptest.ResponseRecorder {
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
	policyPath := "/api/v2/repositories/" + target.ID + "/security-policy"
	if anonymous := request(http.MethodGet, policyPath, "", "", ""); anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous policy read=%d body=%s", anonymous.Code, anonymous.Body.String())
	}
	initial := request(http.MethodGet, policyPath, "", "", "admin-secret")
	if initial.Code != http.StatusOK || initial.Header().Get("ETag") != "1" || !strings.Contains(initial.Body.String(), `"version":"1"`) || !strings.Contains(initial.Body.String(), `"enabled":false`) {
		t.Fatalf("initial=%d body=%s", initial.Code, initial.Body.String())
	}
	body := `{"version":"1","enabled":true,"autoScanOnPublish":true,"requireSbom":true,"maxAllowedSeverity":"high","failOnScanError":true,"allowedLicenses":[" MIT ","mit"]}`
	updated := request(http.MethodPut, policyPath, body, "1", "admin-secret")
	if updated.Code != http.StatusOK || updated.Header().Get("ETag") != "2" || !strings.Contains(updated.Body.String(), `"version":"2"`) || !strings.Contains(updated.Body.String(), `"autoScanOnPublish":true`) || !strings.Contains(updated.Body.String(), `"allowedLicenses":["MIT"]`) {
		t.Fatalf("updated=%d body=%s", updated.Code, updated.Body.String())
	}
	if stale := request(http.MethodPut, policyPath, body, "1", "admin-secret"); stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale=%d body=%s", stale.Code, stale.Body.String())
	}
	evaluatePath := "/api/v2/repositories/" + target.ID + "/security-policy:evaluate"
	evaluateBody := `{"sourceRepositoryId":"` + source.ID + `","coordinate":"` + coordinate + `","digest":"` + digest + `"}`
	missing := request(http.MethodPost, evaluatePath, evaluateBody, "", "admin-secret")
	if missing.Code != http.StatusOK || !strings.Contains(missing.Body.String(), `"allowed":false`) || !strings.Contains(missing.Body.String(), `"sbom_required"`) || !strings.Contains(missing.Body.String(), `"license_required"`) {
		t.Fatalf("missing intelligence=%d body=%s", missing.Code, missing.Body.String())
	}
	if _, err = store.ReplaceArtifactIntelligence(ctx, repository.ArtifactIntelligence{RepositoryID: source.ID, Format: source.Format, Coordinate: coordinate, Digest: digest, SBOMs: []repository.ArtifactSBOM{{MediaType: "application/spdx+json", Digest: digest}}, Licenses: []repository.ArtifactLicense{{SPDXID: "MIT"}}}, ""); err != nil {
		t.Fatal(err)
	}
	allowed := request(http.MethodPost, evaluatePath, evaluateBody, "", "admin-secret")
	if allowed.Code != http.StatusOK || !strings.Contains(allowed.Body.String(), `"allowed":true`) || !strings.Contains(allowed.Body.String(), `"intelligencePresent":true`) {
		t.Fatalf("allowed=%d body=%s", allowed.Code, allowed.Body.String())
	}
}

func TestRepositoryPromotionSecurityPolicyDeniesBeforeEnqueue(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	source, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "promotion-policy-source", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "promotion-policy-target", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("b", 64)
	coordinate := "releases/policy.txt"
	if _, err = store.PutRawAsset(ctx, repository.RawAsset{RepositoryID: source.ID, Path: coordinate, Digest: digest, ObjectKey: "raw/policy", Size: 6}); err != nil {
		t.Fatal(err)
	}
	policy := repository.DefaultRepositorySecurityPolicy()
	policy.Enabled = true
	policy.RequireSignature = true
	if _, err = store.ReplaceRepositorySecurityPolicy(ctx, target.ID, policy, "1"); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+source.ID+"/promotions", strings.NewReader(`{"targetRepositoryId":"`+target.ID+`","coordinate":"`+coordinate+`","digest":"`+digest+`"}`))
	authorize(request, "admin-secret")
	request.Header.Set("Idempotency-Key", "security-denied")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"security_policy_denied"`) || !strings.Contains(response.Body.String(), "signature_required") {
		t.Fatalf("promotion=%d body=%s", response.Code, response.Body.String())
	}
	jobs, err := store.ListLifecycleJobs(ctx, target.ID, 10)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("jobs=%#v err=%v", jobs, err)
	}
	audits, err := store.ListAudits(ctx, repository.AuditQuery{Repository: target.Name, Operation: "promote.security_policy"})
	if err != nil || len(audits) != 1 || audits[0].Outcome != repository.AuditAccessDenied {
		t.Fatalf("audits=%#v err=%v", audits, err)
	}
}
