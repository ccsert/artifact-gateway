package contracts

import "testing"

func TestNuGetPreviewContractKeepsAdmissionSeparateAndDescribesRecoveryFailures(t *testing.T) {
	spec := loadManagementRuntimeSpec(t)
	if err := spec.Components.Schemas["Format"].Value.VisitJSON("nuget"); err == nil {
		t.Fatal("NuGet Hosted preview entered the formal Format enum")
	}
	for _, name := range []string{"DiagnosticRuntime", "RuntimeNode"} {
		schema := spec.Components.Schemas[name].Value
		if err := schema.Properties["workerFormats"].Value.VisitJSON([]any{"nuget"}); err == nil {
			t.Fatalf("%s accepts a preview as an admitted worker format", name)
		}
		preview := schema.Properties["previewWorkerFormats"]
		if preview == nil || preview.Value == nil || preview.Value.VisitJSON([]any{"nuget"}) != nil || preview.Value.VisitJSON([]any{"maven"}) == nil {
			t.Fatalf("%s lacks a separate bounded preview worker contract", name)
		}
	}
	base := "/previews/nuget/repositories/{repositoryId}/publication-sessions"
	create := operation(t, spec, base, "POST")
	if !hasParameter(create.Parameters, "Idempotency-Key", "header") {
		t.Fatal("NuGet publication reservation requires durable idempotency")
	}
	for _, route := range []struct{ path, method string }{
		{base, "POST"},
		{base + "/{sessionId}/package", "PUT"},
		{base + "/{sessionId}/commit", "POST"},
	} {
		requireResponse(t, operation(t, spec, route.path, route.method), "410")
	}
	requireResponse(t, operation(t, spec, base+"/{sessionId}/package", "PUT"), "413")
	native := loadNativeHostedSpec(t)
	for _, method := range []string{"GET", "HEAD"} {
		read := operation(t, native, "/nuget/{repository}/v3/{path}", method)
		for _, status := range []string{"200", "206", "304", "400", "401", "403", "404", "409", "410", "412", "416", "422", "500", "502", "503"} {
			requireResponse(t, read, status)
		}
	}
	push := operation(t, native, "/nuget/{repository}/api/v2/package/", "PUT")
	for _, status := range []string{"201", "400", "401", "403", "404", "405", "409", "410", "413", "503"} {
		requireResponse(t, push, status)
	}
}

func TestNuGetPreviewProxyGroupContractRequiresExplicitVersionsAndReadOnlySources(t *testing.T) {
	spec := loadManagementRuntimeSpec(t)
	for _, route := range []struct {
		path, method string
		responses    []string
	}{
		{"/previews/nuget/repositories/{repositoryId}", "PUT", []string{"200", "400", "401", "403", "404", "409", "503"}},
		{"/previews/nuget/groups", "POST", []string{"201", "400", "401", "403", "409", "503"}},
		{"/previews/nuget/groups/{groupId}", "GET", []string{"200", "401", "403", "404", "409", "503"}},
		{"/previews/nuget/groups/{groupId}", "PUT", []string{"200", "400", "401", "403", "404", "409", "503"}},
		{"/previews/nuget/groups/{groupId}/sources/{packageId}/{version}", "GET", []string{"200", "400", "401", "403", "404", "409", "410", "422", "502", "503"}},
		{"/previews/nuget/groups/{groupId}/sources/{packageId}/{version}/revalidate", "POST", []string{"200", "400", "401", "403", "404", "409", "410", "422", "502", "503"}},
	} {
		for _, status := range route.responses {
			requireResponse(t, operation(t, spec, route.path, route.method), status)
		}
	}
	if err := spec.Components.Schemas["NuGetPreviewRepositoryCreate"].Value.VisitJSON(map[string]any{"name": "private-source", "type": "proxy", "endpoint": "https://packages.example.test/v3/index.json", "upstreamAuth": map[string]any{"scheme": "unknown"}}); err == nil {
		t.Fatal("unsupported source credential scheme entered the contract")
	}
	for _, input := range []map[string]any{{"groupVersion": "1"}, {"repositoryVersion": "2"}} {
		if err := spec.Components.Schemas["NuGetPreviewSourceRevalidation"].Value.VisitJSON(input); err == nil {
			t.Fatal("source revalidation omitted an explicit version")
		}
	}
	base := "/previews/nuget/repositories/{repositoryId}/publication-sessions"
	for _, route := range []struct{ path, method string }{{base, "POST"}, {base + "/{sessionId}", "GET"}, {base + "/{sessionId}/package", "PUT"}, {base + "/{sessionId}/commit", "POST"}} {
		requireResponse(t, operation(t, spec, route.path, route.method), "405")
	}
}

func TestNuGetPreviewReplicationContractAcceptsCompleteItemAndRejectsPlanExtras(t *testing.T) {
	spec := loadManagementRuntimeSpec(t)
	for _, route := range []struct{ path, method string }{{"/previews/nuget/replication-jobs", "POST"}, {"/previews/nuget/replication-jobs/{jobId}", "GET"}, {"/previews/nuget/replication-jobs/{jobId}/retry", "POST"}} {
		op := operation(t, spec, route.path, route.method)
		if route.method == "POST" && !hasParameter(op.Parameters, "Idempotency-Key", "header") {
			t.Fatalf("%s lacks exact idempotency", route.path)
		}
		for _, code := range []string{"401", "403", "404", "503"} {
			requireResponse(t, op, code)
		}
	}
	identity := map[string]any{"packageId": "acme.app", "version": "1.0.0", "digest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "size": float64(123), "sourceGovernanceVersion": "2"}
	item := map[string]any{}
	for k, v := range identity {
		item[k] = v
	}
	item["index"] = float64(0)
	item["state"] = "succeeded"
	item["attempts"] = float64(2)
	item["targetSessionId"] = "33333333-3333-4333-8333-333333333333"
	item["result"] = "published"
	item["listed"] = false
	item["completedAt"] = "2026-10-23T02:00:00Z"
	job := map[string]any{"id": "44444444-4444-4444-8444-444444444444", "planDigest": "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "sourceRepositoryId": "11111111-1111-4111-8111-111111111111", "targetRepositoryId": "22222222-2222-4222-8222-222222222222", "actor": "operator", "version": "7", "state": "succeeded", "items": []any{item}, "createdAt": "2026-10-23T01:00:00Z", "updatedAt": "2026-10-23T02:00:00Z"}
	if err := spec.Components.Schemas["NuGetPreviewReplicationJob"].Value.VisitJSON(job); err != nil {
		t.Fatalf("actual completed job shape rejected: %v", err)
	}
	request := map[string]any{"sourceRepositoryId": job["sourceRepositoryId"], "targetRepositoryId": job["targetRepositoryId"], "items": []any{identity}}
	schema := spec.Components.Schemas["NuGetPreviewReplicationRequest"].Value
	if err := schema.VisitJSON(request); err != nil {
		t.Fatal(err)
	}
	identity["objectKey"] = "must-not-be-client-controlled"
	if err := schema.VisitJSON(request); err == nil {
		t.Fatal("source object-key injection entered a bounded identity")
	}
	delete(identity, "objectKey")
	job["credentialId"] = "must-not-be-exposed"
	if err := spec.Components.Schemas["NuGetPreviewReplicationJob"].Value.VisitJSON(job); err == nil {
		t.Fatal("credential identity escaped the job response")
	}
}
