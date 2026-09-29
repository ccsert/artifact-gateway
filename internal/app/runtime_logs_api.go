package app

import (
	"context"
	"net/http"
	"strings"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/operationalog"
)

// ListRuntimeLogs only reads this process's bounded memory buffer. A caller
// requesting another instance receives an explicit unavailable response.
func (h generatedRepositoryAPIAdapter) ListRuntimeLogs(w http.ResponseWriter, r *http.Request, params adminopenapi.ListRuntimeLogsParams) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	if h.logBuffer == nil {
		writeHostedProblem(w, http.StatusServiceUnavailable, "log_buffer_unavailable", "runtime log buffer is disabled")
		return
	}
	instanceID := h.diagnostics.Runtime.InstanceID
	if params.InstanceId != nil && *params.InstanceId != "" && *params.InstanceId != instanceID {
		writeHostedProblem(w, http.StatusServiceUnavailable, "remote_log_query_unavailable", "this process can only query its own runtime logs")
		return
	}
	now := time.Now().UTC()
	filter := operationalog.Filter{From: now.Add(-time.Hour), To: now, Limit: 50}
	if params.From != nil {
		filter.From = params.From.UTC()
	}
	if params.To != nil {
		filter.To = params.To.UTC()
	}
	if filter.From.After(filter.To) || filter.To.Sub(filter.From) > 24*time.Hour || filter.To.After(now.Add(5*time.Minute)) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_time_window", "time window must be ordered, within 24 hours, and no more than five minutes in the future")
		return
	}
	if params.Limit != nil {
		filter.Limit = *params.Limit
	}
	if filter.Limit < 1 || filter.Limit > 100 {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 100")
		return
	}
	if params.BeforeSequence != nil {
		if *params.BeforeSequence < 1 {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_cursor", "beforeSequence must be positive")
			return
		}
		filter.Before = uint64(*params.BeforeSequence)
	}
	for _, input := range []struct {
		value  *string
		target *string
	}{
		{params.Level, &filter.Level}, {params.Component, &filter.Component},
		{params.RequestId, &filter.RequestID}, {params.TraceId, &filter.TraceID},
		{params.Keyword, &filter.Keyword},
	} {
		if input.value == nil {
			continue
		}
		if len(*input.value) > 100 || strings.ContainsAny(*input.value, "\r\n\x00") {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_filter", "filter is too long or contains control characters")
			return
		}
		*input.target = *input.value
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	page := h.logBuffer.Query(ctx, filter)
	if ctx.Err() != nil {
		writeHostedProblem(w, http.StatusGatewayTimeout, "log_query_timeout", "runtime log query timed out")
		return
	}
	response := adminopenapi.RuntimeLogPage{Scope: "local", InstanceId: instanceID,
		SessionId: h.diagnostics.Runtime.SessionID, Items: make([]adminopenapi.RuntimeLogEntry, 0, len(page.Items))}
	for _, entry := range page.Items {
		response.Items = append(response.Items, adminopenapi.RuntimeLogEntry{
			Sequence: int64(entry.Sequence), Time: entry.Time, Level: entry.Level, Message: entry.Message,
			InstanceId: entry.InstanceID, SessionId: entry.SessionID, Component: entry.Component, Operation: entry.Operation,
			RequestId: entry.RequestID, TraceId: entry.TraceID,
		})
	}
	if page.Next != 0 {
		next := int64(page.Next)
		response.NextSequence = &next
	}
	w.Header().Set("Cache-Control", "no-store")
	writeNativeMavenJSON(w, http.StatusOK, response)
}
