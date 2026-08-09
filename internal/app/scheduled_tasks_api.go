package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func (h generatedRepositoryAPIAdapter) ListScheduledTasks(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	tasks, err := h.scheduledTasks.ListScheduledTasks(r.Context())
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list scheduled tasks failed")
		return
	}
	items := make([]adminopenapi.ScheduledTask, 0, len(tasks))
	for _, task := range tasks {
		items = append(items, scheduledTaskResponse(task))
	}
	writeNativeMavenJSON(w, http.StatusOK, items)
}

func (h generatedRepositoryAPIAdapter) CreateScheduledTask(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authorize(w, r)
	if !ok {
		return
	}
	var request adminopenapi.CreateScheduledTask
	if err := decodeScheduledTaskRequest(w, r, &request); err != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	task, err := h.scheduledTaskFromRequest(r, uuid.NewString(), request, time.Time{})
	if err != nil {
		writeScheduledTaskValidationProblem(w, err)
		return
	}
	created, err := h.scheduledTasks.CreateScheduledTask(r.Context(), task)
	if errors.Is(err, repository.ErrNameExists) {
		writeHostedProblem(w, http.StatusConflict, "name_exists", "scheduled task name already exists")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "create scheduled task failed")
		return
	}
	h.auditScheduledTask(r, principal, created, "scheduled_task.create", http.StatusCreated)
	writeNativeMavenJSON(w, http.StatusCreated, scheduledTaskResponse(created))
}

func (h generatedRepositoryAPIAdapter) GetScheduledTask(w http.ResponseWriter, r *http.Request, taskID adminopenapi.ScheduledTaskId) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	task, err := h.scheduledTasks.GetScheduledTask(r.Context(), taskID.String())
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "scheduled task not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get scheduled task failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusOK, scheduledTaskResponse(task))
}

func (h generatedRepositoryAPIAdapter) UpdateScheduledTask(w http.ResponseWriter, r *http.Request, taskID adminopenapi.ScheduledTaskId, params adminopenapi.UpdateScheduledTaskParams) {
	principal, ok := h.authorize(w, r)
	if !ok {
		return
	}
	current, err := h.scheduledTasks.GetScheduledTask(r.Context(), taskID.String())
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "scheduled task not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get scheduled task failed")
		return
	}
	var request adminopenapi.UpdateScheduledTask
	if err = decodeScheduledTaskRequest(w, r, &request); err != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	task, err := h.scheduledTaskFromRequest(r, current.ID, request, current.NextRunAt)
	if err != nil {
		writeScheduledTaskValidationProblem(w, err)
		return
	}
	updated, err := h.scheduledTasks.UpdateScheduledTask(r.Context(), task, string(params.IfMatch))
	if errors.Is(err, repository.ErrVersionConflict) {
		writeHostedProblem(w, http.StatusPreconditionFailed, "version_conflict", "If-Match does not match the scheduled task version")
		return
	}
	if errors.Is(err, repository.ErrNameExists) {
		writeHostedProblem(w, http.StatusConflict, "name_exists", "scheduled task name already exists")
		return
	}
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "scheduled task not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "update scheduled task failed")
		return
	}
	h.auditScheduledTask(r, principal, updated, "scheduled_task.update", http.StatusOK)
	writeNativeMavenJSON(w, http.StatusOK, scheduledTaskResponse(updated))
}

func (h generatedRepositoryAPIAdapter) DeleteScheduledTask(w http.ResponseWriter, r *http.Request, taskID adminopenapi.ScheduledTaskId) {
	principal, ok := h.authorize(w, r)
	if !ok {
		return
	}
	task, err := h.scheduledTasks.GetScheduledTask(r.Context(), taskID.String())
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "scheduled task not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get scheduled task failed")
		return
	}
	if err = h.scheduledTasks.DeleteScheduledTask(r.Context(), task.ID); err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "delete scheduled task failed")
		return
	}
	h.auditScheduledTask(r, principal, task, "scheduled_task.delete", http.StatusNoContent)
	w.WriteHeader(http.StatusNoContent)
}

func (h generatedRepositoryAPIAdapter) RunScheduledTask(w http.ResponseWriter, r *http.Request, taskID adminopenapi.ScheduledTaskId) {
	principal, ok := h.authorize(w, r)
	if !ok {
		return
	}
	task, err := h.scheduledTasks.GetScheduledTask(r.Context(), taskID.String())
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "scheduled task not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get scheduled task failed")
		return
	}
	run, err := (ScheduledTaskScheduler{Store: h.sessions.store}).RunNow(r.Context(), task.ID)
	if err != nil {
		h.auditScheduledTask(r, principal, task, "scheduled_task.run_failed", http.StatusConflict)
		writeHostedProblem(w, http.StatusConflict, "dispatch_failed", truncateScheduledTaskError(err.Error()))
		return
	}
	h.auditScheduledTask(r, principal, task, "scheduled_task.run", http.StatusAccepted)
	writeNativeMavenJSON(w, http.StatusAccepted, scheduledTaskRunResponse(run))
}

func (h generatedRepositoryAPIAdapter) ListScheduledTaskRuns(w http.ResponseWriter, r *http.Request, taskID adminopenapi.ScheduledTaskId, params adminopenapi.ListScheduledTaskRunsParams) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	if _, err := h.scheduledTasks.GetScheduledTask(r.Context(), taskID.String()); errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "scheduled task not found")
		return
	} else if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get scheduled task failed")
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
	runs, err := h.scheduledTasks.ListScheduledTaskRuns(r.Context(), taskID.String(), limit)
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list scheduled task runs failed")
		return
	}
	items := make([]adminopenapi.ScheduledTaskRun, 0, len(runs))
	for _, run := range runs {
		items = append(items, scheduledTaskRunResponse(run))
	}
	writeNativeMavenJSON(w, http.StatusOK, items)
}

func decodeScheduledTaskRequest(w http.ResponseWriter, r *http.Request, request *adminopenapi.CreateScheduledTask) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(request); err != nil {
		return errors.New("scheduled task body is invalid")
	}
	return nil
}

func (h generatedRepositoryAPIAdapter) scheduledTaskFromRequest(r *http.Request, id string, request adminopenapi.CreateScheduledTask, fallbackNextRun time.Time) (repository.ScheduledTask, error) {
	name := strings.TrimSpace(request.Name)
	description := ""
	if request.Description != nil {
		description = strings.TrimSpace(*request.Description)
	}
	if name == "" || len(name) > 100 || len(description) > 500 || request.IntervalMinutes < 15 || request.IntervalMinutes > 525600 {
		return repository.ScheduledTask{}, errors.New("name, description, and intervalMinutes must be valid")
	}
	task := repository.ScheduledTask{ID: id, Name: name, Description: description, Kind: repository.ScheduledTaskKind(request.Kind), IntervalSeconds: request.IntervalMinutes * 60, Enabled: request.Enabled}
	switch task.Kind {
	case repository.ScheduledTaskRepositoryRetention:
		if request.RepositoryId == nil {
			return repository.ScheduledTask{}, errors.New("repositoryId is required for repository retention")
		}
		task.RepositoryID = request.RepositoryId.String()
		repo, err := h.store.GetHostedRepository(r.Context(), task.RepositoryID)
		if errors.Is(err, repository.ErrNotFound) {
			return repository.ScheduledTask{}, repository.ErrNotFound
		}
		if err != nil {
			return repository.ScheduledTask{}, err
		}
		if repo.State != repository.RepositoryActive || repo.Type != repository.RepositoryTypeHosted || !supportsRepositoryRetention(repo.Format) {
			return repository.ScheduledTask{}, repository.ErrDisabled
		}
	case repository.ScheduledTaskAuditRetention:
		if request.RepositoryId != nil {
			return repository.ScheduledTask{}, errors.New("repositoryId must be omitted for audit retention")
		}
	default:
		return repository.ScheduledTask{}, errors.New("scheduled task kind is unsupported")
	}
	if request.NextRunAt != nil {
		task.NextRunAt = request.NextRunAt.UTC()
	} else if !fallbackNextRun.IsZero() {
		task.NextRunAt = fallbackNextRun.UTC()
	} else {
		task.NextRunAt = time.Now().UTC().Add(time.Duration(task.IntervalSeconds) * time.Second)
	}
	return task, nil
}

func writeScheduledTaskValidationProblem(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		writeHostedProblem(w, http.StatusNotFound, "not_found", "scheduled task repository not found")
	case errors.Is(err, repository.ErrDisabled):
		writeHostedProblem(w, http.StatusConflict, "unsupported_operation", "repository retention is unavailable for this repository")
	default:
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
	}
}

func scheduledTaskResponse(task repository.ScheduledTask) adminopenapi.ScheduledTask {
	item := adminopenapi.ScheduledTask{Id: uuid.MustParse(task.ID), Name: task.Name, Description: task.Description, Kind: adminopenapi.ScheduledTaskKind(task.Kind), IntervalMinutes: task.IntervalSeconds / 60, Enabled: task.Enabled, NextRunAt: task.NextRunAt, Version: task.Version, CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt}
	if task.RepositoryID != "" {
		value := uuid.MustParse(task.RepositoryID)
		item.RepositoryId = &value
	}
	if !task.LastRunAt.IsZero() {
		item.LastRunAt = &task.LastRunAt
		item.LastRunState = (*adminopenapi.ScheduledTaskLastRunState)(&task.LastRunState)
	}
	if task.LastRunID != "" {
		value := uuid.MustParse(task.LastRunID)
		item.LastRunId = &value
	}
	if task.LastError != "" {
		item.LastError = &task.LastError
	}
	return item
}

func scheduledTaskRunResponse(run repository.ScheduledTaskRun) adminopenapi.ScheduledTaskRun {
	item := adminopenapi.ScheduledTaskRun{Id: uuid.MustParse(run.ID), TaskId: uuid.MustParse(run.TaskID), Trigger: adminopenapi.ScheduledTaskRunTrigger(run.Trigger), State: adminopenapi.ScheduledTaskRunState(run.State), ScheduledAt: run.ScheduledAt, CreatedAt: run.CreatedAt}
	if !run.CompletedAt.IsZero() {
		item.CompletedAt = &run.CompletedAt
	}
	if run.TargetKind != "" {
		value := adminopenapi.ScheduledTaskRunTargetKind(run.TargetKind)
		item.TargetKind = &value
	}
	if run.TargetID != "" {
		item.TargetId = &run.TargetID
	}
	if run.LastError != "" {
		item.LastError = &run.LastError
	}
	return item
}

func (h generatedRepositoryAPIAdapter) auditScheduledTask(r *http.Request, principal Principal, task repository.ScheduledTask, operation string, status int) {
	if h.audit == nil {
		return
	}
	repositoryName := "global"
	if task.RepositoryID != "" {
		if repo, err := h.store.GetHostedRepository(r.Context(), task.RepositoryID); err == nil {
			repositoryName = repo.Name
		}
	}
	_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{Repository: repositoryName, GroupName: repositoryName, Actor: principal.Actor, Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(), Format: "management", Resource: "scheduled-tasks/" + task.ID, Operation: operation, Status: status, CacheDisposition: "bypass"})
}
