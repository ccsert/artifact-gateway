//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresCargoDistributionWebhooksCommitWithTerminalState(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	first, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewPostgresStore(databaseURL)
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	source, err := first.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "cargo-wh-source-" + suffix, Format: FormatCargo})
	if err != nil {
		t.Fatal(err)
	}
	target, err := first.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "cargo-wh-target-" + suffix, Format: FormatCargo})
	if err != nil {
		t.Fatal(err)
	}
	subscription, err := first.CreateWebhookSubscription(ctx, WebhookSubscription{
		ID: uuid.NewString(), Name: "cargo-wh-" + suffix, EndpointURL: "https://events.example.test/cargo",
		SecretCiphertext: "encrypted-secret", Enabled: true,
		EventTypes: []WebhookEventType{
			WebhookEventCargoPromotionCompleted, WebhookEventCargoPromotionFailed,
			WebhookEventCargoReplicationCompleted, WebhookEventCargoReplicationFailed,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var operationIDs []string
	t.Cleanup(func() {
		_, _ = first.db.ExecContext(context.Background(), `DELETE FROM webhook_subscriptions WHERE id=$1`, subscription.ID)
		for _, operationID := range operationIDs {
			_, _ = first.db.ExecContext(context.Background(), `DELETE FROM webhook_events WHERE payload->>'operationId'=$1`, operationID)
		}
		_, _ = first.db.ExecContext(context.Background(), `DELETE FROM replication_plans WHERE source_repository_id=$1 OR target_repository_id=$2`, source.ID, target.ID)
		_, _ = first.db.ExecContext(context.Background(), `DELETE FROM lifecycle_jobs WHERE repository_id=$1`, target.ID)
		_, _ = first.db.ExecContext(context.Background(), `DELETE FROM hosted_repositories WHERE id IN ($1,$2)`, source.ID, target.ID)
		_ = first.Close()
		_ = second.Close()
	})

	digest := "sha256:" + strings.Repeat("a", 64)
	payload, _ := json.Marshal(map[string]string{"format": "cargo", "sourceRepositoryId": source.ID, "name": "demo", "version": "1.0.0", "digest": digest})
	newJob := func(key string, maxAttempts int) LifecycleJob {
		job, replayed, createErr := first.EnqueueLifecycleJob(ctx, LifecycleJob{
			ID: uuid.NewString(), RepositoryID: target.ID, Kind: LifecycleJobPromotion,
			IdempotencyKey: key, Payload: payload, MaxAttempts: maxAttempts,
		})
		if createErr != nil || replayed {
			t.Fatalf("enqueue job replayed=%t err=%v", replayed, createErr)
		}
		operationIDs = append(operationIDs, job.ID)
		return job
	}
	newPlan := func(key string, maxAttempts int) ReplicationPlan {
		plan, replayed, createErr := first.CreateReplicationPlan(ctx, ReplicationPlan{
			ID: uuid.NewString(), SourceRepositoryID: source.ID, TargetRepositoryID: target.ID,
			Format: FormatCargo, Coordinate: "demo@1.0.0", Digest: digest,
			IdempotencyKey: key, MaxAttempts: maxAttempts,
		}, []ReplicationCheckpoint{{ObjectKey: "native/cargo/demo", Digest: digest, Size: 8}})
		if createErr != nil || replayed {
			t.Fatalf("create plan replayed=%t err=%v", replayed, createErr)
		}
		operationIDs = append(operationIDs, plan.ID)
		return plan
	}
	assert := func(eventType WebhookEventType, operationID, state string, attempts, total int) {
		t.Helper()
		deliveries, listErr := second.ListWebhookDeliveries(ctx, WebhookDeliveryQuery{SubscriptionID: subscription.ID, Limit: 20})
		if listErr != nil || len(deliveries) != total {
			t.Fatalf("deliveries=%+v err=%v want=%d", deliveries, listErr, total)
		}
		var eventCount int
		if err := second.db.QueryRowContext(ctx, `SELECT count(*) FROM webhook_events WHERE payload->>'operationId'=$1 AND event_type=$2`, operationID, eventType).Scan(&eventCount); err != nil || eventCount != 1 {
			t.Fatalf("event count=%d err=%v", eventCount, err)
		}
		for _, delivery := range deliveries {
			if delivery.EventType != eventType {
				continue
			}
			var raw []byte
			if err := second.db.QueryRowContext(ctx, `SELECT payload FROM webhook_events WHERE id=$1`, delivery.EventID).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var data CargoDistributionWebhookData
			if err := json.Unmarshal(raw, &data); err != nil {
				t.Fatal(err)
			}
			if data.OperationID == operationID && data.SourceRepositoryID == source.ID && data.TargetRepositoryID == target.ID && data.Coordinate == "demo@1.0.0" && data.Digest == digest && data.Format == FormatCargo && data.State == state && data.Attempts == attempts && !strings.Contains(string(raw), "private failure detail") && delivery.State == WebhookDeliveryPending {
				return
			}
		}
		t.Fatalf("missing %s for %s", eventType, operationID)
	}
	assertNoNew := func(total int) {
		t.Helper()
		deliveries, listErr := second.ListWebhookDeliveries(ctx, WebhookDeliveryQuery{SubscriptionID: subscription.ID, Limit: 20})
		if listErr != nil || len(deliveries) != total {
			t.Fatalf("premature deliveries=%+v err=%v want=%d", deliveries, listErr, total)
		}
	}

	completedJob := newJob("promotion-success-"+suffix, 1)
	jobs, err := first.ClaimLifecycleJobsByKindAndFormat(ctx, LifecycleJobPromotion, FormatCargo, 1)
	if err != nil || len(jobs) != 1 || jobs[0].ID != completedJob.ID {
		t.Fatalf("claim promotion=%+v err=%v", jobs, err)
	}
	if err := first.CompleteLifecycleJob(ctx, completedJob.ID, jobs[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err := first.CompleteLifecycleJob(ctx, completedJob.ID, jobs[0].LeaseToken); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale completion=%v", err)
	}
	assert(WebhookEventCargoPromotionCompleted, completedJob.ID, "completed", 1, 1)

	failedJob := newJob("promotion-failed-"+suffix, 1)
	jobs, err = first.ClaimLifecycleJobsByKindAndFormat(ctx, LifecycleJobPromotion, FormatCargo, 1)
	if err != nil || len(jobs) != 1 || jobs[0].ID != failedJob.ID {
		t.Fatalf("claim failed promotion=%+v err=%v", jobs, err)
	}
	if err := first.FailLifecycleJob(ctx, failedJob.ID, jobs[0].LeaseToken, "private failure detail"); err != nil {
		t.Fatal(err)
	}
	assert(WebhookEventCargoPromotionFailed, failedJob.ID, "failed", 1, 2)

	completedPlan := newPlan("replication-success-"+suffix, 1)
	plans, err := first.ClaimReplicationPlansByFormat(ctx, FormatCargo, 1)
	if err != nil || len(plans) != 1 || plans[0].ID != completedPlan.ID {
		t.Fatalf("claim replication=%+v err=%v", plans, err)
	}
	if err := first.CompleteReplicationPlanWithLease(ctx, completedPlan.ID, plans[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	assert(WebhookEventCargoReplicationCompleted, completedPlan.ID, "completed", 1, 3)

	failedPlan := newPlan("replication-failed-"+suffix, 2)
	plans, err = first.ClaimReplicationPlansByFormat(ctx, FormatCargo, 1)
	if err != nil || len(plans) != 1 || plans[0].ID != failedPlan.ID {
		t.Fatalf("claim failed replication=%+v err=%v", plans, err)
	}
	if err := first.FailReplicationPlanWithLease(ctx, failedPlan.ID, "temporary error", plans[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	assertNoNew(3)
	plans, err = first.ClaimReplicationPlansByFormat(ctx, FormatCargo, 1)
	if err != nil || len(plans) != 1 || plans[0].ID != failedPlan.ID || plans[0].Attempts != 2 {
		t.Fatalf("retry replication=%+v err=%v", plans, err)
	}
	if err := first.FailReplicationPlanWithLease(ctx, failedPlan.ID, "private failure detail", plans[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	assert(WebhookEventCargoReplicationFailed, failedPlan.ID, "failed", 2, 4)
}
