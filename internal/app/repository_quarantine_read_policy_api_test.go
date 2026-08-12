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

func TestRepositoryQuarantineReadPolicyAPIUsesAdminCASAndAudits(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "quarantine-read-policy-api", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	path := "/api/v2/repositories/" + repo.ID + "/quarantine-read-policy"
	request := func(method, token, body, ifMatch string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		authorize(r, token)
		if ifMatch != "" {
			r.Header.Set("If-Match", ifMatch)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	if denied := request(http.MethodGet, "resolver-secret", "", ""); denied.Code != http.StatusForbidden {
		t.Fatalf("reader get=%d body=%q", denied.Code, denied.Body.String())
	}
	initial := request(http.MethodGet, "admin-secret", "", "")
	if initial.Code != http.StatusOK || initial.Header().Get("ETag") != "1" || !strings.Contains(initial.Body.String(), `"enabled":false`) {
		t.Fatalf("initial=%d etag=%q body=%q", initial.Code, initial.Header().Get("ETag"), initial.Body.String())
	}
	missingPrecondition := request(http.MethodPut, "admin-secret", `{"version":"1","enabled":true}`, "")
	if missingPrecondition.Code != http.StatusBadRequest {
		t.Fatalf("missing If-Match=%d body=%q", missingPrecondition.Code, missingPrecondition.Body.String())
	}
	updated := request(http.MethodPut, "admin-secret", `{"version":"1","enabled":true}`, "1")
	if updated.Code != http.StatusOK || updated.Header().Get("ETag") != "2" || !strings.Contains(updated.Body.String(), `"enabled":true`) {
		t.Fatalf("updated=%d etag=%q body=%q", updated.Code, updated.Header().Get("ETag"), updated.Body.String())
	}
	stale := request(http.MethodPut, "admin-secret", `{"version":"1","enabled":false}`, "1")
	if stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale=%d body=%q", stale.Code, stale.Body.String())
	}
	audits, err := store.ListAudits(ctx, repository.AuditQuery{Repository: repo.Name})
	if err != nil {
		t.Fatal(err)
	}
	var replaced, denied bool
	for _, audit := range audits {
		replaced = replaced || audit.Actor == "alice" && audit.Operation == "quarantine_read_policy.replace" && audit.AuthorizationReason == "enabled"
		denied = denied || audit.Outcome == repository.AuditAccessDenied
	}
	if !replaced || !denied {
		t.Fatalf("audits=%#v", audits)
	}

	unsupported, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "quarantine-read-policy-go", Format: repository.FormatGo})
	if err != nil {
		t.Fatal(err)
	}
	unsupportedRequest := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+unsupported.ID+"/quarantine-read-policy", nil)
	authorize(unsupportedRequest, "admin-secret")
	unsupportedResponse := httptest.NewRecorder()
	handler.ServeHTTP(unsupportedResponse, unsupportedRequest)
	if unsupportedResponse.Code != http.StatusNotFound {
		t.Fatalf("unsupported format=%d body=%q", unsupportedResponse.Code, unsupportedResponse.Body.String())
	}
}
