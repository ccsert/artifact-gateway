package emailnotification

import (
	"context"
	"github.com/artifact-gateway/artifact-gateway/internal/testsupport"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureSender(f *testsupport.SMTPFixture) Sender {
	return Sender{Config: Config{Enabled: true, Host: "smtp.example.test", Port: 465, Mode: "implicit_tls", From: "gateway@example.test", ApprovedIPs: []string{"192.0.2.10"}, CAFile: f.CAFile}, LookupHost: func(context.Context, string) ([]string, error) { return []string{"192.0.2.10"}, nil }, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, f.Address)
	}}
}
func TestTLSFixtureAcceptsMessage(t *testing.T) {
	f := testsupport.NewSMTPFixture(t, true)
	s := fixtureSender(f)
	result := s.Send(context.Background(), "recipient@example.test", "event-001", []byte("From: gateway@example.test\r\nTo: recipient@example.test\r\nSubject: synthetic\r\n\r\nhello\r\n"))
	if result.Code != "" {
		t.Fatalf("send result=%+v", result)
	}
	messages, _ := f.Snapshot()
	if len(messages) != 1 || !strings.Contains(string(messages[0]), "hello") {
		t.Fatal("TLS fixture did not capture MIME message")
	}
}

func TestSenderRequiresTLSAndPinnedRelay(t *testing.T) {
	for _, test := range []struct {
		name      string
		change    func(*Sender, *testsupport.SMTPFixture)
		code      string
		permanent bool
	}{
		{"required STARTTLS", func(s *Sender, f *testsupport.SMTPFixture) { s.Config.Mode = "starttls_required"; f.NoSTARTTLS = true }, "tls_required", true},
		{"untrusted certificate", func(s *Sender, f *testsupport.SMTPFixture) { s.Config.CAFile = "" }, "tls_verification_failed", false},
		{"changed DNS", func(s *Sender, f *testsupport.SMTPFixture) {
			s.LookupHost = func(context.Context, string) ([]string, error) { return []string{"192.0.2.11"}, nil }
		}, "relay_address_denied", true},
		{"loopback", func(s *Sender, f *testsupport.SMTPFixture) { s.Config.ApprovedIPs = []string{"127.0.0.1"} }, "invalid_message", true},
		{"temporary SMTP", func(s *Sender, f *testsupport.SMTPFixture) { f.RejectRCPT = 451 }, "smtp_temporary_rejection", false},
		{"permanent SMTP", func(s *Sender, f *testsupport.SMTPFixture) { f.RejectRCPT = 550 }, "smtp_permanent_rejection", true},
		{"unknown outcome", func(s *Sender, f *testsupport.SMTPFixture) { f.DropFinalReply = true }, "outcome_unknown", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			implicit := test.name != "required STARTTLS"
			f := testsupport.NewSMTPFixture(t, implicit)
			s := fixtureSender(f)
			test.change(&s, f)
			got := s.Send(context.Background(), "recipient@example.test", "event", []byte("Subject: test\r\n\r\ntest\r\n"))
			if got.Code != test.code || got.Permanent != test.permanent || got.OutcomeUnknown != (test.name == "unknown outcome") {
				t.Fatalf("result=%+v", got)
			}
			m, c := f.Snapshot()
			if test.name != "unknown outcome" && len(m) != 0 {
				t.Fatal("unexpected DATA")
			}
			if test.name == "required STARTTLS" {
				for _, line := range c {
					if strings.HasPrefix(line, "MAIL") || strings.HasPrefix(line, "AUTH") {
						t.Fatal("downgraded connection")
					}
				}
			}
		})
	}
}
func TestRequiredSTARTTLSAcceptsMessage(t *testing.T) {
	f := testsupport.NewSMTPFixture(t, false)
	s := fixtureSender(f)
	s.Config.Mode = "starttls_required"
	if r := s.Send(context.Background(), "recipient@example.test", "event", []byte("Subject: test\r\n\r\ntest\r\n")); r.Code != "" {
		t.Fatalf("result=%+v", r)
	}
}

func TestTLSAuthenticationUsesOnlyOwnedSyntheticSecretFile(t *testing.T) {
	f := testsupport.NewSMTPFixture(t, true)
	s := fixtureSender(f)
	s.Config.AuthFile = filepath.Join(t.TempDir(), "synthetic-auth.json")
	if err := os.WriteFile(s.Config.AuthFile, []byte(`{"username":"fixture","password":"synthetic-only"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if r := s.Send(context.Background(), "recipient@example.test", "event", []byte("Subject: test\r\n\r\ntest\r\n")); r.Code != "" {
		t.Fatalf("result=%+v", r)
	}
	reject := testsupport.NewSMTPFixture(t, true)
	reject.RejectAuth = true
	s = fixtureSender(reject)
	s.Config.AuthFile = filepath.Join(t.TempDir(), "synthetic-auth.json")
	if err := os.WriteFile(s.Config.AuthFile, []byte(`{"username":"fixture","password":"synthetic-only"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if r := s.Send(context.Background(), "recipient@example.test", "event", []byte("Subject: test\r\n\r\ntest\r\n")); r.Code != "authentication_failed" {
		t.Fatal("unsafe auth failure")
	}
	messages, _ := reject.Snapshot()
	if len(messages) != 0 {
		t.Fatal("DATA after failed auth")
	}
}
