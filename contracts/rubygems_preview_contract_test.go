package contracts

import (
	"github.com/getkin/kin-openapi/openapi3"
	"testing"
)

func TestRubyGemsPreviewContractSeparatesWorkerPlacementFromFormalAdmission(t *testing.T) {
	spec := loadManagementRuntimeSpec(t)
	if err := spec.Components.Schemas["Format"].Value.VisitJSON("rubygems"); err == nil {
		t.Fatal("RubyGems Hosted preview entered the admitted format catalog")
	}
	for _, name := range []string{"DiagnosticRuntime", "RuntimeNode"} {
		schema := spec.Components.Schemas[name].Value
		if err := schema.Properties["workerFormats"].Value.VisitJSON([]any{"rubygems"}); err == nil {
			t.Fatalf("%s accepts a preview as a formal worker format", name)
		}
		preview := schema.Properties["previewWorkerFormats"].Value
		if preview.VisitJSON([]any{"rubygems"}) != nil || preview.VisitJSON([]any{"maven"}) == nil {
			t.Fatalf("%s must keep preview worker placement separate", name)
		}
	}
	create := operation(t, spec, "/previews/rubygems/repositories", "POST")
	input := create.RequestBody.Value.Content["application/json"].Schema.Value
	if err := input.VisitJSON(map[string]any{"name": "owned-gems", "anonymousRead": false}); err != nil {
		t.Fatal(err)
	}
	if err := input.VisitJSON(map[string]any{"name": "owned-gems", "type": "proxy"}); err == nil {
		t.Fatal("Hosted preview contract permits unsupported Proxy provisioning")
	}
}

func TestRubyGemsNativeContractDescribesAuthenticationAndReadMetadataConsistently(t *testing.T) {
	spec := loadNativeHostedSpec(t)
	push := operation(t, spec, "/rubygems/{repository}/api/v1/gems", "POST")
	if push.Security == nil || !hasSecurityScheme(*push.Security, "rubyGemsApiKey") || !hasSecurityScheme(*push.Security, "protocolBasicAuth") {
		t.Fatal("native gem publication lacks its actual API-key and Basic authentication contract")
	}
	for _, response := range []string{"201", "400", "401", "403", "409", "413", "500"} {
		requireResponse(t, push, response)
	}
	for _, path := range []string{
		"/rubygems/{repository}/{index}",
		"/rubygems/{repository}/info/{gemName}",
		"/rubygems/{repository}/gems/{gemFile}",
		"/rubygems/{repository}/quick/Marshal.4.8/{gemspecFile}",
	} {
		for _, method := range []string{"GET", "HEAD"} {
			op := operation(t, spec, path, method)
			if op.Security == nil || !hasSecurityScheme(*op.Security, "rubyGemsApiKey") || !hasSecurityScheme(*op.Security, "protocolBasicAuth") || !hasSecurityScheme(*op.Security, "protocolBearerAuth") {
				t.Fatalf("%s %s does not describe current native authentication", method, path)
			}
			anonymous := false
			for _, requirement := range *op.Security {
				anonymous = anonymous || len(requirement) == 0
			}
			if !anonymous {
				t.Fatalf("%s %s omits explicitly enabled anonymous reads", method, path)
			}
			for _, response := range []string{"200", "304", "401", "403", "404"} {
				requireResponse(t, op, response)
			}
		}
	}
}

func TestRubyGemsQuarantineContractRequiresIndependentStateAndPolicyCAS(t *testing.T) {
	for _, spec := range []struct {
		name string
		load func(*testing.T) *openapi3.T
	}{
		{"management", loadManagementRuntimeSpec}, {"native", loadNativeHostedSpec},
	} {
		t.Run(spec.name, func(t *testing.T) {
			doc := spec.load(t)
			governance := doc.Components.Schemas["RubyGemsPreviewGovernance"].Value
			if governance.Properties["quarantined"] == nil {
				t.Fatal("missing independent quarantine state")
			}
			change := doc.Components.Schemas["RubyGemsPreviewGovernanceChange"].Value
			for _, action := range []string{"yank", "restore", "quarantine", "release"} {
				if err := change.Properties["action"].Value.VisitJSON(action); err != nil {
					t.Fatal(action, err)
				}
			}
			path := "/previews/rubygems/repositories/{repositoryId}/quarantine-read-policy"
			operation(t, doc, path, "GET")
			put := operation(t, doc, path, "PUT")
			for _, code := range []string{"200", "400", "401", "403", "404", "409", "500"} {
				requireResponse(t, put, code)
			}
			input := put.RequestBody.Value.Content["application/json"].Schema.Value
			if err := input.VisitJSON(map[string]any{"expectedVersion": "1", "enabled": true}); err != nil {
				t.Fatal(err)
			}
			for _, bad := range []map[string]any{{"enabled": true}, {"expectedVersion": "1"}, {"expectedVersion": "0", "enabled": true}, {"expectedVersion": "1", "enabled": true, "format": "rubygems"}} {
				if err := input.VisitJSON(bad); err == nil {
					t.Fatal("invalid policy admitted", bad)
				}
			}
		})
	}
}
