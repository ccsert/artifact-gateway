package app

import (
	"encoding/json"
	"errors"
	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/emailnotification"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/secrets"
	"github.com/google/uuid"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

func (h generatedRepositoryAPIAdapter) PreviewEmailNotification(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.emailAuthorize(w, r); !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	var input adminopenapi.EmailPreviewInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeHostedProblem(w, 400, "invalid_request", "email preview payload is invalid")
		return
	}
	p, err := emailnotification.Render(string(input.Scenario), string(input.Locale), "synthetic-preview-001", time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), h.diagnostics.Email.ConsoleOrigin)
	if err != nil {
		writeHostedProblem(w, 400, "invalid_request", "email preview scenario or locale is invalid")
		return
	}
	writeNativeMavenJSON(w, 200, p)
}

func emailTargetResponse(v repository.EmailTarget) map[string]any {
	return map[string]any{"id": v.ID, "name": v.Name, "locale": v.Locale, "enabled": v.Enabled, "recipientConfigured": v.RecipientCiphertext != "", "version": v.Version, "createdAt": v.CreatedAt, "updatedAt": v.UpdatedAt}
}
func emailInput(w http.ResponseWriter, r *http.Request, out any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(&struct{}{}) != io.EOF {
		writeHostedProblem(w, 400, "invalid_request", "email request is invalid")
		return false
	}
	return true
}
func (h generatedRepositoryAPIAdapter) CreateEmailTarget(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.emailAuthorize(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	var input adminopenapi.EmailTargetInput
	if !emailInput(w, r, &input) {
		return
	}
	if input.Recipient == nil {
		writeHostedProblem(w, 400, "invalid_request", "email recipient is required")
		return
	}
	v := repository.EmailTarget{ID: uuid.NewString()}
	if !emailTargetInput(w, input, &v) {
		return
	}
	out, err := h.emailTargets.CreateEmailTarget(r.Context(), v)
	if emailProblem(w, err) {
		return
	}
	h.emailAudit(r, principal, out.ID, "email.target.create", 201)
	w.Header().Set("ETag", out.Version)
	writeNativeMavenJSON(w, 201, emailTargetResponse(out))
}
func emailTargetInput(w http.ResponseWriter, input adminopenapi.EmailTargetInput, v *repository.EmailTarget) bool {
	name := strings.TrimSpace(input.Name)
	if name == "" || utf8.RuneCountInString(name) > 128 || strings.ContainsAny(name, "\r\n\x00") || !input.Locale.Valid() {
		writeHostedProblem(w, 400, "invalid_request", "email target is invalid")
		return false
	}
	v.Name = name
	v.Locale = string(input.Locale)
	v.Enabled = input.Enabled != nil && *input.Enabled
	if input.Recipient != nil {
		if emailnotification.ValidAddress(*input.Recipient) != nil {
			writeHostedProblem(w, 400, "invalid_request", "email recipient is invalid")
			return false
		}
		cipher, err := secrets.Seal("email-target:"+v.ID, *input.Recipient)
		if err != nil {
			writeHostedProblem(w, 503, "encryption_key_unavailable", "email settings encryption is unavailable")
			return false
		}
		v.RecipientCiphertext = cipher
	}
	return true
}
func (h generatedRepositoryAPIAdapter) emailAudit(r *http.Request, p Principal, id, operation string, status int) {
	if h.audit != nil {
		_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{Actor: p.Actor, Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(), Format: "management", Resource: id, Operation: operation, Status: status, CacheDisposition: "bypass"})
	}
}
func emailProblem(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	status, code, message := 500, "internal_error", "email operation failed"
	switch {
	case errors.Is(err, repository.ErrNotFound):
		status, code, message = 404, "not_found", "email resource not found"
	case errors.Is(err, repository.ErrVersionConflict):
		status, code, message = 412, "version_conflict", "If-Match does not match current version"
	case errors.Is(err, repository.ErrEmailTargetDisabled):
		status, code, message = 409, "target_disabled", "email target is disabled"
	case errors.Is(err, repository.ErrEmailRateLimited):
		status, code, message = 429, "rate_limited", "email test rate limit exceeded"
	case errors.Is(err, repository.ErrEmailQueueFull):
		status, code, message = 503, "queue_full", "email delivery queue is full"
	case errors.Is(err, repository.ErrEmailIdempotencyConflict):
		status, code, message = 409, "idempotency_conflict", "idempotency key already used for another email test"
	case errors.Is(err, repository.ErrEmailInvalidState):
		status, code, message = 409, "invalid_state", "email delivery cannot be replayed"
	}
	writeHostedProblem(w, status, code, message)
	return true
}
func (h generatedRepositoryAPIAdapter) ListEmailTargets(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.emailAuthorize(w, r); !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	values, err := h.emailTargets.ListEmailTargets(r.Context())
	if emailProblem(w, err) {
		return
	}
	out := make([]map[string]any, 0, len(values))
	for _, v := range values {
		out = append(out, emailTargetResponse(v))
	}
	writeNativeMavenJSON(w, 200, out)
}
func (h generatedRepositoryAPIAdapter) GetEmailTarget(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	if _, ok := h.emailAuthorize(w, r); !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	v, err := h.emailTargets.GetEmailTarget(r.Context(), id.String())
	if emailProblem(w, err) {
		return
	}
	w.Header().Set("ETag", v.Version)
	writeNativeMavenJSON(w, 200, emailTargetResponse(v))
}
func (h generatedRepositoryAPIAdapter) UpdateEmailTarget(w http.ResponseWriter, r *http.Request, id uuid.UUID, params adminopenapi.UpdateEmailTargetParams) {
	p, ok := h.emailAuthorize(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	v, err := h.emailTargets.GetEmailTarget(r.Context(), id.String())
	if emailProblem(w, err) {
		return
	}
	var input adminopenapi.EmailTargetInput
	if !emailInput(w, r, &input) || !emailTargetInput(w, input, &v) {
		return
	}
	out, err := h.emailTargets.UpdateEmailTarget(r.Context(), v, string(params.IfMatch))
	if emailProblem(w, err) {
		return
	}
	h.emailAudit(r, p, out.ID, "email.target.update", 200)
	w.Header().Set("ETag", out.Version)
	writeNativeMavenJSON(w, 200, emailTargetResponse(out))
}
func (h generatedRepositoryAPIAdapter) emailAuthorize(w http.ResponseWriter, r *http.Request) (Principal, bool) {
	w.Header().Set("Cache-Control", "no-store")
	return h.authorize(w, r)
}
func (h generatedRepositoryAPIAdapter) emailReady() string {
	if !h.diagnostics.Email.Enabled || h.diagnostics.Email.Validate() != nil {
		return "email_disabled"
	}
	if _, err := secrets.Seal("email-capability", "synthetic-readiness-probe"); err != nil {
		return "encryption_key_unavailable"
	}
	return "ready"
}
func (h generatedRepositoryAPIAdapter) requireEmailReady(w http.ResponseWriter) bool {
	reason := h.emailReady()
	if reason != "ready" {
		writeHostedProblem(w, 503, reason, "email channel is unavailable")
		return false
	}
	return true
}
func (h generatedRepositoryAPIAdapter) GetEmailNotificationCapability(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.emailAuthorize(w, r); !ok {
		return
	}
	reason := h.emailReady()
	writeNativeMavenJSON(w, 200, map[string]any{"enabled": reason == "ready", "reason": reason})
}
func emailDeliveryResponse(v repository.EmailDelivery) map[string]any {
	out := map[string]any{"id": v.ID, "eventId": v.EventID, "targetId": v.TargetID, "targetVersion": v.TargetVersion, "scenario": v.Scenario, "locale": v.Locale, "templateVersion": v.TemplateVersion, "state": v.State, "attempts": v.Attempts, "possibleDuplicate": v.PossibleDuplicate, "version": v.Version, "createdAt": v.CreatedAt, "updatedAt": v.UpdatedAt}
	if v.ErrorCode != "" {
		out["errorCode"] = v.ErrorCode
	}
	if v.State != "accepted" && v.State != "dead" {
		out["nextAttemptAt"] = v.NextAttemptAt
	}
	if !v.AcceptedAt.IsZero() {
		out["acceptedAt"] = v.AcceptedAt
	}
	return out
}
func (h generatedRepositoryAPIAdapter) TestEmailNotification(w http.ResponseWriter, r *http.Request, params adminopenapi.TestEmailNotificationParams) {
	p, ok := h.emailAuthorize(w, r)
	if !ok || !h.requireEmailReady(w) {
		return
	}
	var input adminopenapi.EmailTestInput
	if !emailInput(w, r, &input) {
		return
	}
	if !input.Scenario.Valid() || params.IdempotencyKey == uuid.Nil || strings.TrimSpace(string(params.IfMatch)) == "" {
		writeHostedProblem(w, 400, "invalid_request", "email test is invalid")
		return
	}
	v, err := h.emailTargets.EnqueueEmailTest(r.Context(), repository.EmailTestRequest{ID: uuid.NewString(), EventID: uuid.NewString(), RequestKey: params.IdempotencyKey.String(), TargetID: input.TargetId.String(), TargetVersion: string(params.IfMatch), Scenario: string(input.Scenario), From: h.diagnostics.Email.From, ConsoleOrigin: h.diagnostics.Email.ConsoleOrigin, TemplateVersion: emailnotification.TemplateVersion})
	if emailProblem(w, err) {
		return
	}
	h.emailAudit(r, p, v.ID, "email.test.enqueue", 202)
	w.Header().Set("ETag", v.Version)
	writeNativeMavenJSON(w, 202, emailDeliveryResponse(v))
}
func (h generatedRepositoryAPIAdapter) ListEmailDeliveries(w http.ResponseWriter, r *http.Request, params adminopenapi.ListEmailDeliveriesParams) {
	if _, ok := h.emailAuthorize(w, r); !ok {
		return
	}
	limit := 50
	if params.Limit != nil {
		limit = *params.Limit
	}
	if limit < 1 || limit > 100 {
		writeHostedProblem(w, 400, "invalid_request", "email delivery limit is invalid")
		return
	}
	values, err := h.emailTargets.ListEmailDeliveries(r.Context(), limit)
	if emailProblem(w, err) {
		return
	}
	out := make([]map[string]any, 0, len(values))
	for _, v := range values {
		out = append(out, emailDeliveryResponse(v))
	}
	writeNativeMavenJSON(w, 200, out)
}
func (h generatedRepositoryAPIAdapter) GetEmailDelivery(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	if _, ok := h.emailAuthorize(w, r); !ok {
		return
	}
	v, err := h.emailTargets.GetEmailDelivery(r.Context(), id.String())
	if emailProblem(w, err) {
		return
	}
	w.Header().Set("ETag", v.Version)
	writeNativeMavenJSON(w, 200, emailDeliveryResponse(v))
}
func (h generatedRepositoryAPIAdapter) ReplayEmailDelivery(w http.ResponseWriter, r *http.Request, id uuid.UUID, params adminopenapi.ReplayEmailDeliveryParams) {
	p, ok := h.emailAuthorize(w, r)
	if !ok || !h.requireEmailReady(w) {
		return
	}
	v, err := h.emailTargets.ReplayEmailDelivery(r.Context(), id.String(), string(params.IfMatch))
	if emailProblem(w, err) {
		return
	}
	h.emailAudit(r, p, v.ID, "email.delivery.replay", 200)
	w.Header().Set("ETag", v.Version)
	writeNativeMavenJSON(w, 200, emailDeliveryResponse(v))
}
