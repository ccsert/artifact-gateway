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

func TestSecurityPolicyEvaluationReportsPyPIAggregateQuarantine(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	source, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "pypi-policy-quarantine-source", Format: repository.FormatPyPI,
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "pypi-policy-quarantine-target", Format: repository.FormatPyPI,
	})
	if err != nil {
		t.Fatal(err)
	}
	coordinate := "widget@5.0.0"
	files := []repository.PyPIFile{
		{RepositoryID: source.ID, Project: "widget", Version: "5.0.0", Filename: "widget-5.0.0.tar.gz", Digest: "sha256:" + strings.Repeat("a", 64), ObjectKey: "native/pypi/widget-5.0.0-sdist", Size: 10},
		{RepositoryID: source.ID, Project: "widget", Version: "5.0.0", Filename: "widget-5.0.0-py3-none-any.whl", Digest: "sha256:" + strings.Repeat("b", 64), ObjectKey: "native/pypi/widget-5.0.0-wheel", Size: 20},
	}
	if _, err = store.PublishPyPIVersion(ctx, files); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceArtifactQuarantine(ctx, repository.ArtifactQuarantine{
		RepositoryID: source.ID,
		Format:       repository.FormatPyPI,
		Coordinate:   coordinate,
		Digest:       files[1].Digest,
		State:        repository.ArtifactQuarantineStateQuarantined,
		Reason:       "wheel is malicious",
		UpdatedBy:    "security-admin",
	}, "0"); err != nil {
		t.Fatal(err)
	}

	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v2/repositories/"+target.ID+"/security-policy:evaluate",
		strings.NewReader(`{"sourceRepositoryId":"`+source.ID+`","coordinate":"`+coordinate+`","digest":"`+files[0].Digest+`"}`),
	)
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"allowed":false`) ||
		!strings.Contains(response.Body.String(), `"enforced":true`) ||
		!strings.Contains(response.Body.String(), repository.ArtifactQuarantinedReason) {
		t.Fatalf("evaluation=%d body=%s", response.Code, response.Body.String())
	}
}
