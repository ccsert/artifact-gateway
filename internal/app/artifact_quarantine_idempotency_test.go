package app

import (
	"net/http"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestArtifactQuarantineTakesPrecedenceOverDistributionIdempotencyReplay(t *testing.T) {
	for _, operation := range []string{"promotions", "replications"} {
		t.Run(operation, func(t *testing.T) {
			fixture := newRawQuarantineDistributionFixture(t)
			key := "quarantine-replay-" + operation
			accepted := fixture.distributionRequest(t, operation, key)
			if accepted.Code != http.StatusAccepted {
				t.Fatalf("accepted=%d body=%s", accepted.Code, accepted.Body.String())
			}
			acceptedBody := accepted.Body.String()

			quarantine := fixture.replaceQuarantine(t, repository.ArtifactQuarantineStateQuarantined, "0")
			denied := fixture.distributionRequest(t, operation, key)
			if denied.Code != http.StatusForbidden || !strings.Contains(denied.Body.String(), `"code":"artifact_quarantined"`) {
				t.Fatalf("quarantined replay=%d body=%s", denied.Code, denied.Body.String())
			}

			fixture.replaceQuarantine(t, repository.ArtifactQuarantineStateReleased, quarantine.Version)
			replayed := fixture.distributionRequest(t, operation, key)
			if replayed.Code != http.StatusAccepted || replayed.Body.String() != acceptedBody {
				t.Fatalf("released replay=%d body=%s want=%s", replayed.Code, replayed.Body.String(), acceptedBody)
			}

			if operation == "promotions" {
				jobs, err := fixture.store.ListLifecycleJobs(fixture.ctx, fixture.target.ID, 10)
				if err != nil || len(jobs) != 1 {
					t.Fatalf("promotion jobs=%#v err=%v", jobs, err)
				}
				return
			}
			plans, err := fixture.store.ListReplicationPlans(fixture.ctx, fixture.target.ID, 10)
			if err != nil || len(plans) != 1 {
				t.Fatalf("replication plans=%#v err=%v", plans, err)
			}
		})
	}
}
