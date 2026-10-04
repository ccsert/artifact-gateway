package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/artifact-gateway/artifact-gateway/internal/emailnotification"
	"github.com/artifact-gateway/artifact-gateway/internal/quotaalert"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAdministratorListsQuotaRulesWithoutActivatingSMTP(t *testing.T) {
	h := NewGatewayHandler(Dependencies{}, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator())
	r := httptest.NewRequest(http.MethodGet, "/api/v2/repository-quota-alert-rules", nil)
	authorize(r, "admin-secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("list status=%d cache=%q body=%s", w.Code, w.Header().Get("Cache-Control"), w.Body.String())
	}
}

func TestQuotaRuleUnsupportedRoutesStillHaveNoStore(t *testing.T) {
	h, _, _ := quotaFixture(t)
	for _, path := range []string{"/api/v2/repository-quota-alert-rules:unknown", "/api/v2/repository-quota-alert-rules"} {
		w := quotaRequest(h, "PATCH", path, "", "")
		if w.Code < 400 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("unsupported quota route cache/status: %d %q", w.Code, w.Header().Get("Cache-Control"))
		}
	}
}

func TestQuotaEventAPIKeepsExactLargeByteCounts(t *testing.T) {
	s := repository.NewMemoryStore()
	ctx := context.Background()
	repo, err := s.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "large-quota", Format: repository.FormatRaw, Type: repository.RepositoryTypeHosted, State: repository.RepositoryActive})
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.CreateEmailTarget(ctx, repository.EmailTarget{ID: uuid.NewString(), Name: "Synthetic", Locale: "en", Enabled: true, RecipientCiphertext: "private"})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := s.CreateRepositoryQuotaAlertRule(ctx, repository.RepositoryQuotaAlertRule{ID: uuid.NewString(), RepositoryID: repo.ID, TargetID: target.ID, TargetVersion: target.Version, Enabled: true, Policy: quotaalert.Policy{WarningBasisPoints: 8500, CriticalBasisPoints: 9500, RecoveryBelowBasisPoints: 8000, WarningForSeconds: 1, CriticalForSeconds: 1, RecoveryForSeconds: 1, MaxSampleAgeSeconds: 60}})
	if err != nil {
		t.Fatal(err)
	}
	quota := int64(math.MaxInt64 - 123)
	used := quota - 1
	if _, err = s.ReplaceRepositoryCapacityQuota(ctx, repo.ID, quota); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PutRawAsset(ctx, repository.RawAsset{RepositoryID: repo.ID, Path: "synthetic", Digest: uuid.NewString(), Size: used}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.EvaluateNextRepositoryQuotaAlert(ctx, repository.QuotaAlertMailConfig{}, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1050 * time.Millisecond)
	if _, err = s.EvaluateNextRepositoryQuotaAlert(ctx, repository.QuotaAlertMailConfig{}, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	h := NewGatewayHandler(Dependencies{}, s, TestAdapter{}, testAuthenticator())
	w := quotaRequest(h, "GET", "/api/v2/repository-quota-alert-rules/"+rule.ID+"/events", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"usedBytes":`+strconv.FormatInt(used, 10)) || !strings.Contains(w.Body.String(), `"quotaBytes":`+strconv.FormatInt(quota, 10)) {
		t.Fatalf("byte evidence rounded by API: %s", w.Body.String())
	}
}

func TestQuotaRuleAdministratorAndPasswordGates(t *testing.T) {
	store := repository.NewMemoryStore()
	user, err := store.CreateUser(context.Background(), repository.User{ID: uuid.NewString(), Name: "quota-reset", Role: string(RoleAdmin), SecretHash: "synthetic", MustChangePassword: true})
	if err != nil {
		t.Fatal(err)
	}
	auth := testAuthenticatorWithUsers(store)
	h := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, auth)
	id := uuid.NewString()
	for _, route := range []struct{ method, path string }{{"GET", "/api/v2/repository-quota-alert-rules"}, {"POST", "/api/v2/repository-quota-alert-rules"}, {"GET", "/api/v2/repository-quota-alert-rules/" + id}, {"PUT", "/api/v2/repository-quota-alert-rules/" + id}, {"DELETE", "/api/v2/repository-quota-alert-rules/" + id}, {"GET", "/api/v2/repository-quota-alert-rules/" + id + "/events"}} {
		for _, test := range []struct {
			token  string
			status int
		}{{"", 401}, {"resolver-secret", 403}, {auth.IssueToken("user:" + user.Name), 403}} {
			r := httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`))
			if test.token != "" {
				authorize(r, test.token)
			}
			r.Header.Set("If-Match", "1")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != test.status || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("quota gate %s %s: %d %s", route.method, route.path, w.Code, w.Body.String())
			}
		}
	}
}

func TestQuotaRuleRejectsIncompleteImplicitAndUnknownPolicy(t *testing.T) {
	h, _, valid := quotaFixture(t)
	for _, body := range []string{`{}`, strings.Replace(valid, `"warningForSeconds":300`, `"warningForSeconds":0`, 1), strings.Replace(valid, `"warningBasisPoints":8500`, `"warningBasisPoints":7900`, 1), strings.Replace(valid, `"maxSampleAgeSeconds":60`, `"maxSampleAgeSeconds":0`, 1), strings.Replace(valid, `"policy":{`, `"policy":{"smtpHost":"private-marker",`, 1), valid + `{}`, strings.Repeat("x", 4097)} {
		w := quotaRequest(h, "POST", "/api/v2/repository-quota-alert-rules", body, "")
		if w.Code != 400 || strings.Contains(w.Body.String(), "private-marker") {
			t.Fatalf("implicit/unknown policy accepted: %d %s", w.Code, w.Body.String())
		}
	}
	enabled := strings.Replace(valid, `"policy":`, `"enabled":true,"policy":`, 1)
	w := quotaRequest(h, "POST", "/api/v2/repository-quota-alert-rules", enabled, "")
	if w.Code != 503 || !strings.Contains(w.Body.String(), "email_disabled") {
		t.Fatalf("disabled relay enabled quota rule: %d %s", w.Code, w.Body.String())
	}
}

func TestQuotaRuleActivationNeedsEncryptionAndEnabledTarget(t *testing.T) {
	s := repository.NewMemoryStore()
	ctx := context.Background()
	repo, err := s.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "quota-ready", Format: repository.FormatRaw, Type: repository.RepositoryTypeHosted, State: repository.RepositoryActive})
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.CreateEmailTarget(ctx, repository.EmailTarget{ID: uuid.NewString(), Name: "Synthetic", Locale: "en", RecipientCiphertext: "private"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := emailnotification.Config{Enabled: true, Host: "smtp.example.test", Port: 465, Mode: "implicit_tls", From: "synthetic@example.test", ApprovedIPs: []string{"192.0.2.10"}}
	h := NewGatewayHandler(Dependencies{Email: cfg}, s, TestAdapter{}, testAuthenticator())
	body := fmt.Sprintf(`{"repositoryId":%q,"targetId":%q,"enabled":true,"policy":{"warningBasisPoints":8500,"criticalBasisPoints":9500,"recoveryBelowBasisPoints":8000,"warningForSeconds":300,"criticalForSeconds":120,"recoveryForSeconds":180,"maxSampleAgeSeconds":60}}`, repo.ID, target.ID)
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", "")
	w := quotaRequest(h, "POST", "/api/v2/repository-quota-alert-rules", body, "")
	if w.Code != 503 || !strings.Contains(w.Body.String(), "encryption_key_unavailable") {
		t.Fatalf("missing encryption activated rule: %d %s", w.Code, w.Body.String())
	}
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", "01234567890123456789012345678901")
	w = quotaRequest(h, "POST", "/api/v2/repository-quota-alert-rules", body, "")
	if w.Code != 409 || !strings.Contains(w.Body.String(), "target_disabled") {
		t.Fatalf("disabled target activated rule: %d %s", w.Code, w.Body.String())
	}
}

func TestQuotaRuleFiniteCountAndUniqueScope(t *testing.T) {
	s := repository.NewMemoryStore()
	ctx := context.Background()
	target, err := s.CreateEmailTarget(ctx, repository.EmailTarget{ID: uuid.NewString(), Name: "Synthetic", Locale: "en", RecipientCiphertext: "private"})
	if err != nil {
		t.Fatal(err)
	}
	h := NewGatewayHandler(Dependencies{}, s, TestAdapter{}, testAuthenticator())
	for i := 0; i < 101; i++ {
		repo, err := s.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: fmt.Sprintf("quota-limit-%d", i), Format: repository.FormatRaw, Type: repository.RepositoryTypeHosted, State: repository.RepositoryActive})
		if err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf(`{"repositoryId":%q,"targetId":%q,"policy":{"warningBasisPoints":8500,"criticalBasisPoints":9500,"recoveryBelowBasisPoints":8000,"warningForSeconds":300,"criticalForSeconds":120,"recoveryForSeconds":180,"maxSampleAgeSeconds":60}}`, repo.ID, target.ID)
		w := quotaRequest(h, "POST", "/api/v2/repository-quota-alert-rules", body, "")
		if i == 100 {
			if w.Code != 409 || !strings.Contains(w.Body.String(), "rule_limit") {
				t.Fatalf("quota rule cap bypass: %d %s", w.Code, w.Body.String())
			}
			break
		}
		if w.Code != 201 {
			t.Fatalf("create bounded rule: %d %s", w.Code, w.Body.String())
		}
		if i == 0 {
			again := quotaRequest(h, "POST", "/api/v2/repository-quota-alert-rules", body, "")
			if again.Code != 409 || !strings.Contains(again.Body.String(), "rule_conflict") {
				t.Fatal("duplicate scope accepted")
			}
		}
	}
}

func TestAdministratorCreatesExplicitQuotaRuleDisabledWithoutSMTP(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "quota-synthetic", Format: repository.FormatRaw, Type: repository.RepositoryTypeHosted, State: repository.RepositoryActive})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateEmailTarget(context.Background(), repository.EmailTarget{ID: uuid.NewString(), Name: "Synthetic", Locale: "en", RecipientCiphertext: "private-marker"})
	if err != nil {
		t.Fatal(err)
	}
	h := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	body := fmt.Sprintf(`{"repositoryId":%q,"targetId":%q,"policy":{"warningBasisPoints":8500,"criticalBasisPoints":9500,"recoveryBelowBasisPoints":8000,"warningForSeconds":300,"criticalForSeconds":120,"recoveryForSeconds":180,"maxSampleAgeSeconds":60}}`, repo.ID, target.ID)
	r := httptest.NewRequest(http.MethodPost, "/api/v2/repository-quota-alert-rules", strings.NewReader(body))
	authorize(r, "admin-secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 201 || strings.Contains(w.Body.String(), "private-marker") {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
	}
	var got struct {
		ID, Version, RepositoryID string
		Enabled                   bool
		State                     struct{ Severity, DataState, Phase string }
		Policy                    struct{ WarningBasisPoints int }
	}
	if err = json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Enabled || got.Version != "1" || got.RepositoryID != repo.ID || got.State.Severity != "normal" || got.State.DataState != "disabled" || got.State.Phase != "normal" || got.Policy.WarningBasisPoints != 8500 {
		t.Fatalf("implicit activation/default policy: %#v", got)
	}
	r = httptest.NewRequest(http.MethodGet, "/api/v2/repository-quota-alert-rules", nil)
	authorize(r, "admin-secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), got.ID) {
		t.Fatalf("created rule not queryable: %d %s", w.Code, w.Body.String())
	}
}

func quotaFixture(t *testing.T) (http.Handler, string, string) {
	t.Helper()
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "quota-synthetic", Format: repository.FormatRaw, Type: repository.RepositoryTypeHosted, State: repository.RepositoryActive})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateEmailTarget(ctx, repository.EmailTarget{ID: uuid.NewString(), Name: "Synthetic", Locale: "en", RecipientCiphertext: "private-marker"})
	if err != nil {
		t.Fatal(err)
	}
	h := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	body := fmt.Sprintf(`{"repositoryId":%q,"targetId":%q,"policy":{"warningBasisPoints":8500,"criticalBasisPoints":9500,"recoveryBelowBasisPoints":8000,"warningForSeconds":300,"criticalForSeconds":120,"recoveryForSeconds":180,"maxSampleAgeSeconds":60}}`, repo.ID, target.ID)
	w := quotaRequest(h, "POST", "/api/v2/repository-quota-alert-rules", body, "")
	if w.Code != 201 {
		t.Fatalf("fixture status=%d body=%s", w.Code, w.Body.String())
	}
	var v struct{ ID string }
	if json.Unmarshal(w.Body.Bytes(), &v) != nil {
		t.Fatal("invalid rule")
	}
	return h, v.ID, body
}
func quotaRequest(h http.Handler, method, path, body, match string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	authorize(r, "admin-secret")
	if match != "" {
		r.Header.Set("If-Match", match)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestQuotaRuleConfigurationCASAndSoftDeletionAreQueryable(t *testing.T) {
	h, id, body := quotaFixture(t)
	path := "/api/v2/repository-quota-alert-rules/" + id
	w := quotaRequest(h, "GET", path, "", "")
	if w.Code != 200 || w.Header().Get("ETag") != "1" {
		t.Fatalf("get %d %s", w.Code, w.Body.String())
	}
	changed := strings.Replace(body, `"warningForSeconds":300`, `"warningForSeconds":301`, 1)
	w = quotaRequest(h, "PUT", path, changed, "1")
	if w.Code != 200 || w.Header().Get("ETag") != "2" {
		t.Fatalf("update %d %s", w.Code, w.Body.String())
	}
	w = quotaRequest(h, "PUT", path, body, "1")
	if w.Code != 412 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("stale CAS %d %s", w.Code, w.Body.String())
	}
	w = quotaRequest(h, "PUT", path, changed, "2")
	if w.Code != 200 || w.Header().Get("ETag") != "2" {
		t.Fatalf("no-op edit changed version: %d %s", w.Code, w.Body.String())
	}
	w = quotaRequest(h, "DELETE", path, "", "2")
	if w.Code != 204 || w.Header().Get("ETag") != "3" {
		t.Fatalf("delete %d %s", w.Code, w.Body.String())
	}
	w = quotaRequest(h, "GET", path, "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"deleted":true`) || !strings.Contains(w.Body.String(), `"dataState":"deleted"`) {
		t.Fatalf("deleted state lost: %d %s", w.Code, w.Body.String())
	}
	w = quotaRequest(h, "PUT", path, body, "3")
	if w.Code != 409 {
		t.Fatalf("deleted rule edited: %d %s", w.Code, w.Body.String())
	}
}

func TestQuotaAlertEventsAreQueryableBeforeFirstEvaluation(t *testing.T) {
	h, id, _ := quotaFixture(t)
	w := quotaRequest(h, "GET", "/api/v2/repository-quota-alert-rules/"+id+"/events", "", "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("events status=%d body=%s", w.Code, w.Body.String())
	}
}
