//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/artifact-gateway/artifact-gateway/internal/quotaalert"
	"github.com/google/uuid"
	"os"
	"sync"
	"testing"
	"time"
)

func quotaPostgresFixture(t *testing.T) (*PostgresStore, RepositoryQuotaAlertRule, QuotaAlertMailConfig) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	s, err := NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err = s.db.Exec(`TRUNCATE repository_quota_alert_events,email_test_deliveries,repository_quota_alert_rules,email_targets,email_test_requests`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repo, err := s.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "quota-fixture-" + uuid.NewString(), Format: FormatRaw, Type: RepositoryTypeHosted, State: RepositoryActive})
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.CreateEmailTarget(ctx, EmailTarget{ID: uuid.NewString(), Name: "Synthetic", Locale: "en", Enabled: true, RecipientCiphertext: "synthetic-cipher"})
	if err != nil {
		t.Fatal(err)
	}
	p := quotaalert.Policy{WarningBasisPoints: 8500, CriticalBasisPoints: 9500, RecoveryBelowBasisPoints: 8000, WarningForSeconds: 1, CriticalForSeconds: 1, RecoveryForSeconds: 1, MaxSampleAgeSeconds: 60}
	r, err := s.CreateRepositoryQuotaAlertRule(ctx, RepositoryQuotaAlertRule{ID: uuid.NewString(), RepositoryID: repo.ID, TargetID: target.ID, TargetVersion: target.Version, Enabled: true, Policy: p})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReplaceRepositoryCapacityQuota(ctx, repo.ID, 1000); err != nil {
		t.Fatal(err)
	}
	return s, r, QuotaAlertMailConfig{Enabled: true, From: "synthetic@example.test", ConsoleOrigin: "https://synthetic.example.invalid"}
}
func quotaPGBytes(t *testing.T, s *PostgresStore, r RepositoryQuotaAlertRule, size int64) {
	t.Helper()
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(uuid.NewString())))
	if _, err := s.PutRawAsset(context.Background(), RawAsset{RepositoryID: r.RepositoryID, Path: "fixture.bin", Digest: digest, ObjectKey: "synthetic/" + digest, Size: size}); err != nil {
		t.Fatal(err)
	}
}
func quotaPGDue(t *testing.T, s *PostgresStore, r RepositoryQuotaAlertRule) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE repository_quota_alert_rules SET next_evaluate_at=clock_timestamp() WHERE id=$1`, r.ID); err != nil {
		t.Fatal(err)
	}
}
func quotaPGSample(t *testing.T, s *PostgresStore, r RepositoryQuotaAlertRule, cfg QuotaAlertMailConfig, size int64) {
	t.Helper()
	quotaPGBytes(t, s, r, size)
	quotaPGDue(t, s, r)
	if _, err := s.EvaluateNextRepositoryQuotaAlert(context.Background(), cfg, 15*time.Second); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1050 * time.Millisecond)
	quotaPGDue(t, s, r)
	if _, err := s.EvaluateNextRepositoryQuotaAlert(context.Background(), cfg, 15*time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresQuotaConcurrentEvaluationOrderingAndConfigurationFence(t *testing.T) {
	s, r, cfg := quotaPostgresFixture(t)
	ctx := context.Background()
	other, err := NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	quotaPGBytes(t, s, r, 880)
	evaluateTogether := func() {
		t.Helper()
		var wg sync.WaitGroup
		results := make(chan bool, 2)
		errs := make(chan error, 2)
		for _, store := range []*PostgresStore{s, other} {
			wg.Add(1)
			go func(store *PostgresStore) {
				defer wg.Done()
				worked, e := store.EvaluateNextRepositoryQuotaAlert(ctx, cfg, 15*time.Second)
				results <- worked
				errs <- e
			}(store)
		}
		wg.Wait()
		close(results)
		close(errs)
		count := 0
		for b := range results {
			if b {
				count++
			}
		}
		for e := range errs {
			if e != nil {
				t.Fatal(e)
			}
		}
		if count != 1 {
			t.Fatalf("two evaluators advanced the same due sample: %d", count)
		}
	}
	evaluateTogether()
	time.Sleep(1050 * time.Millisecond)
	quotaPGDue(t, s, r)
	evaluateTogether()
	quotaPGSample(t, s, r, cfg, 980)
	events, err := other.ListRepositoryQuotaAlertEvents(ctx, r.ID)
	if err != nil || len(events) != 2 {
		t.Fatalf("shared dedup: %v %v", events, err)
	}
	first, err := s.ClaimEmailDelivery(ctx, "first/session")
	if err != nil || first.EventSequence != 1 {
		t.Fatalf("first claim: %#v %v", first, err)
	}
	if _, err = other.ClaimEmailDelivery(ctx, "other/session"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("critical overtook warning: %v", err)
	}
	config, err := s.GetRepositoryQuotaAlertRule(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	config.Policy.WarningForSeconds = 2
	updated, err := s.UpdateRepositoryQuotaAlertRule(ctx, config, config.Version)
	if err != nil || updated.Version != "2" || updated.State.Severity != "critical" || !updated.State.CriticalSince.IsZero() {
		t.Fatalf("config reset/latched state: %#v %v", updated, err)
	}
	unclaimed, err := s.GetEmailDelivery(ctx, events[0].DeliveryID)
	if err != nil || unclaimed.State != "dead" || unclaimed.ErrorCode != "rule_changed" {
		t.Fatalf("old pending mail survived new config: %#v %v", unclaimed, err)
	}
	inflight, err := s.GetEmailDelivery(ctx, first.ID)
	if err != nil || inflight.State != "delivering" {
		t.Fatalf("configuration revoked already claimed SMTP permission: %#v %v", inflight, err)
	}
	if err = s.FinishEmailDelivery(ctx, first.ID, first.LeaseToken, EmailAttemptResult{}); err != nil {
		t.Fatal(err)
	}
	deleted, err := s.DeleteRepositoryQuotaAlertRule(ctx, r.ID, updated.Version)
	if err != nil || deleted.State.Severity != "critical" || !deleted.Deleted {
		t.Fatalf("delete fabricated recovery: %#v %v", deleted, err)
	}
	history, err := other.ListRepositoryQuotaAlertEvents(ctx, r.ID)
	if err != nil || len(history) != 2 {
		t.Fatal("soft delete lost history")
	}
}

func TestPostgresQuotaInvalidEvidenceCannotResolveAndReadFailureIsUnknown(t *testing.T) {
	s, r, cfg := quotaPostgresFixture(t)
	ctx := context.Background()
	quotaPGSample(t, s, r, cfg, 880)
	if _, err := s.ReplaceRepositoryCapacityQuota(ctx, r.RepositoryID, 0); err != nil {
		t.Fatal(err)
	}
	quotaPGDue(t, s, r)
	if _, err := s.EvaluateNextRepositoryQuotaAlert(ctx, cfg, time.Second); err != nil {
		t.Fatal(err)
	}
	state, err := s.GetRepositoryQuotaAlertRule(ctx, r.ID)
	if err != nil || state.State.Severity != "warning" || state.State.DataState != "not_configured" || state.State.UsedBytes != 880 || !state.State.RecoverySince.IsZero() {
		t.Fatalf("unlimited quota resolved alarm: %#v %v", state, err)
	}
	if _, err = s.ReplaceRepositoryCapacityQuota(ctx, r.RepositoryID, 1000); err != nil {
		t.Fatal(err)
	}
	quotaPGDue(t, s, r)
	// Failure injection is confined to the owned integration database.
	if _, err = s.db.Exec(`ALTER TABLE native_raw_assets RENAME TO quota_fixture_raw_assets`); err != nil {
		t.Fatal(err)
	}
	_, evalErr := s.EvaluateNextRepositoryQuotaAlert(ctx, cfg, time.Second)
	if _, err = s.db.Exec(`ALTER TABLE quota_fixture_raw_assets RENAME TO native_raw_assets`); err != nil {
		t.Fatal(err)
	}
	if evalErr == nil {
		t.Fatal("capacity failure was hidden")
	}
	state, err = s.GetRepositoryQuotaAlertRule(ctx, r.ID)
	if err != nil || state.State.Severity != "warning" || state.State.DataState != "unknown" || !state.State.WarningSince.IsZero() {
		t.Fatalf("capacity failure fabricated recovery: %#v %v", state, err)
	}
	events, err := s.ListRepositoryQuotaAlertEvents(ctx, r.ID)
	if err != nil || len(events) != 1 {
		t.Fatalf("invalid evidence emitted event: %v %v", events, err)
	}
}

func TestPostgresQuotaQueueFullRetainsDeadReplayableDelivery(t *testing.T) {
	s, r, cfg := quotaPostgresFixture(t)
	ctx := context.Background()
	// Synthetic active deliveries exercise the shared global cap without real SMTP.
	if _, err := s.db.Exec(`INSERT INTO email_test_deliveries(id,event_id,request_key,target_id,target_version,scenario,locale,template_version,recipient_ciphertext,from_address,console_origin) SELECT gen_random_uuid(),gen_random_uuid(),gen_random_uuid(),$1,1,'warning','en','1','synthetic','synthetic@example.test','' FROM generate_series(1,1000)`, r.TargetID); err != nil {
		t.Fatal(err)
	}
	quotaPGSample(t, s, r, cfg, 880)
	events, err := s.ListRepositoryQuotaAlertEvents(ctx, r.ID)
	if err != nil || len(events) != 1 || events[0].NotificationCode != "queue_full" || events[0].DeliveryState != "dead" || events[0].DeliveryErrorCode != "queue_full" {
		t.Fatalf("queue overflow dropped durable event: %#v %v", events, err)
	}
	d, err := s.GetEmailDelivery(ctx, events[0].DeliveryID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReplayEmailDelivery(ctx, d.ID, d.Version); !errors.Is(err, ErrEmailQueueFull) {
		t.Fatalf("replay bypassed cap: %v", err)
	}
	if _, err = s.db.Exec(`UPDATE email_test_deliveries SET state='accepted' WHERE kind='test'`); err != nil {
		t.Fatal(err)
	}
	replay, err := s.ReplayEmailDelivery(ctx, d.ID, d.Version)
	if err != nil || replay.EventID != d.EventID || replay.QuotaEvent != d.QuotaEvent || replay.State != "pending" {
		t.Fatalf("replay changed immutable event: %#v %v", replay, err)
	}
}

func TestPostgresQuotaEventFailureRollsBackStateAndMail(t *testing.T) {
	s, r, cfg := quotaPostgresFixture(t)
	ctx := context.Background()
	quotaPGSample(t, s, r, cfg, 880)
	quotaPGBytes(t, s, r, 980)
	quotaPGDue(t, s, r)
	if _, err := s.EvaluateNextRepositoryQuotaAlert(ctx, cfg, time.Second); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1050 * time.Millisecond)
	quotaPGDue(t, s, r)
	if _, err := s.db.Exec(`CREATE FUNCTION quota_fixture_reject_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic event persistence failure'; END $$; CREATE TRIGGER quota_fixture_reject_event BEFORE INSERT ON repository_quota_alert_events FOR EACH ROW EXECUTE FUNCTION quota_fixture_reject_event()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.db.Exec(`DROP TRIGGER IF EXISTS quota_fixture_reject_event ON repository_quota_alert_events;DROP FUNCTION IF EXISTS quota_fixture_reject_event()`)
	})
	if _, err := s.EvaluateNextRepositoryQuotaAlert(ctx, cfg, time.Second); err == nil {
		t.Fatal("event insert failure was hidden")
	}
	state, err := s.GetRepositoryQuotaAlertRule(ctx, r.ID)
	if err != nil || state.Sequence != 1 || state.State.Severity != "warning" {
		t.Fatalf("partial state committed: %#v %v", state, err)
	}
	deliveries, err := s.ListEmailDeliveries(ctx, 100)
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("uncommitted event mail escaped: %v %v", deliveries, err)
	}
	if _, err = s.db.Exec(`DROP TRIGGER quota_fixture_reject_event ON repository_quota_alert_events;DROP FUNCTION quota_fixture_reject_event()`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.EvaluateNextRepositoryQuotaAlert(ctx, cfg, time.Second); err != nil {
		t.Fatal(err)
	}
	events, err := s.ListRepositoryQuotaAlertEvents(ctx, r.ID)
	if err != nil || len(events) != 2 || events[0].Snapshot.Sequence != 2 {
		t.Fatalf("recovery after rollback lost transition: %v %v", events, err)
	}
}

func TestPostgresQuotaRestartGapAndStaleReadPreserveAlert(t *testing.T) {
	s, r, cfg := quotaPostgresFixture(t)
	ctx := context.Background()
	quotaPGSample(t, s, r, cfg, 980)
	quotaPGBytes(t, s, r, 790)
	quotaPGDue(t, s, r)
	if _, err := s.EvaluateNextRepositoryQuotaAlert(ctx, cfg, time.Second); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetRepositoryQuotaAlertRule(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a persisted sample from before process downtime; no real clock sleep.
	before.State.LastSampleAt = time.Now().UTC().Add(-45 * time.Second)
	before.State.RecoverySince = before.State.LastSampleAt
	raw, _ := json.Marshal(before.State)
	if _, err = s.db.Exec(`UPDATE repository_quota_alert_rules SET state=$2,next_evaluate_at=clock_timestamp() WHERE id=$1`, r.ID, raw); err != nil {
		t.Fatal(err)
	}
	if _, err = s.EvaluateNextRepositoryQuotaAlert(ctx, cfg, time.Second); err != nil {
		t.Fatal(err)
	}
	after, err := s.GetRepositoryQuotaAlertRule(ctx, r.ID)
	if err != nil || after.State.Severity != "critical" || after.Sequence != 1 || !after.State.RecoverySince.After(before.State.LastSampleAt) {
		t.Fatalf("restart filled unknown duration: %#v %v", after, err)
	}
	after.State.LastSampleAt = time.Now().UTC().Add(-61 * time.Second)
	raw, _ = json.Marshal(after.State)
	if _, err = s.db.Exec(`UPDATE repository_quota_alert_rules SET state=$2 WHERE id=$1`, r.ID, raw); err != nil {
		t.Fatal(err)
	}
	stale, err := s.GetRepositoryQuotaAlertRule(ctx, r.ID)
	if err != nil || stale.State.DataState != "stale" || stale.State.Severity != "critical" || !stale.State.RecoverySince.IsZero() {
		t.Fatalf("stale read: %#v %v", stale, err)
	}
	var persisted []byte
	if err = s.db.QueryRow(`SELECT state FROM repository_quota_alert_rules WHERE id=$1`, r.ID).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	var actual quotaalert.State
	_ = json.Unmarshal(persisted, &actual)
	if actual.DataState != "available" || actual.RecoverySince.IsZero() {
		t.Fatal("GET mutated durable state")
	}
	if _, err = s.DisableHostedRepository(ctx, r.RepositoryID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.FinalizeHostedRepositoryDeletion(ctx, r.RepositoryID); err != nil {
		t.Fatal(err)
	}
	quotaPGDue(t, s, r)
	if _, err = s.EvaluateNextRepositoryQuotaAlert(ctx, cfg, time.Second); err != nil {
		t.Fatal(err)
	}
	deleted, err := s.GetRepositoryQuotaAlertRule(ctx, r.ID)
	if err != nil || deleted.State.DataState != "repository_deleted" || deleted.State.Severity != "critical" || deleted.Sequence != 1 {
		t.Fatalf("repository removal fabricated recovery: %#v %v", deleted, err)
	}
}

func TestPostgresQuotaCancellationFencesFailedAndExpiredInFlightAttempts(t *testing.T) {
	for _, mode := range []string{"failed_after_disable", "expired_after_delete"} {
		t.Run(mode, func(t *testing.T) {
			s, r, cfg := quotaPostgresFixture(t)
			ctx := context.Background()
			quotaPGSample(t, s, r, cfg, 880)
			claim, err := s.ClaimEmailDelivery(ctx, "inflight/session")
			if err != nil {
				t.Fatal(err)
			}
			if mode == "failed_after_disable" {
				r.Enabled = false
				if _, err = s.UpdateRepositoryQuotaAlertRule(ctx, r, r.Version); err != nil {
					t.Fatal(err)
				}
				if err = s.FinishEmailDelivery(ctx, claim.ID, claim.LeaseToken, EmailAttemptResult{Code: "smtp_temporary_rejection"}); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err = s.DeleteRepositoryQuotaAlertRule(ctx, r.ID, r.Version); err != nil {
					t.Fatal(err)
				}
				if _, err = s.db.Exec(`UPDATE email_test_deliveries SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, claim.ID); err != nil {
					t.Fatal(err)
				}
				if _, err = s.ClaimEmailDelivery(ctx, "replacement/session"); !errors.Is(err, ErrNotFound) {
					t.Fatalf("revoked expired claim was reauthorized: %v", err)
				}
			}
			dead, err := s.GetEmailDelivery(ctx, claim.ID)
			want := "rule_disabled"
			if mode == "expired_after_delete" {
				want = "rule_deleted"
			}
			if err != nil || dead.State != "dead" || dead.ErrorCode != want {
				t.Fatalf("revoked inflight mail revived: %#v %v", dead, err)
			}
			replay, err := s.ReplayEmailDelivery(ctx, dead.ID, dead.Version)
			if err != nil || replay.EventID != dead.EventID {
				t.Fatalf("explicit historical replay blocked: %v", err)
			}
			manual, err := s.ClaimEmailDelivery(ctx, "manual/session")
			if err != nil {
				t.Fatal(err)
			}
			if err = s.FinishEmailDelivery(ctx, manual.ID, manual.LeaseToken, EmailAttemptResult{Code: "smtp_temporary_rejection"}); err != nil {
				t.Fatal(err)
			}
			retried, err := s.GetEmailDelivery(ctx, manual.ID)
			if err != nil || retried.State != "retrying" {
				t.Fatalf("manual replay did not renew retry permission: %#v %v", retried, err)
			}
		})
	}
}
