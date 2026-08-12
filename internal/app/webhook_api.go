package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/secrets"
	"github.com/google/uuid"
)

func webhookSubscriptionResponse(value repository.WebhookSubscription) adminopenapi.WebhookSubscription {
	eventTypes := make([]adminopenapi.WebhookEventType, 0, len(value.EventTypes))
	for _, eventType := range value.EventTypes {
		eventTypes = append(eventTypes, adminopenapi.WebhookEventType(eventType))
	}
	return adminopenapi.WebhookSubscription{
		Id: uuid.MustParse(value.ID), Name: value.Name, EndpointUrl: value.EndpointURL,
		EventTypes: eventTypes, Enabled: value.Enabled, Version: value.Version,
		SecretConfigured: value.SecretCiphertext != "", CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func webhookDeliveryResponse(value repository.WebhookDelivery) adminopenapi.WebhookDelivery {
	response := adminopenapi.WebhookDelivery{
		Id: uuid.MustParse(value.ID), EventId: uuid.MustParse(value.EventID), EventType: adminopenapi.WebhookEventType(value.EventType),
		SubscriptionId: uuid.MustParse(value.SubscriptionID), State: adminopenapi.WebhookDeliveryState(value.State), Attempts: value.Attempts,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
	if !value.NextAttemptAt.IsZero() {
		response.NextAttemptAt = &value.NextAttemptAt
	}
	if value.LastStatus != 0 {
		response.LastStatus = &value.LastStatus
	}
	if value.LastError != "" {
		response.LastError = &value.LastError
	}
	if !value.DeliveredAt.IsZero() {
		response.DeliveredAt = &value.DeliveredAt
	}
	return response
}

func validateWebhookSubscriptionInput(name, endpoint, secret string, eventTypes []adminopenapi.WebhookEventType, secretRequired bool) ([]repository.WebhookEventType, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 128 || strings.ContainsAny(name, "\r\n\x00") {
		return nil, errors.New("webhook subscription name is invalid")
	}
	parsed, err := url.ParseRequestURI(strings.TrimSpace(endpoint))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, errors.New("webhook endpoint must be an HTTPS URL without credentials or fragment")
	}
	if len(endpoint) > 2048 {
		return nil, errors.New("webhook endpoint is too long")
	}
	if secretRequired && secret == "" {
		return nil, errors.New("webhook signing secret is required")
	}
	if secret != "" && (len(secret) < 32 || len(secret) > 256 || strings.ContainsAny(secret, "\r\n\x00")) {
		return nil, errors.New("webhook signing secret must be between 32 and 256 bytes")
	}
	if len(eventTypes) == 0 {
		return nil, errors.New("at least one webhook event type is required")
	}
	values := make([]repository.WebhookEventType, 0, len(eventTypes))
	seen := make(map[repository.WebhookEventType]bool, len(eventTypes))
	for _, raw := range eventTypes {
		value := repository.WebhookEventType(raw)
		if value != repository.WebhookEventArtifactQuarantined && value != repository.WebhookEventArtifactReleased || seen[value] {
			return nil, errors.New("webhook event types are invalid")
		}
		seen[value] = true
		values = append(values, value)
	}
	return values, nil
}

func (h generatedRepositoryAPIAdapter) ListWebhookSubscriptions(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	values, err := h.webhooks.ListWebhookSubscriptions(r.Context())
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list webhook subscriptions failed")
		return
	}
	response := make(adminopenapi.WebhookSubscriptionList, 0, len(values))
	for _, value := range values {
		response = append(response, webhookSubscriptionResponse(value))
	}
	writeNativeMavenJSON(w, http.StatusOK, response)
}

func (h generatedRepositoryAPIAdapter) CreateWebhookSubscription(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authorize(w, r)
	if !ok {
		return
	}
	var input adminopenapi.CreateWebhookSubscription
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "webhook subscription payload is invalid")
		return
	}
	secret := ""
	if input.Secret != nil {
		secret = *input.Secret
	}
	eventTypes, err := validateWebhookSubscriptionInput(input.Name, input.EndpointUrl, secret, input.EventTypes, true)
	if err != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	id := uuid.NewString()
	ciphertext, err := secrets.Seal("webhook-subscription:"+id, secret)
	if errors.Is(err, secrets.ErrKeyNotConfigured) || errors.Is(err, secrets.ErrInvalidKey) {
		writeHostedProblem(w, http.StatusServiceUnavailable, "encryption_key_unavailable", secrets.KeyEnv+" is required to persist webhook signing secrets")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "encrypt webhook signing secret failed")
		return
	}
	created, err := h.webhooks.CreateWebhookSubscription(r.Context(), repository.WebhookSubscription{
		ID: id, Name: strings.TrimSpace(input.Name), EndpointURL: strings.TrimSpace(input.EndpointUrl),
		SecretCiphertext: ciphertext, EventTypes: eventTypes, Enabled: input.Enabled,
	})
	if errors.Is(err, repository.ErrWebhookSubscriptionNameExists) {
		writeHostedProblem(w, http.StatusConflict, "name_exists", "webhook subscription name already exists")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "create webhook subscription failed")
		return
	}
	if h.audit != nil {
		_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{Actor: principal.Actor, Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(), Format: "management", Resource: created.ID, Operation: "webhook.subscription.create", Status: http.StatusCreated, CacheDisposition: "bypass"})
	}
	w.Header().Set("ETag", created.Version)
	writeNativeMavenJSON(w, http.StatusCreated, webhookSubscriptionResponse(created))
}

func (h generatedRepositoryAPIAdapter) GetWebhookSubscription(w http.ResponseWriter, r *http.Request, subscriptionID uuid.UUID) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	value, err := h.webhooks.GetWebhookSubscription(r.Context(), subscriptionID.String())
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "webhook subscription not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get webhook subscription failed")
		return
	}
	w.Header().Set("ETag", value.Version)
	writeNativeMavenJSON(w, http.StatusOK, webhookSubscriptionResponse(value))
}

func (h generatedRepositoryAPIAdapter) UpdateWebhookSubscription(w http.ResponseWriter, r *http.Request, subscriptionID uuid.UUID, params adminopenapi.UpdateWebhookSubscriptionParams) {
	principal, ok := h.authorize(w, r)
	if !ok {
		return
	}
	current, err := h.webhooks.GetWebhookSubscription(r.Context(), subscriptionID.String())
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "webhook subscription not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get webhook subscription failed")
		return
	}
	var input adminopenapi.UpdateWebhookSubscription
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&input); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "webhook subscription payload is invalid")
		return
	}
	secret := ""
	if input.Secret != nil {
		secret = *input.Secret
	}
	eventTypes, err := validateWebhookSubscriptionInput(input.Name, input.EndpointUrl, secret, input.EventTypes, false)
	if err != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	ciphertext := current.SecretCiphertext
	if secret != "" {
		ciphertext, err = secrets.Seal("webhook-subscription:"+current.ID, secret)
		if errors.Is(err, secrets.ErrKeyNotConfigured) || errors.Is(err, secrets.ErrInvalidKey) {
			writeHostedProblem(w, http.StatusServiceUnavailable, "encryption_key_unavailable", secrets.KeyEnv+" is required to persist webhook signing secrets")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "encrypt webhook signing secret failed")
			return
		}
	}
	updated, err := h.webhooks.UpdateWebhookSubscription(r.Context(), repository.WebhookSubscription{
		ID: current.ID, Name: strings.TrimSpace(input.Name), EndpointURL: strings.TrimSpace(input.EndpointUrl),
		SecretCiphertext: ciphertext, EventTypes: eventTypes, Enabled: input.Enabled,
	}, string(params.IfMatch))
	if errors.Is(err, repository.ErrVersionConflict) {
		writeHostedProblem(w, http.StatusPreconditionFailed, "version_conflict", "If-Match does not match current webhook subscription version")
		return
	}
	if errors.Is(err, repository.ErrWebhookSubscriptionNameExists) {
		writeHostedProblem(w, http.StatusConflict, "name_exists", "webhook subscription name already exists")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "update webhook subscription failed")
		return
	}
	if h.audit != nil {
		_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{Actor: principal.Actor, Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(), Format: "management", Resource: updated.ID, Operation: "webhook.subscription.update", Status: http.StatusOK, CacheDisposition: "bypass"})
	}
	w.Header().Set("ETag", updated.Version)
	writeNativeMavenJSON(w, http.StatusOK, webhookSubscriptionResponse(updated))
}

func (h generatedRepositoryAPIAdapter) ListWebhookDeliveries(w http.ResponseWriter, r *http.Request, params adminopenapi.ListWebhookDeliveriesParams) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	query := repository.WebhookDeliveryQuery{}
	if params.SubscriptionId != nil {
		query.SubscriptionID = params.SubscriptionId.String()
	}
	if params.State != nil {
		query.State = repository.WebhookDeliveryState(*params.State)
	}
	if params.Limit != nil {
		query.Limit = *params.Limit
	}
	values, err := h.webhooks.ListWebhookDeliveries(r.Context(), query)
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list webhook deliveries failed")
		return
	}
	response := make(adminopenapi.WebhookDeliveryList, 0, len(values))
	for _, value := range values {
		response = append(response, webhookDeliveryResponse(value))
	}
	writeNativeMavenJSON(w, http.StatusOK, response)
}

func (h generatedRepositoryAPIAdapter) GetWebhookDelivery(w http.ResponseWriter, r *http.Request, deliveryID uuid.UUID) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	value, err := h.webhooks.GetWebhookDelivery(r.Context(), deliveryID.String())
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "webhook delivery not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get webhook delivery failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusOK, webhookDeliveryResponse(value))
}

func (h generatedRepositoryAPIAdapter) ReplayWebhookDelivery(w http.ResponseWriter, r *http.Request, deliveryID uuid.UUID) {
	principal, ok := h.authorize(w, r)
	if !ok {
		return
	}
	value, err := h.webhooks.ReplayWebhookDelivery(r.Context(), deliveryID.String(), time.Now().UTC())
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "webhook delivery not found")
		return
	}
	if errors.Is(err, repository.ErrInvalidWebhookDeliveryState) {
		writeHostedProblem(w, http.StatusConflict, "invalid_state", "only dead webhook deliveries can be replayed")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "replay webhook delivery failed")
		return
	}
	if h.audit != nil {
		_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{Actor: principal.Actor, Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(), Format: "management", Resource: value.ID, Operation: "webhook.delivery.replay", Status: http.StatusOK, CacheDisposition: "bypass"})
	}
	writeNativeMavenJSON(w, http.StatusOK, webhookDeliveryResponse(value))
}
