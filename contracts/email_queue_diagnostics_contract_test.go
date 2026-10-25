package contracts

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/getkin/kin-openapi/openapi3"
)

func TestEmailQueueDiagnosticsBothContractsAcceptStoreEvidence(t *testing.T) {
	snapshot, err := repository.NewMemoryStore().EmailQueueDiagnostics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var payload any
	if err = json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	for _, spec := range []*openapi3.T{loadManagementRuntimeSpec(t), loadNativeHostedSpec(t)} {
		op := operation(t, spec, "/email-queue/diagnostics", "GET")
		if op.OperationID != "getEmailQueueDiagnostics" {
			t.Fatalf("operation=%s", op.OperationID)
		}
		response := requireResponse(t, op, "200")
		if response.Headers["Cache-Control"] == nil {
			t.Fatal("missing no-store header contract")
		}
		schema := response.Content.Get("application/json").Schema.Value
		if err = schema.VisitJSON(payload); err != nil {
			t.Fatalf("store response violates contract: %v", err)
		}
		// A code path that leaked a recipient must fail the closed response schema.
		unsafe := payload.(map[string]any)
		unsafe["recipient"] = "private@example.test"
		if err = schema.VisitJSON(unsafe); err == nil {
			t.Fatal("schema permits private extra fields")
		}
		delete(unsafe, "recipient")
		for _, status := range []string{"401", "403", "500"} {
			requireResponse(t, op, status)
		}
	}
}
