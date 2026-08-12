//go:build integration

package repository

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresWebhookOutboxCommitsWithQuarantineAndClaimsOnce(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
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
	subscription, err := first.CreateWebhookSubscription(ctx, WebhookSubscription{
		ID: uuid.NewString(), Name: "webhook-integration-" + uuid.NewString(), EndpointURL: "https://events.example.test/artifacts",
		SecretCiphertext: "encrypted-secret", EventTypes: []WebhookEventType{WebhookEventArtifactQuarantined, WebhookEventArtifactReleased}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	repo, err := first.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "webhook-integration-" + uuid.NewString(), Format: FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = first.db.ExecContext(context.Background(), `DELETE FROM webhook_subscriptions WHERE id=$1`, subscription.ID)
		_, _ = first.db.ExecContext(context.Background(), `DELETE FROM hosted_repositories WHERE id=$1`, repo.ID)
		_ = first.Close()
		_ = second.Close()
	})

	if _, err = second.UpdateWebhookSubscription(ctx, subscription, "0"); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("cross-connection stale update = %v", err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	quarantined, err := first.ReplaceArtifactQuarantine(ctx, ArtifactQuarantine{
		RepositoryID: repo.ID, Format: FormatRaw, Coordinate: "release/app.tgz", Digest: digest,
		State: ArtifactQuarantineStateQuarantined, Reason: "malware", UpdatedBy: "alice",
	}, "0")
	if err != nil {
		t.Fatal(err)
	}
	deliveries, err := second.ListWebhookDeliveries(ctx, WebhookDeliveryQuery{SubscriptionID: subscription.ID, Limit: 10})
	if err != nil || len(deliveries) != 1 || deliveries[0].EventType != WebhookEventArtifactQuarantined {
		t.Fatalf("transactional delivery = %#v, err=%v", deliveries, err)
	}

	now := time.Now().UTC().Add(time.Second)
	type claimResult struct {
		claims []WebhookDeliveryClaim
		err    error
	}
	results := make(chan claimResult, 2)
	var wait sync.WaitGroup
	for index, store := range []*PostgresStore{first, second} {
		wait.Add(1)
		go func(index int, store *PostgresStore) {
			defer wait.Done()
			claims, claimErr := store.ClaimWebhookDeliveries(ctx, "worker-"+string(rune('a'+index)), now, 30*time.Second, 1)
			results <- claimResult{claims: claims, err: claimErr}
		}(index, store)
	}
	wait.Wait()
	close(results)
	claimed := make([]WebhookDeliveryClaim, 0, 1)
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		claimed = append(claimed, result.claims...)
	}
	if len(claimed) != 1 || claimed[0].Event.ID != deliveries[0].EventID || claimed[0].Subscription.SecretCiphertext != "encrypted-secret" {
		t.Fatalf("cross-connection claims = %#v", claimed)
	}
	if err = first.CompleteWebhookDelivery(ctx, claimed[0].Delivery.ID, claimed[0].Delivery.LeaseOwner, httpStatusNoContent, now); err != nil {
		t.Fatal(err)
	}

	if _, err = first.ReplaceArtifactQuarantine(ctx, ArtifactQuarantine{
		RepositoryID: repo.ID, Format: FormatRaw, Coordinate: quarantined.Coordinate, Digest: quarantined.Digest,
		State: ArtifactQuarantineStateReleased, Reason: "false positive", UpdatedBy: "alice",
	}, quarantined.Version); err != nil {
		t.Fatal(err)
	}
	deliveries, err = second.ListWebhookDeliveries(ctx, WebhookDeliveryQuery{SubscriptionID: subscription.ID, Limit: 10})
	if err != nil || len(deliveries) != 2 || deliveries[0].EventType != WebhookEventArtifactReleased || deliveries[1].State != WebhookDeliverySucceeded {
		t.Fatalf("release deliveries = %#v, err=%v", deliveries, err)
	}
	releaseClaims, err := first.ClaimWebhookDeliveries(ctx, "release-worker", now.Add(30*time.Second), time.Second, 1)
	if err != nil || len(releaseClaims) != 1 || releaseClaims[0].Event.Type != WebhookEventArtifactReleased {
		t.Fatalf("release claims=%#v err=%v", releaseClaims, err)
	}
	if err = first.CompleteWebhookDelivery(ctx, releaseClaims[0].Delivery.ID, "release-worker", httpStatusNoContent, now.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}

	recoveryEvent, err := first.EnqueueWebhookEvent(ctx, WebhookEvent{ID: uuid.NewString(), Type: WebhookEventArtifactQuarantined, OccurredAt: now.Add(time.Minute), Data: []byte(`{"state":"quarantined"}`)})
	if err != nil {
		t.Fatal(err)
	}
	recoveryNow := recoveryEvent.OccurredAt
	var recoveryID string
	for attempt := 1; attempt <= 8; attempt++ {
		claims, claimErr := first.ClaimWebhookDeliveries(ctx, "recovery-worker-"+string(rune('a'+attempt)), recoveryNow, time.Second, 1)
		if claimErr != nil || len(claims) != 1 || claims[0].Delivery.Attempts != attempt {
			t.Fatalf("recovery attempt %d claims=%#v err=%v", attempt, claims, claimErr)
		}
		recoveryID = claims[0].Delivery.ID
		recoveryNow = recoveryNow.Add(time.Second)
	}
	claims, err := second.ClaimWebhookDeliveries(ctx, "recovery-exhausted", recoveryNow, time.Second, 1)
	if err != nil || len(claims) != 0 {
		t.Fatalf("exhausted claims=%#v err=%v", claims, err)
	}
	exhausted, err := second.GetWebhookDelivery(ctx, recoveryID)
	if err != nil || exhausted.State != WebhookDeliveryDead || exhausted.Attempts != 8 || exhausted.LeaseOwner != "" {
		t.Fatalf("exhausted delivery=%#v err=%v", exhausted, err)
	}
}

const httpStatusNoContent = 204
