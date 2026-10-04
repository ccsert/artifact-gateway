//go:build integration

package app

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/emailnotification"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/testsupport"
)

// Opt-in host browser gate. Its owned SMTP fixtures cannot forward mail; production
// validation and TLS verification remain intact through the existing test dial seam.
func TestPostgresEmailConsoleBrowserTLS(t *testing.T) {
	if os.Getenv("EMAIL_CONSOLE_BROWSER_E2E") != "1" {
		t.Skip("EMAIL_CONSOLE_BROWSER_E2E=1 requires host Node/Chromium and an isolated migrated TEST_DATABASE_URL")
	}
	connection := os.Getenv("TEST_DATABASE_URL")
	if connection == "" {
		t.Fatal("isolated TEST_DATABASE_URL is required")
	}
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", "01234567890123456789012345678901")
	db, err := sql.Open("pgx", connection)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`TRUNCATE repository_quota_alert_events,email_test_deliveries,repository_quota_alert_rules,email_targets,email_test_requests`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()

	store, err := repository.NewPostgresStore(connection)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	accepted := testsupport.NewSMTPFixture(t, true)
	rejected := testsupport.NewSMTPFixture(t, true)
	rejected.RejectRCPT = 550
	cfg := emailnotification.Config{Enabled: true, Host: "smtp.example.test", Port: 465, Mode: "implicit_tls", From: "synthetic-gateway@example.test", ApprovedIPs: []string{"192.0.2.10"}, CAFile: accepted.CAFile, ConsoleOrigin: "https://console.example.invalid"}
	var failure atomic.Bool
	var attempts atomic.Int32
	sender := emailnotification.Sender{Config: cfg, LookupHost: func(context.Context, string) ([]string, error) { return []string{"192.0.2.10"}, nil }, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		attempts.Add(1)
		fixture := accepted
		if failure.Load() {
			fixture = rejected
		}
		return (&net.Dialer{}).DialContext(ctx, network, fixture.Address)
	}}
	// Both fixtures use their own CA; switch the complete sender at the worker boundary.
	worker := EmailNotificationWorker{Store: store, Sender: sender, InstanceID: "email-console-browser-owned"}
	handler := NewGatewayHandler(Dependencies{Email: cfg}, store, TestAdapter{}, testAuthenticator())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__fixture/failure" && r.Method == http.MethodPost {
			failure.Store(true)
			w.WriteHeader(204)
			return
		}
		if r.URL.Path == "/__fixture/attempts" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"attempts":`+strconv.Itoa(int(attempts.Load()))+`}`)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				worker.Sender = sender
				if failure.Load() {
					copySender := sender
					copySender.Config.CAFile = rejected.CAFile
					worker.Sender = copySender
				}
				_, _ = worker.Run(ctx)
			}
		}
	}()
	defer func() { cancel(); <-done }()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, "node", filepath.Join(root, "scripts/email-console-browser-check.mjs"))
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "EMAIL_CONSOLE_API="+server.URL)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser gate failed: %v\n%s", err, output)
	}
	t.Log(string(output))
	messages, commands := accepted.Snapshot()
	if len(messages) != 1 {
		t.Fatalf("expected one accepted synthetic MIME, got %d", len(messages))
	}
	foundRecipient := false
	for _, c := range commands {
		if c == "RCPT TO:<synthetic-recipient@example.test>" {
			foundRecipient = true
		}
	}
	if !foundRecipient {
		t.Fatal("SMTP did not receive the selected synthetic recipient")
	}
	message, err := mail.ReadMessage(bytes.NewReader(messages[0]))
	if err != nil {
		t.Fatal(err)
	}
	contentType, params, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
	if err != nil || contentType != "multipart/alternative" {
		t.Fatal("missing multipart alternative")
	}
	parts := multipart.NewReader(message.Body, params["boundary"])
	count := 0
	for {
		part, e := parts.NextPart()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		body, e := io.ReadAll(part)
		if e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(string(body), "合成") {
			t.Fatal("MIME lost synthetic Chinese semantics")
		}
		count++
	}
	if count != 2 {
		t.Fatal("HTML/plain MIME parts not paired")
	}
	failedMessages, _ := rejected.Snapshot()
	if len(failedMessages) != 0 {
		t.Fatal("rejected recipient unexpectedly received DATA")
	}
}
