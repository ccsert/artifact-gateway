package contracts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
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

func loadNativeHostedSource(t *testing.T) *openapi3.T {
	t.Helper()
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true
	spec, err := loader.LoadFromFile(filepath.Join("..", "api", "openapi", "native-hosted.yaml"))
	if err != nil {
		t.Fatalf("load OpenAPI source: %v", err)
	}
	if err := spec.Validate(loader.Context); err != nil {
		t.Fatalf("validate OpenAPI source: %v", err)
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
	case "HEAD":
		op = item.Head
	case "PUT":
		op = item.Put
	case "POST":
		op = item.Post
	case "PATCH":
		op = item.Patch
	case "DELETE":
		op = item.Delete
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
		"/artifact-search",
		"/audits",
		"/repositories",
		"/groups",
		"/groups/{groupId}/members",
		"/repositories/{repositoryId}/publish-sessions",
		"/publish-sessions/{sessionId}:commit",
		"/repository/maven/{repository}/coordinates/{coordinate}:commit",
		"/v2/{name}/blobs/uploads/",
		"/v2/{name}/blobs/uploads/{uuid}",
		"/repositories/{repositoryId}/artifacts/{artifactId}",
	} {
		if spec.Paths.Find(path) == nil {
			t.Errorf("missing management path %s", path)
		}
	}
	if spec.Paths.Find("/repositories/{repositoryId}/members") != nil {
		t.Error("repository-owned member mutation must not be exposed")
	}
	for _, schema := range []string{"Repository", "Group", "Member", "Grant", "RetentionPolicy", "PublishSession", "Artifact", "Problem", "CommitMavenCoordinate", "AuditRecord", "AuditList"} {
		if spec.Components.Schemas[schema] == nil {
			t.Errorf("missing schema %s", schema)
		}
	}
	publish := spec.Components.Schemas["CreatePublishSession"].Value
	if len(publish.OneOf) != 1 {
		t.Fatalf("publish session variants=%d want 1 Maven-only variant", len(publish.OneOf))
	}
	for _, schema := range []string{"CreateMavenPublishSession"} {
		if spec.Components.Schemas[schema] == nil {
			t.Errorf("missing format-specific publish schema %s", schema)
		}
	}
	for _, schema := range []string{"CreateRawPublishSession", "CreateOCIPublishSession"} {
		if spec.Components.Schemas[schema] != nil {
			t.Errorf("Raw/OCI publish session schema %s must not be exposed", schema)
		}
	}
	problem := spec.Components.Schemas["Problem"].Value
	for _, field := range []string{"code", "requestId"} {
		if !contains(problem.Required, field) {
			t.Errorf("Problem missing required %s", field)
		}
	}
	generated, err := os.ReadFile(filepath.Join("..", "internal", "admin", "openapi", "generated.go"))
	if err != nil {
		t.Fatalf("read generated management contract: %v", err)
	}
	for _, declaration := range []string{"type ServerInterface interface", "type StrictServerInterface interface", "ListAudits", "ListRepositories", "CreateRepository", "GetRepository", "DeleteRepository"} {
		if !strings.Contains(string(generated), declaration) {
			t.Errorf("generated management contract is missing %q", declaration)
		}
	}
	audit := spec.Components.Schemas["AuditRecord"].Value
	for _, field := range []string{"authorizationSource", "authorizationReason"} {
		if audit.Properties[field] == nil {
			t.Errorf("AuditRecord missing %s", field)
		}
		if contains(audit.Required, field) {
			t.Errorf("AuditRecord %s must remain optional", field)
		}
	}
	audits := operation(t, spec, "/audits", "GET")
	for _, status := range []string{"200", "401"} {
		requireResponse(t, audits, status)
	}
	for _, parameter := range []string{"group", "repository", "limit"} {
		if !hasParameter(audits.Parameters, parameter, "query") {
			t.Errorf("audit list missing query parameter %s", parameter)
		}
	}
}

func TestFormatProfileContractMatchesRepositoryProfiles(t *testing.T) {
	spec := loadNativeHostedSpec(t)
	formats := operation(t, spec, "/formats", "GET")
	if formats.OperationID != "listFormatProfiles" {
		t.Fatalf("operationId=%q", formats.OperationID)
	}
	for _, status := range []string{"200", "401"} {
		requireResponse(t, formats, status)
	}
	for _, schema := range []string{"FormatProfile", "FormatProfileList", "RepositoryOperation"} {
		if spec.Components.Schemas[schema] == nil {
			t.Fatalf("missing schema %s", schema)
		}
	}

	formatSchema := spec.Components.Schemas["Format"].Value
	wantFormats := make(map[string]bool)
	for _, format := range repository.SupportedFormats() {
		wantFormats[string(format)] = true
	}
	if len(formatSchema.Enum) != len(wantFormats) {
		t.Fatalf("OpenAPI formats=%v profiles=%v", formatSchema.Enum, wantFormats)
	}
	for _, value := range formatSchema.Enum {
		format, ok := value.(string)
		if !ok || !wantFormats[format] {
			t.Fatalf("OpenAPI format %v is not in repository profiles", value)
		}
	}
}

func TestNativeHostedSourceBundlesToThePublishedContract(t *testing.T) {
	source := loadNativeHostedSource(t)
	bundle := loadNativeHostedSpec(t)
	if source.OpenAPI != "3.1.0" || bundle.OpenAPI != source.OpenAPI {
		t.Fatalf("source=%q bundle=%q", source.OpenAPI, bundle.OpenAPI)
	}
	if len(source.Paths.Map()) != len(bundle.Paths.Map()) {
		t.Fatalf("source paths=%d bundle paths=%d", len(source.Paths.Map()), len(bundle.Paths.Map()))
	}
	for path := range bundle.Paths.Map() {
		if source.Paths.Find(path) == nil {
			t.Errorf("source is missing bundled path %s", path)
		}
	}
	for _, path := range []string{
		filepath.Join("..", "api", "openapi", "components", "schemas.yaml"),
		filepath.Join("..", "api", "openapi", "components", "parameters.yaml"),
		filepath.Join("..", "api", "openapi", "components", "responses.yaml"),
		filepath.Join("..", "api", "openapi", "management", "repositories.yaml"),
		filepath.Join("..", "api", "openapi", "management", "search.yaml"),
		filepath.Join("..", "api", "openapi", "management", "audits.yaml"),
		filepath.Join("..", "api", "openapi", "protocols", "oci.yaml"),
		filepath.Join("..", "api", "openapi", "protocols", "raw.yaml"),
		filepath.Join("..", "api", "openapi", "protocols", "maven.yaml"),
		filepath.Join("..", "api", "openapi", "protocols", "conan.yaml"),
		filepath.Join("..", "internal", "admin", "openapi", "generated.go"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("required generated-contract input %s: %v", path, err)
		}
	}
}

func TestGlobalArtifactSearchContract(t *testing.T) {
	spec := loadNativeHostedSpec(t)
	search := operation(t, spec, "/artifact-search", "GET")
	if search.OperationID != "searchArtifacts" {
		t.Fatalf("operationId=%q", search.OperationID)
	}
	for _, parameter := range []string{"q", "format", "pageSize", "pageToken"} {
		if !hasParameter(search.Parameters, parameter, "query") {
			t.Errorf("global artifact search missing query parameter %s", parameter)
		}
	}
	for _, status := range []string{"200", "400", "401"} {
		requireResponse(t, search, status)
	}
	hit := spec.Components.Schemas["GlobalArtifactSearchHit"].Value
	if len(hit.AllOf) != 2 || hit.AllOf[1].Value == nil {
		t.Fatal("GlobalArtifactSearchHit must extend ArtifactSummary with search metadata")
	}
	metadata := hit.AllOf[1].Value
	if !contains(metadata.Required, "matchKind") || metadata.Properties["matchKind"] == nil {
		t.Fatal("GlobalArtifactSearchHit must require matchKind")
	}
	matchKind := metadata.Properties["matchKind"].Value
	if matchKind == nil {
		t.Fatal("matchKind schema is unresolved")
	}
	if len(matchKind.Enum) != 2 || matchKind.Enum[0] != "coordinate" || matchKind.Enum[1] != "digest" {
		t.Fatalf("matchKind enum=%v", matchKind.Enum)
	}
}

func TestMavenCoordinateCommitContract(t *testing.T) {
	spec := loadNativeHostedSpec(t)
	stage := operation(t, spec, "/repository/maven/{repository}/{assetPath}", "PUT")
	for _, status := range []string{"201", "401", "409", "422"} {
		requireResponse(t, stage, status)
	}
	path := spec.Paths.Find("/repository/maven/{repository}/{assetPath}")
	if path.Extensions["x-gateway-publication"] != "stage_only_until_explicit_coordinate_commit" {
		t.Fatal("Maven deploy must stage until explicit coordinate commit")
	}

	commit := operation(t, spec, "/repository/maven/{repository}/coordinates/{coordinate}:commit", "POST")
	if !hasParameter(commit.Parameters, "Idempotency-Key", "header") {
		t.Fatal("Maven coordinate commit must be idempotent")
	}
	for _, status := range []string{"200", "409", "422"} {
		requireResponse(t, commit, status)
	}
	item := spec.Paths.Find("/repository/maven/{repository}/coordinates/{coordinate}:commit")
	if item.Extensions["x-gateway-visibility-transition"] != "single_postgres_transaction" {
		t.Fatal("Maven visibility must use one PostgreSQL transaction")
	}
	commitRequest := spec.Components.Schemas["CommitMavenCoordinate"].Value
	if !contains(commitRequest.Required, "expectedAssetNames") || !commitRequest.Properties["expectedAssetNames"].Value.UniqueItems {
		t.Fatal("commit must require a unique expected asset set")
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
		{"Raw multi-segment path", "/raw/{repository}/{path}", "GET", []string{"200", "401", "404"}, "path", []string{"protocolBasicAuth", "protocolBearerAuth"}},
		{"Raw multi-segment path metadata", "/raw/{repository}/{path}", "HEAD", []string{"200", "401", "404"}, "path", []string{"protocolBasicAuth", "protocolBearerAuth"}},
		{"OCI manifest by tag or digest", "/v2/{name}/manifests/{reference}", "GET", []string{"200", "401", "404", "406"}, "", []string{"ociBearerAuth"}},
		{"OCI blob by digest", "/v2/{name}/blobs/{digest}", "GET", []string{"200", "401", "404"}, "", []string{"ociBearerAuth"}},
		{"OCI tag list", "/v2/{name}/tags/list", "GET", []string{"200", "400", "401", "404"}, "", []string{"ociBearerAuth"}},
		{"OCI tag list metadata", "/v2/{name}/tags/list", "HEAD", []string{"200", "400", "401", "404"}, "", []string{"ociBearerAuth"}},
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
	rawPut := operation(t, spec, "/raw/{repository}/{path}", "PUT")
	for _, status := range []string{"201", "401", "422"} {
		requireResponse(t, rawPut, status)
	}
	for _, fixture := range []struct {
		method string
		status []string
	}{
		{"POST", []string{"201", "202", "400", "401"}},
		{"GET", []string{"204", "401", "404"}},
		{"PATCH", []string{"202", "401", "404", "416"}},
		{"PUT", []string{"201", "400", "401", "404", "409", "416"}},
		{"DELETE", []string{"204", "401", "404"}},
	} {
		op := operation(t, spec, "/v2/{name}/blobs/uploads/"+map[bool]string{true: "", false: "{uuid}"}[fixture.method == "POST"], fixture.method)
		for _, status := range fixture.status {
			requireResponse(t, op, status)
		}
	}
	for _, fixture := range []struct {
		method string
		status []string
	}{{"HEAD", []string{"200", "401", "406", "404"}}, {"PUT", []string{"201", "400", "401", "413"}}, {"DELETE", []string{"202", "400", "401", "404"}}} {
		op := operation(t, spec, "/v2/{name}/manifests/{reference}", fixture.method)
		for _, status := range fixture.status {
			requireResponse(t, op, status)
		}
	}
	rawDelete := operation(t, spec, "/raw/{repository}/{path}", "DELETE")
	for _, status := range []string{"204", "401", "404"} {
		requireResponse(t, rawDelete, status)
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
	for _, path := range []string{"/raw/{repository}/{path}", "/repository/maven/{repository}/{assetPath}"} {
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
		"Idempotency-Key", "pageToken", "Native hosted completion boundary", "non-goals",
		"/groups/{groupId}/members", "gateway catch-all", "Docker-Content-Digest", "generated from committed coordinates",
		"Maven and Gradle do not define a portable transaction-complete request", "Gateway never infers publication completion",
		"The production flow retains standard Maven repository URLs and HTTP `PUT`", "expected-name list is an incompleteness assertion",
		"FOR UPDATE SKIP LOCKED", "session_expired",
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
