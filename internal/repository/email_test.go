package repository

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"testing"
)

func TestEmailTestQueueIsIdempotentAndFencesWorkers(t *testing.T) {
	exerciseEmailQueue(t, NewMemoryStore())
}
func exerciseEmailQueue(t *testing.T, s EmailStore) {
	t.Helper()
	ctx := context.Background()
	target, err := s.CreateEmailTarget(ctx, EmailTarget{ID: uuid.NewString(), Name: "Synthetic", Locale: "en", RecipientCiphertext: "synthetic-ciphertext", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	req := EmailTestRequest{ID: uuid.NewString(), EventID: uuid.NewString(), RequestKey: uuid.NewString(), TargetID: target.ID, TargetVersion: target.Version, Scenario: "warning", TemplateVersion: "1", From: "gateway@example.test", ConsoleOrigin: "https://console.example.invalid"}
	v, err := s.EnqueueEmailTest(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if v.State != "pending" || v.RecipientCiphertext != target.RecipientCiphertext {
		t.Fatal("no immutable pending snapshot")
	}
	again, err := s.EnqueueEmailTest(ctx, req)
	if err != nil || again.ID != v.ID {
		t.Fatal("duplicate request created another event")
	}
	changed := req
	changed.Scenario = "critical"
	if _, err = s.EnqueueEmailTest(ctx, changed); !errors.Is(err, ErrEmailIdempotencyConflict) {
		t.Fatal("conflicting idempotency key accepted")
	}
	first, err := s.ClaimEmailDelivery(ctx, "worker-one/session")
	if err != nil || first.Attempts != 1 {
		t.Fatalf("claim=%+v err=%v", first, err)
	}
	if _, err = s.ClaimEmailDelivery(ctx, "worker-two/session"); !errors.Is(err, ErrNotFound) {
		t.Fatal("two workers own the same delivery")
	}
	if err = s.FinishEmailDelivery(ctx, first.ID, uuid.NewString(), EmailAttemptResult{}); !errors.Is(err, ErrVersionConflict) {
		t.Fatal("invalid fencing token completed delivery")
	}
	if err = s.FinishEmailDelivery(ctx, first.ID, first.LeaseToken, EmailAttemptResult{Code: "outcome_unknown", OutcomeUnknown: true}); err != nil {
		t.Fatal(err)
	}
	retry, err := s.GetEmailDelivery(ctx, v.ID)
	if err != nil || retry.State != "retrying" || !retry.PossibleDuplicate || retry.ErrorCode != "outcome_unknown" {
		t.Fatalf("lost ambiguous outcome: %+v %v", retry, err)
	}
	if _, err = s.ReplayEmailDelivery(ctx, v.ID, retry.Version); !errors.Is(err, ErrEmailInvalidState) {
		t.Fatal("replayed a live delivery")
	}
}
