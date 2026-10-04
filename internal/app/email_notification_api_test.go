package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/artifact-gateway/artifact-gateway/internal/emailnotification"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestAdministratorCanPreviewWarningEmailWithoutSMTP(t *testing.T) {
	h := NewGatewayHandler(Dependencies{}, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator())
	r := httptest.NewRequest(http.MethodPost, "/api/v2/email-notifications:preview", strings.NewReader(`{"scenario":"warning","locale":"zh-CN"}`))
	authorize(r, "admin-secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", w.Code, w.Body.String())
	}
	var got struct {
		Subject, HTML, Text, TemplateVersion string
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Subject, "警告") || !strings.Contains(got.HTML, "12%") || !strings.Contains(got.Text, "12%") || got.TemplateVersion != "2" {
		t.Fatalf("missing designed HTML/plain warning preview: %#v", got)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("administrator preview must not be cached")
	}
}

func TestEmailTargetsRequireAdministratorAndHideRecipient(t *testing.T) {
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", "01234567890123456789012345678901")
	h := NewGatewayHandler(Dependencies{}, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator())
	r := httptest.NewRequest(http.MethodPost, "/api/v2/email-targets", strings.NewReader(`{"name":"Operations","recipient":"private@example.test","locale":"en"}`))
	authorize(r, "admin-secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "private@example.test") || strings.Contains(w.Body.String(), "cipher") {
		t.Fatal("recipient leaked")
	}
	var got struct {
		Enabled             bool
		RecipientConfigured bool
		Version             string
	}
	if json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Enabled || !got.RecipientConfigured || got.Version != "1" {
		t.Fatal("target must default disabled")
	}
}

func TestAdministratorEmailTestEnqueuesOnceAndDoesNotSendInHTTP(t *testing.T) {
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", "01234567890123456789012345678901")
	store := repository.NewMemoryStore()
	target, err := store.CreateEmailTarget(context.Background(), repository.EmailTarget{ID: uuid.NewString(), Name: "Synthetic", Locale: "en", RecipientCiphertext: "synthetic", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	h := NewGatewayHandler(Dependencies{Email: emailnotification.Config{Enabled: true, Host: "smtp.example.test", Port: 465, Mode: "implicit_tls", From: "gateway@example.test", ApprovedIPs: []string{"192.0.2.10"}}}, store, TestAdapter{}, testAuthenticator())
	key := uuid.NewString()
	var id string
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest(http.MethodPost, "/api/v2/email-notifications:test", strings.NewReader(fmt.Sprintf(`{"targetId":"%s","scenario":"warning"}`, target.ID)))
		authorize(r, "admin-secret")
		r.Header.Set("If-Match", target.Version)
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 202 {
			t.Fatalf("test status=%d body=%s", w.Code, w.Body.String())
		}
		var got struct{ ID, State string }
		if json.Unmarshal(w.Body.Bytes(), &got) != nil || got.State != "pending" {
			t.Fatal("HTTP request must enqueue a pending delivery")
		}
		if i == 0 {
			id = got.ID
		} else if got.ID != id {
			t.Fatal("same key created a duplicate")
		}
	}
	deliveries, err := store.ListEmailDeliveries(context.Background(), 50)
	if err != nil || len(deliveries) != 1 || deliveries[0].Attempts != 0 {
		t.Fatal("HTTP handler sent mail or duplicated queue")
	}
}

func TestEmailManagementAuthorizationAndDisabledGate(t *testing.T) {
	store := repository.NewMemoryStore()
	h := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	id := uuid.NewString()
	for _, endpoint := range []struct{ method, path string }{{"POST", "/api/v2/email-notifications:preview"}, {"GET", "/api/v2/email-notifications"}, {"GET", "/api/v2/email-targets"}, {"POST", "/api/v2/email-targets"}, {"GET", "/api/v2/email-targets/" + id}, {"PUT", "/api/v2/email-targets/" + id}, {"POST", "/api/v2/email-notifications:test"}, {"GET", "/api/v2/email-deliveries"}, {"GET", "/api/v2/email-deliveries/" + id}, {"POST", "/api/v2/email-deliveries/" + id + ":replay"}} {
		for _, auth := range []struct {
			token  string
			status int
		}{{"", 401}, {"resolver-secret", 403}} {
			r := httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader(`{}`))
			if auth.token != "" {
				authorize(r, auth.token)
			}
			r.Header.Set("If-Match", "1")
			r.Header.Set("Idempotency-Key", uuid.NewString())
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != auth.status {
				t.Fatalf("%s %s auth status=%d", endpoint.method, endpoint.path, w.Code)
			}
		}
	}
	r := httptest.NewRequest("POST", "/api/v2/email-notifications:test", strings.NewReader(`{}`))
	authorize(r, "admin-secret")
	r.Header.Set("If-Match", "1")
	r.Header.Set("Idempotency-Key", uuid.NewString())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "email_disabled") {
		t.Fatal("disabled relay queued mail")
	}
	d, _ := store.ListEmailDeliveries(context.Background(), 100)
	if len(d) != 0 {
		t.Fatal("disabled test created a delivery")
	}
}

func TestEmailIdempotencyRetainsV1DescriptorAcrossTemplateUpgrade(t *testing.T) {
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", "01234567890123456789012345678901")
	s := repository.NewMemoryStore()
	ctx := context.Background()
	target, err := s.CreateEmailTarget(ctx, repository.EmailTarget{ID: uuid.NewString(), Name: "Synthetic", Locale: "en", Enabled: true, RecipientCiphertext: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	old, err := s.EnqueueEmailTest(ctx, repository.EmailTestRequest{ID: uuid.NewString(), EventID: uuid.NewString(), RequestKey: key, TargetID: target.ID, TargetVersion: target.Version, Scenario: "warning", TemplateVersion: "1", From: "original@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := emailnotification.Config{Enabled: true, Host: "smtp.example.test", Port: 465, Mode: "implicit_tls", From: "new@example.test", ApprovedIPs: []string{"192.0.2.10"}}
	h := NewGatewayHandler(Dependencies{Email: cfg}, s, TestAdapter{}, testAuthenticator())
	r := httptest.NewRequest("POST", "/api/v2/email-notifications:test", strings.NewReader(`{"targetId":"`+target.ID+`","scenario":"warning"}`))
	authorize(r, "admin-secret")
	r.Header.Set("If-Match", target.Version)
	r.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 202 || !strings.Contains(w.Body.String(), old.ID) || !strings.Contains(w.Body.String(), `"templateVersion":"1"`) {
		t.Fatalf("same client request conflicted after template upgrade: %d %s", w.Code, w.Body.String())
	}
	retained, err := s.GetEmailDelivery(ctx, old.ID)
	if err != nil || retained.From != "original@example.test" || retained.TemplateVersion != "1" {
		t.Fatal("retry rewrote immutable descriptor")
	}
}

func TestEmailManagementRejectsForcedPasswordChange(t *testing.T) {
	store := repository.NewMemoryStore()
	_, err := store.CreateUser(context.Background(), repository.User{ID: uuid.NewString(), Name: "email-reset", Role: string(RoleAdmin), SecretHash: "synthetic", MustChangePassword: true})
	if err != nil {
		t.Fatal(err)
	}
	auth := testAuthenticatorWithUsers(store)
	h := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, auth)
	for _, path := range []string{"/api/v2/email-notifications:preview", "/api/v2/email-targets", "/api/v2/email-notifications:test"} {
		r := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
		authorize(r, auth.IssueToken("user:email-reset"))
		r.Header.Set("If-Match", "1")
		r.Header.Set("Idempotency-Key", uuid.NewString())
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 || !strings.Contains(w.Body.String(), "password_change_required") {
			t.Fatal("forced password gate bypass")
		}
	}
}

func TestEmailTargetCASValidationMissingKeyAndDeadReplay(t *testing.T) {
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", "01234567890123456789012345678901")
	store := repository.NewMemoryStore()
	cfg := emailnotification.Config{Enabled: true, Host: "smtp.example.test", Port: 465, Mode: "implicit_tls", From: "gateway@example.test", ApprovedIPs: []string{"192.0.2.10"}}
	h := NewGatewayHandler(Dependencies{Email: cfg}, store, TestAdapter{}, testAuthenticator())
	request := func(method, path, body, match, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		authorize(r, "admin-secret")
		r.Header.Set("If-Match", match)
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, body := range []string{`{"name":"a","locale":"en","recipient":"a@example.test\r\nBcc: other@example.test"}`, `{"name":"a","locale":"en","recipient":"a@example.test","extra":"secret"}`, `{"name":"a","locale":"en"}`} {
		if w := request("POST", "/api/v2/email-targets", body, "", ""); w.Code != 400 {
			t.Fatal("unsafe target accepted")
		}
	}
	created := request("POST", "/api/v2/email-targets", `{"name":"Ops","locale":"en","recipient":"private@example.test","enabled":true}`, "", "")
	if created.Code != 201 {
		t.Fatal("target create failed")
	}
	var target struct{ ID, Version string }
	_ = json.Unmarshal(created.Body.Bytes(), &target)
	path := "/api/v2/email-targets/" + target.ID
	get := request("GET", path, "", "", "")
	if get.Code != 200 || strings.Contains(get.Body.String(), "private@example.test") {
		t.Fatal("target query leaked")
	}
	if request("GET", "/api/v2/email-targets", "", "", "").Code != 200 {
		t.Fatal("target list failed")
	}
	update := `{"name":"Ops renamed","locale":"zh-CN","enabled":true}`
	if request("PUT", path, update, "0", "").Code != 412 {
		t.Fatal("target ignored CAS")
	}
	updated := request("PUT", path, update, target.Version, "")
	if updated.Code != 200 {
		t.Fatal("target update failed")
	}
	_ = json.Unmarshal(updated.Body.Bytes(), &target)
	body := fmt.Sprintf(`{"targetId":"%s","scenario":"resolved"}`, target.ID)
	key := uuid.NewString()
	queued := request("POST", "/api/v2/email-notifications:test", body, target.Version, key)
	if queued.Code != 202 {
		t.Fatal("enqueue failed")
	}
	conflicting := fmt.Sprintf(`{"targetId":"%s","scenario":"critical"}`, target.ID)
	if request("POST", "/api/v2/email-notifications:test", conflicting, target.Version, key).Code != 409 {
		t.Fatal("idempotency conflict not rejected")
	}
	claim, err := store.ClaimEmailDelivery(context.Background(), "synthetic/session")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.FinishEmailDelivery(context.Background(), claim.ID, claim.LeaseToken, repository.EmailAttemptResult{Code: "smtp_permanent_rejection", Permanent: true}); err != nil {
		t.Fatal(err)
	}
	status := request("GET", "/api/v2/email-deliveries/"+claim.ID, "", "", "")
	if status.Code != 200 {
		t.Fatal("delivery get failed")
	}
	var dead struct{ Version string }
	_ = json.Unmarshal(status.Body.Bytes(), &dead)
	replay := request("POST", "/api/v2/email-deliveries/"+claim.ID+":replay", "", dead.Version, "")
	if replay.Code != 200 || !strings.Contains(replay.Body.String(), `"state":"pending"`) {
		t.Fatal("dead replay failed")
	}
	if request("POST", "/api/v2/email-deliveries/"+claim.ID+":replay", "", dead.Version, "").Code != 412 {
		t.Fatal("stale replay mutated delivery")
	}
	if request("GET", "/api/v2/email-deliveries?limit=0", "", "", "").Code != 400 || request("GET", "/api/v2/email-deliveries?limit=100", "", "", "").Code != 200 {
		t.Fatal("delivery query bounds failed")
	}
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", "")
	if w := request("GET", "/api/v2/email-notifications", "", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "encryption_key_unavailable") {
		t.Fatal("capability hid missing encryption key")
	}
	if request("POST", "/api/v2/email-notifications:test", body, target.Version, uuid.NewString()).Code != 503 {
		t.Fatal("no-key channel queued mail")
	}
}

func TestEmailEarlyRejectionsAreNotCached(t *testing.T) {
	h := NewGatewayHandler(Dependencies{}, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator())
	for _, test := range []struct {
		method, path, token string
		status              int
	}{{"GET", "/api/v2/email-targets", "", 401}, {"GET", "/api/v2/email-targets/not-a-uuid", "admin-secret", 400}, {"POST", "/api/v2/email-notifications:test", "admin-secret", 400}, {"POST", "/api/v2/email-deliveries/not-a-uuid:replay", "admin-secret", 400}} {
		r := httptest.NewRequest(test.method, test.path, strings.NewReader(`{}`))
		if test.token != "" {
			authorize(r, test.token)
		}
		r.Header.Set("If-Match", "1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != test.status || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("early rejection status=%d cache=%q", w.Code, w.Header().Get("Cache-Control"))
		}
	}
}
