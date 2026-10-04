//go:build integration

package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/artifact-gateway/artifact-gateway/internal/emailnotification"
	"github.com/artifact-gateway/artifact-gateway/internal/quotaalert"
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
	"time"
)

func TestPostgresQuotaHTTPTransitionsDeliverActualTLSMIME(t *testing.T) {
	for _, locale := range []string{"en", "zh-CN"} {
		t.Run(locale, func(t *testing.T) { quotaHTTPTransitionsDeliverActualTLSMIME(t, locale) })
	}
}

func quotaHTTPTransitionsDeliverActualTLSMIME(t *testing.T, locale string) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", "01234567890123456789012345678901")
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.Exec(`TRUNCATE repository_quota_alert_events,email_test_deliveries,repository_quota_alert_rules,email_targets,email_test_requests`); err != nil {
		t.Fatal(err)
	}
	store, err := repository.NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "quota-http-" + uuid.NewString(), Format: repository.FormatRaw, Type: repository.RepositoryTypeHosted, State: repository.RepositoryActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryCapacityQuota(ctx, repo.ID, 1000); err != nil {
		t.Fatal(err)
	}
	f := testsupport.NewSMTPFixture(t, true)
	cfg := emailnotification.Config{Enabled: true, Host: "smtp.example.test", Port: 465, Mode: "implicit_tls", From: "gateway@example.test", ApprovedIPs: []string{"192.0.2.10"}, CAFile: f.CAFile, ConsoleOrigin: "https://console.example.invalid"}
	h := NewGatewayHandler(Dependencies{Email: cfg, NativeOCIObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator())
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		authorize(r, "admin-secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	targetResponse := request("POST", "/api/v2/email-targets", fmt.Sprintf(`{"name":"Synthetic quota","locale":%q,"recipient":"synthetic@example.test","enabled":true}`, locale))
	if targetResponse.Code != 201 {
		t.Fatalf("target: %d %s", targetResponse.Code, targetResponse.Body.String())
	}
	var target struct{ ID string }
	_ = json.Unmarshal(targetResponse.Body.Bytes(), &target)
	body := fmt.Sprintf(`{"repositoryId":%q,"targetId":%q,"enabled":true,"policy":{"warningBasisPoints":8500,"criticalBasisPoints":9500,"recoveryBelowBasisPoints":8000,"warningForSeconds":1,"criticalForSeconds":1,"recoveryForSeconds":1,"maxSampleAgeSeconds":60}}`, repo.ID, target.ID)
	created := request("POST", "/api/v2/repository-quota-alert-rules", body)
	if created.Code != 201 {
		t.Fatalf("rule: %d %s", created.Code, created.Body.String())
	}
	var rule struct{ ID string }
	_ = json.Unmarshal(created.Body.Bytes(), &rule)
	sender := emailnotification.Sender{Config: cfg, LookupHost: func(context.Context, string) ([]string, error) { return []string{"192.0.2.10"}, nil }, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, f.Address)
	}}
	mailCfg := repository.QuotaAlertMailConfig{Enabled: true, From: cfg.From, ConsoleOrigin: cfg.ConsoleOrigin}
	var episode string
	for i, step := range []struct {
		size     int
		scenario string
	}{{880, "warning"}, {980, "critical"}, {790, "resolved"}} {
		written := request("PUT", "/raw/"+repo.Name+"/file.bin", strings.Repeat("q", step.size))
		if written.Code < 200 || written.Code >= 300 {
			t.Fatalf("data PUT: %d %s", written.Code, written.Body.String())
		}
		if _, err = store.EvaluateNextRepositoryQuotaAlert(ctx, mailCfg, time.Millisecond); err != nil {
			t.Fatal(err)
		}
		time.Sleep(1050 * time.Millisecond)
		if _, err = store.EvaluateNextRepositoryQuotaAlert(ctx, mailCfg, time.Millisecond); err != nil {
			t.Fatal(err)
		}
		eventsResponse := request("GET", "/api/v2/repository-quota-alert-rules/"+rule.ID+"/events", "")
		if eventsResponse.Code != 200 {
			t.Fatalf("events: %d %s", eventsResponse.Code, eventsResponse.Body.String())
		}
		var events []struct {
			quotaalert.Event
			DeliveryID, DeliveryState, NotificationCode string
		}
		if json.Unmarshal(eventsResponse.Body.Bytes(), &events) != nil || len(events) != i+1 {
			t.Fatalf("actual quota transition missing: %s", eventsResponse.Body.String())
		}
		event := events[0]
		if i == 0 {
			episode = event.EpisodeID
		}
		if event.Scenario != step.scenario || event.UsedBytes != int64(step.size) || event.QuotaBytes != 1000 || event.EpisodeID != episode || event.Sequence != int64(i+1) || event.NotificationCode != "queued" {
			t.Fatalf("snapshot: %#v", event)
		}
		// New process uses the same durable event/outbox; no fabricated enqueue.
		reopened, e := repository.NewPostgresStore(url)
		if e != nil {
			t.Fatal(e)
		}
		worked, e := (EmailNotificationWorker{Store: reopened, Sender: sender, InstanceID: "quota-reopen/session"}).Run(ctx)
		_ = reopened.Close()
		if e != nil || !worked {
			t.Fatalf("worker: %v %v", worked, e)
		}
		delivery, e := store.GetEmailDelivery(ctx, event.DeliveryID)
		if e != nil || delivery.State != "accepted" {
			t.Fatalf("actual mail: %s %s %v", delivery.State, delivery.ErrorCode, e)
		}
		messages, _ := f.Snapshot()
		if len(messages) != i+1 {
			t.Fatal("isolated TLS sink received no actual mail")
		}
		{
			msg, e := mail.ReadMessage(strings.NewReader(string(messages[i])))
			if e != nil {
				t.Fatal(e)
			}
			_, params, e := mime.ParseMediaType(msg.Header.Get("Content-Type"))
			if e != nil {
				t.Fatal(e)
			}
			parts := multipart.NewReader(msg.Body, params["boundary"])
			for j := 0; j < 2; j++ {
				part, e := parts.NextPart()
				if e != nil {
					t.Fatal(e)
				}
				data, _ := io.ReadAll(part)
				label := "logical quota"
				if locale == "zh-CN" {
					label = "逻辑配额"
				}
				if !strings.Contains(string(data), repo.Name) || !strings.Contains(string(data), event.ID) || !strings.Contains(string(data), label) {
					t.Fatalf("actual MIME omitted evidence: %s", data)
				}
			}
		}
	}
	time.Sleep(2 * time.Millisecond)
	if _, err = store.EvaluateNextRepositoryQuotaAlert(ctx, mailCfg, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListRepositoryQuotaAlertEvents(ctx, rule.ID)
	if err != nil || len(events) != 3 {
		t.Fatalf("stable duplicate: %v %v", events, err)
	}
}
