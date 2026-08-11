package contracts

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestArtifactVulnerabilityFindingContractDescribesEnforcedBounds(t *testing.T) {
	spec, err := openapi3.NewLoader().LoadFromFile(filepath.Join("..", "api", "openapi", "management-runtime-v1.json"))
	if err != nil {
		t.Fatalf("load management OpenAPI: %v", err)
	}
	if err := spec.Validate(openapi3.NewLoader().Context); err != nil {
		t.Fatalf("validate management OpenAPI: %v", err)
	}

	requireSchema := func(name string) *openapi3.Schema {
		t.Helper()
		ref, ok := spec.Components.Schemas[name]
		if !ok || ref == nil || ref.Value == nil {
			t.Fatalf("required schema %q is missing", name)
		}
		return ref.Value
	}
	requireProperty := func(schema *openapi3.Schema, name string) *openapi3.Schema {
		t.Helper()
		ref, ok := schema.Properties[name]
		if !ok || ref == nil || ref.Value == nil {
			t.Fatalf("required property %q is missing", name)
		}
		return ref.Value
	}

	summary := requireSchema("ArtifactVulnerabilitySummary")
	if !strings.Contains(summary.Description, "complete set") || !strings.Contains(summary.Description, "exactly match") {
		t.Fatalf("summary consistency description=%q", summary.Description)
	}
	findings := requireProperty(summary, "findings")
	if findings.MaxItems == nil || *findings.MaxItems != 1000 {
		t.Fatalf("findings maxItems=%v", findings.MaxItems)
	}

	finding := requireSchema("ArtifactVulnerabilityFinding")
	for _, property := range []string{"id", "source", "component", "version", "fixedVersion", "location", "title", "description", "cvssVector"} {
		if schema := requireProperty(finding, property); schema.Pattern == "" {
			t.Errorf("finding property %s must publish its text constraint", property)
		}
	}
	url := requireProperty(finding, "url")
	if url.Pattern != "^https?://" || !strings.Contains(url.Description, "without user information") {
		t.Fatalf("finding URL pattern=%q description=%q", url.Pattern, url.Description)
	}
}
