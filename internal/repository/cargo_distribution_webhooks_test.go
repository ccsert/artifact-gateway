package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMemoryCargoDistributionEventsFollowTerminalStates(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	subscription, err := store.CreateWebhookSubscription(ctx, WebhookSubscription{
		ID: uuid.NewString(), Name: "cargo-distribution", EndpointURL: "https://events.example.test/cargo",
		SecretCiphertext: "encrypted-secret", Enabled: true,
		EventTypes: []WebhookEventType{
			WebhookEventCargoPromotionCompleted, WebhookEventCargoPromotionFailed,
			WebhookEventCargoReplicationCompleted, WebhookEventCargoReplicationFailed,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	sourceID, targetID := uuid.NewString(), uuid.NewString()
	payload := []byte(`{"format":"cargo","sourceRepositoryId":"` + sourceID + `","name":"demo","version":"1.0.0","digest":"` + digest + `"}`)
	job, _, err := store.EnqueueLifecycleJob(ctx, LifecycleJob{
		ID: uuid.NewString(), RepositoryID: targetID, Kind: LifecycleJobPromotion,
		IdempotencyKey: "promotion-success", Payload: payload, MaxAttempts: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimLifecycleJobsByKindAndFormat(ctx, LifecycleJobPromotion, FormatCargo, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("promotion claim=%+v err=%v", claimed, err)
	}
	if err := store.CompleteLifecycleJob(ctx, job.ID, claimed[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteLifecycleJob(ctx, job.ID, claimed[0].LeaseToken); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale completion=%v", err)
	}
	assertCargoWebhookDelivery(t, store, subscription.ID, WebhookEventCargoPromotionCompleted, job.ID, "completed", 1, 1)

	failedJob, _, err := store.EnqueueLifecycleJob(ctx, LifecycleJob{
		ID: uuid.NewString(), RepositoryID: targetID, Kind: LifecycleJobPromotion,
		IdempotencyKey: "promotion-failure", Payload: payload, MaxAttempts: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err = store.ClaimLifecycleJobsByKindAndFormat(ctx, LifecycleJobPromotion, FormatCargo, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != failedJob.ID {
		t.Fatalf("failure claim=%+v err=%v", claimed, err)
	}
	if err := store.FailLifecycleJob(ctx, failedJob.ID, claimed[0].LeaseToken, "temporary dependency error"); err != nil {
		t.Fatal(err)
	}
	assertCargoWebhookDelivery(t, store, subscription.ID, WebhookEventCargoPromotionCompleted, job.ID, "completed", 1, 1)
	if _, err := store.RunLifecycleJobNow(ctx, targetID, failedJob.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err = store.ClaimLifecycleJobsByKindAndFormat(ctx, LifecycleJobPromotion, FormatCargo, 1)
	if err != nil || len(claimed) != 1 || claimed[0].Attempts != 2 {
		t.Fatalf("final failure claim=%+v err=%v", claimed, err)
	}
	if err := store.FailLifecycleJob(ctx, failedJob.ID, claimed[0].LeaseToken, "final dependency error"); err != nil {
		t.Fatal(err)
	}
	assertCargoWebhookDelivery(t, store, subscription.ID, WebhookEventCargoPromotionFailed, failedJob.ID, "failed", 2, 2)

	newPlan := func(key string, maxAttempts int) ReplicationPlan {
		plan, _, createErr := store.CreateReplicationPlan(ctx, ReplicationPlan{
			ID: uuid.NewString(), SourceRepositoryID: sourceID, TargetRepositoryID: targetID,
			Format: FormatCargo, Coordinate: "demo@1.0.0", Digest: digest,
			IdempotencyKey: key, MaxAttempts: maxAttempts,
		}, []ReplicationCheckpoint{{ObjectKey: "native/cargo/sha256/" + strings.Repeat("a", 64), Digest: digest, Size: 8}})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return plan
	}
	completedPlan := newPlan("replication-success", 1)
	plans, err := store.ClaimReplicationPlansByFormat(ctx, FormatCargo, 1)
	if err != nil || len(plans) != 1 || plans[0].ID != completedPlan.ID {
		t.Fatalf("completion claim=%+v err=%v", plans, err)
	}
	if err := store.CompleteReplicationPlanWithLease(ctx, completedPlan.ID, plans[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	assertCargoWebhookDelivery(t, store, subscription.ID, WebhookEventCargoReplicationCompleted, completedPlan.ID, "completed", 1, 3)

	failedPlan := newPlan("replication-failure", 2)
	plans, err = store.ClaimReplicationPlansByFormat(ctx, FormatCargo, 1)
	if err != nil || len(plans) != 1 || plans[0].ID != failedPlan.ID {
		t.Fatalf("failure claim=%+v err=%v", plans, err)
	}
	if err := store.FailReplicationPlanWithLease(ctx, failedPlan.ID, "temporary copy error", plans[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	assertCargoWebhookDelivery(t, store, subscription.ID, WebhookEventCargoReplicationCompleted, completedPlan.ID, "completed", 1, 3)
	plans, err = store.ClaimReplicationPlansByFormat(ctx, FormatCargo, 1)
	if err != nil || len(plans) != 1 || plans[0].ID != failedPlan.ID || plans[0].Attempts != 2 {
		t.Fatalf("final failure claim=%+v err=%v", plans, err)
	}
	if err := store.FailReplicationPlanWithLease(ctx, failedPlan.ID, "final copy error", plans[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	assertCargoWebhookDelivery(t, store, subscription.ID, WebhookEventCargoReplicationFailed, failedPlan.ID, "failed", 2, 4)
}

func assertCargoWebhookDelivery(t *testing.T, store *MemoryStore, subscriptionID string, eventType WebhookEventType, operationID, state string, attempts, expectedTotal int) {
	t.Helper()
	ctx := context.Background()
	deliveries, err := store.ListWebhookDeliveries(ctx, WebhookDeliveryQuery{SubscriptionID: subscriptionID, Limit: 20})
	if err != nil || len(deliveries) != expectedTotal {
		t.Fatalf("deliveries=%+v err=%v, want %d", deliveries, err, expectedTotal)
	}
	for _, delivery := range deliveries {
		if delivery.EventType != eventType {
			continue
		}
		var data CargoDistributionWebhookData
		if err := json.Unmarshal(store.webhookEvents[delivery.EventID].Data, &data); err != nil {
			t.Fatal(err)
		}
		if data.OperationID == operationID && data.State == state && data.Attempts == attempts && data.Format == FormatCargo &&
			data.Coordinate == "demo@1.0.0" && data.Digest == "sha256:"+strings.Repeat("a", 64) && delivery.State == WebhookDeliveryPending {
			return
		}
	}
	t.Fatalf("missing %s for operation %s in %+v", eventType, operationID, deliveries)
}

func TestMemoryCargoPromotionLeaseExpiryEmitsOnlyFinalFailure(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	_, err := store.CreateWebhookSubscription(ctx, WebhookSubscription{ID: uuid.NewString(), Name: "expiry", EndpointURL: "https://events.example.test/cargo", SecretCiphertext: "encrypted-secret", Enabled: true, EventTypes: []WebhookEventType{WebhookEventCargoPromotionFailed}})
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := store.EnqueueLifecycleJob(ctx, LifecycleJob{ID: uuid.NewString(), RepositoryID: uuid.NewString(), Kind: LifecycleJobPromotion, IdempotencyKey: "expiry", Payload: []byte(`{"format":"cargo"}`), MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimLifecycleJobsByKindAndFormat(ctx, LifecycleJobPromotion, FormatCargo, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	if count, err := store.RecoverExpiredLifecycleJobs(ctx, claimed[0].LeaseExpiresAt.Add(time.Second)); err != nil || count != 1 {
		t.Fatalf("recover count=%d err=%v", count, err)
	}
	if failed, err := store.GetLifecycleJob(ctx, job.RepositoryID, job.ID); err != nil || failed.State != LifecycleJobFailed {
		t.Fatalf("failed=%+v err=%v", failed, err)
	}
	deliveries, err := store.ListWebhookDeliveries(ctx, WebhookDeliveryQuery{Limit: 10})
	if err != nil || len(deliveries) != 1 || deliveries[0].EventType != WebhookEventCargoPromotionFailed {
		t.Fatalf("expiry deliveries=%+v err=%v", deliveries, err)
	}
}
