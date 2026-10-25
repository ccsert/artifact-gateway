package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestEmailQueueDiagnosticsForcedPasswordAndCancellation(t *testing.T) {
	s := repository.NewMemoryStore()
	_, err := s.CreateUser(context.Background(), repository.User{ID: uuid.NewString(), Name: "diagnostics-reset", Role: string(RoleAdmin), SecretHash: "synthetic", MustChangePassword: true})
	if err != nil {
		t.Fatal(err)
	}
	auth := testAuthenticatorWithUsers(s)
	h := newTestGateway(t, GatewayOptions{Store: s, Adapter: TestAdapter{}, Authenticator: auth})
	r := httptest.NewRequest("GET", "/api/v2/email-queue/diagnostics", nil)
	authorize(r, auth.IssueToken("user:diagnostics-reset"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "password_change_required") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("forced password=%d %s", w.Code, w.Body.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r = httptest.NewRequest("GET", "/api/v2/email-queue/diagnostics", nil).WithContext(ctx)
	authorize(r, "admin-secret")
	h = newTestGateway(t, GatewayOptions{Store: s, Adapter: TestAdapter{}, Authenticator: testAuthenticator()})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 500 || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "context canceled") {
		t.Fatalf("cancellation=%d %s", w.Code, w.Body.String())
	}
}

func TestEmailQueueDiagnosticsReadOnlyAuthorizationAndEvidence(t *testing.T) {
	s := repository.NewMemoryStore()
	ctx := context.Background()
	target, err := s.CreateEmailTarget(ctx, repository.EmailTarget{ID: uuid.NewString(), Enabled: true, RecipientCiphertext: "private-recipient-ciphertext", Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.EnqueueEmailTest(ctx, repository.EmailTestRequest{ID: uuid.NewString(), EventID: uuid.NewString(), RequestKey: uuid.NewString(), TargetID: target.ID, TargetVersion: target.Version, From: "private-smtp@example.test", ConsoleOrigin: "https://private-smtp.test"})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := s.ListEmailDeliveries(ctx, 100)
	h := newTestGateway(t, GatewayOptions{Store: s, Adapter: TestAdapter{}, Authenticator: testAuthenticator()})
	for _, tc := range []struct {
		token  string
		status int
	}{{"", 401}, {"resolver-secret", 403}, {"admin-secret", 200}} {
		r := httptest.NewRequest("GET", "/api/v2/email-queue/diagnostics", nil)
		if tc.token != "" {
			authorize(r, tc.token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("status=%d cache=%q body=%s", w.Code, w.Header().Get("Cache-Control"), w.Body.String())
		}
		if tc.status == 200 {
			var got struct {
				Scope, Clock, WorkerHealth string
				Counts                     struct{ Total, ExecutableCandidates int }
				LastSmtpAcceptedAt         *string
				EvidenceLimitations        []string
			}
			if err = json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Scope != "whole_retained_queue" || got.Clock != "memory_store" || got.WorkerHealth != "unknown" || got.Counts.Total != 1 || got.Counts.ExecutableCandidates != 1 || got.LastSmtpAcceptedAt != nil || len(got.EvidenceLimitations) == 0 {
				t.Fatalf("snapshot=%s", w.Body.String())
			}
			for _, secret := range []string{"private-", "recipient", "smtpHost", "keyDigest", "leaseToken", "leaseOwner", "ciphertext", "html", "healthy", "stalled"} {
				if strings.Contains(w.Body.String(), secret) {
					t.Fatalf("unsafe evidence %q: %s", secret, w.Body.String())
				}
			}
		}
	}
	after, _ := s.ListEmailDeliveries(ctx, 100)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("diagnostic GET mutated queue")
	}
}
