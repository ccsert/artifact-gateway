//go:build integration

package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"github.com/artifact-gateway/artifact-gateway/internal/emailnotification"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/testsupport"
	"github.com/google/uuid"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http/httptest"
	"net/mail"
	"os"
	"strings"
	"testing"
)

func TestPostgresAdministratorEmailReopensAndDeliversActualTLSMIME(t *testing.T) {
	connection := os.Getenv("TEST_DATABASE_URL")
	if connection == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", "01234567890123456789012345678901")
	db, err := sql.Open("pgx", connection)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.Exec(`TRUNCATE repository_quota_alert_events,email_test_deliveries,repository_quota_alert_rules,email_targets,email_test_requests`); err != nil {
		t.Fatal(err)
	}
	store, err := repository.NewPostgresStore(connection)
	if err != nil {
		t.Fatal(err)
	}
	f := testsupport.NewSMTPFixture(t, true)
	cfg := emailnotification.Config{Enabled: true, Host: "smtp.example.test", Port: 465, Mode: "implicit_tls", From: "gateway@example.test", ApprovedIPs: []string{"192.0.2.10"}, CAFile: f.CAFile, ConsoleOrigin: "https://console.example.invalid"}
	handler := NewGatewayHandler(Dependencies{Email: cfg}, store, TestAdapter{}, testAuthenticator())
	request := func(method, path, body, match, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		authorize(r, "admin-secret")
		if match != "" {
			r.Header.Set("If-Match", match)
		}
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	created := request("POST", "/api/v2/email-targets", `{"name":"Synthetic operations","locale":"zh-CN","recipient":"private-recipient@example.test","enabled":true}`, "", "")
	if created.Code != 201 {
		t.Fatalf("target status=%d", created.Code)
	}
	var target struct{ ID, Version string }
	if json.Unmarshal(created.Body.Bytes(), &target) != nil {
		t.Fatal("invalid target")
	}
	if strings.Contains(created.Body.String(), "private-recipient") {
		t.Fatal("recipient leaked into API")
	}
	body := `{"targetId":"` + target.ID + `","scenario":"warning"}`
	key := uuid.NewString()
	queued := request("POST", "/api/v2/email-notifications:test", body, target.Version, key)
	if queued.Code != 202 {
		t.Fatalf("test status=%d body=%s", queued.Code, queued.Body.String())
	}
	var delivery struct{ ID, EventID string }
	if json.Unmarshal(queued.Body.Bytes(), &delivery) != nil {
		t.Fatal("invalid delivery")
	}
	repeated := request("POST", "/api/v2/email-notifications:test", body, target.Version, key)
	if repeated.Code != 202 || repeated.Body.String() != queued.Body.String() {
		t.Fatal("repeated HTTP request changed delivery")
	}
	claim, err := store.ClaimEmailDelivery(context.Background(), "old/session")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE email_test_deliveries SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, delivery.ID); err != nil {
		t.Fatal(err)
	}
	reopened, err := repository.NewPostgresStore(connection)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	sender := emailnotification.Sender{Config: cfg, LookupHost: func(context.Context, string) ([]string, error) { return []string{"192.0.2.10"}, nil }, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, f.Address)
	}}
	worker := EmailNotificationWorker{Store: reopened, Sender: sender, InstanceID: "new/session"}
	did, err := worker.Run(context.Background())
	if err != nil || !did {
		t.Fatalf("worker=%v err=%v", did, err)
	}
	if err = reopened.FinishEmailDelivery(context.Background(), delivery.ID, claim.LeaseToken, repository.EmailAttemptResult{}); err != repository.ErrVersionConflict {
		t.Fatal("old worker completed a restarted lease")
	}
	handler = NewGatewayHandler(Dependencies{Email: cfg}, reopened, TestAdapter{}, testAuthenticator())
	got := request("GET", "/api/v2/email-deliveries/"+delivery.ID, "", "", "")
	if got.Code != 200 {
		t.Fatal("delivery unavailable")
	}
	var status struct {
		State, ErrorCode  string
		Attempts          int
		PossibleDuplicate bool
	}
	if json.Unmarshal(got.Body.Bytes(), &status) != nil || status.State != "accepted" || status.Attempts != 2 || !status.PossibleDuplicate {
		t.Fatalf("lost restarted acceptance: %s", got.Body.String())
	}
	if strings.Contains(got.Body.String(), "private-recipient") || strings.Contains(got.Body.String(), "smtp.example") || strings.Contains(got.Body.String(), "cipher") {
		t.Fatal("sensitive delivery diagnostics")
	}
	messages, _ := f.Snapshot()
	if len(messages) != 1 {
		t.Fatal("fixture captured wrong message count")
	}
	message, err := mail.ReadMessage(bytes.NewReader(messages[0]))
	if err != nil {
		t.Fatal(err)
	}
	if message.Header.Get("Message-ID") != "<"+delivery.EventID+"@artifact-gateway.invalid>" {
		t.Fatal("unstable message identity")
	}
	_, params, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	parts := multipart.NewReader(message.Body, params["boundary"])
	for _, kind := range []string{"text/plain", "text/html"} {
		part, e := parts.NextPart()
		if e != nil {
			t.Fatal(e)
		}
		body, e := io.ReadAll(part)
		if e != nil || !strings.HasPrefix(part.Header.Get("Content-Type"), kind) || !bytes.Contains(body, []byte("12%")) || !bytes.Contains(body, []byte("警告")) {
			t.Fatal("actual TLS MIME missing localized HTML/text")
		}
	}
}
