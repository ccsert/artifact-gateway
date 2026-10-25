//go:build integration

package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresEmailQueueDiagnosticsHidesLegacyFailures(t *testing.T) {
	connection := os.Getenv("TEST_DATABASE_URL")
	if connection == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	db, err := sql.Open("pgx", connection)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.Exec(`TRUNCATE repository_quota_alert_events,email_test_deliveries,repository_quota_alert_rules,email_targets,email_test_requests`); err != nil {
		t.Fatal(err)
	}
	s, err := repository.NewPostgresStore(connection)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	target, err := s.CreateEmailTarget(ctx, repository.EmailTarget{ID: uuid.NewString(), Name: "secret-recipient@example.test", Enabled: true, Locale: "en", RecipientCiphertext: "secret-ciphertext"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.EnqueueEmailTest(ctx, repository.EmailTestRequest{ID: uuid.NewString(), EventID: uuid.NewString(), RequestKey: uuid.NewString(), TargetID: target.ID, TargetVersion: target.Version, Scenario: "warning", TemplateVersion: "2", From: "secret-smtp@example.test", ConsoleOrigin: "https://secret-smtp.test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE email_test_deliveries SET state='dead',error_code=$2,lease_token=$3,lease_owner='secret-lease-owner' WHERE id=$1`, d.ID, "secret-raw-error body secret-key-digest", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetEmailDelivery(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	h := newTestGateway(t, GatewayOptions{Store: s, Adapter: TestAdapter{}, Authenticator: testAuthenticator()})
	r := httptest.NewRequest("GET", "/api/v2/email-queue/diagnostics", nil)
	authorize(r, "admin-secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var got repository.EmailQueueDiagnostics
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Counts.Dead != 1 || got.LastSMTPAcceptedAt != nil || got.OldestExecutableWaitSeconds != nil || !reflect.DeepEqual(got.FailureClasses, []repository.EmailQueueFailureClass{{Category: "unknown", Count: 1}}) {
		t.Fatalf("response=%d %s", w.Code, w.Body.String())
	}
	for _, secret := range []string{"secret-", d.ID, target.ID, before.LeaseToken, "leaseToken", "recipient", "keyDigest", "html"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("private data %q exposed", secret)
		}
	}
	after, err := s.GetEmailDelivery(ctx, d.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("GET mutated persisted delivery")
	}
}
