package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/secrets"
	"github.com/google/uuid"
)

func TestWebhookManagementAPIEncryptsSecretsUsesCASAndReplaysDeadDelivery(t *testing.T) {
	t.Setenv(secrets.KeyEnv, "0123456789abcdef0123456789abcdef")
	ctx := context.Background()
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := func(method, path, token, body, ifMatch string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		authorize(r, token)
		if ifMatch != "" {
			r.Header.Set("If-Match", ifMatch)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	path := "/api/v2/webhook-subscriptions"
	body := `{"name":"security-events","endpointUrl":"https://events.example.test/hooks/artifacts","secret":"0123456789abcdef0123456789abcdef","eventTypes":["artifact.quarantined","artifact.released"],"enabled":true}`
	if denied := request(http.MethodPost, path, "resolver-secret", body, ""); denied.Code != http.StatusUnauthorized {
		t.Fatalf("reader create=%d body=%q", denied.Code, denied.Body.String())
	}
	created := request(http.MethodPost, path, "admin-secret", body, "")
	if created.Code != http.StatusCreated || created.Header().Get("ETag") != "1" || strings.Contains(created.Body.String(), "0123456789abcdef") || !strings.Contains(created.Body.String(), `"secretConfigured":true`) {
		t.Fatalf("created=%d etag=%q body=%q", created.Code, created.Header().Get("ETag"), created.Body.String())
	}
	subscriptions, err := store.ListWebhookSubscriptions(ctx)
	if err != nil || len(subscriptions) != 1 || subscriptions[0].SecretCiphertext == "0123456789abcdef0123456789abcdef" {
		t.Fatalf("persisted subscriptions=%#v err=%v", subscriptions, err)
	}
	plaintext, err := secrets.Open("webhook-subscription:"+subscriptions[0].ID, subscriptions[0].SecretCiphertext)
	if err != nil || plaintext != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("decrypted secret=%q err=%v", plaintext, err)
	}

	itemPath := path + "/" + subscriptions[0].ID
	if got := request(http.MethodGet, itemPath, "admin-secret", "", ""); got.Code != http.StatusOK || got.Header().Get("ETag") != "1" {
		t.Fatalf("get=%d etag=%q body=%q", got.Code, got.Header().Get("ETag"), got.Body.String())
	}
	updateBody := `{"name":"security-events","endpointUrl":"https://events.example.test/hooks/artifacts","eventTypes":["artifact.quarantined"],"enabled":false}`
	if missing := request(http.MethodPut, itemPath, "admin-secret", updateBody, ""); missing.Code != http.StatusBadRequest {
		t.Fatalf("missing If-Match=%d body=%q", missing.Code, missing.Body.String())
	}
	updated := request(http.MethodPut, itemPath, "admin-secret", updateBody, "1")
	if updated.Code != http.StatusOK || updated.Header().Get("ETag") != "2" || !strings.Contains(updated.Body.String(), `"enabled":false`) {
		t.Fatalf("updated=%d etag=%q body=%q", updated.Code, updated.Header().Get("ETag"), updated.Body.String())
	}
	if stale := request(http.MethodPut, itemPath, "admin-secret", updateBody, "1"); stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale=%d body=%q", stale.Code, stale.Body.String())
	}
	if invalid := request(http.MethodPost, path, "admin-secret", strings.Replace(body, "https://events.example.test", "http://metadata.internal", 1), ""); invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid endpoint=%d body=%q", invalid.Code, invalid.Body.String())
	}
	if trailing := request(http.MethodPost, path, "admin-secret", body+` {}`, ""); trailing.Code != http.StatusBadRequest {
		t.Fatalf("trailing payload=%d body=%q", trailing.Code, trailing.Body.String())
	}

	updatedSubscription, err := store.GetWebhookSubscription(ctx, subscriptions[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	updatedSubscription.Enabled = true
	updatedSubscription, err = store.UpdateWebhookSubscription(ctx, updatedSubscription, updatedSubscription.Version)
	if err != nil {
		t.Fatal(err)
	}
	event, err := store.EnqueueWebhookEvent(ctx, repository.WebhookEvent{ID: uuid.NewString(), Type: repository.WebhookEventArtifactQuarantined, OccurredAt: time.Now().UTC(), Data: []byte(`{"repositoryId":"repo-1"}`)})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := store.ClaimWebhookDeliveries(ctx, "test-worker", time.Now().UTC().Add(time.Second), time.Minute, 1)
	if err != nil || len(claims) != 1 || claims[0].Event.ID != event.ID {
		t.Fatalf("claims=%#v err=%v", claims, err)
	}
	failedAt := time.Now().UTC().Add(time.Second)
	if err = store.FailWebhookDelivery(ctx, claims[0].Delivery.ID, claims[0].Delivery.LeaseToken, failedAt, time.Time{}, 503, "webhook returned HTTP 503", true); err != nil {
		t.Fatal(err)
	}
	deliveryPath := "/api/v2/webhook-deliveries/" + claims[0].Delivery.ID
	if got := request(http.MethodGet, deliveryPath, "admin-secret", "", ""); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"state":"dead"`) {
		t.Fatalf("dead delivery=%d body=%q", got.Code, got.Body.String())
	}
	replayed := request(http.MethodPost, deliveryPath+":replay", "admin-secret", "", "")
	if replayed.Code != http.StatusOK || !strings.Contains(replayed.Body.String(), `"state":"pending"`) || !strings.Contains(replayed.Body.String(), `"attempts":0`) {
		t.Fatalf("replayed=%d body=%q", replayed.Code, replayed.Body.String())
	}
	listed := request(http.MethodGet, "/api/v2/webhook-deliveries?state=pending&limit=10", "admin-secret", "", "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), claims[0].Delivery.ID) {
		t.Fatalf("listed=%d body=%q", listed.Code, listed.Body.String())
	}
}

func TestWebhookManagementAPIRequiresSettingsEncryptionKey(t *testing.T) {
	t.Setenv(secrets.KeyEnv, "")
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodPost, "/api/v2/webhook-subscriptions", strings.NewReader(`{"name":"missing-key","endpointUrl":"https://events.example.test/hooks/artifacts","secret":"0123456789abcdef0123456789abcdef","eventTypes":["artifact.quarantined"],"enabled":true}`))
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"encryption_key_unavailable"`) {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}
