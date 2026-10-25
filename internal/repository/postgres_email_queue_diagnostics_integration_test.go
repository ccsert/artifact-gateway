//go:build integration

package repository

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresEmailQueueDiagnosticsSnapshotAndConcurrentClaims(t *testing.T) {
	s, rule, _ := quotaPostgresFixture(t)
	ctx := context.Background()
	empty, err := s.EmailQueueDiagnostics(ctx)
	if err != nil || empty.Counts.Total != 0 || empty.LastSMTPAcceptedAt != nil || empty.OldestExecutableWaitSeconds != nil {
		t.Fatalf("empty=%+v %v", empty, err)
	}
	var now time.Time
	if err = s.db.QueryRow(`SELECT clock_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	rows := emailDiagnosticsFixture(now)
	rules := map[string]bool{}
	for _, v := range rows {
		if v.Kind == "repository_quota" {
			rules[v.QuotaRuleID] = true
		}
	}
	for id := range rules {
		_, err = s.db.Exec(`INSERT INTO repository_quota_alert_rules(id,repository_id,target_id,target_version,policy,state) SELECT $1,$2,target_id,target_version,policy,state FROM repository_quota_alert_rules WHERE id=$3`, id, uuid.NewString(), rule.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range rows {
		var quotaID, episode, seq, descriptor, token, expiry, accepted any
		if v.Kind == "repository_quota" {
			quotaID = v.QuotaRuleID
			episode = v.EpisodeID
			seq = v.EventSequence
			descriptor = "{}"
		}
		if v.LeaseToken != "" {
			token = v.LeaseToken
			expiry = v.LeaseExpiresAt
		}
		if !v.AcceptedAt.IsZero() {
			accepted = v.AcceptedAt
		}
		_, err = s.db.Exec(`INSERT INTO email_test_deliveries(id,event_id,request_key,target_id,target_version,scenario,locale,template_version,recipient_ciphertext,from_address,console_origin,state,attempts,error_code,lease_owner,lease_token,lease_expires_at,next_attempt_at,accepted_at,created_at,updated_at,kind,quota_rule_id,episode_id,event_sequence,quota_descriptor,automatic_cancellation_code)
 VALUES($1,$2,$3,$4,1,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26)`, v.ID, v.EventID, v.RequestKey, rule.TargetID, v.Scenario, v.Locale, v.TemplateVersion, v.RecipientCiphertext, v.From, v.ConsoleOrigin, v.State, v.Attempts, v.ErrorCode, v.LeaseOwner, token, expiry, v.NextAttemptAt, accepted, v.CreatedAt, v.UpdatedAt, v.Kind, quotaID, episode, seq, descriptor, v.AutomaticCancellationCode)
		if err != nil {
			t.Fatal(err)
		}
	}
	assertEmailDiagnosticsFixture(t, s, now, "database")
	for _, v := range rows {
		if v.Kind != "repository_quota" || v.EventSequence != 1 || v.AutomaticCancellationCode != "" {
			continue
		}
		if _, err = s.db.Exec(`UPDATE email_test_deliveries SET state='delivering',attempts=8,lease_expires_at=NULL WHERE id=$1`, v.ID); err != nil {
			t.Fatal(err)
		}
		missing, e := s.EmailQueueDiagnostics(ctx)
		if e != nil || missing.Counts.PredecessorBlocked != 1 || missing.Counts.ExecutableCandidates != 62 || missing.Counts.AwaitingExhaustion != 2 {
			t.Fatalf("missing lease must retain predecessor: %+v %v", missing.Counts, e)
		}
		if _, err = s.db.Exec(`UPDATE email_test_deliveries SET state='retrying',attempts=0 WHERE id=$1`, v.ID); err != nil {
			t.Fatal(err)
		}
	}
	// An uncommitted writer owns every row. Diagnostics must neither wait for row
	// locks nor see part of the uncommitted changes or perform claim cleanup.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Exec(`UPDATE email_test_deliveries SET state='dead',updated_at=clock_timestamp()`); err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(ctx, 2*time.Second)
	locked, err := s.EmailQueueDiagnostics(deadline)
	cancel()
	if err != nil || locked.Counts.Pending != 64 || locked.Counts.Dead != 1 {
		t.Fatalf("MVCC snapshot=%+v %v", locked.Counts, err)
	}
	var dbNow time.Time
	if err = s.db.QueryRow(`SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
		t.Fatal(err)
	}
	if locked.SampledAt.Before(now) || locked.SampledAt.After(dbNow) {
		t.Fatalf("database sample=%v DB range=%v..%v", locked.SampledAt, now, dbNow)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	// Exercise the actual claim housekeeping and SKIP LOCKED predicate alongside
	// readonly snapshots. No SMTP is involved; fenced successful finishes are stored.
	var wg sync.WaitGroup
	results := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				v, e := s.ClaimEmailDelivery(ctx, "diagnostics-test/session")
				if errors.Is(e, ErrNotFound) {
					results <- nil
					return
				}
				if e != nil {
					results <- e
					return
				}
				if e = s.FinishEmailDelivery(ctx, v.ID, v.LeaseToken, EmailAttemptResult{}); e != nil {
					results <- e
					return
				}
			}
		}()
	}
	for i := 0; i < 20; i++ {
		got, e := s.EmailQueueDiagnostics(ctx)
		if e != nil {
			t.Fatal(e)
		}
		c := got.Counts
		if c.Total != 72 || c.Pending+c.Retrying+c.Delivering+c.Accepted+c.Dead != 72 || c.ValidLeases+c.ExpiredLeases != c.Delivering || c.ExecutableCandidates > c.Pending+c.Retrying+c.ExpiredLeases {
			t.Fatalf("torn aggregate=%+v", c)
		}
	}
	wg.Wait()
	close(results)
	for e := range results {
		if e != nil {
			t.Fatal(e)
		}
	}
	final, err := s.EmailQueueDiagnostics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if final.Counts.Accepted != 63 || final.Counts.Dead != 5 || final.Counts.ExecutableCandidates != 0 || final.Counts.AwaitingCancellation != 0 || final.Counts.AwaitingExhaustion != 0 || final.LastSMTPAcceptedAt == nil || !final.LastSMTPAcceptedAt.After(now) {
		t.Fatalf("claim parity=%+v %v", final, err)
	}
}
