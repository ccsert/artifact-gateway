package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestReplicationResponseMappersExposeCompleteArtifactIdentity(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	plan := repository.ReplicationPlan{
		ID:                 uuid.NewString(),
		SourceRepositoryID: uuid.NewString(),
		TargetRepositoryID: uuid.NewString(),
		Format:             repository.FormatRaw,
		Coordinate:         "releases/widget.tar.gz",
		Digest:             digest,
		State:              "pending",
		CreatedAt:          time.Now().UTC(),
	}

	summary := toOpenAPIReplicationPlan(plan)
	if summary.Coordinate == nil || *summary.Coordinate != plan.Coordinate || summary.Digest == nil || *summary.Digest != digest {
		t.Fatalf("summary identity coordinate=%v digest=%v", summary.Coordinate, summary.Digest)
	}
	detail := toOpenAPIReplicationPlanDetail(plan, nil)
	if detail.Coordinate == nil || *detail.Coordinate != plan.Coordinate || detail.Digest == nil || *detail.Digest != digest {
		t.Fatalf("detail identity coordinate=%v digest=%v", detail.Coordinate, detail.Digest)
	}
}

func TestReplicationResponseMappersOmitLegacyEmptyArtifactIdentity(t *testing.T) {
	plan := repository.ReplicationPlan{
		ID:                 uuid.NewString(),
		SourceRepositoryID: uuid.NewString(),
		TargetRepositoryID: uuid.NewString(),
		Format:             repository.FormatRaw,
		State:              "completed",
		CreatedAt:          time.Now().UTC(),
	}

	for name, response := range map[string]any{
		"summary": toOpenAPIReplicationPlan(plan),
		"detail":  toOpenAPIReplicationPlanDetail(plan, nil),
	} {
		body, err := json.Marshal(response)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		if strings.Contains(string(body), `"coordinate"`) || strings.Contains(string(body), `"digest"`) {
			t.Fatalf("legacy %s exposed an incomplete identity: %s", name, body)
		}
	}
}
