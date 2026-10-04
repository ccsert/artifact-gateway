package repository

import (
	"context"
	"errors"
	"github.com/artifact-gateway/artifact-gateway/internal/quotaalert"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestQuotaEvaluationPersistsTransitionsAndMailSnapshot(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	repo, err := s.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "synthetic-quota", Format: FormatRaw, Type: RepositoryTypeHosted, State: RepositoryActive})
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.CreateEmailTarget(ctx, EmailTarget{ID: uuid.NewString(), Name: "Synthetic", Locale: "en", Enabled: true, RecipientCiphertext: "cipher-snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	p := quotaalert.Policy{WarningBasisPoints: 8500, CriticalBasisPoints: 9500, RecoveryBelowBasisPoints: 8000, WarningForSeconds: 1, CriticalForSeconds: 1, RecoveryForSeconds: 1, MaxSampleAgeSeconds: 60}
	rule, err := s.CreateRepositoryQuotaAlertRule(ctx, RepositoryQuotaAlertRule{ID: uuid.NewString(), RepositoryID: repo.ID, TargetID: target.ID, TargetVersion: target.Version, Enabled: true, Policy: p})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ReplaceRepositoryCapacityQuota(ctx, repo.ID, 1000)
	if err != nil {
		t.Fatal(err)
	}
	mail := QuotaAlertMailConfig{Enabled: true, From: "snapshot@example.test", ConsoleOrigin: "https://snapshot.example.invalid"}
	sample := func(size int64) {
		t.Helper()
		_, e := s.PutRawAsset(ctx, RawAsset{RepositoryID: repo.ID, Path: "synthetic.bin", Digest: uuid.NewString(), Size: size})
		if e != nil {
			t.Fatal(e)
		}
		time.Sleep(2 * time.Millisecond)
		if _, e = s.EvaluateNextRepositoryQuotaAlert(ctx, mail, time.Millisecond); e != nil {
			t.Fatal(e)
		}
		time.Sleep(1050 * time.Millisecond)
		if _, e = s.EvaluateNextRepositoryQuotaAlert(ctx, mail, time.Millisecond); e != nil {
			t.Fatal(e)
		}
	}
	sample(880)
	events, err := s.ListRepositoryQuotaAlertEvents(ctx, rule.ID)
	if err != nil || len(events) != 1 {
		t.Fatalf("warning event not persisted: %#v %v", events, err)
	}
	d, err := s.GetEmailDelivery(ctx, events[0].DeliveryID)
	if err != nil || d.From != mail.From || d.RecipientCiphertext != "cipher-snapshot" {
		t.Fatalf("mail snapshot: %#v %v", d, err)
	}
	if _, err = s.EnqueueEmailTest(ctx, EmailTestRequest{ID: uuid.NewString(), EventID: uuid.NewString(), RequestKey: events[0].Snapshot.ID, TargetID: target.ID, TargetVersion: target.Version, Scenario: "warning", TemplateVersion: "2"}); !errors.Is(err, ErrEmailIdempotencyConflict) {
		t.Fatalf("quota event aliased synthetic test request: %v", err)
	}
	sample(980)
	sample(790)
	events, err = s.ListRepositoryQuotaAlertEvents(ctx, rule.ID)
	if err != nil || len(events) != 3 {
		t.Fatalf("transitions: %#v %v", events, err)
	}
	for i, want := range []string{"resolved", "critical", "warning"} {
		v := events[i].Snapshot
		if v.Scenario != want || v.Sequence != int64(3-i) || v.EpisodeID != events[0].Snapshot.EpisodeID || v.Policy != p || v.EvidenceSince.IsZero() {
			t.Fatalf("transition order/evidence: %#v", events)
		}
	}
	stable, err := s.GetRepositoryQuotaAlertRule(ctx, rule.ID)
	if err != nil || stable.State.Severity != "normal" || stable.ActiveEpisodeID != "" {
		t.Fatalf("recovery: %#v %v", stable, err)
	}
	first, err := s.ClaimEmailDelivery(ctx, "worker-one")
	if err != nil || first.EventSequence != 1 {
		t.Fatalf("earliest event was not claimed: %d %v", first.EventSequence, err)
	}
	if _, err = s.ClaimEmailDelivery(ctx, "worker-two"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("later quota mail overtook claimed warning: %v", err)
	}
	if err = s.FinishEmailDelivery(ctx, first.ID, first.LeaseToken, EmailAttemptResult{Code: "smtp_temporary_rejection"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimEmailDelivery(ctx, "worker-two"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("later quota mail overtook retry: %v", err)
	}
	stable.Enabled = false
	if _, err = s.UpdateRepositoryQuotaAlertRule(ctx, stable, stable.Version); err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		d, err := s.GetEmailDelivery(ctx, e.DeliveryID)
		if err != nil || d.State != "dead" || d.ErrorCode != "rule_disabled" {
			t.Fatalf("unclaimed mail survived disable: %#v %v", d, err)
		}
	}
}
