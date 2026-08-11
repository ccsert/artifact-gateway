package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func (h generatedRepositoryAPIAdapter) ListAudits(w http.ResponseWriter, r *http.Request, params adminopenapi.ListAuditsParams) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	limit := 100
	if params.Limit != nil {
		limit = *params.Limit
	}
	if limit < 1 || limit > 500 {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "limit must be between 1 and 500")
		return
	}
	query := repository.AuditQuery{Limit: limit}
	if params.Group != nil {
		query.GroupName = *params.Group
	}
	if params.Repository != nil {
		query.Repository = *params.Repository
	}
	if params.Outcome != nil {
		query.Outcome = *params.Outcome
	}
	if params.Format != nil {
		query.Format = *params.Format
	}
	if params.Operation != nil {
		query.Operation = *params.Operation
	}
	if params.Actor != nil {
		query.Actor = *params.Actor
	}
	audits, err := h.audit.ListAudits(r.Context(), query)
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list audits failed")
		return
	}
	response := make([]auditResponse, 0, len(audits))
	for _, audit := range audits {
		response = append(response, auditResponseFromRecord(audit))
	}
	writeNativeMavenJSON(w, http.StatusOK, response)
}

func (h generatedRepositoryAPIAdapter) ListAuditPage(w http.ResponseWriter, r *http.Request, params adminopenapi.ListAuditPageParams) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	pageSize := 100
	if params.PageSize != nil {
		pageSize = int(*params.PageSize)
	}
	if pageSize < 1 || pageSize > 200 {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "pageSize must be between 1 and 200")
		return
	}
	from := time.Time{}
	if params.From != nil {
		from = params.From.UTC()
	}
	to := time.Time{}
	if params.To != nil {
		to = params.To.UTC()
	}
	if !from.IsZero() && !to.IsZero() && from.After(to) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "from must be before or equal to to")
		return
	}
	query := repository.AuditQuery{
		GroupName: auditString(params.Group), Repository: auditString(params.Repository), Outcome: auditString(params.Outcome),
		Format: auditString(params.Format), Operation: auditString(params.Operation), Actor: auditString(params.Actor), Limit: pageSize,
		From: from, To: to,
	}
	if token := auditToken(params.PageToken); token != "" {
		var cursor auditPageCursor
		if decodeSignedCursor(h.authenticator.AdminToken, token, &cursor) != nil || cursor.Endpoint != "audits" || time.Now().UTC().Unix() >= cursor.ExpiresAt ||
			cursor.GroupName != query.GroupName || cursor.Repository != query.Repository || cursor.Outcome != query.Outcome || cursor.Format != query.Format ||
			cursor.Operation != query.Operation || cursor.Actor != query.Actor || !cursor.From.Equal(query.From) || !cursor.To.Equal(query.To) || cursor.OccurredAt.IsZero() || cursor.ID <= 0 {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_page_token", "page token is invalid or expired")
			return
		}
		query.Before = repository.AuditCursor{OccurredAt: cursor.OccurredAt, ID: cursor.ID}
	}
	pageStore, ok := h.audit.(repository.AuditPageStore)
	if !ok {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "audit cursor paging is unavailable")
		return
	}
	page, err := pageStore.ListAuditPage(r.Context(), query)
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list audit page failed")
		return
	}
	response := auditPageResponse{Items: make([]auditResponse, 0, len(page.Items))}
	for _, audit := range page.Items {
		response.Items = append(response.Items, auditResponseFromRecord(audit))
	}
	if page.Next != nil {
		response.NextPageToken = h.encodeAuditCursor(query, *page.Next)
	}
	writeNativeMavenJSON(w, http.StatusOK, response)
}

func (h generatedRepositoryAPIAdapter) GetAnonymousAccessPolicy(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	policy, err := h.anonymousAccess.GetAnonymousAccessPolicy(r.Context())
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get anonymous access policy failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusOK, anonymousAccessPolicyResponse(policy))
}

func (h generatedRepositoryAPIAdapter) ReplaceAnonymousAccessPolicy(w http.ResponseWriter, r *http.Request, params adminopenapi.ReplaceAnonymousAccessPolicyParams) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	var request adminopenapi.AnonymousAccessPolicy
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.Version == "" {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "version and enabled are required")
		return
	}
	policy, err := h.anonymousAccess.ReplaceAnonymousAccessPolicy(r.Context(), repository.AnonymousAccessPolicy{Enabled: request.Enabled}, string(params.IfMatch))
	if errors.Is(err, repository.ErrVersionConflict) {
		writeHostedProblem(w, http.StatusPreconditionFailed, "version_conflict", "If-Match does not match current version")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "replace anonymous access policy failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusOK, anonymousAccessPolicyResponse(policy))
}

func anonymousAccessPolicyResponse(policy repository.AnonymousAccessPolicy) adminopenapi.AnonymousAccessPolicy {
	return adminopenapi.AnonymousAccessPolicy{Enabled: policy.Enabled, Version: policy.Version}
}

func (h generatedRepositoryAPIAdapter) GetAuditRetentionPolicy(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	p, err := h.auditRetention.GetAuditRetentionPolicy(r.Context())
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get audit retention policy failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusOK, adminopenapi.AuditRetentionPolicy{Version: p.Version, Enabled: p.Enabled, KeepDays: p.KeepDays})
}

func (h generatedRepositoryAPIAdapter) ReplaceAuditRetentionPolicy(w http.ResponseWriter, r *http.Request, params adminopenapi.ReplaceAuditRetentionPolicyParams) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	var request adminopenapi.AuditRetentionPolicy
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.Version == "" || request.KeepDays < 0 || request.KeepDays > 36500 || (request.Enabled && request.KeepDays < 1) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "version, enabled, and keepDays must be valid")
		return
	}
	p, err := h.auditRetention.ReplaceAuditRetentionPolicy(r.Context(), repository.AuditRetentionPolicy{Version: request.Version, Enabled: request.Enabled, KeepDays: request.KeepDays}, string(params.IfMatch))
	if errors.Is(err, repository.ErrVersionConflict) {
		writeHostedProblem(w, http.StatusPreconditionFailed, "version_conflict", "If-Match does not match current version")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "replace audit retention policy failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusOK, adminopenapi.AuditRetentionPolicy{Version: p.Version, Enabled: p.Enabled, KeepDays: p.KeepDays})
}

func (h generatedRepositoryAPIAdapter) ExecuteAuditRetention(w http.ResponseWriter, r *http.Request, params adminopenapi.ExecuteAuditRetentionParams) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	p, err := h.auditRetention.GetAuditRetentionPolicy(r.Context())
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get audit retention policy failed")
		return
	}
	if !p.Enabled {
		writeHostedProblem(w, http.StatusConflict, "retention_disabled", "audit retention is disabled")
		return
	}
	job, _, err := (AuditRetentionWorker{Store: h.auditRetention}).Enqueue(r.Context(), string(params.IdempotencyKey), 1000)
	if errors.Is(err, repository.ErrIdempotencyConflict) {
		writeHostedProblem(w, http.StatusConflict, "idempotency_conflict", "Idempotency-Key conflicts with an existing audit cleanup job")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "enqueue audit cleanup job failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusAccepted, auditCleanupJobResponse(job))
}

func (h generatedRepositoryAPIAdapter) ListAuditRetentionJobs(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	jobs, err := h.auditRetention.ListAuditCleanupJobs(r.Context(), 100)
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list audit cleanup jobs failed")
		return
	}
	response := make([]adminopenapi.AuditCleanupJob, 0, len(jobs))
	for _, job := range jobs {
		response = append(response, auditCleanupJobResponse(job))
	}
	writeNativeMavenJSON(w, http.StatusOK, response)
}

func auditCleanupJobResponse(job repository.AuditCleanupJob) adminopenapi.AuditCleanupJob {
	response := adminopenapi.AuditCleanupJob{Id: uuid.MustParse(job.ID), PolicyVersion: job.PolicyVersion, CutoffAt: job.CutoffAt, BatchSize: job.BatchSize, Deleted: job.Deleted, State: adminopenapi.AuditCleanupJobState(job.State), CreatedAt: job.CreatedAt}
	if !job.StartedAt.IsZero() {
		response.StartedAt = &job.StartedAt
	}
	if !job.CompletedAt.IsZero() {
		response.CompletedAt = &job.CompletedAt
	}
	if job.LastError != "" {
		response.LastError = &job.LastError
	}
	return response
}

// auditResponse is the V2 audit representation. V1 keeps returning the
// historical repository.AuditRecord JSON field names for compatibility.
type auditResponse struct {
	GroupName           string                  `json:"groupName,omitempty"`
	Repository          string                  `json:"repository,omitempty"`
	MemberName          string                  `json:"memberName,omitempty"`
	Outcome             repository.AuditOutcome `json:"outcome"`
	Actor               string                  `json:"actor,omitempty"`
	OccurredAt          time.Time               `json:"occurredAt"`
	Format              string                  `json:"format,omitempty"`
	Resource            string                  `json:"resource,omitempty"`
	Representation      string                  `json:"representation,omitempty"`
	MemberType          string                  `json:"memberType,omitempty"`
	UpstreamHost        string                  `json:"upstreamHost,omitempty"`
	Operation           string                  `json:"operation,omitempty"`
	Status              int                     `json:"status,omitempty"`
	CacheDisposition    string                  `json:"cacheDisposition,omitempty"`
	Bytes               int64                   `json:"bytes,omitempty"`
	AuthorizationSource string                  `json:"authorizationSource,omitempty"`
	AuthorizationReason string                  `json:"authorizationReason,omitempty"`
	RequestID           string                  `json:"requestId,omitempty"`
	TraceID             string                  `json:"traceId,omitempty"`
}

type auditPageResponse struct {
	Items         []auditResponse `json:"items"`
	NextPageToken string          `json:"nextPageToken,omitempty"`
}

func auditString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func auditToken(value *adminopenapi.PageToken) string {
	if value == nil {
		return ""
	}
	return string(*value)
}

func (h generatedRepositoryAPIAdapter) encodeAuditCursor(query repository.AuditQuery, cursor repository.AuditCursor) string {
	return encodeSignedCursor(h.authenticator.AdminToken, auditPageCursor{
		Endpoint: "audits", GroupName: query.GroupName, Repository: query.Repository, Outcome: query.Outcome,
		Format: query.Format, Operation: query.Operation, Actor: query.Actor, From: query.From, To: query.To,
		OccurredAt: cursor.OccurredAt, ID: cursor.ID, ExpiresAt: time.Now().UTC().Add(15 * time.Minute).Unix(),
	})
}

func auditResponseFromRecord(audit repository.AuditRecord) auditResponse {
	return auditResponse{
		GroupName: audit.GroupName, Repository: audit.Repository, MemberName: audit.MemberName, Outcome: audit.Outcome, Actor: audit.Actor, OccurredAt: audit.OccurredAt,
		Format: audit.Format, Resource: audit.Resource, Representation: audit.Representation, MemberType: audit.MemberType, UpstreamHost: audit.UpstreamHost,
		Operation: audit.Operation, CacheDisposition: audit.CacheDisposition, AuthorizationSource: audit.AuthorizationSource, AuthorizationReason: audit.AuthorizationReason,
		RequestID: audit.RequestID, TraceID: audit.TraceID, Status: audit.Status, Bytes: audit.Bytes,
	}
}
