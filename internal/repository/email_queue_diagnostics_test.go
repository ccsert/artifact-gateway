package repository

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Fixture writes establish otherwise unreachable expired leases and legacy errors;
// assertions exercise the public diagnostics and delivery-read boundaries.
func emailDiagnosticsFixture(now time.Time) []EmailDelivery {
	rows := make([]EmailDelivery, 60)
	for i := range rows {
		rows[i] = EmailDelivery{State: "pending"}
	}
	rows = append(rows,
		EmailDelivery{State: "retrying", Attempts: 1, NextAttemptAt: now.Add(time.Hour), ErrorCode: "smtp_temporary_rejection"},
		EmailDelivery{State: "delivering", Attempts: 1, LeaseExpiresAt: now.Add(time.Hour), ErrorCode: "authentication_failed"},
		EmailDelivery{State: "delivering", Attempts: 2, LeaseExpiresAt: now.Add(-5 * time.Minute), NextAttemptAt: now.Add(time.Hour)},
		EmailDelivery{State: "accepted", AcceptedAt: now.Add(-time.Minute)},
		EmailDelivery{State: "dead", ErrorCode: "RAW private@example.test smtp://secret password body keyDigest leaseToken"},
		EmailDelivery{State: "pending", NextAttemptAt: now.Add(time.Hour), AutomaticCancellationCode: "rule_disabled"},
		EmailDelivery{State: "retrying", Attempts: 8, ErrorCode: "smtp_transport_failed"},
		EmailDelivery{State: "delivering", Attempts: 8, LeaseExpiresAt: now.Add(-time.Minute), AutomaticCancellationCode: "rule_deleted", ErrorCode: "outcome_unknown"},
	)
	rule, cleanupRule := uuid.NewString(), uuid.NewString()
	rows = append(rows,
		EmailDelivery{Kind: "repository_quota", QuotaRuleID: rule, EventSequence: 1, State: "retrying", NextAttemptAt: now.Add(time.Hour)},
		EmailDelivery{Kind: "repository_quota", QuotaRuleID: rule, EventSequence: 2, State: "pending"},
		EmailDelivery{Kind: "repository_quota", QuotaRuleID: cleanupRule, EventSequence: 1, State: "pending", NextAttemptAt: now.Add(time.Hour), AutomaticCancellationCode: "rule_changed"},
		EmailDelivery{Kind: "repository_quota", QuotaRuleID: cleanupRule, EventSequence: 2, State: "pending"},
	)
	for i := range rows {
		v := &rows[i]
		v.ID = uuid.NewString()
		v.EventID = uuid.NewString()
		v.RequestKey = uuid.NewString()
		v.EpisodeID = uuid.NewString()
		v.Version = "1"
		v.TargetVersion = "1"
		v.Scenario = "warning"
		v.Locale = "en"
		v.TemplateVersion = "2"
		v.RecipientCiphertext = "private-cipher"
		v.From = "private@example.test"
		v.ConsoleOrigin = "https://private.test"
		v.CreatedAt = now.Add(-2 * time.Hour)
		v.UpdatedAt = now
		if v.Kind == "" {
			v.Kind = "test"
		}
		if v.NextAttemptAt.IsZero() {
			v.NextAttemptAt = now.Add(-10 * time.Minute)
		}
		if v.State == "delivering" {
			v.LeaseToken = uuid.NewString()
			v.LeaseOwner = "private-worker"
		}
	}
	return rows
}
func assertEmailDiagnosticsFixture(t *testing.T, s interface {
	EmailStore
	EmailQueueDiagnosticsStore
}, now time.Time, clock string) {
	t.Helper()
	ctx := context.Background()
	before, err := s.ListEmailDeliveries(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.EmailQueueDiagnostics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := EmailQueueCounts{Total: 72, Pending: 64, Retrying: 3, Delivering: 3, Accepted: 1, Dead: 1, ExecutableCandidates: 62, FutureBackoff: 4, ValidLeases: 1, ExpiredLeases: 2, PredecessorBlocked: 1, AwaitingCancellation: 3, AwaitingExhaustion: 2}
	if got.Counts != want {
		t.Fatalf("counts=%+v want=%+v", got.Counts, want)
	}
	if got.Clock != clock || got.Scope != "whole_retained_queue" || got.WorkerHealth != "unknown" {
		t.Fatalf("scope=%+v", got)
	}
	if got.OldestExecutableWaitSeconds == nil || *got.OldestExecutableWaitSeconds < 600 || *got.OldestExecutableWaitSeconds > 630 {
		t.Fatalf("age=%v", got.OldestExecutableWaitSeconds)
	}
	if got.LastSMTPAcceptedAt == nil || !got.LastSMTPAcceptedAt.Equal(now.Add(-time.Minute)) {
		t.Fatalf("accepted=%v", got.LastSMTPAcceptedAt)
	}
	wantFailures := []EmailQueueFailureClass{{"configuration", 1}, {"outcome_unknown", 1}, {"temporary_rejection", 1}, {"transport", 1}, {"unknown", 1}}
	if !reflect.DeepEqual(got.FailureClasses, wantFailures) {
		t.Fatalf("failures=%+v", got.FailureClasses)
	}
	after, err := s.ListEmailDeliveries(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(before, func(i, j int) bool { return before[i].ID < before[j].ID })
	sort.Slice(after, func(i, j int) bool { return after[i].ID < after[j].ID })
	if !reflect.DeepEqual(before, after) {
		t.Fatal("readonly diagnostics mutated rows")
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = s.EmailQueueDiagnostics(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
}
func TestMemoryEmailQueueDiagnosticsWholeQueue(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	empty, err := s.EmailQueueDiagnostics(ctx)
	if err != nil || empty.Counts.Total != 0 || empty.OldestExecutableWaitSeconds != nil || empty.LastSMTPAcceptedAt != nil || len(empty.FailureClasses) != 0 {
		t.Fatalf("empty=%+v %v", empty, err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, v := range emailDiagnosticsFixture(now) {
		s.emailDeliveries[v.ID] = v
	}
	assertEmailDiagnosticsFixture(t, s, now, "memory_store")
}
