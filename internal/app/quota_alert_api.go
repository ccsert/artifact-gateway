package app

import (
	"encoding/json"
	"errors"
	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/quotaalert"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
	"io"
	"net/http"
	"time"
)

func (h generatedRepositoryAPIAdapter) ListRepositoryQuotaAlertRules(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.emailAuthorize(w, r); !ok {
		return
	}
	values, err := h.quotaAlerts.ListRepositoryQuotaAlertRules(r.Context())
	if err != nil {
		writeHostedProblem(w, 500, "internal_error", "quota alert operation failed")
		return
	}
	out := make([]map[string]any, 0, len(values))
	for _, v := range values {
		out = append(out, quotaRuleResponse(v))
	}
	writeNativeMavenJSON(w, 200, out)
}

func quotaRuleResponse(v repository.RepositoryQuotaAlertRule) map[string]any {
	phase := "normal"
	if v.State.Severity != "normal" {
		phase = "firing"
	} else if !v.State.WarningSince.IsZero() || !v.State.CriticalSince.IsZero() {
		phase = "pending"
	}
	state := map[string]any{"severity": v.State.Severity, "phase": phase, "dataState": v.State.DataState}
	if !v.State.LastSampleAt.IsZero() {
		state["usedBytes"], state["quotaBytes"] = v.State.UsedBytes, v.State.QuotaBytes
	}
	for k, t := range map[string]time.Time{"lastSampleAt": v.State.LastSampleAt, "warningSince": v.State.WarningSince, "criticalSince": v.State.CriticalSince, "recoverySince": v.State.RecoverySince} {
		if !t.IsZero() {
			state[k] = t
		}
	}
	out := map[string]any{"id": v.ID, "repositoryId": v.RepositoryID, "targetId": v.TargetID, "targetVersion": v.TargetVersion, "enabled": v.Enabled, "deleted": v.Deleted, "version": v.Version, "stateVersion": v.StateVersion, "policy": v.Policy, "state": state, "sequence": v.Sequence, "createdAt": v.CreatedAt, "updatedAt": v.UpdatedAt}
	if v.ActiveEpisodeID != "" {
		out["activeEpisodeId"] = v.ActiveEpisodeID
	}
	if v.LastEventID != "" {
		out["lastEventId"] = v.LastEventID
	}
	if !v.EvaluatedAt.IsZero() {
		out["evaluatedAt"] = v.EvaluatedAt
	}
	return out
}

type quotaRuleInput struct {
	RepositoryID uuid.UUID         `json:"repositoryId"`
	TargetID     uuid.UUID         `json:"targetId"`
	Enabled      bool              `json:"enabled"`
	Policy       quotaalert.Policy `json:"policy"`
}

type quotaEventResponse struct {
	quotaalert.Event
	TargetID          string `json:"targetId"`
	TargetVersion     string `json:"targetVersion"`
	NotificationCode  string `json:"notificationCode"`
	TemplateVersion   string `json:"templateVersion"`
	DeliveryID        string `json:"deliveryId,omitempty"`
	DeliveryState     string `json:"deliveryState,omitempty"`
	DeliveryErrorCode string `json:"deliveryErrorCode,omitempty"`
}

func (h generatedRepositoryAPIAdapter) ListRepositoryQuotaAlertEvents(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	if _, ok := h.emailAuthorize(w, r); !ok {
		return
	}
	values, err := h.quotaAlerts.ListRepositoryQuotaAlertEvents(r.Context(), id.String())
	if quotaProblem(w, err) {
		return
	}
	out := make([]quotaEventResponse, 0, len(values))
	for _, v := range values {
		out = append(out, quotaEventResponse{Event: v.Snapshot, TargetID: v.TargetID, TargetVersion: v.TargetVersion, NotificationCode: v.NotificationCode, TemplateVersion: quotaalert.TemplateVersion, DeliveryID: v.DeliveryID, DeliveryState: v.DeliveryState, DeliveryErrorCode: v.DeliveryErrorCode})
	}
	writeNativeMavenJSON(w, 200, out)
}

func quotaInput(w http.ResponseWriter, r *http.Request, out *quotaRuleInput) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(&struct{}{}) != io.EOF || out.RepositoryID == uuid.Nil || out.TargetID == uuid.Nil || out.Policy.Validate() != nil {
		writeHostedProblem(w, 400, "invalid_request", "quota alert rule must be explicitly configured")
		return false
	}
	return true
}
func quotaProblem(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	status, code := 500, "internal_error"
	switch {
	case errors.Is(err, repository.ErrNotFound):
		status, code = 404, "not_found"
	case errors.Is(err, repository.ErrVersionConflict):
		status, code = 412, "version_conflict"
	case errors.Is(err, repository.ErrQuotaAlertConflict):
		status, code = 409, "rule_conflict"
	case errors.Is(err, repository.ErrQuotaAlertLimit):
		status, code = 409, "rule_limit"
	case errors.Is(err, repository.ErrQuotaAlertDeleted):
		status, code = 409, "rule_deleted"
	case errors.Is(err, repository.ErrQuotaAlertScopeImmutable):
		status, code = 409, "scope_immutable"
	case errors.Is(err, repository.ErrEmailTargetDisabled):
		status, code = 409, "target_disabled"
	case errors.Is(err, repository.ErrDisabled):
		status, code = 409, "repository_inactive"
	}
	writeHostedProblem(w, status, code, "quota alert operation failed")
	return true
}

func (h generatedRepositoryAPIAdapter) GetRepositoryQuotaAlertRule(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	if _, ok := h.emailAuthorize(w, r); !ok {
		return
	}
	v, err := h.quotaAlerts.GetRepositoryQuotaAlertRule(r.Context(), id.String())
	if quotaProblem(w, err) {
		return
	}
	w.Header().Set("ETag", v.Version)
	writeNativeMavenJSON(w, 200, quotaRuleResponse(v))
}
func (h generatedRepositoryAPIAdapter) UpdateRepositoryQuotaAlertRule(w http.ResponseWriter, r *http.Request, id uuid.UUID, params adminopenapi.UpdateRepositoryQuotaAlertRuleParams) {
	p, ok := h.emailAuthorize(w, r)
	if !ok {
		return
	}
	var in quotaRuleInput
	if !quotaInput(w, r, &in) {
		return
	}
	if in.Enabled && !h.requireEmailReady(w) {
		return
	}
	target, err := h.emailTargets.GetEmailTarget(r.Context(), in.TargetID.String())
	if quotaProblem(w, err) {
		return
	}
	v, err := h.quotaAlerts.UpdateRepositoryQuotaAlertRule(r.Context(), repository.RepositoryQuotaAlertRule{ID: id.String(), RepositoryID: in.RepositoryID.String(), TargetID: target.ID, TargetVersion: target.Version, Policy: in.Policy, Enabled: in.Enabled}, params.IfMatch)
	if quotaProblem(w, err) {
		return
	}
	if v.Version != params.IfMatch {
		h.emailAudit(r, p, v.ID, "quota.rule.update", 200)
	}
	w.Header().Set("ETag", v.Version)
	writeNativeMavenJSON(w, 200, quotaRuleResponse(v))
}
func (h generatedRepositoryAPIAdapter) DeleteRepositoryQuotaAlertRule(w http.ResponseWriter, r *http.Request, id uuid.UUID, params adminopenapi.DeleteRepositoryQuotaAlertRuleParams) {
	p, ok := h.emailAuthorize(w, r)
	if !ok {
		return
	}
	v, err := h.quotaAlerts.DeleteRepositoryQuotaAlertRule(r.Context(), id.String(), params.IfMatch)
	if quotaProblem(w, err) {
		return
	}
	if v.Version != params.IfMatch {
		h.emailAudit(r, p, v.ID, "quota.rule.delete", 204)
	}
	w.Header().Set("ETag", v.Version)
	w.WriteHeader(204)
}
func (h generatedRepositoryAPIAdapter) CreateRepositoryQuotaAlertRule(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.emailAuthorize(w, r)
	if !ok {
		return
	}
	var in quotaRuleInput
	if !quotaInput(w, r, &in) {
		return
	}
	if in.Enabled && !h.requireEmailReady(w) {
		return
	}
	target, err := h.emailTargets.GetEmailTarget(r.Context(), in.TargetID.String())
	if quotaProblem(w, err) {
		return
	}
	v, err := h.quotaAlerts.CreateRepositoryQuotaAlertRule(r.Context(), repository.RepositoryQuotaAlertRule{ID: uuid.NewString(), RepositoryID: in.RepositoryID.String(), TargetID: target.ID, TargetVersion: target.Version, Enabled: in.Enabled, Policy: in.Policy})
	if quotaProblem(w, err) {
		return
	}
	h.emailAudit(r, principal, v.ID, "quota.rule.create", 201)
	w.Header().Set("ETag", v.Version)
	writeNativeMavenJSON(w, 201, quotaRuleResponse(v))
}
