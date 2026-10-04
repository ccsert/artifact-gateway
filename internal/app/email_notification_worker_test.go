package app

import (
	"bytes"
	"context"
	"github.com/artifact-gateway/artifact-gateway/internal/emailnotification"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/secrets"
	"github.com/artifact-gateway/artifact-gateway/internal/testsupport"
	"github.com/google/uuid"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"testing"
	"time"
)

type skewedEmailStore struct{ repository.EmailStore }

func (s skewedEmailStore) ClaimEmailDelivery(ctx context.Context, owner string) (repository.EmailDelivery, error) {
	v, e := s.EmailStore.ClaimEmailDelivery(ctx, owner)
	v.LeaseExpiresAt = v.LeaseExpiresAt.Add(-time.Hour)
	return v, e
}
func TestEmailWorkerDoesNotUseDatabaseWallClockAsLocalDeadline(t *testing.T) {
	t.Setenv(secrets.KeyEnv, "01234567890123456789012345678901")
	ctx := context.Background()
	store := repository.NewMemoryStore()
	id := uuid.NewString()
	cipher, err := secrets.Seal("email-target:"+id, "recipient@example.test")
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateEmailTarget(ctx, repository.EmailTarget{ID: id, Name: "Synthetic", Locale: "en", RecipientCiphertext: cipher, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := store.EnqueueEmailTest(ctx, repository.EmailTestRequest{ID: uuid.NewString(), EventID: uuid.NewString(), RequestKey: uuid.NewString(), TargetID: id, TargetVersion: target.Version, Scenario: "warning", TemplateVersion: "1", From: "gateway@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	f := testsupport.NewSMTPFixture(t, true)
	sender := emailnotification.Sender{Config: emailnotification.Config{Enabled: true, Host: "smtp.example.test", Port: 465, Mode: "implicit_tls", From: "gateway@example.test", ApprovedIPs: []string{"192.0.2.10"}, CAFile: f.CAFile}, LookupHost: func(context.Context, string) ([]string, error) { return []string{"192.0.2.10"}, nil }, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, f.Address)
	}}
	worker := EmailNotificationWorker{Store: skewedEmailStore{store}, Sender: sender, InstanceID: "synthetic/session"}
	if done, e := worker.Run(ctx); e != nil || !done {
		t.Fatalf("run=%v %v", done, e)
	}
	v, err := store.GetEmailDelivery(ctx, queued.ID)
	if err != nil || v.State != "accepted" {
		t.Fatalf("DB clock skew prevented acceptance: state=%s code=%s err=%v", v.State, v.ErrorCode, err)
	}
}

func TestEmailWorkerKeepsMIMEFromAndConsoleSnapshotAcrossConfigChange(t *testing.T) {
	t.Setenv(secrets.KeyEnv, "01234567890123456789012345678901")
	ctx := context.Background()
	store := repository.NewMemoryStore()
	id := uuid.NewString()
	cipher, err := secrets.Seal("email-target:"+id, "recipient@example.test")
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateEmailTarget(ctx, repository.EmailTarget{ID: id, Name: "Synthetic", Locale: "en", RecipientCiphertext: cipher, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.EnqueueEmailTest(ctx, repository.EmailTestRequest{ID: uuid.NewString(), EventID: uuid.NewString(), RequestKey: uuid.NewString(), TargetID: id, TargetVersion: target.Version, Scenario: "resolved", TemplateVersion: "1", From: "original@example.test", ConsoleOrigin: "https://original.example.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	f := testsupport.NewSMTPFixture(t, true)
	sender := emailnotification.Sender{Config: emailnotification.Config{Enabled: true, Host: "smtp.example.test", Port: 465, Mode: "implicit_tls", From: "changed@example.test", ConsoleOrigin: "https://changed.example.invalid", ApprovedIPs: []string{"192.0.2.10"}, CAFile: f.CAFile}, LookupHost: func(context.Context, string) ([]string, error) { return []string{"192.0.2.10"}, nil }, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, f.Address)
	}}
	worker := EmailNotificationWorker{Store: store, Sender: sender, InstanceID: "synthetic/session"}
	if _, err = worker.Run(ctx); err != nil {
		t.Fatal(err)
	}
	messages, _ := f.Snapshot()
	if len(messages) != 1 {
		t.Fatal("missing fixture mail")
	}
	message, err := mail.ReadMessage(bytes.NewReader(messages[0]))
	if err != nil {
		t.Fatal(err)
	}
	if message.Header.Get("From") != "original@example.test" {
		t.Fatal("From drifted after relay reconfiguration")
	}
	_, params, _ := mime.ParseMediaType(message.Header.Get("Content-Type"))
	parts := multipart.NewReader(message.Body, params["boundary"])
	for i := 0; i < 2; i++ {
		part, e := parts.NextPart()
		if e != nil {
			t.Fatal(e)
		}
		body, e := io.ReadAll(part)
		if e != nil || !bytes.Contains(body, []byte("https://original.example.invalid/system?tab=diagnostics")) || bytes.Contains(body, []byte("changed.example.invalid")) {
			t.Fatal("Console link drifted after reconfiguration")
		}
	}
}
