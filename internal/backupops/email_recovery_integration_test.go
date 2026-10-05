package backupops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/mail"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/app"
	"github.com/artifact-gateway/artifact-gateway/internal/emailnotification"
	"github.com/artifact-gateway/artifact-gateway/internal/quotaalert"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/secrets"
	"github.com/artifact-gateway/artifact-gateway/internal/testsupport"
	"github.com/google/uuid"
)

// Synthetic fixture key, supplied separately to the API and explicitly opted-in
// worker. Operational keys are never exported as part of a backup set.
const recoveryEmailKey = "01234567890123456789012345678901"
const recoveryRecipient = "synthetic-recovery@example.test"

type recoveryEmail struct {
	Target     repository.EmailTarget
	Rules      []repository.RepositoryQuotaAlertRule
	Events     [][]repository.RepositoryQuotaAlertEvent
	Deliveries map[string]repository.EmailDelivery
	IDs        map[string]string
}

func recoveryStore(t *testing.T, p Postgres) *repository.PostgresStore {
	t.Helper()
	s, err := repository.NewPostgresStore(p.DSN)
	if err != nil {
		t.Fatal("synthetic store unavailable")
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func recoverySQL(t *testing.T, ctx context.Context, p Postgres, sql string, args ...any) {
	t.Helper()
	c, err := p.connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(ctx) }()
	if _, err = c.Exec(ctx, sql, args...); err != nil {
		t.Fatal(err)
	}
}

func seedRecoveryEmail(t *testing.T, ctx context.Context, source *OwnedTarget, endpoint, liveRepo, cancelledRepo string) recoveryEmail {
	t.Helper()
	t.Setenv(secrets.KeyEnv, recoveryEmailKey)
	s := recoveryStore(t, source.database)
	data, _ := fixtureHTTP(t, ctx, endpoint, "POST", "/api/v2/email-targets", source.spec.AdminToken,
		[]byte(`{"name":"Synthetic recovery","locale":"en","recipient":"`+recoveryRecipient+`","enabled":true}`), 201, "")
	var identity struct{ ID string }
	if json.Unmarshal(data, &identity) != nil || identity.ID == "" {
		t.Fatal("email target identity missing")
	}
	target, err := s.GetEmailTarget(ctx, identity.ID)
	if err != nil || target.RecipientCiphertext == "" || target.RecipientCiphertext == recoveryRecipient {
		t.Fatal("target not encrypted")
	}
	f := recoveryEmail{Target: target, IDs: map[string]string{}, Deliveries: map[string]repository.EmailDelivery{}}
	enqueue := func(label string) repository.EmailDelivery {
		t.Helper()
		v, e := s.EnqueueEmailTest(ctx, repository.EmailTestRequest{ID: uuid.NewString(), EventID: uuid.NewString(), RequestKey: uuid.NewString(), TargetID: target.ID, TargetVersion: target.Version, Scenario: "warning", TemplateVersion: "1", From: "gateway@example.test", ConsoleOrigin: "https://console.example.invalid"})
		if e != nil {
			t.Fatal(e)
		}
		f.IDs[label] = v.ID
		v, e = s.ClaimEmailDelivery(ctx, "source/"+label)
		if e != nil || v.ID != f.IDs[label] {
			t.Fatal("unexpected fixture claim")
		}
		return v
	}
	accepted := enqueue("accepted")
	if err = s.FinishEmailDelivery(ctx, accepted.ID, accepted.LeaseToken, repository.EmailAttemptResult{}); err != nil {
		t.Fatal(err)
	}
	dead := enqueue("dead")
	if err = s.FinishEmailDelivery(ctx, dead.ID, dead.LeaseToken, repository.EmailAttemptResult{Code: "smtp_permanent_rejection", Permanent: true}); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"active", "expired"} {
		v := enqueue(label)
		recoverySQL(t, ctx, source.database, `UPDATE email_test_deliveries SET lease_expires_at=clock_timestamp()+interval '1 hour' WHERE id::text=$1`, v.ID)
	}
	retry := enqueue("future")
	if err = s.FinishEmailDelivery(ctx, retry.ID, retry.LeaseToken, repository.EmailAttemptResult{Code: "outcome_unknown", OutcomeUnknown: true}); err != nil {
		t.Fatal(err)
	}
	recoverySQL(t, ctx, source.database, `UPDATE email_test_deliveries SET next_attempt_at=clock_timestamp()+interval '1 hour' WHERE id::text=$1`, retry.ID)
	policy := quotaalert.Policy{WarningBasisPoints: 8500, CriticalBasisPoints: 9500, RecoveryBelowBasisPoints: 8000, WarningForSeconds: 1, CriticalForSeconds: 1, RecoveryForSeconds: 1, MaxSampleAgeSeconds: 3600}
	// Existing Raw objects remain the sole reference/byte fixture. These explicit
	// quotas make their 14/15-byte usage fire; no extra artifact or object is added.
	for i, repoID := range []string{cancelledRepo, liveRepo} {
		quota := int64(17)
		if i == 1 {
			quota = 15
			policy.CriticalBasisPoints = 9200
			policy.CriticalForSeconds = 2
		}
		if _, err = s.ReplaceRepositoryCapacityQuota(ctx, repoID, quota); err != nil {
			t.Fatal(err)
		}
		r, e := s.CreateRepositoryQuotaAlertRule(ctx, repository.RepositoryQuotaAlertRule{ID: uuid.NewString(), RepositoryID: repoID, TargetID: target.ID, TargetVersion: target.Version, Enabled: true, Policy: policy})
		if e != nil {
			t.Fatal(e)
		}
		cfg := repository.QuotaAlertMailConfig{Enabled: true, From: "gateway@example.test", ConsoleOrigin: "https://console.example.invalid"}
		for sample := 0; sample < 2; sample++ {
			recoverySQL(t, ctx, source.database, `UPDATE repository_quota_alert_rules SET next_evaluate_at=clock_timestamp() WHERE id::text=$1`, r.ID)
			if worked, e := s.EvaluateNextRepositoryQuotaAlert(ctx, cfg, time.Hour); e != nil || !worked {
				t.Fatalf("quota sample: %v %v", worked, e)
			}
			if sample == 0 {
				time.Sleep(1050 * time.Millisecond)
			}
		}
		events, e := s.ListRepositoryQuotaAlertEvents(ctx, r.ID)
		if e != nil || len(events) != 1 || events[0].Snapshot.Scenario != "warning" || events[0].NotificationCode != "queued" {
			t.Fatal("real warning event missing")
		}
		label := "pending"
		if i == 0 {
			label = "cancelled"
			claim, e := s.ClaimEmailDelivery(ctx, "source/cancelled")
			if e != nil || claim.ID != events[0].DeliveryID {
				t.Fatal("cancelled fixture claim missing")
			}
			r.Enabled = false
			if _, e = s.UpdateRepositoryQuotaAlertRule(ctx, r, r.Version); e != nil {
				t.Fatal(e)
			}
			recoverySQL(t, ctx, source.database, `UPDATE email_test_deliveries SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id::text=$1`, claim.ID)
		}
		f.IDs[label] = events[0].DeliveryID
		if i == 1 {
			time.Sleep(1050 * time.Millisecond)
			recoverySQL(t, ctx, source.database, `UPDATE repository_quota_alert_rules SET next_evaluate_at=clock_timestamp() WHERE id::text=$1`, r.ID)
			if worked, e := s.EvaluateNextRepositoryQuotaAlert(ctx, cfg, time.Hour); e != nil || !worked {
				t.Fatal("critical sample failed")
			}
			ordered, e := s.ListRepositoryQuotaAlertEvents(ctx, r.ID)
			if e != nil || len(ordered) != 2 || ordered[0].Snapshot.Sequence != 2 || ordered[0].Snapshot.Scenario != "critical" || ordered[1].Snapshot.Sequence != 1 || ordered[1].Snapshot.Scenario != "warning" || ordered[0].Snapshot.EpisodeID != ordered[1].Snapshot.EpisodeID {
				t.Fatal("ordered same-episode warning/critical missing")
			}
			f.IDs["ordered"] = ordered[0].DeliveryID
		}

		r, e = s.GetRepositoryQuotaAlertRule(ctx, r.ID)
		if e != nil {
			t.Fatal(e)
		}
		events, e = s.ListRepositoryQuotaAlertEvents(ctx, r.ID)
		if e != nil {
			t.Fatal(e)
		}
		f.Rules = append(f.Rules, r)
		f.Events = append(f.Events, events)
	}
	recoverySQL(t, ctx, source.database, `UPDATE email_test_deliveries SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id::text=$1`, f.IDs["expired"])
	for _, id := range f.IDs {
		v, e := s.GetEmailDelivery(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		f.Deliveries[id] = v
	}
	return f
}

func verifyRecoveryEmail(t *testing.T, ctx context.Context, target *OwnedTarget, endpoint, denied string, f recoveryEmail, bundle string) {
	t.Helper()
	s := recoveryStore(t, target.database)
	restored, e := s.GetEmailTarget(ctx, f.Target.ID)
	if e != nil || !reflect.DeepEqual(restored, f.Target) {
		t.Fatal("restored target changed")
	}
	for i, before := range f.Rules {
		after, e := s.GetRepositoryQuotaAlertRule(ctx, before.ID)
		events, ee := s.ListRepositoryQuotaAlertEvents(ctx, before.ID)
		if e != nil || ee != nil || !reflect.DeepEqual(after, before) || !reflect.DeepEqual(events, f.Events[i]) {
			t.Fatal("restored rule/state/episode/event/descriptor changed")
		}
	}
	for id, before := range f.Deliveries {
		after, e := s.GetEmailDelivery(ctx, id)
		if e != nil || !reflect.DeepEqual(after, before) {
			t.Fatal("restored delivery/token/permission changed")
		}
	}

	// The nonempty request ledger must still enforce the same shared rate limit;
	// retrying an original idempotency key must return its original delivery.
	accepted := f.Deliveries[f.IDs["accepted"]]
	request := repository.EmailTestRequest{ID: uuid.NewString(), EventID: uuid.NewString(), RequestKey: accepted.RequestKey, TargetID: accepted.TargetID, TargetVersion: accepted.TargetVersion, Scenario: accepted.Scenario, TemplateVersion: accepted.TemplateVersion, From: accepted.From, ConsoleOrigin: accepted.ConsoleOrigin}
	duplicate, e := s.EnqueueEmailTest(ctx, request)
	if e != nil || duplicate.ID != accepted.ID {
		t.Fatal("restored idempotency key did not preserve delivery")
	}
	request.RequestKey = uuid.NewString()
	if _, e = s.EnqueueEmailTest(ctx, request); !errors.Is(e, repository.ErrEmailRateLimited) {
		t.Fatal("restored request ledger lost rate boundary")
	}
	gatewayID := target.resourceID("container", target.spec.Project+"-gateway")
	inspect, e := target.spec.Docker.output(ctx, "container", "inspect", gatewayID)
	var containers []struct{ Config struct{ Env []string } }
	if e != nil || json.Unmarshal(inspect, &containers) != nil || len(containers) != 1 {
		t.Fatal("restored runtime identity unavailable")
	}
	apiOnly := false
	for _, env := range containers[0].Config.Env {
		key, value, _ := strings.Cut(env, "=")
		if key == "GATEWAY_NODE_ROLES" {
			apiOnly = value == "api"
		}
		if key == "GATEWAY_EMAIL_CONFIG_FILE" && value != "" {
			t.Fatal("restore enabled SMTP configuration")
		}
	}
	if !apiOnly {
		t.Fatal("restore enabled worker/scheduler roles")
	}
	for _, key := range []string{"", "11111111111111111111111111111111", recoveryEmailKey} {
		t.Setenv(secrets.KeyEnv, key)
		plaintext, e := secrets.Open("email-target:"+restored.ID, restored.RecipientCiphertext)
		if key == recoveryEmailKey {
			if e != nil || plaintext != recoveryRecipient {
				t.Fatal("independent original key did not decrypt")
			}
		} else if e == nil {
			t.Fatal("missing/wrong key decrypted backup")
		}
	}
	if e = filepath.WalkDir(bundle, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0077 != 0 {
			return errors.New("backup permissions too broad")
		}
		if !entry.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if bytes.Contains(data, []byte(recoveryEmailKey)) || bytes.Contains(data, []byte(recoveryRecipient)) {
				return errors.New("plaintext key/recipient exported")
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"/api/v2/email-targets/" + restored.ID, "/api/v2/email-deliveries", "/api/v2/repository-quota-alert-rules", "/api/v2/repository-quota-alert-rules/" + f.Rules[1].ID + "/events"} {
		data, _ := fixtureHTTP(t, ctx, endpoint, "GET", path, target.spec.AdminToken, nil, 200, "")
		if bytes.Contains(data, []byte(restored.RecipientCiphertext)) || bytes.Contains(data, []byte(recoveryRecipient)) || bytes.Contains(data, []byte(recoveryEmailKey)) {
			t.Fatal("safe API leaked encrypted settings")
		}
		assertRecoverySafeAPI(t, path, data, f)
		fixtureHTTP(t, ctx, endpoint, "GET", path, denied, nil, 403, "")
	}
	// Restore always starts API-only, with no SMTP configuration or worker role.
	fixture := testsupport.NewSMTPFixture(t, true)
	config := emailnotification.Config{Enabled: true, Host: "smtp.example.test", Port: 465, Mode: "implicit_tls", From: "gateway@example.test", ConsoleOrigin: "https://console.example.invalid", ApprovedIPs: []string{"192.0.2.10"}, CAFile: fixture.CAFile}
	sender := emailnotification.Sender{Config: config, LookupHost: func(context.Context, string) ([]string, error) { return []string{"192.0.2.10"}, nil }, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, fixture.Address)
	}}
	messages, _ := fixture.Snapshot()
	if len(messages) != 0 {
		t.Fatal("restore sent mail before worker opt-in")
	}
	// Hold the first two SMTP attempts before completion. The six other workers
	// must finish idle while critical remains blocked behind in-flight warning.
	releaseSMTP := make(chan struct{})
	fixture.AfterAccept = func() { <-releaseSMTP }
	firstWave := true
	runTogether := func(want int) {
		t.Helper()
		var wg sync.WaitGroup
		results := make(chan bool, 8)
		errs := make(chan error, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				worked, e := (app.EmailNotificationWorker{Store: s, Sender: sender, InstanceID: uuid.NewString()}).Run(ctx)
				results <- worked
				errs <- e
			}()
		}
		if firstWave {
			deadline := time.NewTimer(10 * time.Second)
			ticker := time.NewTicker(5 * time.Millisecond)
			for {
				messages, _ := fixture.Snapshot()
				if len(messages) == 2 && len(results) == 6 {
					break
				}
				select {
				case <-deadline.C:
					close(releaseSMTP)
					wg.Wait()
					ticker.Stop()
					t.Fatal("ordered warning did not block later concurrent claims")
				case <-ticker.C:
				}
			}
			deadline.Stop()
			ticker.Stop()
			close(releaseSMTP)
			firstWave = false
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
		if count != want {
			t.Fatalf("restored concurrent work=%d want=%d", count, want)
		}
	}
	runTogether(2) // due quota warning + expired unrevoked test; active/future blocked.
	runTogether(1) // critical can proceed only after warning becomes terminal.
	runTogether(0)
	cancelled, e := s.GetEmailDelivery(ctx, f.IDs["cancelled"])
	if e != nil || cancelled.State != "dead" || cancelled.ErrorCode != "rule_disabled" || !cancelled.PossibleDuplicate || cancelled.AutomaticCancellationCode != "rule_disabled" {
		t.Fatal("cancelled lease regained permission")
	}
	if e = s.FinishEmailDelivery(ctx, cancelled.ID, f.Deliveries[cancelled.ID].LeaseToken, repository.EmailAttemptResult{}); !errors.Is(e, repository.ErrVersionConflict) {
		t.Fatal("cancelled stale token finished")
	}
	expired, e := s.GetEmailDelivery(ctx, f.IDs["expired"])
	if e != nil || expired.State != "accepted" || !expired.PossibleDuplicate {
		t.Fatal("expired recovery lost ambiguous outcome")
	}
	active, e := s.GetEmailDelivery(ctx, f.IDs["active"])
	future, fe := s.GetEmailDelivery(ctx, f.IDs["future"])
	if e != nil || fe != nil || !reflect.DeepEqual(active, f.Deliveries[active.ID]) || !reflect.DeepEqual(future, f.Deliveries[future.ID]) {
		t.Fatal("unexpired lease/future retry advanced")
	}
	recoverySQL(t, ctx, target.database, `UPDATE email_test_deliveries SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id::text=$1`, active.ID)
	if e = s.FinishEmailDelivery(ctx, active.ID, active.LeaseToken, repository.EmailAttemptResult{}); !errors.Is(e, repository.ErrVersionConflict) {
		t.Fatal("expired token finished before lease reclamation")
	}
	newer, e := s.ClaimEmailDelivery(ctx, "restored/new-session")
	if e != nil || newer.ID != active.ID || newer.LeaseToken == active.LeaseToken || !newer.PossibleDuplicate {
		t.Fatal("expired lease was not fenced/reclaimed")
	}
	if e = s.FinishEmailDelivery(ctx, active.ID, active.LeaseToken, repository.EmailAttemptResult{}); !errors.Is(e, repository.ErrVersionConflict) {
		t.Fatal("old token finished newer claim")
	}
	if e = s.FinishEmailDelivery(ctx, newer.ID, newer.LeaseToken, repository.EmailAttemptResult{Code: "outcome_unknown", OutcomeUnknown: true}); e != nil {
		t.Fatal(e)
	}
	recoverySQL(t, ctx, target.database, `UPDATE email_test_deliveries SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE id::text=ANY($1::text[])`, []string{active.ID, future.ID})
	runTogether(2)
	runTogether(0)
	messages, commands := fixture.Snapshot()
	if len(messages) != 5 {
		t.Fatalf("SMTP received %d messages want 5", len(messages))
	}
	for _, label := range []string{"pending", "ordered", "expired", "active", "future"} {
		id := f.Deliveries[f.IDs[label]].EventID
		count := 0
		for _, message := range messages {
			if recoveryMessageID(t, message) == id {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("permitted event %s sent %d times", label, count)
		}
	}
	warningPosition, criticalPosition := -1, -1
	for i, message := range messages {
		if recoveryMessageID(t, message) == f.Deliveries[f.IDs["pending"]].EventID {
			warningPosition = i
		}
		if recoveryMessageID(t, message) == f.Deliveries[f.IDs["ordered"]].EventID {
			criticalPosition = i
		}
	}
	if warningPosition < 0 || criticalPosition <= warningPosition {
		t.Fatal("SMTP quota event order changed")
	}
	for _, label := range []string{"accepted", "dead", "cancelled"} {
		after, e := s.GetEmailDelivery(ctx, f.IDs[label])
		if e != nil || (label != "cancelled" && !reflect.DeepEqual(after, f.Deliveries[after.ID])) {
			t.Fatal("terminal delivery changed")
		}
		for _, message := range messages {
			if recoveryMessageID(t, message) == after.EventID {
				t.Fatal("forbidden event resent")
			}
		}
	}
	recipients := 0
	for _, command := range commands {
		if strings.HasPrefix(command, "RCPT TO:") {
			recipients++
			if !strings.Contains(command, recoveryRecipient) {
				t.Fatal("wrong SMTP recipient")
			}
		}
	}
	if recipients != 5 {
		t.Fatal("TLS sink recipient evidence missing")
	}
	t.Log("nonempty email/rule/event/outbox restored; key separate; permissions/API denial verified; TLS sink accepted only 5 permitted events in quota sequence; cancelled and terminal mail not resent; concurrent claims, future retry, lease fencing and possibleDuplicate preserved")
}

func recoveryMessageID(t *testing.T, data []byte) string {
	t.Helper()
	message, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil || message.Header.Get("To") != recoveryRecipient || !strings.HasPrefix(message.Header.Get("Content-Type"), "multipart/alternative;") {
		t.Fatal("actual TLS MIME identity missing")
	}
	id := message.Header.Get("Message-ID")
	if !strings.HasPrefix(id, "<") || !strings.HasSuffix(id, "@artifact-gateway.invalid>") {
		t.Fatal("actual TLS Message-ID invalid")
	}
	return strings.TrimSuffix(strings.TrimPrefix(id, "<"), "@artifact-gateway.invalid>")
}
