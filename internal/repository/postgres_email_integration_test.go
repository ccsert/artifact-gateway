//go:build integration

package repository

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"os"
	"sync"
	"testing"
)

func TestPostgresEmailQueueFencesWorkers(t *testing.T) {
	connection := os.Getenv("TEST_DATABASE_URL")
	if connection == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	store, err := NewPostgresStore(connection)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if _, err = store.db.ExecContext(context.Background(), `TRUNCATE repository_quota_alert_events,email_test_deliveries,repository_quota_alert_rules,email_targets,email_test_requests`); err != nil {
		t.Fatal(err)
	}
	exerciseEmailQueue(t, store)
}

func TestPostgresEmailConcurrentLimitRestartExhaustionAndReplay(t *testing.T) {
	connection := os.Getenv("TEST_DATABASE_URL")
	if connection == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	ctx := context.Background()
	first, err := NewPostgresStore(connection)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	second, err := NewPostgresStore(connection)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	if _, err = first.db.Exec(`TRUNCATE repository_quota_alert_events,email_test_deliveries,repository_quota_alert_rules,email_targets,email_test_requests`); err != nil {
		t.Fatal(err)
	}
	target, err := first.CreateEmailTarget(ctx, EmailTarget{ID: uuid.NewString(), Name: "Synthetic", Locale: "en", RecipientCiphertext: "immutable", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := first
			if i%2 == 1 {
				s = second
			}
			_, e := s.EnqueueEmailTest(ctx, EmailTestRequest{ID: uuid.NewString(), EventID: uuid.NewString(), RequestKey: uuid.NewString(), TargetID: target.ID, TargetVersion: target.Version, Scenario: "critical", TemplateVersion: "1", From: "gateway@example.test", ConsoleOrigin: "https://console.example.invalid"})
			results <- e
		}(i)
	}
	wg.Wait()
	close(results)
	accepted, limited := 0, 0
	for e := range results {
		if e == nil {
			accepted++
		} else if errors.Is(e, ErrEmailRateLimited) {
			limited++
		} else {
			t.Fatal(e)
		}
	}
	if accepted != 5 || limited != 5 {
		t.Fatalf("global rate outcomes=%d/%d", accepted, limited)
	}
	var delivery EmailDelivery
	// Finish other deliveries so this test owns one live delivery for recovery.
	for i := 0; i < 5; i++ {
		v, e := first.ClaimEmailDelivery(ctx, "initial/session")
		if e != nil {
			t.Fatal(e)
		}
		if i == 0 {
			delivery = v
		} else if e = first.FinishEmailDelivery(ctx, v.ID, v.LeaseToken, EmailAttemptResult{}); e != nil {
			t.Fatal(e)
		}
	}
	if _, err = first.db.Exec(`UPDATE email_test_deliveries SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, delivery.ID); err != nil {
		t.Fatal(err)
	}
	replacement, e := second.ClaimEmailDelivery(ctx, "replacement/session")
	if e != nil || replacement.ID != delivery.ID || !replacement.PossibleDuplicate || replacement.Attempts != 2 {
		t.Fatalf("recovery=%+v err=%v", replacement, e)
	}
	if e = first.FinishEmailDelivery(ctx, delivery.ID, delivery.LeaseToken, EmailAttemptResult{}); !errors.Is(e, ErrVersionConflict) {
		t.Fatal("expired owner mutated new lease")
	}
	for attempt := 2; attempt <= EmailMaxAttempts; attempt++ {
		if e = second.FinishEmailDelivery(ctx, replacement.ID, replacement.LeaseToken, EmailAttemptResult{Code: "smtp_temporary_rejection"}); e != nil {
			t.Fatal(e)
		}
		if attempt < EmailMaxAttempts {
			if _, e = first.db.Exec(`UPDATE email_test_deliveries SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE id=$1`, delivery.ID); e != nil {
				t.Fatal(e)
			}
			replacement, e = first.ClaimEmailDelivery(ctx, "next/session")
			if e != nil || replacement.Attempts != attempt+1 {
				t.Fatal("bounded retry failed")
			}
		}
	}
	dead, e := second.GetEmailDelivery(ctx, delivery.ID)
	if e != nil || dead.State != "dead" || dead.Attempts != 8 || !dead.PossibleDuplicate {
		t.Fatalf("exhaustion=%+v %v", dead, e)
	}
	if _, e = second.ReplayEmailDelivery(ctx, dead.ID, "0"); !errors.Is(e, ErrVersionConflict) {
		t.Fatal("replay ignored CAS")
	}
	if _, e = second.ReplayEmailDelivery(ctx, dead.ID, dead.Version); !errors.Is(e, ErrEmailRateLimited) {
		t.Fatal("replay escaped global rate")
	}
	if _, e = first.db.Exec(`UPDATE email_test_requests SET created_at=clock_timestamp()-interval '2 minutes'`); e != nil {
		t.Fatal(e)
	}
	replay, e := second.ReplayEmailDelivery(ctx, dead.ID, dead.Version)
	if e != nil || replay.EventID != dead.EventID || replay.RecipientCiphertext != dead.RecipientCiphertext || replay.State != "pending" || replay.Attempts != 0 || !replay.PossibleDuplicate {
		t.Fatalf("replay=%+v %v", replay, e)
	}
	claimed, e := first.ClaimEmailDelivery(ctx, "replay/session")
	if e != nil {
		t.Fatal(e)
	}
	if e = first.FinishEmailDelivery(ctx, claimed.ID, claimed.LeaseToken, EmailAttemptResult{Code: "smtp_permanent_rejection", Permanent: true}); e != nil {
		t.Fatal(e)
	}
	final, e := second.GetEmailDelivery(ctx, delivery.ID)
	if e != nil {
		t.Fatal(e)
	}
	target.Enabled = false
	if _, e = first.UpdateEmailTarget(ctx, target, target.Version); e != nil {
		t.Fatal(e)
	}
	if _, e = second.ReplayEmailDelivery(ctx, final.ID, final.Version); !errors.Is(e, ErrVersionConflict) {
		t.Fatal("replay silently changed target version")
	}
}
