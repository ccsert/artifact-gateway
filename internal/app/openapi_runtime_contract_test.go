package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/google/uuid"
)

func TestRuntimeManagementRoutesConformToOpenAPI(t *testing.T) {
	loader := openapi3.NewLoader()
	spec, err := loader.LoadFromFile(filepath.Join("..", "..", "api", "openapi", "management-runtime-v1.json"))
	if err != nil || spec.Validate(loader.Context) != nil {
		t.Fatalf("load runtime contract: %v", err)
	}
	router, err := legacy.NewRouter(spec)
	if err != nil {
		t.Fatal(err)
	}
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "conformance-conan", Format: repository.FormatConan, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	for _, path := range []string{
		"/api/v2/repositories/" + repo.ID,
		"/api/v2/repositories/" + repo.ID + "/capabilities",
		"/api/v2/repositories/" + repo.ID + "/capacity",
		"/api/v2/repositories/" + repo.ID + "/conan/references?pageSize=10",
		"/api/v2/repositories/" + repo.ID + "/conan/recipe-revisions?reference=hello%2F1.0%2Fdemo%2Fstable",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "https://gateway.example.com"+path, nil)
			authorize(req, "admin-secret")
			route, params, err := router.FindRoute(req)
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			input := &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route}
			options := &openapi3filter.Options{IncludeResponseStatus: true}
			if err := openapi3filter.ValidateResponse(req.Context(), (&openapi3filter.ResponseValidationInput{RequestValidationInput: input, Status: response.Code, Header: response.Header(), Options: options}).SetBodyBytes(response.Body.Bytes())); err != nil {
				t.Fatalf("status=%d does not conform: %v; body=%s", response.Code, err, response.Body.String())
			}
		})
	}
}
