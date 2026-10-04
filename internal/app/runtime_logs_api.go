package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/operationalog"
)

// ListRuntimeLogs only reads this process's bounded memory buffer. A caller
// requesting another instance receives an explicit unavailable response.
func (h generatedRepositoryAPIAdapter) ListRuntimeLogs(w http.ResponseWriter, r *http.Request, params adminopenapi.ListRuntimeLogsParams) {
	w.Header().Set("Cache-Control", "no-store")
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	if h.logBuffer == nil {
		writeHostedProblem(w, http.StatusServiceUnavailable, "log_buffer_unavailable", "runtime log buffer is disabled")
		return
	}
	instanceID := h.diagnostics.Runtime.InstanceID
	sessionID := h.diagnostics.Runtime.SessionID
	if instanceID == "" || sessionID == "" {
		writeHostedProblem(w, http.StatusServiceUnavailable, "log_source_identity_unavailable", "runtime log instance and session identity are unavailable")
		return
	}
	if params.InstanceId != nil && *params.InstanceId != "" && *params.InstanceId != instanceID {
		writeHostedProblem(w, http.StatusServiceUnavailable, "remote_log_query_unavailable", "this process can only query its own runtime logs")
		return
	}
	now := time.Now().UTC()
	filter := operationalog.Filter{From: now.Add(-time.Hour), To: now, MaxWindow: 24 * time.Hour, Limit: 50, InstanceID: instanceID, SessionID: sessionID}
	if params.From != nil {
		filter.From = params.From.UTC()
	}
	if params.To != nil {
		filter.To = params.To.UTC()
	}
	filter.RollingFrom, filter.RollingTo = params.From == nil, params.To == nil
	if params.WindowSeconds != nil {
		seconds := *params.WindowSeconds
		if seconds < 1 || seconds > 86400 || params.From != nil || params.To != nil {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_time_window", "rolling duration must be 1 to 86400 seconds and cannot be combined with explicit bounds")
			return
		}
		filter.RollingWindow = time.Duration(seconds) * time.Second
		filter.From = now.Add(-filter.RollingWindow)
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
		if params.AfterCursor != nil {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_cursor", "beforeSequence and afterCursor are mutually exclusive")
			return
		}
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
		if !validRuntimeLogFilter(*input.value) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_filter", "filter is too long or contains control characters")
			return
		}
		*input.target = *input.value
	}
	// Bind explicit time bounds; omitted bounds remain rolling on subsequent polls.
	// Limits and backward positions do not change which entries match.
	from, to := "", ""
	if params.From != nil {
		from = params.From.UTC().Format(time.RFC3339Nano)
	}
	if params.To != nil {
		to = params.To.UTC().Format(time.RFC3339Nano)
	}
	window := ""
	if params.WindowSeconds != nil {
		window = strconv.Itoa(*params.WindowSeconds)
	}
	spec, _ := json.Marshal([]string{from, to, window, strings.ToUpper(filter.Level), filter.Component, filter.RequestID, filter.TraceID, strings.ToLower(filter.Keyword)})
	if params.AfterCursor != nil {
		position, err := h.logBuffer.ParseCursor(*params.AfterCursor, instanceID, sessionID, string(spec))
		if err != nil {
			status, code, message := http.StatusBadRequest, "invalid_cursor", "cursor is malformed, modified, or belongs to different filters"
			if errors.Is(err, operationalog.ErrCursorScopeChanged) {
				status, code, message = http.StatusConflict, "log_cursor_scope_changed", "runtime log instance or session changed; reset the query"
			}
			writeHostedProblem(w, status, code, message)
			return
		}
		filter.After, filter.Forward = position, true
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	page := h.logBuffer.Query(ctx, filter)
	if ctx.Err() != nil {
		writeHostedProblem(w, http.StatusGatewayTimeout, "log_query_timeout", "runtime log query timed out")
		return
	}
	if page.InvalidWindow {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_time_window", "time window must remain ordered and within 24 hours at the query snapshot")
		return
	}
	response := adminopenapi.RuntimeLogPage{Scope: "local", InstanceId: instanceID,
		SessionId: sessionID, Items: make([]adminopenapi.RuntimeLogEntry, 0, len(page.Items))}
	for _, entry := range page.Items {
		item := adminopenapi.RuntimeLogEntry{
			Sequence: int64(entry.Sequence), Time: entry.Time, Level: entry.Level, Message: entry.Message,
			InstanceId: entry.InstanceID, SessionId: entry.SessionID, Component: entry.Component, Operation: entry.Operation,
			RequestId: entry.RequestID, TraceId: entry.TraceID,
			Status: entry.Status, DurationMs: entry.DurationMS, Attempt: entry.Attempt,
		}
		if entry.Method != "" {
			value := adminopenapi.RuntimeLogEntryMethod(entry.Method)
			item.Method = &value
		}
		if entry.RequestClass != "" {
			value := adminopenapi.RuntimeLogEntryRequestClass(entry.RequestClass)
			item.RequestClass = &value
		}
		if entry.Route != "" {
			value := entry.Route
			item.Route = &value
		}
		if entry.ErrorCode != "" {
			code := adminopenapi.RuntimeLogEntryErrorCode(entry.ErrorCode)
			phase := adminopenapi.RuntimeLogEntryPhase(entry.Phase)
			item.ErrorCode, item.Phase = &code, &phase
		}
		if entry.JobID != "" {
			value := entry.JobID
			item.JobId = &value
		}
		response.Items = append(response.Items, item)
	}
	if page.Next != 0 {
		next := int64(page.Next)
		response.NextSequence = &next
	}
	cursor := h.logBuffer.Cursor(instanceID, sessionID, string(spec), page.Position)
	response.AfterCursor = &cursor
	order := adminopenapi.RuntimeLogPageOrder("descending")
	if filter.Forward {
		order = "ascending"
	}
	response.Order, response.HasMore = &order, &page.HasMore
	response.Retention = &adminopenapi.RuntimeLogRetention{EarliestSequence: int64(page.Earliest), LatestSequence: int64(page.Latest), Gap: page.Gap}
	mode := adminopenapi.RuntimeLogSourceAccessMode("limited")
	if h.diagnostics.AccessLog.Mode == "full" {
		mode = "full"
	}
	threshold := h.diagnostics.AccessLog.SlowThreshold
	if threshold <= 0 {
		threshold = time.Second
	}
	components := page.Components
	if components == nil {
		components = []string{}
	}
	response.Source = &adminopenapi.RuntimeLogSource{MinimumLevel: "INFO", AccessMode: mode, SlowThresholdMs: threshold.Milliseconds(), CapacityLines: page.Capacity, MaxLineBytes: 16384, Components: components, ComponentsTruncated: page.ComponentsTruncated}
	writeNativeMavenJSON(w, http.StatusOK, response)
}

func validRuntimeLogFilter(value string) bool {
	if len(value) > 100 {
		return false
	}
	for _, c := range value {
		if c < 32 || c == 127 {
			return false
		}
	}
	return true
}
