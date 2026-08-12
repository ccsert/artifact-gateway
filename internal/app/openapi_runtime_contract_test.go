package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/secrets"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	openapirouters "github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/google/uuid"
)

func TestAPTPublicationRuntimeResponsesConformToOpenAPI(t *testing.T) {
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
	handler := NewGatewayHandler(Dependencies{NativeAPTObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator())
	validate := func(req *http.Request) *httptest.ResponseRecorder {
		t.Helper()
		route, params, routeErr := router.FindRoute(req)
		if routeErr != nil {
			t.Fatal(routeErr)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		input := &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route}
		options := &openapi3filter.Options{IncludeResponseStatus: true}
		if validateErr := openapi3filter.ValidateResponse(req.Context(), (&openapi3filter.ResponseValidationInput{RequestValidationInput: input, Status: response.Code, Header: response.Header(), Options: options}).SetBodyBytes(response.Body.Bytes())); validateErr != nil {
			t.Fatalf("status=%d does not conform: %v; body=%s", response.Code, validateErr, response.Body.String())
		}
		return response
	}
	provision := httptest.NewRequest(http.MethodPost, "https://gateway.example.com/api/v2/repositories", strings.NewReader(`{"name":"conformance-apt","format":"apt","type":"hosted"}`))
	authorize(provision, "admin-secret")
	provision.Header.Set("Content-Type", "application/json")
	provision.Header.Set("Idempotency-Key", "contract-apt-repository")
	provisioned := validate(provision)
	if provisioned.Code != http.StatusCreated {
		t.Fatalf("provision=%d body=%s", provisioned.Code, provisioned.Body.String())
	}
	var repo repository.HostedRepository
	if err = json.Unmarshal(provisioned.Body.Bytes(), &repo); err != nil || repo.ID == "" || repo.Format != repository.FormatAPT || repo.Type != repository.RepositoryTypeHosted {
		t.Fatalf("repository=%#v err=%v", repo, err)
	}

	deb := aptManagementDebianPackage(t, "Package: contract\nVersion: 1.0-1\nArchitecture: amd64\n")
	digestBytes := sha256.Sum256(deb)
	digest := "sha256:" + hex.EncodeToString(digestBytes[:])
	createBody := fmt.Sprintf(`{"suite":"stable","component":"main","objectName":"contract.deb","declaredDigest":%q,"declaredSize":%d}`, digest, len(deb))
	create := httptest.NewRequest(http.MethodPost, "https://gateway.example.com/api/v2/repositories/"+repo.ID+"/apt/publication-sessions", strings.NewReader(createBody))
	authorize(create, "admin-secret")
	create.Header.Set("Content-Type", "application/json")
	create.Header.Set("Idempotency-Key", "contract-build")
	created := validate(create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create=%d body=%s", created.Code, created.Body.String())
	}
	var session struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal(created.Body.Bytes(), &session); err != nil || session.ID == "" {
		t.Fatalf("session=%#v err=%v", session, err)
	}
	upload := httptest.NewRequest(http.MethodPut, "https://gateway.example.com/api/v2/repositories/"+repo.ID+"/apt/publication-sessions/"+session.ID+"/package", bytes.NewReader(deb))
	authorize(upload, "admin-secret")
	upload.Header.Set("Content-Type", "application/vnd.debian.binary-package")
	if response := validate(upload); response.Code != http.StatusOK {
		t.Fatalf("upload=%d body=%s", response.Code, response.Body.String())
	}
	get := httptest.NewRequest(http.MethodGet, "https://gateway.example.com/api/v2/repositories/"+repo.ID+"/apt/publication-sessions/"+session.ID, nil)
	authorize(get, "admin-secret")
	if response := validate(get); response.Code != http.StatusOK {
		t.Fatalf("get=%d body=%s", response.Code, response.Body.String())
	}

	wrongMedia := httptest.NewRequest(http.MethodPut, "https://gateway.example.com/api/v2/repositories/"+repo.ID+"/apt/publication-sessions/"+session.ID+"/package", bytes.NewReader(deb))
	authorize(wrongMedia, "admin-secret")
	wrongMedia.Header.Set("Content-Type", "application/octet-stream")
	if response := validate(wrongMedia); response.Code != http.StatusUnsupportedMediaType || !strings.Contains(response.Body.String(), `"code":"unsupported_media_type"`) {
		t.Fatalf("media type=%d body=%s", response.Code, response.Body.String())
	}

	createErrorSession := func(key, objectName, declaredDigest string, size int, expectedIdentity string) string {
		t.Helper()
		body := fmt.Sprintf(`{"suite":"stable","component":"main","objectName":%q,"declaredDigest":%q,"declaredSize":%d`, objectName, declaredDigest, size)
		if expectedIdentity != "" {
			body += fmt.Sprintf(`,"expectedIdentity":%q`, expectedIdentity)
		}
		body += `}`
		request := httptest.NewRequest(http.MethodPost, "https://gateway.example.com/api/v2/repositories/"+repo.ID+"/apt/publication-sessions", strings.NewReader(body))
		authorize(request, "admin-secret")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", key)
		response := validate(request)
		if response.Code != http.StatusCreated {
			t.Fatalf("create error fixture=%d body=%s", response.Code, response.Body.String())
		}
		var createdSession struct {
			ID string `json:"id"`
		}
		if decodeErr := json.Unmarshal(response.Body.Bytes(), &createdSession); decodeErr != nil || createdSession.ID == "" {
			t.Fatalf("error fixture session=%#v err=%v", createdSession, decodeErr)
		}
		return createdSession.ID
	}
	uploadError := func(sessionID string, body []byte, wantStatus int, wantCode string) {
		t.Helper()
		request := httptest.NewRequest(http.MethodPut, "https://gateway.example.com/api/v2/repositories/"+repo.ID+"/apt/publication-sessions/"+sessionID+"/package", bytes.NewReader(body))
		authorize(request, "admin-secret")
		request.Header.Set("Content-Type", "application/vnd.debian.binary-package")
		response := validate(request)
		if response.Code != wantStatus || !strings.Contains(response.Body.String(), `"code":"`+wantCode+`"`) {
			t.Fatalf("upload error=%d body=%s", response.Code, response.Body.String())
		}
	}

	invalidPackage := []byte("not-a-debian-package")
	invalidSum := sha256.Sum256(invalidPackage)
	invalidSession := createErrorSession("contract-invalid-package", "invalid.deb", "sha256:"+hex.EncodeToString(invalidSum[:]), len(invalidPackage), "")
	uploadError(invalidSession, invalidPackage, http.StatusUnprocessableEntity, "invalid_request")

	digestMismatchSession := createErrorSession("contract-digest-mismatch", "digest.deb", "sha256:"+strings.Repeat("a", 64), len(deb), "")
	uploadError(digestMismatchSession, deb, http.StatusUnprocessableEntity, "digest_mismatch")

	identityMismatchSession := createErrorSession("contract-identity-mismatch", "identity.deb", digest, len(deb), "other@1.0-1#amd64")
	uploadError(identityMismatchSession, deb, http.StatusUnprocessableEntity, "identity_mismatch")

	if _, err = store.ReplaceRepositoryCapacityQuota(context.Background(), repo.ID, 1); err != nil {
		t.Fatal(err)
	}
	quotaBody := `{"suite":"stable","component":"main","objectName":"quota.deb","declaredDigest":"sha256:` + strings.Repeat("b", 64) + `","declaredSize":2}`
	quotaRequest := httptest.NewRequest(http.MethodPost, "https://gateway.example.com/api/v2/repositories/"+repo.ID+"/apt/publication-sessions", strings.NewReader(quotaBody))
	authorize(quotaRequest, "admin-secret")
	quotaRequest.Header.Set("Content-Type", "application/json")
	quotaRequest.Header.Set("Idempotency-Key", "contract-quota")
	if response := validate(quotaRequest); response.Code != http.StatusInsufficientStorage || !strings.Contains(response.Body.String(), `"code":"quota_exceeded"`) {
		t.Fatalf("quota=%d body=%s", response.Code, response.Body.String())
	}
}

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
		"/api/v2/repositories/" + repo.ID + "/artifact-identities?purpose=distribution",
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

func TestWebhookRuntimeResponsesConformToOpenAPI(t *testing.T) {
	t.Setenv(secrets.KeyEnv, "0123456789abcdef0123456789abcdef")
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
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := func(method, path, body, ifMatch string, wantStatus int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "https://gateway.example.com"+path, strings.NewReader(body))
		authorize(req, "admin-secret")
		if ifMatch != "" {
			req.Header.Set("If-Match", ifMatch)
		}
		route, params, routeErr := router.FindRoute(req)
		if routeErr != nil && strings.HasSuffix(path, ":replay") {
			// kin-openapi's legacy router does not match a path-parameter suffix,
			// although OpenAPI and the generated server both support this shape.
			pathItem := spec.Paths.Value("/webhook-deliveries/{deliveryId}:replay")
			route = &openapirouters.Route{Spec: spec, Server: spec.Servers[0], Path: "/webhook-deliveries/{deliveryId}:replay", PathItem: pathItem, Method: http.MethodPost, Operation: pathItem.Post}
			params = map[string]string{"deliveryId": strings.TrimSuffix(strings.TrimPrefix(path, "/api/v2/webhook-deliveries/"), ":replay")}
			routeErr = nil
		}
		if routeErr != nil {
			t.Fatal(routeErr)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != wantStatus {
			t.Fatalf("%s %s status=%d body=%s", method, path, response.Code, response.Body.String())
		}
		input := &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route}
		options := &openapi3filter.Options{IncludeResponseStatus: true}
		if validateErr := openapi3filter.ValidateResponse(req.Context(), (&openapi3filter.ResponseValidationInput{RequestValidationInput: input, Status: response.Code, Header: response.Header(), Options: options}).SetBodyBytes(response.Body.Bytes())); validateErr != nil {
			t.Fatalf("%s %s status=%d does not conform: %v; body=%s", method, path, response.Code, validateErr, response.Body.String())
		}
		return response
	}

	request(http.MethodPost, "/api/v2/webhook-subscriptions", `{"name":"contract-webhook","endpointUrl":"https://events.example.test/artifacts","secret":"0123456789abcdef0123456789abcdef","eventTypes":["artifact.quarantined"],"enabled":true}`, "", http.StatusCreated)
	subscriptions, err := store.ListWebhookSubscriptions(ctx)
	if err != nil || len(subscriptions) != 1 {
		t.Fatalf("subscriptions=%#v err=%v", subscriptions, err)
	}
	request(http.MethodPut, "/api/v2/webhook-subscriptions/"+subscriptions[0].ID, `{"name":"contract-webhook","endpointUrl":"https://events.example.test/artifacts","eventTypes":["artifact.quarantined"],"enabled":true}`, "0", http.StatusPreconditionFailed)
	request(http.MethodGet, "/api/v2/webhook-subscriptions", "", "", http.StatusOK)

	event, err := store.EnqueueWebhookEvent(ctx, repository.WebhookEvent{ID: uuid.NewString(), Type: repository.WebhookEventArtifactQuarantined, OccurredAt: time.Now().UTC(), Data: []byte(`{"repositoryId":"contract"}`)})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := store.ClaimWebhookDeliveries(ctx, "contract-worker", event.OccurredAt.Add(time.Second), time.Minute, 1)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims=%#v err=%v", claims, err)
	}
	if err = store.FailWebhookDelivery(ctx, claims[0].Delivery.ID, claims[0].Delivery.LeaseToken, event.OccurredAt.Add(2*time.Second), time.Time{}, http.StatusServiceUnavailable, "webhook returned HTTP 503", true); err != nil {
		t.Fatal(err)
	}
	deliveryPath := "/api/v2/webhook-deliveries/" + claims[0].Delivery.ID
	request(http.MethodGet, deliveryPath, "", "", http.StatusOK)
	request(http.MethodPost, deliveryPath+":replay", "", "", http.StatusOK)
	request(http.MethodGet, "/api/v2/webhook-deliveries?state=pending&limit=10", "", "", http.StatusOK)
	t.Setenv(secrets.KeyEnv, "")
	request(http.MethodPost, "/api/v2/webhook-subscriptions", `{"name":"missing-key","endpointUrl":"https://events.example.test/artifacts","secret":"0123456789abcdef0123456789abcdef","eventTypes":["artifact.quarantined"],"enabled":true}`, "", http.StatusServiceUnavailable)
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
		"{subscriptionId}":    uuid.NewString(),
		"{deliveryId}":        uuid.NewString(),
		"{userId}":            "inventory-user",
	}
	for placeholder, value := range replacements {
		path = strings.ReplaceAll(path, placeholder, value)
	}
	return path
}
