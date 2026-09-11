package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/secrets"
	"github.com/google/uuid"
)

const upstreamAuthTestKey = "0123456789abcdef0123456789abcdef"

func goProxyBody(upstreamAuth string) string {
	body := `{"name":"go-proxy","format":"go","type":"proxy","endpoint":"https://proxy.golang.org","allowedHosts":["proxy.golang.org"]`
	if upstreamAuth != "" {
		body += `,"upstreamAuth":` + upstreamAuth
	}
	return body + "}"
}

func TestRepositoryUpstreamAuthCreateRedactsAndEncrypts(t *testing.T) {
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", upstreamAuthTestKey)
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())

	r := httptest.NewRequest(http.MethodPost, "/api/v2/repositories", strings.NewReader(goProxyBody(`{"scheme":"bearer","secret":"ghp_s3cret"}`)))
	r.Header.Set("Idempotency-Key", "create-upstream-auth-1")
	authorize(r, "admin-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create=%d body=%s", rec.Code, rec.Body.String())
	}
	var response repository.HostedRepository
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.UpstreamAuth == nil || response.UpstreamAuth.Scheme != repository.UpstreamAuthSchemeBearer {
		t.Fatalf("response upstreamAuth=%+v", response.UpstreamAuth)
	}
	if !response.UpstreamAuth.CredentialsConfigured {
		t.Fatalf("expected credentialsConfigured: %+v", response.UpstreamAuth)
	}
	if response.UpstreamAuth.Secret != "" || strings.Contains(rec.Body.String(), "ghp_s3cret") {
		t.Fatalf("response leaks the upstream secret: %s", rec.Body.String())
	}

	persisted, err := store.GetHostedRepository(context.Background(), response.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.UpstreamAuth == nil || persisted.UpstreamAuth.Secret == "" || persisted.UpstreamAuth.Secret == "ghp_s3cret" {
		t.Fatalf("persisted secret not encrypted: %+v", persisted.UpstreamAuth)
	}
	if persisted.UpstreamAuth.CredentialsConfigured {
		t.Fatalf("response-only marker must not persist: %+v", persisted.UpstreamAuth)
	}
}

func TestRepositoryUpstreamAuthUpdateKeepsClearsAndSwitchesScheme(t *testing.T) {
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", upstreamAuthTestKey)
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID:           uuid.NewString(),
		Name:         "go-proxy",
		Format:       repository.FormatGo,
		Type:         repository.RepositoryTypeProxy,
		Endpoint:     "https://proxy.golang.org",
		AllowedHosts: []string{"proxy.golang.org"},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	patch := func(version, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPatch, "/api/v2/repositories/"+repo.ID, strings.NewReader(body))
		authorize(r, "admin-secret")
		r.Header.Set("If-Match", version)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		return rec
	}

	configured := patch("1", `{"upstreamAuth":{"scheme":"basic","username":"gateway","secret":"s3cret"}}`)
	if configured.Code != http.StatusOK {
		t.Fatalf("configure=%d body=%s", configured.Code, configured.Body.String())
	}
	if strings.Contains(configured.Body.String(), "s3cret") || !strings.Contains(configured.Body.String(), `"credentialsConfigured":true`) {
		t.Fatalf("configure body=%s", configured.Body.String())
	}
	persisted, _ := store.GetHostedRepository(context.Background(), repo.ID)
	ciphertext := persisted.UpstreamAuth.Secret
	if ciphertext == "" || ciphertext == "s3cret" {
		t.Fatalf("stored secret=%q", ciphertext)
	}

	// Omitting the secret must reuse the stored one only while the scheme is
	// unchanged.
	kept := patch("2", `{"upstreamAuth":{"scheme":"basic","username":"gateway"}}`)
	if kept.Code != http.StatusOK {
		t.Fatalf("keep=%d body=%s", kept.Code, kept.Body.String())
	}
	persisted, _ = store.GetHostedRepository(context.Background(), repo.ID)
	if persisted.UpstreamAuth.Secret != ciphertext {
		t.Fatalf("secret not preserved across update: %+v", persisted.UpstreamAuth)
	}

	// Switching scheme without resubmitting the secret must fail loudly rather
	// than silently reuse a credential the operator did not intend.
	switched := patch("3", `{"upstreamAuth":{"scheme":"bearer"}}`)
	if switched.Code != http.StatusBadRequest {
		t.Fatalf("scheme switch without secret=%d body=%s", switched.Code, switched.Body.String())
	}

	// Both schemes require a secret, so removing the credential is expressed by
	// switching to none rather than by clearing the secret in place.
	removed := patch("3", `{"upstreamAuth":{"scheme":"none"}}`)
	if removed.Code != http.StatusOK {
		t.Fatalf("remove=%d body=%s", removed.Code, removed.Body.String())
	}
	if strings.Contains(removed.Body.String(), `"credentialsConfigured":true`) {
		t.Fatalf("remove body=%s", removed.Body.String())
	}
	persisted, _ = store.GetHostedRepository(context.Background(), repo.ID)
	if persisted.UpstreamAuth != nil {
		t.Fatalf("expected no stored upstreamAuth, got %+v", persisted.UpstreamAuth)
	}
}

func TestRepositoryUpstreamAuthRejectedOutsideGoProxy(t *testing.T) {
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", upstreamAuthTestKey)
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())

	// A non-Go proxy format must reject the field outright. Accepting it would
	// let an operator configure authentication that the fetch path never reads.
	post := func(idempotencyKey, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v2/repositories", strings.NewReader(body))
		r.Header.Set("Idempotency-Key", idempotencyKey)
		authorize(r, "admin-secret")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		return rec
	}
	maven := post("reject-maven", `{"name":"maven-proxy","format":"maven","type":"proxy","endpoint":"https://repo1.maven.org","upstreamAuth":{"scheme":"bearer","secret":"s3cret"}}`)
	if maven.Code != http.StatusBadRequest || !strings.Contains(maven.Body.String(), "Go Proxy") {
		t.Fatalf("maven upstreamAuth=%d body=%s", maven.Code, maven.Body.String())
	}

	// Hosted repositories reject proxy-only settings as before.
	hosted := post("reject-hosted", `{"name":"go-hosted","format":"go","upstreamAuth":{"scheme":"bearer","secret":"s3cret"}}`)
	if hosted.Code != http.StatusBadRequest {
		t.Fatalf("hosted upstreamAuth=%d body=%s", hosted.Code, hosted.Body.String())
	}

	// The same guard applies on update.
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: uuid.NewString(), Name: "npm-proxy", Format: repository.FormatNPM, Type: repository.RepositoryTypeProxy,
		Endpoint: "https://registry.npmjs.org", AllowedHosts: []string{"registry.npmjs.org"},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPatch, "/api/v2/repositories/"+repo.ID, strings.NewReader(`{"upstreamAuth":{"scheme":"bearer","secret":"s3cret"}}`))
	authorize(r, "admin-secret")
	r.Header.Set("If-Match", "1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Go Proxy") {
		t.Fatalf("npm update upstreamAuth=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRepositoryUpstreamAuthValidation(t *testing.T) {
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", upstreamAuthTestKey)
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	post := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v2/repositories", strings.NewReader(body))
		r.Header.Set("Idempotency-Key", uuid.NewString())
		authorize(r, "admin-secret")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		return rec
	}
	cases := []struct {
		name string
		body string
	}{
		{"basic without username", goProxyBody(`{"scheme":"basic","secret":"s3cret"}`)},
		{"basic without secret", goProxyBody(`{"scheme":"basic","username":"gateway"}`)},
		{"bearer with username", goProxyBody(`{"scheme":"bearer","username":"gateway","secret":"s3cret"}`)},
		{"unknown scheme", goProxyBody(`{"scheme":"oauth","secret":"s3cret"}`)},
		{"none with credentials", goProxyBody(`{"scheme":"none","secret":"s3cret"}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rec := post(tc.body); rec.Code != http.StatusBadRequest {
				t.Fatalf("post=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestRepositoryUpstreamAuthRequiresEncryptionKey(t *testing.T) {
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", "")
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	r := httptest.NewRequest(http.MethodPost, "/api/v2/repositories", strings.NewReader(goProxyBody(`{"scheme":"bearer","secret":"s3cret"}`)))
	r.Header.Set("Idempotency-Key", uuid.NewString())
	authorize(r, "admin-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "encryption key") {
		t.Fatalf("create=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestRepositoryUpstreamAuthPurposeIsBound pins the AAD binding: a ciphertext
// sealed for a different purpose must never open as an upstream credential.
func TestRepositoryUpstreamAuthPurposeIsBound(t *testing.T) {
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", upstreamAuthTestKey)
	foreign, err := secrets.Seal("webhook-signing-secret", "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.Open(upstreamAuthSecretPurpose, foreign); err == nil {
		t.Fatal("expected a foreign-purpose ciphertext to fail to open")
	}
	own, err := secrets.Seal(upstreamAuthSecretPurpose, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	opened, err := secrets.Open(upstreamAuthSecretPurpose, own)
	if err != nil || opened != "s3cret" {
		t.Fatalf("own-purpose ciphertext must open: value=%q err=%v", opened, err)
	}
}
