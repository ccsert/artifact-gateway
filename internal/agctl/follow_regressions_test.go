package agctl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestFollowFailureAndInterruptHaveOneTerminalEvent(t *testing.T) {
	for _, name := range []string{"logs", "audits"} {
		t.Run(name, func(t *testing.T) {
			api := newRouteAPI(t, "")
			path, args := runtimeLogsPath, []string{"runtime", "logs"}
			if name == "audits" {
				path, args = auditPagePath, []string{"audit", "list"}
			}
			args = append(args, "--follow", "--duration", "1m", "--json")
			api.on("GET", path, 403, `{"code":"access_denied","message":"forbidden"}`)
			code, stdout, _ := runCLI(t, args...)
			events := ndjson(t, stdout)
			end := events[len(events)-1]
			if code != ExitForbidden || end["data"].(map[string]any)["reason"] != "error" || end["error"] == nil {
				t.Fatalf("failure exit=%d events=%v", code, events)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			var out, stderr bytes.Buffer
			code = RunCLI(ctx, args, strings.NewReader(""), &out, &stderr)
			events = ndjson(t, out.String())
			if code != ExitPartial || events[len(events)-1]["data"].(map[string]any)["reason"] != "interrupted" {
				t.Fatalf("interrupt exit=%d events=%v", code, events)
			}
		})
	}
}

func TestAuthorizationWritesRejectMissingRequiredFields(t *testing.T) {
	for _, body := range []string{`[null]`, `[{}]`, `[{"principal":"user:a"}]`, `[{"principal":"user:a","scopes":null}]`} {
		t.Run("template_"+body, func(t *testing.T) {
			api := newRouteAPI(t, "")
			api.onETag("POST", templatesPath+"/"+templateID+"/apply", 200, body, "2")
			code, stdout, _ := runCLI(t, "template", "apply", templateID, "--repo", testRepoID, "--if-match", "1", "--yes", "--json")
			var result unknownOutcome
			_ = json.Unmarshal(decodeEnvelope(t, stdout).Data, &result)
			if code == ExitOK || result.Outcome != "unknown" || api.count("POST") != 1 {
				t.Fatalf("incomplete write must be unknown: exit=%d, stdout=%s", code, stdout)
			}
		})
	}
	for _, body := range []string{`{"version":"2"}`, `{"version":"2","enabled":null}`} {
		t.Run("anonymous_"+body, func(t *testing.T) {
			api := newRouteAPI(t, "")
			api.on("PUT", anonymousPolicyPath, 200, body)
			code, stdout, _ := runCLI(t, "access", "anonymous", "set", "--enabled=false", "--if-match", "1", "--json")
			var result unknownOutcome
			_ = json.Unmarshal(decodeEnvelope(t, stdout).Data, &result)
			if code == ExitOK || result.Outcome != "unknown" || api.count("PUT") != 1 {
				t.Fatalf("missing enabled must be unknown: exit=%d, stdout=%s", code, stdout)
			}
		})
	}
}

func TestRuntimeLogsFollowResumesWithoutSkippingPageTail(t *testing.T) {
	api := newRouteAPI(t, "")
	api.onFunc("GET", runtimeLogsPath, func(r *http.Request) (int, string) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		cursor := r.URL.Query().Get("afterCursor")
		if cursor == "" {
			return 200, `{"scope":"local","instanceId":"i-1","sessionId":"s-1","items":[` + logEntry(2) + `,` + logEntry(1) + `],"afterCursor":"c-2","hasMore":true}`
		}
		var after int
		_, _ = fmt.Sscanf(cursor, "c-%d", &after)
		var items []string
		for next := after + 1; next <= 5 && len(items) < limit; next++ {
			items = append(items, logEntry(next))
		}
		return 200, fmt.Sprintf(`{"scope":"local","instanceId":"i-1","sessionId":"s-1","items":[%s],"afterCursor":"c-%d","hasMore":true}`, strings.Join(items, ","), after+len(items))
	})
	code, stdout, _ := runCLI(t, "runtime", "logs", "--follow", "--max-events", "4", "--json")
	events := ndjson(t, stdout)
	end := events[len(events)-1]["data"].(map[string]any)
	cursor := end["resume"].(map[string]any)["afterCursor"].(string)
	if code != ExitOK || cursor != "c-4" {
		t.Fatalf("resume skipped an undelivered entry: exit=%d, end=%v", code, end)
	}
	code, stdout, _ = runCLI(t, "runtime", "logs", "--after-cursor", cursor, "--follow", "--max-events", "1", "--json")
	if code != ExitOK {
		t.Fatalf("resume exit=%d, stdout=%s", code, stdout)
	}
	var sequences []float64
	for _, event := range ndjson(t, stdout) {
		if event["type"] == "entry" {
			sequences = append(sequences, event["data"].(map[string]any)["sequence"].(float64))
		}
	}
	if len(sequences) != 1 || sequences[0] != 5 {
		t.Fatalf("resumed sequences=%v", sequences)
	}
}

// Cancel only after the stream actually identifies its source. A millisecond
// deadline can expire before the first HTTP response on a busy CI runner.
type cancelOnScopeWriter struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (w *cancelOnScopeWriter) Write(data []byte) (int, error) {
	n, err := w.Buffer.Write(data)
	if bytes.Contains(data, []byte(`"type":"scope"`)) {
		w.cancel()
	}
	return n, err
}

func TestRuntimeLogsEmptyFollowReportsScope(t *testing.T) {
	api := newRouteAPI(t, "")
	api.on("GET", runtimeLogsPath, 200, `{"scope":"local","instanceId":"i-1","sessionId":"s-1","items":[],"afterCursor":"c-0","hasMore":false}`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stdout := &cancelOnScopeWriter{cancel: cancel}
	var stderr bytes.Buffer
	code := RunCLI(ctx, []string{"runtime", "logs", "--follow", "--duration", "5s", "--json"}, strings.NewReader(""), stdout, &stderr)
	events := ndjson(t, stdout.String())
	if code != ExitPartial || events[0]["type"] != "scope" {
		t.Fatalf("empty stream must identify its source: exit=%d, events=%v", code, events)
	}
	if end := events[len(events)-1]["data"].(map[string]any); end["reason"] != "interrupted" || end["events"] != float64(0) {
		t.Fatalf("empty stream end=%v", end)
	}
	scope := events[0]["data"].(map[string]any)
	if scope["scope"] != "local" || scope["instanceId"] != "i-1" || scope["sessionId"] != "s-1" {
		t.Fatalf("scope=%v", scope)
	}
}

func TestAuditFollowPollBudgetDoesNotSkipHistory(t *testing.T) {
	api := newRouteAPI(t, "")
	from := "2026-10-15T08:00:00Z"
	base, _ := time.Parse(time.RFC3339, from)
	api.on("GET", auditPagePath, 200, `{"items":[`+auditRecord(base, time.Hour, "newest")+`],"nextPageToken":"more-history"}`)
	code, stdout, _ := runCLI(t, "audit", "list", "--follow", "--from", from, "--max-events", "1", "--json")
	events := ndjson(t, stdout)
	end := events[len(events)-1]["data"].(map[string]any)
	if code != ExitPartial || end["reason"] != "poll_budget" || end["events"] != float64(0) || end["resume"].(map[string]any)["from"] != from {
		t.Fatalf("a truncated history must not advance past unread pages: exit=%d, end=%v", code, end)
	}
}

func TestAuditFollowResumePreservesLookback(t *testing.T) {
	api := newRouteAPI(t, "")
	from := "2026-10-15T08:00:00Z"
	base, _ := time.Parse(time.RFC3339, from)
	api.on("GET", auditPagePath, 200, `{"items":[`+auditRecord(base, 3*time.Second, "b")+`,`+auditRecord(base, time.Second, "a")+`]}`)
	code, stdout, _ := runCLI(t, "audit", "list", "--follow", "--from", from, "--max-events", "2", "--json")
	events := ndjson(t, stdout)
	end := events[len(events)-1]["data"].(map[string]any)
	if code != ExitOK || end["resume"].(map[string]any)["from"] != from {
		t.Fatalf("resume must include late records in the lookback: exit=%d, end=%v", code, end)
	}
}
