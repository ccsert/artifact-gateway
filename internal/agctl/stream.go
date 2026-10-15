package agctl

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
)

const runtimeLogsPath = managementPrefix + "/runtime/logs"

// streamedError marks a failure that a stream already reported in its
// terminal event. RunCLI maps it to an exit code without printing a second
// document.
type streamedError struct{ err error }

func (e *streamedError) Error() string { return e.err.Error() }

func (e *streamedError) Unwrap() error { return e.err }

// followOptions bound a --follow stream. Commands never prompt and never
// loop forever: a stream needs --duration or --max-events.
type followOptions struct {
	follow    bool
	duration  time.Duration
	maxEvents int
	interval  time.Duration
}

func (f *followOptions) register(fs *flag.FlagSet) {
	fs.BoolVar(&f.follow, "follow", false, "keep reading new entries; needs --duration or --max-events")
	fs.DurationVar(&f.duration, "duration", 0, "stop following after this long")
	fs.IntVar(&f.maxEvents, "max-events", 0, "stop following after this many entries")
	fs.DurationVar(&f.interval, "interval", 2*time.Second, "wait between polls while following (at least 1s)")
}

func (f followOptions) validate(env *commandContext) error {
	if !f.follow {
		for _, name := range []string{"duration", "max-events", "interval"} {
			if env.flagSet[name] {
				return &UsageError{Message: "--" + name + " only applies with --follow", Hint: "add --follow to stream new entries"}
			}
		}
		return nil
	}
	if f.duration <= 0 && f.maxEvents <= 0 {
		return &UsageError{Message: "--follow needs --duration or --max-events", Hint: "bound the stream, for example --follow --duration 5m"}
	}
	if f.duration < 0 || f.maxEvents < 0 {
		return &UsageError{Message: "--duration and --max-events must be positive", Hint: "for example --duration 5m or --max-events 100"}
	}
	if f.interval < time.Second {
		return &UsageError{Message: "--interval must be at least 1s", Hint: "polling faster only loads the server"}
	}
	return nil
}

// eventStream writes a follow stream. JSON mode is NDJSON: one versioned
// event per line, then exactly one terminal "end" event that names why the
// stream stopped. Human mode prints entries on stdout and the end on stderr.
type eventStream struct {
	env    *commandContext
	secret string
	events int
	limit  int
}

type streamEvent struct {
	SchemaVersion int          `json:"schemaVersion"`
	Type          string       `json:"type"`
	Data          any          `json:"data"`
	Error         *errorReport `json:"error,omitempty"`
}

// streamEnd is the data of the terminal event: why the stream stopped, how
// many entries it delivered, and where a new stream can continue.
type streamEnd struct {
	Reason string         `json:"reason"`
	Events int            `json:"events"`
	Resume map[string]any `json:"resume"`
}

func (s *eventStream) write(event streamEvent) error {
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(s.env.stdout, "%s\n", encoded)
	return err
}

// emit writes one entry; it reports whether the --max-events budget is spent.
func (s *eventStream) emit(kind string, data any, human string) (bool, error) {
	var err error
	if s.env.format == outputJSON {
		err = s.write(streamEvent{SchemaVersion: outputSchemaVersion, Type: kind, Data: data})
	} else {
		_, err = fmt.Fprintln(s.env.stdout, human)
	}
	if kind == "entry" {
		s.events++
	}
	return s.limit > 0 && s.events >= s.limit, err
}

// note writes a non-entry event, such as a gap, that does not count against
// --max-events.
func (s *eventStream) note(kind string, data any, human string) error {
	if s.env.format == outputJSON {
		return s.write(streamEvent{SchemaVersion: outputSchemaVersion, Type: kind, Data: data})
	}
	_, err := fmt.Fprintln(s.env.stderr, human)
	return err
}

// end writes the terminal event and returns the command's result: nil when
// the stream reached its bound, a partial failure when interrupted, or the
// failure that stopped it.
func (s *eventStream) end(reason string, resume map[string]any, cause error) error {
	if resume == nil {
		resume = map[string]any{}
	}
	result := cause
	if reason == "interrupted" {
		result = &PartialError{Message: fmt.Sprintf("stream interrupted after %d entries", s.events), Hint: "resume from the position in the end event"}
	}
	if s.env.format == outputJSON {
		event := streamEvent{SchemaVersion: outputSchemaVersion, Type: "end", Data: streamEnd{Reason: reason, Events: s.events, Resume: resume}}
		if result != nil {
			event.Error = errorReportFrom(result, s.secret)
		}
		if err := s.write(event); err != nil {
			return err
		}
	} else {
		message := fmt.Sprintf("stream ended (%s) after %d entries", reason, s.events)
		if result != nil {
			message += ": " + redactText(result.Error(), s.secret)
		}
		_, _ = fmt.Fprintln(s.env.stderr, message)
	}
	if result == nil {
		return nil
	}
	return &streamedError{err: result}
}

// streamStop classifies why polling stopped: the time budget, an interrupt,
// or a failure.
func streamStop(ctx, parent context.Context, err error) (string, error) {
	if parent.Err() != nil {
		return "interrupted", nil
	}
	if ctx.Err() != nil {
		return "duration", nil
	}
	return "error", err
}

// sleepOrDone waits for the poll interval or the end of the stream.
func sleepOrDone(ctx context.Context, wait time.Duration) bool {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// runtimeLogFilters are the filters the runtime log endpoint binds into its
// cursors; a stream must send the same ones on every request.
type runtimeLogFilters struct {
	level, component, requestID, traceID, keyword, from, to string
	window                                                  time.Duration
}

func (f *runtimeLogFilters) register(fs *flag.FlagSet) {
	fs.StringVar(&f.level, "level", "", "only this level or above, for example WARN")
	fs.StringVar(&f.component, "component", "", "only this component")
	fs.StringVar(&f.requestID, "request-id", "", "only this request id")
	fs.StringVar(&f.traceID, "trace-id", "", "only this trace id")
	fs.StringVar(&f.keyword, "keyword", "", "only entries containing this text")
	fs.StringVar(&f.from, "from", "", "earliest time, RFC 3339")
	fs.StringVar(&f.to, "to", "", "latest time, RFC 3339")
	fs.DurationVar(&f.window, "window", 0, "rolling window ending now, for example 15m (at most 24h)")
}

func (f runtimeLogFilters) values(follow bool) (url.Values, error) {
	values := url.Values{}
	for name, value := range map[string]string{"level": f.level, "component": f.component, "requestId": f.requestID, "traceId": f.traceID, "keyword": f.keyword} {
		if value != "" {
			values.Set(name, value)
		}
	}
	for name, value := range map[string]string{"from": f.from, "to": f.to} {
		if value == "" {
			continue
		}
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			return nil, &UsageError{Message: fmt.Sprintf("--%s %q is not an RFC 3339 time", name, value), Hint: "use a time such as 2026-10-15T08:00:00Z"}
		}
		values.Set(name, value)
	}
	if f.window != 0 {
		if f.from != "" || f.to != "" || f.window < time.Second || f.window > 24*time.Hour {
			return nil, &UsageError{Message: "--window must be 1s to 24h and cannot be combined with --from or --to", Hint: "for example --window 15m"}
		}
		values.Set("windowSeconds", strconv.Itoa(int(f.window/time.Second)))
	}
	if follow && f.to != "" {
		return nil, &UsageError{Message: "--to cannot be combined with --follow", Hint: "a followed stream reads entries as they arrive"}
	}
	return values, nil
}

func logLine(entry adminopenapi.RuntimeLogEntry) string {
	line := fmt.Sprintf("%s %-5s %s %s %s", entry.Time.UTC().Format(time.RFC3339Nano), entry.Level, entry.Component, entry.Operation, entry.Message)
	if entry.Status != nil {
		line += fmt.Sprintf(" status=%d", *entry.Status)
	}
	if entry.ErrorCode != nil {
		line += " error=" + string(*entry.ErrorCode)
	}
	if entry.RequestId != "" {
		line += " request=" + entry.RequestId
	}
	return line
}

// logScope is what a runtime log page says about where it came from: one
// process's memory, which a restart empties.
func logScope(page adminopenapi.RuntimeLogPage) map[string]any {
	scope := map[string]any{"scope": string(page.Scope), "instanceId": page.InstanceId, "sessionId": page.SessionId}
	if page.Retention != nil {
		scope["retention"] = page.Retention
	}
	if page.AfterCursor != nil {
		scope["afterCursor"] = *page.AfterCursor
	}
	if page.HasMore != nil {
		scope["hasMore"] = *page.HasMore
	}
	return scope
}

func runtimeLogsCommand() command {
	var filters runtimeLogFilters
	var follow followOptions
	var limit int
	var before int64
	return command{
		name:     "runtime logs",
		synopsis: "read the responding process's recent logs; --follow streams new entries as NDJSON",
		usage:    "agctl runtime logs [--level L] [--component C] [--request-id ID] [--trace-id ID] [--keyword TEXT] [--from TIME] [--to TIME | --window DURATION] [--limit N] [--before SEQUENCE] [--follow (--duration D | --max-events N)] [--json]",
		setFlags: func(fs *flag.FlagSet) {
			filters.register(fs)
			follow.register(fs)
			fs.IntVar(&limit, "limit", 50, "entries per page (1-100)")
			fs.Int64Var(&before, "before", 0, "read entries older than this sequence (the nextCursor of a page)")
		},
		meta: commandMeta{
			Resource:     "runtime",
			Scenarios:    []string{"admin"},
			Stage:        "m1",
			Endpoints:    []string{"GET " + runtimeLogsPath},
			MinimumRole:  "platform admin",
			OutputSchema: "RuntimeLogPage | NDJSON stream",
			Pagination:   true,
			DryRun:       "unsupported",
			Retry:        true,
			Follow:       true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			if err := follow.validate(env); err != nil {
				return err
			}
			if limit < 1 || limit > 100 {
				return &UsageError{Message: fmt.Sprintf("--limit %d is out of range", limit), Hint: "use a limit from 1 to 100"}
			}
			if before < 0 || (follow.follow && env.flagSet["before"]) {
				return &UsageError{Message: "--before must be a positive sequence and cannot be combined with --follow", Hint: "page older entries with --before; stream newer ones with --follow"}
			}
			values, err := filters.values(follow.follow)
			if err != nil {
				return err
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			query := url.Values{}
			for key, value := range values {
				query[key] = value
			}
			query.Set("limit", strconv.Itoa(limit))
			if before > 0 {
				query.Set("beforeSequence", strconv.FormatInt(before, 10))
			}
			if !follow.follow {
				var page adminopenapi.RuntimeLogPage
				if err := client.getJSON(ctx, runtimeLogsPath, query, &page); err != nil {
					return err
				}
				payload := newListPayload(page.Items)
				if page.NextSequence != nil {
					next := strconv.FormatInt(*page.NextSequence, 10)
					payload.NextCursor = &next
				}
				meta := &envelopeMeta{Pagination: &paginationMeta{Mode: "cursor", PageSize: limit, Pages: 1}, Scope: logScope(page)}
				return env.render(payload, meta, func(w io.Writer) error {
					if _, err := fmt.Fprintf(w, "# %s logs of instance %s, session %s (lost on restart)\n", page.Scope, page.InstanceId, page.SessionId); err != nil {
						return err
					}
					for index := len(page.Items) - 1; index >= 0; index-- {
						if _, err := fmt.Fprintln(w, logLine(page.Items[index])); err != nil {
							return err
						}
					}
					if payload.NextCursor != nil {
						_, _ = fmt.Fprintf(env.stderr, "older entries: re-run with --before %s\n", *payload.NextCursor)
					}
					return nil
				})
			}
			return followRuntimeLogs(ctx, env, client, values, query, follow)
		},
	}
}

// followRuntimeLogs prints the newest page oldest first, then polls forward
// with the cursor it returned. The cursor belongs to one process session: a
// restart or a different instance ends the stream with scope_changed.
func followRuntimeLogs(parent context.Context, env *commandContext, client *apiClient, filters, first url.Values, follow followOptions) error {
	stream := &eventStream{env: env, secret: client.token, limit: follow.maxEvents}
	ctx := parent
	if follow.duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(parent, follow.duration)
		defer cancel()
	}
	cursor := ""
	resume := func() map[string]any {
		if cursor == "" {
			return nil
		}
		return map[string]any{"afterCursor": cursor}
	}
	query := first
	for {
		var page adminopenapi.RuntimeLogPage
		err := client.getJSON(ctx, runtimeLogsPath, query, &page)
		if err != nil {
			reason, cause := streamStop(ctx, parent, err)
			var api *APIError
			if reason == "error" && errors.As(err, &api) && api.HTTPStatus == http.StatusConflict {
				reason = "scope_changed"
				api.Hint = "the Gateway process restarted or another instance answered; start a new stream"
			}
			return stream.end(reason, resume(), cause)
		}
		items := page.Items
		if cursor == "" {
			// The first page is newest first; stream entries oldest first.
			for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
				items[i], items[j] = items[j], items[i]
			}
		} else if page.Retention != nil && page.Retention.Gap {
			if err := stream.note("gap", page.Retention, "gap: unread log entries were overwritten before they could be read"); err != nil {
				return err
			}
		}
		if page.AfterCursor != nil {
			cursor = *page.AfterCursor
		}
		for _, entry := range items {
			done, err := stream.emit("entry", entry, logLine(entry))
			if err != nil {
				return err
			}
			if done {
				return stream.end("max_events", resume(), nil)
			}
		}
		if cursor == "" {
			return stream.end("error", nil, &APIError{Code: "invalid_response", HTTPStatus: http.StatusOK, Message: "server returned no cursor to follow"})
		}
		if page.HasMore == nil || !*page.HasMore {
			if !sleepOrDone(ctx, follow.interval) {
				reason, _ := streamStop(ctx, parent, nil)
				return stream.end(reason, resume(), nil)
			}
		}
		query = url.Values{}
		for key, value := range filters {
			query[key] = value
		}
		query.Set("limit", "100")
		query.Set("afterCursor", cursor)
	}
}

// auditStreamLookback re-reads recent audit records on every poll, so a
// record written a little after a newer one is still delivered.
const auditStreamLookback = 2 * time.Minute

// auditPollPageBudget bounds one poll; a burst beyond it is reported as a gap.
const auditPollPageBudget = 10

func auditRecordKey(record adminopenapi.AuditRecord) string {
	encoded, _ := json.Marshal(record)
	return string(encoded)
}

// followAudits polls the cursor pages for records newer than the last poll.
// Audit records have no sequence, so each poll re-reads a short lookback
// window and skips records already delivered. Without --from the stream
// starts now; with it, it first delivers the records since --from.
func followAudits(parent context.Context, env *commandContext, client *apiClient, filters auditFilters, follow followOptions) error {
	stream := &eventStream{env: env, secret: client.token, limit: follow.maxEvents}
	ctx := parent
	if follow.duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(parent, follow.duration)
		defer cancel()
	}
	base, err := filters.values(true)
	if err != nil {
		return err
	}
	seen := map[string]time.Time{}
	since := time.Now().UTC().Add(-auditStreamLookback)
	deliver := filters.from != ""
	if deliver {
		since, _ = time.Parse(time.RFC3339, filters.from)
	}
	// start is the stream's floor: no poll reads before it, so a record older
	// than the stream is never delivered as new.
	start, watermark := since, since
	resume := func() map[string]any {
		return map[string]any{"from": watermark.Format(time.RFC3339Nano)}
	}
	for {
		query := url.Values{}
		for key, value := range base {
			query[key] = value
		}
		query.Set("from", since.Format(time.RFC3339Nano))
		query.Set("pageSize", strconv.Itoa(maxPageSize))
		var fresh []adminopenapi.AuditRecord
		exhausted := false
		for pages := 0; ; pages++ {
			if pages == auditPollPageBudget {
				exhausted = true
				break
			}
			var page serverPage[adminopenapi.AuditRecord]
			if err := client.getJSON(ctx, auditPagePath, query, &page); err != nil {
				reason, cause := streamStop(ctx, parent, err)
				return stream.end(reason, resume(), cause)
			}
			for _, record := range page.Items {
				// The server filters by from; records before the window are
				// skipped here as well, since the seen set no longer covers them.
				if record.OccurredAt.Before(since) {
					continue
				}
				if _, ok := seen[auditRecordKey(record)]; !ok {
					fresh = append(fresh, record)
				}
			}
			if page.NextPageToken == nil || *page.NextPageToken == "" {
				break
			}
			query.Set("pageToken", *page.NextPageToken)
		}
		if exhausted {
			if err := stream.note("gap", map[string]any{"reason": "poll_budget", "pages": auditPollPageBudget}, "gap: more audit records arrived than one poll reads; some may be missing"); err != nil {
				return err
			}
		}
		// Pages are newest first; deliver oldest first.
		for index := len(fresh) - 1; index >= 0; index-- {
			record := fresh[index]
			seen[auditRecordKey(record)] = record.OccurredAt
			if record.OccurredAt.After(watermark) {
				watermark = record.OccurredAt
			}
			if !deliver {
				continue
			}
			done, err := stream.emit("entry", record, strings.Join(auditRow(record), "  "))
			if err != nil {
				return err
			}
			if done {
				return stream.end("max_events", resume(), nil)
			}
		}
		deliver = true
		since = watermark.Add(-auditStreamLookback)
		if since.Before(start) {
			since = start
		}
		for key, occurred := range seen {
			if occurred.Before(since) {
				delete(seen, key)
			}
		}
		if !sleepOrDone(ctx, follow.interval) {
			reason, _ := streamStop(ctx, parent, nil)
			return stream.end(reason, resume(), nil)
		}
	}
}
