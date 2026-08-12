package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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
		"/api/v2/repositories/" + repo.ID + "/quarantine-read-policy",
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
	for _, test := range []struct {
		name, ifMatch string
		wantStatus    int
	}{
		{name: "replace quarantine read policy", ifMatch: "1", wantStatus: http.StatusOK},
		{name: "stale quarantine read policy", ifMatch: "1", wantStatus: http.StatusPreconditionFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "https://gateway.example.com/api/v2/repositories/"+repo.ID+"/quarantine-read-policy", strings.NewReader(`{"version":"1","enabled":true}`))
			authorize(req, "admin-secret")
			req.Header.Set("If-Match", test.ifMatch)
			route, params, routeErr := router.FindRoute(req)
			if routeErr != nil {
				t.Fatal(routeErr)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			input := &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route}
			options := &openapi3filter.Options{IncludeResponseStatus: true}
			if err := openapi3filter.ValidateResponse(req.Context(), (&openapi3filter.ResponseValidationInput{RequestValidationInput: input, Status: response.Code, Header: response.Header(), Options: options}).SetBodyBytes(response.Body.Bytes())); err != nil {
				t.Fatalf("status=%d does not conform: %v; body=%s", response.Code, err, response.Body.String())
			}
		})
	}
}

func TestArtifactQuarantineRuntimeResponsesConformToOpenAPI(t *testing.T) {
	loader := openapi3.NewLoader()
	spec, err := loader.LoadFromFile(filepath.Join("..", "..", "api", "openapi", "management-runtime-v1.json"))
	if err != nil || spec.Validate(loader.Context) != nil {
		t.Fatalf("load runtime contract: %v", err)
	}
	router, err := legacy.NewRouter(spec)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	store := repository.NewMemoryStore()
	source, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "contract-quarantine-source", Format: repository.FormatRaw, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "contract-quarantine-target", Format: repository.FormatRaw, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	coordinate := "releases/contract.bin"
	if _, err = store.PutRawAsset(ctx, repository.RawAsset{RepositoryID: source.ID, Path: coordinate, Digest: digest, ObjectKey: "raw/contract", Size: 8}); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	quarantinePath := "/api/v2/repositories/" + source.ID + "/artifact-quarantine?coordinate=" + coordinate + "&digest=" + digest
	distributionBody := `{"targetRepositoryId":"` + target.ID + `","coordinate":"` + coordinate + `","digest":"` + digest + `"}`

	cases := []struct {
		name, method, path, body, ifMatch, idempotencyKey string
		wantStatus                                        int
	}{
		{name: "quarantine", method: http.MethodPut, path: quarantinePath, body: `{"state":"quarantined","reason":"contract verification"}`, ifMatch: "0", wantStatus: http.StatusOK},
		{name: "duplicate quarantine", method: http.MethodPut, path: quarantinePath, body: `{"state":"quarantined","reason":"duplicate contract verification"}`, ifMatch: "1", wantStatus: http.StatusConflict},
		{name: "stale release", method: http.MethodPut, path: quarantinePath, body: `{"state":"released","reason":"stale contract verification"}`, ifMatch: "0", wantStatus: http.StatusPreconditionFailed},
		{name: "promotion denial", method: http.MethodPost, path: "/api/v2/repositories/" + source.ID + "/promotions", body: distributionBody, idempotencyKey: "contract-quarantine-promotion", wantStatus: http.StatusForbidden},
		{name: "replication denial", method: http.MethodPost, path: "/api/v2/repositories/" + source.ID + "/replications", body: distributionBody, idempotencyKey: "contract-quarantine-replication", wantStatus: http.StatusForbidden},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(test.method, "https://gateway.example.com"+test.path, strings.NewReader(test.body))
			authorize(req, "admin-secret")
			if test.ifMatch != "" {
				req.Header.Set("If-Match", test.ifMatch)
			}
			if test.idempotencyKey != "" {
				req.Header.Set("Idempotency-Key", test.idempotencyKey)
			}
			route, params, routeErr := router.FindRoute(req)
			if routeErr != nil {
				t.Fatal(routeErr)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			input := &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route}
			options := &openapi3filter.Options{IncludeResponseStatus: true}
			if err := openapi3filter.ValidateResponse(req.Context(), (&openapi3filter.ResponseValidationInput{RequestValidationInput: input, Status: response.Code, Header: response.Header(), Options: options}).SetBodyBytes(response.Body.Bytes())); err != nil {
				t.Fatalf("status=%d does not conform: %v; body=%s", response.Code, err, response.Body.String())
			}
		})
	}
}

// TestRuntimeManagementOperationInventory verifies that every published
// management operation reaches the assembled Gateway. Scenario tests own the
// successful state transitions; this inventory catches a route that is absent
// from the runtime even when its generated client contract still exists.
func TestRuntimeManagementOperationInventory(t *testing.T) {
	loader := openapi3.NewLoader()
	spec, err := loader.LoadFromFile(filepath.Join("..", "..", "api", "openapi", "management-runtime-v1.json"))
	if err != nil || spec.Validate(loader.Context) != nil {
		t.Fatalf("load runtime contract: %v", err)
	}
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "inventory-conan", Format: repository.FormatConan, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())

	operationCount := 0
	operationIDs := map[string]string{}
	for path, pathItem := range spec.Paths.Map() {
		for method, operation := range pathItem.Operations() {
			operationCount++
			if operation.OperationID == "" {
				t.Fatalf("%s %s has no operationId", strings.ToUpper(method), path)
			}
			if prior, exists := operationIDs[operation.OperationID]; exists {
				t.Fatalf("operationId=%s is reused by %s and %s %s", operation.OperationID, prior, strings.ToUpper(method), path)
			}
			operationIDs[operation.OperationID] = strings.ToUpper(method) + " " + path
			path := inventoryPath(path, repo.ID)
			method := strings.ToUpper(method)
			t.Run(fmt.Sprintf("%s %s", method, path), func(t *testing.T) {
				req := httptest.NewRequest(method, "https://gateway.example.com/api/v2"+path, nil)
				authorize(req, "admin-secret")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, req)
				if response.Code == http.StatusMethodNotAllowed {
					t.Fatalf("operationId=%s is not registered", operation.OperationID)
				}
			})
		}
	}
	if operationCount == 0 {
		t.Fatal("runtime contract has no operations")
	}
}

func inventoryPath(path, repositoryID string) string {
	replacements := map[string]string{
		"{apiKeyId}":          uuid.NewString(),
		"{artifactId}":        uuid.NewString(),
		"{groupId}":           uuid.NewString(),
		"{objectName}":        "artifact.jar",
		"{replicationPlanId}": uuid.NewString(),
		"{repositoryId}":      repositoryID,
		"{revision}":          "rrev1",
		"{sessionId}":         uuid.NewString(),
		"{userId}":            "inventory-user",
	}
	for placeholder, value := range replacements {
		path = strings.ReplaceAll(path, placeholder, value)
	}
	return path
}
