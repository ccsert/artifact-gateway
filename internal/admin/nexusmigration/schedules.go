package nexusmigration

import (
	"net/http"

	api "github.com/ccsert/artifact-gateway/internal/admin/openapi"
	m "github.com/ccsert/artifact-gateway/internal/nexusmigration"
	"github.com/google/uuid"
)

func (h *BatchAdapter) beginSchedule(w http.ResponseWriter, r *http.Request, action, id string) (*operation, bool) {
	op, ok := h.begin(w, r, "migration.schedule."+action, id)
	if ok && !h.schedules.Available() {
		op.status = http.StatusServiceUnavailable
		problem(w, op.status, "migration_service_unavailable")
		op.finish()
		return op, false
	}
	return op, ok
}

func (h *BatchAdapter) CreateNexusMigrationSchedule(w http.ResponseWriter, r *http.Request, params api.CreateNexusMigrationScheduleParams) {
	op, ok := h.beginSchedule(w, r, "create", "")
	if !ok {
		return
	}
	defer op.finish()
	var input m.ScheduleCreate
	if !op.body(w, &input) {
		return
	}
	p, err := h.schedules.Create(m.WithOperatorIdentity(r.Context(), m.OperatorIdentity{Actor: op.p.Actor, AuthenticationKind: string(op.p.AuthenticationKind), UserID: op.p.UserID}), input, op.p.Actor, params.IdempotencyKey)
	if op.failed(w, err) {
		return
	}
	op.id = p.ID
	w.Header().Set("ETag", p.Version)
	op.respond(w, http.StatusCreated, p)
}

func (h *BatchAdapter) GetNexusMigrationSchedule(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	op, ok := h.beginSchedule(w, r, "get", id.String())
	if !ok {
		return
	}
	defer op.finish()
	p, err := h.schedules.Get(r.Context(), id.String())
	if op.failed(w, err) {
		return
	}
	w.Header().Set("ETag", p.Version)
	op.respond(w, http.StatusOK, p)
}

func (h *BatchAdapter) ListNexusMigrationSchedules(w http.ResponseWriter, r *http.Request, params api.ListNexusMigrationSchedulesParams) {
	op, ok := h.beginSchedule(w, r, "list", "")
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
	items, next, err := h.schedules.List(r.Context(), limit, cursor)
	if op.failed(w, err) {
		return
	}
	op.respond(w, http.StatusOK, struct {
		Items      []m.Schedule `json:"items"`
		NextCursor string       `json:"nextCursor"`
	}{items, next})
}

func (h *BatchAdapter) controlSchedule(w http.ResponseWriter, r *http.Request, id uuid.UUID, version, key, action string) {
	op, ok := h.beginSchedule(w, r, action, id.String())
	if !ok {
		return
	}
	defer op.finish()
	var evidence *m.CapacityEvidence
	if action == "resume" || action == "capacity" {
		var body struct {
			CapacityEvidence *m.CapacityEvidence `json:"capacityEvidence"`
		}
		if !op.body(w, &body) {
			return
		}
		evidence = body.CapacityEvidence
	} else if r.ContentLength != 0 {
		op.status = http.StatusBadRequest
		problem(w, op.status, "invalid_request")
		return
	}
	p, err := h.schedules.Control(r.Context(), id.String(), version, op.p.Actor, key, action, evidence)
	if op.failed(w, err) {
		return
	}
	w.Header().Set("ETag", p.Version)
	op.respond(w, http.StatusOK, p)
}

func (h *BatchAdapter) PauseNexusMigrationSchedule(w http.ResponseWriter, r *http.Request, id uuid.UUID, p api.PauseNexusMigrationScheduleParams) {
	h.controlSchedule(w, r, id, p.IfMatch, p.IdempotencyKey, "pause")
}
func (h *BatchAdapter) ResumeNexusMigrationSchedule(w http.ResponseWriter, r *http.Request, id uuid.UUID, p api.ResumeNexusMigrationScheduleParams) {
	h.controlSchedule(w, r, id, p.IfMatch, p.IdempotencyKey, "resume")
}
func (h *BatchAdapter) StopNexusMigrationSchedule(w http.ResponseWriter, r *http.Request, id uuid.UUID, p api.StopNexusMigrationScheduleParams) {
	h.controlSchedule(w, r, id, p.IfMatch, p.IdempotencyKey, "stop")
}
func (h *BatchAdapter) RefreshNexusMigrationScheduleCapacity(w http.ResponseWriter, r *http.Request, id uuid.UUID, p api.RefreshNexusMigrationScheduleCapacityParams) {
	h.controlSchedule(w, r, id, p.IfMatch, p.IdempotencyKey, "capacity")
}
