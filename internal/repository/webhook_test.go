package repository

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMemoryWebhookSubscriptionCASAndDeliveryLifecycle(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Date(2026, 8, 12, 2, 0, 0, 0, time.UTC)
	subscription, err := store.CreateWebhookSubscription(ctx, WebhookSubscription{
		ID: uuid.NewString(), Name: "security-events", EndpointURL: "https://events.example.test/hooks/artifacts",
		SecretCiphertext: "encrypted-secret", EventTypes: []WebhookEventType{WebhookEventArtifactQuarantined}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if subscription.Version != "1" || subscription.SecretCiphertext != "encrypted-secret" {
		t.Fatalf("created subscription = %#v", subscription)
	}

	if _, err = store.UpdateWebhookSubscription(ctx, WebhookSubscription{ID: subscription.ID, Name: "stale", EndpointURL: subscription.EndpointURL, SecretCiphertext: subscription.SecretCiphertext, EventTypes: subscription.EventTypes, Enabled: true}, "0"); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale update error = %v", err)
	}

	event, err := store.EnqueueWebhookEvent(ctx, WebhookEvent{
		ID: uuid.NewString(), Type: WebhookEventArtifactQuarantined, OccurredAt: now,
		Data: []byte(`{"repositoryId":"repo-1","coordinate":"release/app.tgz"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	deliveries, err := store.ListWebhookDeliveries(ctx, WebhookDeliveryQuery{Limit: 10})
	if err != nil || len(deliveries) != 1 || deliveries[0].EventID != event.ID || deliveries[0].State != WebhookDeliveryPending {
		t.Fatalf("pending deliveries = %#v, err=%v", deliveries, err)
	}

	claims, err := store.ClaimWebhookDeliveries(ctx, "worker-a", now, 30*time.Second, 10)
	if err != nil || len(claims) != 1 || claims[0].Delivery.Attempts != 1 || !claims[0].Delivery.NextAttemptAt.IsZero() || claims[0].Subscription.SecretCiphertext != "encrypted-secret" {
		t.Fatalf("claims = %#v, err=%v", claims, err)
	}
	if err = store.FailWebhookDelivery(ctx, claims[0].Delivery.ID, claims[0].Delivery.LeaseToken, now.Add(time.Second), now.Add(5*time.Second), 503, "webhook returned HTTP 503", false); err != nil {
		t.Fatal(err)
	}
	if claims, err = store.ClaimWebhookDeliveries(ctx, "worker-b", now.Add(4*time.Second), 30*time.Second, 10); err != nil || len(claims) != 0 {
		t.Fatalf("early retry claims = %#v, err=%v", claims, err)
	}
	claims, err = store.ClaimWebhookDeliveries(ctx, "worker-b", now.Add(5*time.Second), 30*time.Second, 10)
	if err != nil || len(claims) != 1 || claims[0].Delivery.Attempts != 2 {
		t.Fatalf("retry claims = %#v, err=%v", claims, err)
	}
	if err = store.FailWebhookDelivery(ctx, claims[0].Delivery.ID, claims[0].Delivery.LeaseToken, now.Add(6*time.Second), time.Time{}, 503, "webhook returned HTTP 503", true); err != nil {
		t.Fatal(err)
	}
	replayed, err := store.ReplayWebhookDelivery(ctx, claims[0].Delivery.ID, now.Add(time.Minute))
	if err != nil || replayed.State != WebhookDeliveryPending || replayed.Attempts != 0 || replayed.LastError != "" {
		t.Fatalf("replayed = %#v, err=%v", replayed, err)
	}
	claims, err = store.ClaimWebhookDeliveries(ctx, "worker-c", now.Add(time.Minute), 30*time.Second, 10)
	if err != nil || len(claims) != 1 {
		t.Fatalf("replay claims = %#v, err=%v", claims, err)
	}
	if err = store.CompleteWebhookDelivery(ctx, claims[0].Delivery.ID, claims[0].Delivery.LeaseToken, 204, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	deliveries, err = store.ListWebhookDeliveries(ctx, WebhookDeliveryQuery{Limit: 10})
	if err != nil || len(deliveries) != 1 || deliveries[0].State != WebhookDeliverySucceeded || deliveries[0].LastStatus != 204 {
		t.Fatalf("completed deliveries = %#v, err=%v", deliveries, err)
	}
}

func TestMemoryArtifactQuarantineCreatesMatchingWebhookEventAtomically(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "webhook-quarantine", Format: FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	for _, subscription := range []WebhookSubscription{
		{ID: uuid.NewString(), Name: "matching", EndpointURL: "https://events.example.test/matching", SecretCiphertext: "ciphertext", EventTypes: []WebhookEventType{WebhookEventArtifactQuarantined, WebhookEventArtifactReleased}, Enabled: true},
		{ID: uuid.NewString(), Name: "disabled", EndpointURL: "https://events.example.test/disabled", SecretCiphertext: "ciphertext", EventTypes: []WebhookEventType{WebhookEventArtifactQuarantined}, Enabled: false},
	} {
		if _, err = store.CreateWebhookSubscription(ctx, subscription); err != nil {
			t.Fatal(err)
		}
	}

	quarantined, err := store.ReplaceArtifactQuarantine(ctx, ArtifactQuarantine{
		RepositoryID: repo.ID, Format: FormatRaw, Coordinate: "release/app.tgz", Digest: "sha256:" + strings.Repeat("a", 64),
		State: ArtifactQuarantineStateQuarantined, Reason: "malware", UpdatedBy: "alice",
	}, "0")
	if err != nil {
		t.Fatal(err)
	}
	deliveries, err := store.ListWebhookDeliveries(ctx, WebhookDeliveryQuery{Limit: 10})
	if err != nil || len(deliveries) != 1 || deliveries[0].EventType != WebhookEventArtifactQuarantined {
		t.Fatalf("quarantine deliveries = %#v, err=%v", deliveries, err)
	}
	if _, err = store.ReplaceArtifactQuarantine(ctx, ArtifactQuarantine{
		RepositoryID: repo.ID, Format: FormatRaw, Coordinate: quarantined.Coordinate, Digest: quarantined.Digest,
		State: ArtifactQuarantineStateReleased, Reason: "false positive", UpdatedBy: "alice",
	}, quarantined.Version); err != nil {
		t.Fatal(err)
	}
	deliveries, err = store.ListWebhookDeliveries(ctx, WebhookDeliveryQuery{Limit: 10})
	if err != nil || len(deliveries) != 2 || deliveries[0].EventType != WebhookEventArtifactReleased {
		t.Fatalf("release deliveries = %#v, err=%v", deliveries, err)
	}
}

func TestMemoryWebhookExpiredLeaseIsReclaimedAndAttemptEightStops(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Date(2026, 8, 12, 5, 0, 0, 0, time.UTC)
	subscription, err := store.CreateWebhookSubscription(ctx, WebhookSubscription{
		ID: uuid.NewString(), Name: "lease-recovery", EndpointURL: "https://events.example.test/recovery",
		SecretCiphertext: "encrypted-secret", EventTypes: []WebhookEventType{WebhookEventArtifactQuarantined}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.EnqueueWebhookEvent(ctx, WebhookEvent{ID: uuid.NewString(), Type: WebhookEventArtifactQuarantined, OccurredAt: now, Data: []byte(`{"state":"quarantined"}`)}); err != nil {
		t.Fatal(err)
	}

	var deliveryID string
	for attempt := 1; attempt <= 8; attempt++ {
		claims, claimErr := store.ClaimWebhookDeliveries(ctx, "worker-"+string(rune('a'+attempt)), now, time.Second, 1)
		if claimErr != nil || len(claims) != 1 || claims[0].Delivery.Attempts != attempt || claims[0].Subscription.ID != subscription.ID {
			t.Fatalf("attempt %d claims=%#v err=%v", attempt, claims, claimErr)
		}
		deliveryID = claims[0].Delivery.ID
		now = now.Add(time.Second)
	}
	claims, err := store.ClaimWebhookDeliveries(ctx, "worker-exhausted", now, time.Second, 1)
	if err != nil || len(claims) != 0 {
		t.Fatalf("exhausted claims=%#v err=%v", claims, err)
	}
	delivery, err := store.GetWebhookDelivery(ctx, deliveryID)
	if err != nil || delivery.State != WebhookDeliveryDead || delivery.Attempts != 8 || delivery.LeaseOwner != "" || !delivery.NextAttemptAt.IsZero() {
		t.Fatalf("exhausted delivery=%#v err=%v", delivery, err)
	}
}

func TestMemoryWebhookExpiredLeaseFencesThePreviousOwner(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Date(2026, 8, 12, 6, 0, 0, 0, time.UTC)
	subscription, err := store.CreateWebhookSubscription(ctx, WebhookSubscription{
		ID: uuid.NewString(), Name: "lease-fencing", EndpointURL: "https://events.example.test/fencing",
		SecretCiphertext: "encrypted-secret", EventTypes: []WebhookEventType{WebhookEventArtifactReleased}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.EnqueueWebhookEvent(ctx, WebhookEvent{ID: uuid.NewString(), Type: WebhookEventArtifactReleased, OccurredAt: now, Data: []byte(`{"state":"released"}`)}); err != nil {
		t.Fatal(err)
	}
	first, err := store.ClaimWebhookDeliveries(ctx, "gateway-a/session-old", now, time.Second, 1)
	if err != nil || len(first) != 1 || first[0].Subscription.ID != subscription.ID {
		t.Fatalf("first claim=%#v err=%v", first, err)
	}
	if err = store.CompleteWebhookDelivery(ctx, first[0].Delivery.ID, first[0].Delivery.LeaseToken, 204, now.Add(time.Second)); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("expired token before reclaim completion error=%v", err)
	}
	second, err := store.ClaimWebhookDeliveries(ctx, "gateway-a/session-new", now.Add(time.Second), time.Minute, 1)
	if err != nil || len(second) != 1 || second[0].Delivery.Attempts != 2 {
		t.Fatalf("second claim=%#v err=%v", second, err)
	}
	if err = store.CompleteWebhookDelivery(ctx, first[0].Delivery.ID, first[0].Delivery.LeaseToken, 204, now.Add(2*time.Second)); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("expired token completion error=%v", err)
	}
	if first[0].Delivery.LeaseToken == second[0].Delivery.LeaseToken {
		t.Fatal("reclaimed delivery reused its fencing token")
	}
	if err = store.CompleteWebhookDelivery(ctx, second[0].Delivery.ID, second[0].Delivery.LeaseToken, 204, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
}
