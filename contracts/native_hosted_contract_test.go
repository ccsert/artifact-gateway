package contracts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func loadNativeHostedSpec(t *testing.T) *openapi3.T {
	t.Helper()
	spec, err := openapi3.NewLoader().LoadFromFile(filepath.Join("..", "api", "openapi", "native-hosted-v1.json"))
	if err != nil {
		t.Fatalf("load OpenAPI: %v", err)
	}
	if err := spec.Validate(openapi3.NewLoader().Context); err != nil {
		t.Fatalf("validate OpenAPI: %v", err)
	}
	return spec
}

func operation(t *testing.T, spec *openapi3.T, path, method string) *openapi3.Operation {
	t.Helper()
	item := spec.Paths.Find(path)
	if item == nil {
		t.Fatalf("missing path %s", path)
	}
	var op *openapi3.Operation
	switch method {
	case "GET":
		op = item.Get
	case "PUT":
		op = item.Put
	case "POST":
		op = item.Post
	default:
		t.Fatalf("unsupported method %s", method)
	}
	if op == nil {
		t.Fatalf("missing %s %s", method, path)
	}
	return op
}

func requireResponse(t *testing.T, op *openapi3.Operation, status string) *openapi3.Response {
	t.Helper()
	response := op.Responses.Value(status)
	if response == nil || response.Value == nil {
		t.Fatalf("operation %s missing response %s", op.OperationID, status)
	}
	return response.Value
}

func TestNativeHostedOpenAPIContract(t *testing.T) {
	spec := loadNativeHostedSpec(t)
	if spec.OpenAPI != "3.1.0" {
		t.Fatalf("openapi=%s", spec.OpenAPI)
	}
	for _, path := range []string{
		"/repositories",
		"/groups",
		"/groups/{groupId}/members",
		"/repositories/{repositoryId}/publish-sessions",
		"/publish-sessions/{sessionId}:commit",
		"/repositories/{repositoryId}/artifacts/{artifactId}",
	} {
		if spec.Paths.Find(path) == nil {
			t.Errorf("missing management path %s", path)
		}
	}
	if spec.Paths.Find("/repositories/{repositoryId}/members") != nil {
		t.Error("repository-owned member mutation must not be exposed")
	}
	for _, schema := range []string{"Repository", "Group", "Member", "Grant", "RetentionPolicy", "PublishSession", "Artifact", "Problem"} {
		if spec.Components.Schemas[schema] == nil {
			t.Errorf("missing schema %s", schema)
		}
	}
	publish := spec.Components.Schemas["CreatePublishSession"].Value
	if len(publish.OneOf) != 3 {
		t.Fatalf("publish session variants=%d want 3", len(publish.OneOf))
	}
	for _, schema := range []string{"CreateRawPublishSession", "CreateOCIPublishSession", "CreateMavenPublishSession"} {
		if spec.Components.Schemas[schema] == nil {
			t.Errorf("missing format-specific publish schema %s", schema)
		}
	}
	problem := spec.Components.Schemas["Problem"].Value
	for _, field := range []string{"code", "requestId"} {
		if !contains(problem.Required, field) {
			t.Errorf("Problem missing required %s", field)
		}
	}
}

func TestNativeHostedGroupMembershipContract(t *testing.T) {
	spec := loadNativeHostedSpec(t)
	put := operation(t, spec, "/groups/{groupId}/members", "PUT")
	if put.OperationID != "replaceGroupMembers" {
		t.Fatalf("operationId=%q", put.OperationID)
	}
	if !hasParameter(put.Parameters, "If-Match", "header") {
		t.Fatal("group member replacement must use If-Match")
	}
	member := spec.Components.Schemas["Member"].Value
	for _, field := range []string{"repositoryId", "position"} {
		if !contains(member.Required, field) {
			t.Errorf("Member missing required %s", field)
		}
	}
	if member.Properties["position"].Value.Min == nil || *member.Properties["position"].Value.Min != 0 {
		t.Error("member position must be non-negative")
	}
	constraints, ok := put.Extensions["x-gateway-memberConstraints"].([]any)
	if !ok {
		t.Fatal("group member replacement missing machine-readable constraints")
	}
	for _, want := range []string{"repositoryId_unique", "position_contiguous_from_zero", "repository_format_matches_group"} {
		found := false
		for _, got := range constraints {
			found = found || got == want
		}
		if !found {
			t.Errorf("group member constraint missing %q", want)
		}
	}
}

func TestNativeHostedProtocolReadLifecycleFixtures(t *testing.T) {
	spec := loadNativeHostedSpec(t)
	fixtures := []struct {
		name, path, method string
		status             []string
		catchAll           string
		securitySchemes    []string
	}{
		{"Raw multi-segment path", "/raw/{repository}/content/{path}", "GET", []string{"200", "401", "404"}, "path", []string{"protocolBasicAuth", "protocolBearerAuth"}},
		{"OCI manifest by tag or digest", "/v2/{name}/manifests/{reference}", "GET", []string{"200", "401", "404"}, "", []string{"ociBearerAuth"}},
		{"OCI blob by digest", "/v2/{name}/blobs/{digest}", "GET", []string{"200", "401", "404"}, "", []string{"ociBearerAuth"}},
		{"Maven POM component metadata path", "/repository/maven/{repository}/{assetPath}", "GET", []string{"200", "401", "404"}, "assetPath", []string{"protocolBasicAuth", "protocolBearerAuth"}},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			item := spec.Paths.Find(fixture.path)
			op := operation(t, spec, fixture.path, fixture.method)
			for _, status := range fixture.status {
				requireResponse(t, op, status)
			}
			// 404 covers a staged session: protocol reads become addressable only after commit.
			if fixture.catchAll != "" && item.Extensions["x-gateway-catchAll"] != fixture.catchAll {
				t.Fatalf("catch-all=%v want %q", item.Extensions["x-gateway-catchAll"], fixture.catchAll)
			}
			if len(item.Servers) != 1 || item.Servers[0].URL != "https://gateway.example.com" {
				t.Fatalf("protocol route must override management API server: %#v", item.Servers)
			}
			if item.Extensions["x-gateway-visibility"] != "committed_only" {
				t.Fatalf("protocol fixture must be unreadable before commit: %v", item.Extensions["x-gateway-visibility"])
			}
			if op.Security == nil {
				t.Fatal("protocol operation must override management API security")
			}
			for _, scheme := range fixture.securitySchemes {
				if !hasSecurityScheme(*op.Security, scheme) {
					t.Errorf("protocol operation missing security scheme %s", scheme)
				}
			}
		})
	}
	ociManifest := operation(t, spec, "/v2/{name}/manifests/{reference}", "GET")
	if requireResponse(t, ociManifest, "200").Headers["Docker-Content-Digest"] == nil {
		t.Error("OCI manifest must return Docker-Content-Digest")
	}
	ociUnauthorized := requireResponse(t, ociManifest, "401")
	challenge := ociUnauthorized.Headers["WWW-Authenticate"]
	if challenge == nil || challenge.Value.Schema.Value.Pattern != "^Bearer " {
		t.Error("OCI authentication must be a Bearer challenge")
	}
	for _, path := range []string{"/raw/{repository}/content/{path}", "/repository/maven/{repository}/{assetPath}"} {
		challenge := requireResponse(t, operation(t, spec, path, "GET"), "401").Headers["WWW-Authenticate"]
		if challenge == nil || challenge.Value.Schema.Value.Pattern != "^Basic " {
			t.Errorf("%s must return a Basic challenge", path)
		}
	}
}

func TestNativeHostedAPICompatibility(t *testing.T) {
	baseline := os.Getenv("API_BASELINE")
	if baseline == "" {
		t.Skip("no prior API baseline")
	}
	prior, err := openapi3.NewLoader().LoadFromFile(baseline)
	if err != nil {
		t.Fatalf("load API baseline: %v", err)
	}
	current := loadNativeHostedSpec(t)
	for path, priorItem := range prior.Paths.Map() {
		currentItem := current.Paths.Find(path)
		if currentItem == nil {
			t.Errorf("breaking API change: removed path %s", path)
			continue
		}
		for _, method := range []struct {
			name           string
			prior, current *openapi3.Operation
		}{
			{"GET", priorItem.Get, currentItem.Get},
			{"PUT", priorItem.Put, currentItem.Put},
			{"POST", priorItem.Post, currentItem.Post},
			{"DELETE", priorItem.Delete, currentItem.Delete},
			{"PATCH", priorItem.Patch, currentItem.Patch},
		} {
			if method.prior != nil && method.current == nil {
				t.Errorf("breaking API change: removed %s %s", method.name, path)
			}
		}
	}
}

func TestNativeHostedContractHasLifecycleAndRetirementDecisions(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "docs", "native-hosted-contract.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	for _, phrase := range []string{
		"PostgreSQL is authoritative", "Object upload precedes metadata promotion", "orphan collector",
		"Idempotency-Key", "pageToken", "Gitea retirement boundary", "shadow-read mode", "non-goals",
		"/groups/{groupId}/members", "gateway catch-all", "Docker-Content-Digest", "generated from committed coordinates",
	} {
		if !strings.Contains(doc, phrase) {
			t.Errorf("missing contract decision %q", phrase)
		}
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func hasParameter(parameters openapi3.Parameters, name, location string) bool {
	for _, parameter := range parameters {
		if parameter.Value != nil && parameter.Value.Name == name && parameter.Value.In == location {
			return true
		}
	}
	return false
}

func hasSecurityScheme(requirements openapi3.SecurityRequirements, want string) bool {
	for _, requirement := range requirements {
		if _, ok := requirement[want]; ok {
			return true
		}
	}
	return false
}
