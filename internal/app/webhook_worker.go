package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/egress"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/secrets"
)

const (
	webhookDeliveryMaxAttempts = 8
	webhookDeliveryLease       = 30 * time.Second
	webhookDeliveryClaimLimit  = 1
)

type webhookEnvelope struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	OccurredAt time.Time       `json:"occurredAt"`
	Data       json.RawMessage `json:"data"`
}

type WebhookClientFactory func(string) (*http.Client, error)

// WebhookDeliveryWorker claims durable deliveries and sends signed HTTPS
// requests. Delivery failures are persisted for retry and do not stop the
// worker from processing the rest of the batch.
type WebhookDeliveryWorker struct {
	Store         repository.WebhookStore
	InstanceID    string
	Now           func() time.Time
	ClientFactory WebhookClientFactory
}

func (w WebhookDeliveryWorker) now() time.Time {
	if w.Now != nil {
		return w.Now().UTC()
	}
	return time.Now().UTC()
}

func ProductionWebhookClient(endpoint string) (*http.Client, error) {
	base := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("webhook redirects are disabled")
		},
	}
	return egress.Apply(base, &repository.EgressProxy{Mode: repository.EgressProxyModeDirect}, endpoint, egress.DefaultHooks())
}

func (w WebhookDeliveryWorker) RunOnce(ctx context.Context) (int, error) {
	if w.Store == nil {
		return 0, errors.New("webhook store is required")
	}
	now := w.now()
	owner := w.InstanceID
	if owner == "" {
		owner = "webhook-worker"
	}
	claims, err := w.Store.ClaimWebhookDeliveries(ctx, owner, now, webhookDeliveryLease, webhookDeliveryClaimLimit)
	if err != nil {
		return 0, err
	}
	factory := w.ClientFactory
	if factory == nil {
		factory = ProductionWebhookClient
	}
	for _, claim := range claims {
		status, message, permanent := deliverWebhook(ctx, factory, claim, now)
		finishedAt := w.now()
		if status >= 200 && status < 300 && message == "" {
			if err = w.Store.CompleteWebhookDelivery(ctx, claim.Delivery.ID, claim.Delivery.LeaseToken, status, finishedAt); err != nil {
				return len(claims), err
			}
			continue
		}
		dead := permanent || claim.Delivery.Attempts >= webhookDeliveryMaxAttempts
		retryAt := time.Time{}
		if !dead {
			retryAt = finishedAt.Add(webhookRetryDelay(claim.Delivery.Attempts))
		}
		if err = w.Store.FailWebhookDelivery(ctx, claim.Delivery.ID, claim.Delivery.LeaseToken, finishedAt, retryAt, status, message, dead); err != nil {
			return len(claims), err
		}
	}
	return len(claims), nil
}

func deliverWebhook(ctx context.Context, factory WebhookClientFactory, claim repository.WebhookDeliveryClaim, now time.Time) (int, string, bool) {
	secret, err := secrets.Open("webhook-subscription:"+claim.Subscription.ID, claim.Subscription.SecretCiphertext)
	if err != nil || secret == "" {
		return 0, "webhook signing secret unavailable", false
	}
	body, err := json.Marshal(webhookEnvelope{ID: claim.Event.ID, Type: string(claim.Event.Type), OccurredAt: claim.Event.OccurredAt, Data: json.RawMessage(claim.Event.Data)})
	if err != nil {
		return 0, "webhook event payload is invalid", true
	}
	timestamp := strconv.FormatInt(now.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, claim.Subscription.EndpointURL, bytes.NewReader(body))
	if err != nil {
		return 0, "webhook endpoint is invalid", true
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "Artifact-Gateway-Webhook/1.0")
	request.Header.Set("X-Artifact-Gateway-Event-ID", claim.Event.ID)
	request.Header.Set("X-Artifact-Gateway-Event-Type", string(claim.Event.Type))
	request.Header.Set("X-Artifact-Gateway-Timestamp", timestamp)
	request.Header.Set("X-Artifact-Gateway-Signature", "v1="+hex.EncodeToString(mac.Sum(nil)))
	client, err := factory(claim.Subscription.EndpointURL)
	if err != nil {
		return 0, "webhook endpoint validation failed", false
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, "webhook request failed", false
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	_ = response.Body.Close()
	if response.StatusCode < 100 || response.StatusCode > 599 {
		return 0, "webhook returned invalid HTTP status", false
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, "webhook returned HTTP " + strconv.Itoa(response.StatusCode), false
	}
	return response.StatusCode, "", false
}

func webhookRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := 5 * time.Second
	for index := 1; index < attempt && delay < time.Hour; index++ {
		delay *= 2
	}
	if delay > time.Hour {
		return time.Hour
	}
	return delay
}

func (w WebhookDeliveryWorker) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			processed, err := w.RunOnce(ctx)
			if err != nil && ctx.Err() == nil {
				slog.Warn("webhook delivery worker iteration failed", "error", err)
			}
			if err == nil && processed > 0 {
				continue
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
