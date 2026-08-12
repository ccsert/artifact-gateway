package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/secrets"
	"github.com/google/uuid"
)

func TestWebhookDeliveryWorkerSignsRetriesAndCompletes(t *testing.T) {
	t.Setenv(secrets.KeyEnv, "0123456789abcdef0123456789abcdef")
	ctx := context.Background()
	store := repository.NewMemoryStore()
	secret := "webhook-signing-secret-32-bytes!!"
	var calls int
	var capturedBody []byte
	var capturedHeader http.Header
	var captureMu sync.Mutex
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captureMu.Lock()
		calls++
		capturedBody, capturedHeader = body, r.Header.Clone()
		call := calls
		captureMu.Unlock()
		if call == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	subscriptionID := uuid.NewString()
	ciphertext, err := secrets.Seal("webhook-subscription:"+subscriptionID, secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateWebhookSubscription(ctx, repository.WebhookSubscription{
		ID: subscriptionID, Name: "worker-test", EndpointURL: server.URL, SecretCiphertext: ciphertext,
		EventTypes: []repository.WebhookEventType{repository.WebhookEventArtifactQuarantined}, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	event, err := store.EnqueueWebhookEvent(ctx, repository.WebhookEvent{
		ID: uuid.NewString(), Type: repository.WebhookEventArtifactQuarantined,
		OccurredAt: time.Date(2026, 8, 12, 3, 0, 0, 0, time.UTC), Data: []byte(`{"repositoryId":"repo-1","state":"quarantined"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	now := event.OccurredAt
	worker := WebhookDeliveryWorker{
		Store: store, InstanceID: "worker-a", Now: func() time.Time { return now },
		ClientFactory: func(string) (*http.Client, error) { return server.Client(), nil },
	}
	if count, err := worker.RunOnce(ctx); err != nil || count != 1 {
		t.Fatalf("first run count=%d err=%v", count, err)
	}
	deliveries, err := store.ListWebhookDeliveries(ctx, repository.WebhookDeliveryQuery{Limit: 10})
	if err != nil || len(deliveries) != 1 || deliveries[0].State != repository.WebhookDeliveryRetrying || deliveries[0].Attempts != 1 || !deliveries[0].NextAttemptAt.Equal(now.Add(5*time.Second)) {
		t.Fatalf("retry delivery=%#v err=%v", deliveries, err)
	}
	captureMu.Lock()
	body := append([]byte(nil), capturedBody...)
	header := capturedHeader.Clone()
	captureMu.Unlock()
	timestamp := strconv.FormatInt(now.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)
	wantSignature := "v1=" + hex.EncodeToString(mac.Sum(nil))
	if header.Get("X-Artifact-Gateway-Event-ID") != event.ID || header.Get("X-Artifact-Gateway-Event-Type") != string(event.Type) || header.Get("X-Artifact-Gateway-Timestamp") != timestamp || header.Get("X-Artifact-Gateway-Signature") != wantSignature {
		t.Fatalf("headers=%v want signature=%q", header, wantSignature)
	}
	if !strings.Contains(string(body), `"id":"`+event.ID+`"`) || !strings.Contains(string(body), `"data":{"repositoryId":"repo-1","state":"quarantined"}`) {
		t.Fatalf("body=%s", body)
	}

	now = now.Add(5 * time.Second)
	if count, err := worker.RunOnce(ctx); err != nil || count != 1 {
		t.Fatalf("second run count=%d err=%v", count, err)
	}
	deliveries, err = store.ListWebhookDeliveries(ctx, repository.WebhookDeliveryQuery{Limit: 10})
	if err != nil || deliveries[0].State != repository.WebhookDeliverySucceeded || deliveries[0].Attempts != 2 || deliveries[0].LastStatus != http.StatusNoContent {
		t.Fatalf("completed delivery=%#v err=%v", deliveries, err)
	}
}

func TestWebhookDeliveryWorkerMarksAttemptEightDeadWithoutPersistingResponseBody(t *testing.T) {
	t.Setenv(secrets.KeyEnv, "0123456789abcdef0123456789abcdef")
	ctx := context.Background()
	store := repository.NewMemoryStore()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("sensitive upstream response must not be persisted"))
	}))
	defer server.Close()
	subscriptionID := uuid.NewString()
	ciphertext, err := secrets.Seal("webhook-subscription:"+subscriptionID, "webhook-signing-secret-32-bytes!!")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateWebhookSubscription(ctx, repository.WebhookSubscription{ID: subscriptionID, Name: "dead-test", EndpointURL: server.URL, SecretCiphertext: ciphertext, EventTypes: []repository.WebhookEventType{repository.WebhookEventArtifactReleased}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 12, 4, 0, 0, 0, time.UTC)
	if _, err = store.EnqueueWebhookEvent(ctx, repository.WebhookEvent{ID: uuid.NewString(), Type: repository.WebhookEventArtifactReleased, OccurredAt: now, Data: []byte(`{"state":"released"}`)}); err != nil {
		t.Fatal(err)
	}
	worker := WebhookDeliveryWorker{Store: store, InstanceID: "worker-dead", Now: func() time.Time { return now }, ClientFactory: func(string) (*http.Client, error) { return server.Client(), nil }}
	for attempt := 1; attempt <= 8; attempt++ {
		if count, runErr := worker.RunOnce(ctx); runErr != nil || count != 1 {
			t.Fatalf("attempt %d count=%d err=%v", attempt, count, runErr)
		}
		deliveries, listErr := store.ListWebhookDeliveries(ctx, repository.WebhookDeliveryQuery{Limit: 1})
		if listErr != nil {
			t.Fatal(listErr)
		}
		if attempt == 8 {
			if deliveries[0].State != repository.WebhookDeliveryDead || deliveries[0].Attempts != 8 || strings.Contains(deliveries[0].LastError, "sensitive") {
				t.Fatalf("dead delivery=%#v", deliveries[0])
			}
			break
		}
		if deliveries[0].State != repository.WebhookDeliveryRetrying {
			t.Fatalf("attempt %d delivery=%#v", attempt, deliveries[0])
		}
		now = deliveries[0].NextAttemptAt
	}
}

func TestWebhookDeliveryWorkerDoesNotPreclaimSerialQueue(t *testing.T) {
	t.Setenv(secrets.KeyEnv, "0123456789abcdef0123456789abcdef")
	ctx := context.Background()
	store := repository.NewMemoryStore()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	subscriptionID := uuid.NewString()
	ciphertext, err := secrets.Seal("webhook-subscription:"+subscriptionID, "webhook-signing-secret-32-bytes!!")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateWebhookSubscription(ctx, repository.WebhookSubscription{ID: subscriptionID, Name: "serial-lease", EndpointURL: server.URL, SecretCiphertext: ciphertext, EventTypes: []repository.WebhookEventType{repository.WebhookEventArtifactQuarantined}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 12, 7, 0, 0, 0, time.UTC)
	for index := 0; index < 2; index++ {
		if _, err = store.EnqueueWebhookEvent(ctx, repository.WebhookEvent{ID: uuid.NewString(), Type: repository.WebhookEventArtifactQuarantined, OccurredAt: base, Data: []byte(`{"state":"quarantined"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	nowCalls := 0
	worker := WebhookDeliveryWorker{
		Store: store, InstanceID: "serial-worker",
		Now: func() time.Time {
			value := base.Add(time.Duration(nowCalls) * 20 * time.Second)
			nowCalls++
			return value
		},
		ClientFactory: func(string) (*http.Client, error) { return server.Client(), nil },
	}
	if count, runErr := worker.RunOnce(ctx); runErr != nil || count != 1 {
		t.Fatalf("run count=%d err=%v", count, runErr)
	}
	deliveries, err := store.ListWebhookDeliveries(ctx, repository.WebhookDeliveryQuery{Limit: 10})
	if err != nil || len(deliveries) != 2 {
		t.Fatalf("deliveries=%#v err=%v", deliveries, err)
	}
	states := map[repository.WebhookDeliveryState]int{}
	for _, delivery := range deliveries {
		states[delivery.State]++
		if delivery.State == repository.WebhookDeliveryPending && delivery.Attempts != 0 {
			t.Fatalf("pending delivery was preclaimed: %#v", delivery)
		}
	}
	if states[repository.WebhookDeliverySucceeded] != 1 || states[repository.WebhookDeliveryPending] != 1 {
		t.Fatalf("delivery states=%v", states)
	}
}
