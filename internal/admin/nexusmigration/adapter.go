// Package nexusmigration owns the administrator HTTP boundary for durable batches.
package nexusmigration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	api "github.com/ccsert/artifact-gateway/internal/admin/openapi"
	"github.com/ccsert/artifact-gateway/internal/authorization"
	m "github.com/ccsert/artifact-gateway/internal/nexusmigration"
	"github.com/ccsert/artifact-gateway/internal/nexussource"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type Authenticator interface {
	AuthenticateContext(context.Context, string) (authorization.Principal, bool)
}
type Auditor interface {
	RecordAudit(context.Context, repository.AuditRecord) error
}
type BatchAdapter struct {
	service *m.Service
	auth    Authenticator
	audit   Auditor
}

func New(service *m.Service, auth Authenticator, audit Auditor) *BatchAdapter {
	return &BatchAdapter{service, auth, audit}
}

type operation struct {
	h        *BatchAdapter
	r        *http.Request
	p        authorization.Principal
	name, id string
	status   int
}

func (h *BatchAdapter) begin(w http.ResponseWriter, r *http.Request, name, id string) (*operation, bool) {
	w.Header().Set("Cache-Control", "no-store")
	op := &operation{h: h, r: r, name: name, id: id, status: 500}
	if h == nil || h.auth == nil {
		problem(w, 503, "migration_service_unavailable")
		return op, false
	}
	p, ok := h.auth.AuthenticateContext(r.Context(), r.Header.Get("Authorization"))
	op.p = p
	if !ok {
		op.status = 401
		problem(w, 401, "access_denied")
		op.finish()
		return op, false
	}
	if !p.Admin || p.MustChangePassword {
		op.status = 403
		problem(w, 403, "access_denied")
		op.finish()
		return op, false
	}
	if !h.service.Available() {
		op.status = 503
		problem(w, 503, "migration_service_unavailable")
		op.finish()
		return op, false
	}
	return op, true
}
func (op *operation) finish() {
	if op.h == nil || op.h.audit == nil {
		return
	}
	outcome := repository.AuditResolved
	if op.status >= 400 {
		outcome = repository.AuditAccessDenied
	}
	_ = op.h.audit.RecordAudit(op.r.Context(), repository.AuditRecord{Actor: op.p.Actor, Operation: op.name, Resource: op.id, Format: "management", Outcome: outcome, Status: op.status, OccurredAt: time.Now().UTC(), CacheDisposition: "bypass"})
}
func problem(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": code, "status": status, "code": code, "detail": "migration batch operation failed"})
}
func (op *operation) failed(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	status, code := 500, "internal_error"
	var f *m.Failure
	var source *nexussource.Failure
	switch {
	case errors.Is(err, m.ErrNotFound) || errors.Is(err, nexussource.ErrNotFound):
		status, code = 404, "not_found"
	case errors.Is(err, m.ErrVersionConflict):
		status, code = 412, "version_conflict"
	case errors.Is(err, m.ErrIdempotencyConflict):
		status, code = 409, "idempotency_conflict"
	case errors.As(err, &f):
		code = f.Code
		status = 422
		switch code {
		case "invalid_request":
			status = 400
		case "invalid_state", "source_version_changed":
			status = 409
		case "migration_execution_disabled":
			status = 503
		}
	case errors.As(err, &source):
		status, code = 422, source.Code
	}
	op.status = status
	problem(w, status, code)
	return true
}
func (op *operation) body(w http.ResponseWriter, out any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, op.r.Body, 64<<10))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(&struct{}{}) != io.EOF {
		op.status = 400
		problem(w, 400, "invalid_request")
		return false
	}
	return true
}
func (op *operation) respond(w http.ResponseWriter, status int, value any) {
	op.status = status
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func batchResponse(b m.Batch) api.NexusMigrationBatch {
	raw, _ := json.Marshal(b)
	var out api.NexusMigrationBatch
	_ = json.Unmarshal(raw, &out)
	return out
}
func entryResponse(e m.Entry) api.NexusMigrationEntry {
	if e.Format == "npm" {
		assets := make([]m.Asset, 0, len(e.Assets))
		for _, asset := range e.Assets {
			if asset.TargetPath != m.NPMPackumentPath {
				assets = append(assets, asset)
			}
		}
		e.Assets = assets
	}
	raw, _ := json.Marshal(e)
	var out api.NexusMigrationEntry
	_ = json.Unmarshal(raw, &out)
	return out
}
func (h *BatchAdapter) GetNexusMigrationCapabilities(w http.ResponseWriter, r *http.Request) {
	op, ok := h.begin(w, r, "migration.capabilities", "")
	if !ok {
		return
	}
	defer op.finish()
	c := h.service.Capabilities()
	op.respond(w, 200, api.NexusMigrationCapabilities{TargetId: c.TargetID, PoolId: c.PoolID, ExecutionEnabled: c.ExecutionEnabled, CapacityEvidenceSource: api.OperatorReported, PolicyVersion: c.PolicyVersion, MaxAttempts: c.MaxAttempts})
}
func (h *BatchAdapter) StartNexusMigrationBatch(w http.ResponseWriter, r *http.Request, params api.StartNexusMigrationBatchParams) {
	op, ok := h.begin(w, r, "migration.batch.start", "")
	if !ok {
		return
	}
	defer op.finish()
	var body api.NexusMigrationStart
	if !op.body(w, &body) {
		return
	}
	raw, _ := json.Marshal(body)
	var input m.StartInput
	if op.failed(w, json.Unmarshal(raw, &input)) {
		return
	}
	b, err := h.service.Start(r.Context(), input, op.p.Actor, params.IdempotencyKey)
	if op.failed(w, err) {
		return
	}
	op.id = b.ID
	w.Header().Set("ETag", b.Version)
	op.respond(w, 202, batchResponse(b))
}
func (h *BatchAdapter) GetNexusMigrationBatch(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	op, ok := h.begin(w, r, "migration.batch.get", id.String())
	if !ok {
		return
	}
	defer op.finish()
	b, err := h.service.Get(r.Context(), id.String())
	if op.failed(w, err) {
		return
	}
	w.Header().Set("ETag", b.Version)
	op.respond(w, 200, batchResponse(b))
}
func (h *BatchAdapter) ListNexusMigrationBatches(w http.ResponseWriter, r *http.Request, params api.ListNexusMigrationBatchesParams) {
	op, ok := h.begin(w, r, "migration.batch.list", "")
	if !ok {
		return
	}
	defer op.finish()
	limit, cursor := 20, ""
	if params.Limit != nil {
		limit = *params.Limit
	}
	if params.Cursor != nil {
		cursor = *params.Cursor
	}
	batches, next, err := h.service.List(r.Context(), limit, cursor)
	if op.failed(w, err) {
		return
	}
	out := api.NexusMigrationBatchPage{Items: []api.NexusMigrationBatch{}, NextCursor: next}
	for _, b := range batches {
		out.Items = append(out.Items, batchResponse(b))
	}
	op.respond(w, 200, out)
}
func (h *BatchAdapter) ListNexusMigrationEntries(w http.ResponseWriter, r *http.Request, id uuid.UUID, params api.ListNexusMigrationEntriesParams) {
	op, ok := h.begin(w, r, "migration.entry.list", id.String())
	if !ok {
		return
	}
	defer op.finish()
	limit, cursor := 20, ""
	if params.Limit != nil {
		limit = *params.Limit
	}
	if params.Cursor != nil {
		cursor = *params.Cursor
	}
	entries, next, err := h.service.Entries(r.Context(), id.String(), limit, cursor)
	if op.failed(w, err) {
		return
	}
	out := api.NexusMigrationEntryPage{Items: []api.NexusMigrationEntry{}, NextCursor: next}
	for _, e := range entries {
		out.Items = append(out.Items, entryResponse(e))
	}
	op.respond(w, 200, out)
}
func (h *BatchAdapter) control(w http.ResponseWriter, r *http.Request, id uuid.UUID, version, key, action string) {
	op, ok := h.begin(w, r, "migration.batch."+action, id.String())
	if !ok {
		return
	}
	defer op.finish()
	var evidence *m.CapacityEvidence
	if action == "resume" || action == "retry" {
		var body api.NexusMigrationResume
		if !op.body(w, &body) {
			return
		}
		raw, _ := json.Marshal(body.CapacityEvidence)
		var e m.CapacityEvidence
		if op.failed(w, json.Unmarshal(raw, &e)) {
			return
		}
		evidence = &e
	} else if r.ContentLength != 0 {
		op.status = 400
		problem(w, 400, "invalid_request")
		return
	}
	b, err := h.service.Control(r.Context(), id.String(), version, op.p.Actor, key, action, evidence)
	if op.failed(w, err) {
		return
	}
	w.Header().Set("ETag", b.Version)
	op.respond(w, 200, batchResponse(b))
}
func (h *BatchAdapter) PauseNexusMigrationBatch(w http.ResponseWriter, r *http.Request, id uuid.UUID, p api.PauseNexusMigrationBatchParams) {
	h.control(w, r, id, p.IfMatch, p.IdempotencyKey, "pause")
}
func (h *BatchAdapter) ResumeNexusMigrationBatch(w http.ResponseWriter, r *http.Request, id uuid.UUID, p api.ResumeNexusMigrationBatchParams) {
	h.control(w, r, id, p.IfMatch, p.IdempotencyKey, "resume")
}
func (h *BatchAdapter) RetryNexusMigrationBatch(w http.ResponseWriter, r *http.Request, id uuid.UUID, p api.RetryNexusMigrationBatchParams) {
	h.control(w, r, id, p.IfMatch, p.IdempotencyKey, "retry")
}
func (h *BatchAdapter) CancelNexusMigrationBatch(w http.ResponseWriter, r *http.Request, id uuid.UUID, p api.CancelNexusMigrationBatchParams) {
	h.control(w, r, id, p.IfMatch, p.IdempotencyKey, "cancel")
}
