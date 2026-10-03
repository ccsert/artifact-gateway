package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/operationalog"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func runtimeLogFixture(buffer *operationalog.Buffer, instance, session string) http.Handler {
	return NewGatewayHandler(Dependencies{LogBuffer: buffer, Runtime: DiagnosticRuntime{InstanceID: instance, SessionID: session}}, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator())
}

func runtimeLogPage(t *testing.T, handler http.Handler, query url.Values) adminopenapi.RuntimeLogPage {
	t.Helper()
	response := runtimeLogRequest(handler, query, context.Background())
	if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response %d %s", response.Code, response.Body.String())
	}
	var p adminopenapi.RuntimeLogPage
	if err := json.Unmarshal(response.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.AfterCursor == nil || p.Retention == nil || p.Source == nil || p.HasMore == nil || p.Order == nil {
		t.Fatalf("missing incremental/source contract: %#v", p)
	}
	return p
}

func TestRuntimeLogIncrementalTimeAndLevelFiltersPreserveUnreadPages(t *testing.T) {
	b := operationalog.NewBuffer(350)
	l := operationalog.NewLogger(b, "node", "session")
	h := runtimeLogFixture(b, "node", "session")
	now := time.Now().UTC()
	from, to := now.Add(-30*time.Minute), now.Add(-time.Minute)
	query := url.Values{"from": {from.Format(time.RFC3339Nano)}, "to": {to.Format(time.RFC3339Nano)}, "level": {"ERROR"}, "limit": {"100"}}
	initial := runtimeLogPage(t, h, query)
	for i := 1; i <= 260; i++ {
		when, level := from.Add(time.Minute), slog.LevelInfo
		if i <= 20 {
			when = from.Add(-time.Minute)
		} else if i > 240 {
			when = to.Add(30 * time.Second)
		}
		if i%2 == 0 {
			level = slog.LevelError
		}
		record := slog.NewRecord(when, level, fmt.Sprintf("event-%d", i), 0)
		record.AddAttrs(slog.String("component", "http"))
		if err := l.Handler().Handle(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}
	query.Set("afterCursor", *initial.AfterCursor)
	first := runtimeLogPage(t, h, query)
	if len(first.Items) != 100 || first.Retention.Gap || !*first.HasMore || first.Items[0].Sequence != 22 || first.Items[99].Sequence != 220 {
		t.Fatalf("first filtered page %#v", first)
	}
	// Appends between pages must not erase the still-unread filtered history.
	for i := 0; i < 10; i++ {
		record := slog.NewRecord(from.Add(time.Minute), slog.LevelError, "late append", 0)
		record.AddAttrs(slog.String("component", "http"))
		if err := l.Handler().Handle(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}
	query.Set("afterCursor", *first.AfterCursor)
	second := runtimeLogPage(t, h, query)
	if len(second.Items) != 20 || second.Retention.Gap || *second.HasMore || second.Items[0].Sequence != 222 || second.Items[19].Sequence != 270 {
		t.Fatalf("unread page skipped %#v", second)
	}
	for _, entry := range append(first.Items, second.Items...) {
		if entry.Level != "ERROR" || entry.Time.Before(from) || entry.Time.After(to) {
			t.Fatalf("filter leaked %#v", entry)
		}
	}
}

func TestRuntimeLogOverwriteBetweenForwardPagesReportsCoverage(t *testing.T) {
	b := operationalog.NewBuffer(120)
	l := operationalog.NewLogger(b, "node", "session")
	h := runtimeLogFixture(b, "node", "session")
	p := runtimeLogPage(t, h, nil)
	for i := 0; i < 100; i++ {
		l.Info("retained")
	}
	query := url.Values{"afterCursor": {*p.AfterCursor}, "limit": {"50"}}
	p = runtimeLogPage(t, h, query)
	if len(p.Items) != 50 || p.Retention.Gap || p.Items[49].Sequence != 50 || !*p.HasMore {
		t.Fatalf("first page %#v", p)
	}
	query.Set("afterCursor", *p.AfterCursor)
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := 0; i < 80; i++ {
			l.Info("new retained")
		}
	})
	wg.Wait()
	p = runtimeLogPage(t, h, query)
	if !p.Retention.Gap || p.Retention.EarliestSequence != 61 || p.Retention.LatestSequence != 180 || len(p.Items) != 50 || p.Items[0].Sequence != 61 {
		t.Fatalf("overwrite not explicit %#v", p)
	}
	query.Set("afterCursor", *p.AfterCursor)
	p = runtimeLogPage(t, h, query)
	if p.Retention.Gap || p.Items[0].Sequence != 111 {
		t.Fatalf("covered read falsely lost %#v", p)
	}
}

func TestRuntimeLogSourceUsesObservedExactComponentsAndActualPolicy(t *testing.T) {
	b := operationalog.NewBuffer(150)
	l := operationalog.NewLogger(b, "node", "session")
	for i := 0; i < 110; i++ {
		l.Info("synthetic worker event", "component", fmt.Sprintf("worker_%03d", i))
	}
	operationalog.NewLogger(b, "other-node", "other-session").Info("foreign", "component", "foreign_worker")
	h := NewGatewayHandler(Dependencies{LogBuffer: b, Runtime: DiagnosticRuntime{InstanceID: "node", SessionID: "session"}, AccessLog: AccessLogOptions{Mode: "full", SlowThreshold: 25 * time.Millisecond}}, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator())
	p := runtimeLogPage(t, h, url.Values{"level": {"DEBUG"}})
	if len(p.Items) != 0 || p.Source.AccessMode != "full" || p.Source.SlowThresholdMs != 25 || p.Source.MinimumLevel != "INFO" || p.Source.CapacityLines != 150 || p.Source.MaxLineBytes != 16384 || len(p.Source.Components) != 100 || !p.Source.ComponentsTruncated {
		t.Fatalf("source metadata %#v", p)
	}
	for _, component := range p.Source.Components {
		if component == "foreign_worker" || component == "worker" {
			t.Fatalf("fabricated component %q", component)
		}
	}
	if len(runtimeLogPage(t, h, url.Values{"component": {"worker"}}).Items) != 0 {
		t.Fatal("worker filter became a wildcard")
	}
}

func TestRuntimeLogProjectionOfRealHTTPAccessEvent(t *testing.T) {
	const marker = "synthetic-private-http-payload"
	b := operationalog.NewBuffer(10)
	l := operationalog.NewLogger(b, "node", "session")
	h := NewGatewayHandler(Dependencies{LogBuffer: b, Runtime: DiagnosticRuntime{InstanceID: "node", SessionID: "session"}, AccessLog: AccessLogOptions{Mode: "full", Logger: l}}, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator())
	r := httptest.NewRequest("GET", "/api/v2/repositories?token="+marker, strings.NewReader(marker))
	r.Header.Set("X-Request-ID", "synthetic-http-event")
	authorize(r, "admin-secret")
	r.Header.Set("X-Api-Token", marker)
	r.Header.Set("Cookie", "session="+marker)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("management fixture %d %s", w.Code, w.Body.String())
	}
	p := runtimeLogPage(t, h, url.Values{"requestId": {"synthetic-http-event"}})
	if len(p.Items) != 1 {
		t.Fatalf("real HTTP event absent %#v", p)
	}
	e := p.Items[0]
	if e.Route == nil || *e.Route != "GET /api/v2/repositories" {
		t.Fatalf("registered HTTP template missing %#v", e)
	}
	if e.Status == nil || *e.Status != 200 || e.DurationMs == nil || *e.DurationMs < 0 || e.Method == nil || *e.Method != "GET" || e.RequestClass == nil || *e.RequestClass != "management" || e.Component != "http" || e.Operation != "http.request" || e.TraceId == "" {
		t.Fatalf("lost HTTP diagnostics %#v", e)
	}
	encoded, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), marker) || strings.Contains(string(encoded), "admin-secret") || strings.Contains(string(encoded), `"headers"`) {
		t.Fatalf("raw HTTP payload projected %s", encoded)
	}
	if p = runtimeLogPage(t, h, url.Values{"keyword": {marker}}); len(p.Items) != 0 {
		t.Fatalf("raw HTTP payload searchable %#v", p)
	}
}

func TestRuntimeLogMalformedDiagnosticValuesDoNotDiscardSafeEvent(t *testing.T) {
	b := operationalog.NewBuffer(4)
	h := runtimeLogFixture(b, "node", "session")
	for _, extra := range []string{`"status":"503","durationMs":{},"attempt":[],"method":1,"requestClass":{},"jobId":[]`, `"status":null,"durationMs":null,"attempt":null,"method":null,"requestClass":null,"jobId":null`} {
		line := fmt.Sprintf(`{"time":%q,"level":"INFO","msg":"safe event","instanceId":"node","sessionId":"session",%s}`+"\n", time.Now().UTC().Format(time.RFC3339Nano), extra)
		if _, err := b.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	p := runtimeLogPage(t, h, nil)
	if len(p.Items) != 2 {
		t.Fatalf("safe events discarded %#v", p)
	}
	for _, entry := range p.Items {
		if entry.Status != nil || entry.DurationMs != nil || entry.Method != nil || entry.RequestClass != nil || entry.JobId != nil || entry.Attempt != nil {
			t.Fatalf("malformed diagnostics exposed %#v", entry)
		}
	}
}

func runtimeLogRequest(handler http.Handler, query url.Values, ctx context.Context) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "/api/v2/runtime/logs?"+query.Encode(), nil).WithContext(ctx)
	authorize(r, "admin-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestRuntimeLogIncrementalPublicBoundary(t *testing.T) {
	b := operationalog.NewBuffer(500)
	l := operationalog.NewLogger(b, "node", "session")
	h := runtimeLogFixture(b, "node", "session")
	query := url.Values{"component": {"http"}, "limit": {"100"}}
	initial := runtimeLogPage(t, h, query)
	if initial.AfterCursor == nil || initial.Source == nil || initial.Retention == nil || len(initial.Items) != 0 || initial.Source.MinimumLevel != "INFO" || initial.Source.AccessMode != "limited" || initial.Source.CapacityLines != 500 {
		t.Fatalf("initial %#v", initial)
	}
	for i := 1; i <= 230; i++ {
		l.Info(fmt.Sprintf("event-%d", i), "component", "http", "status", 503, "method", "GET", "durationMs", 25, "requestClass", "maven")
	}
	query.Set("afterCursor", *initial.AfterCursor)
	seen := map[int64]bool{}
	for _, count := range []int{100, 100, 30} {
		p := runtimeLogPage(t, h, query)
		if len(p.Items) != count || p.AfterCursor == nil || p.Order == nil || *p.Order != "ascending" || p.Retention.Gap {
			t.Fatalf("forward page %#v", p)
		}
		for _, entry := range p.Items {
			if seen[entry.Sequence] || entry.Sequence != int64(len(seen)+1) || entry.Status == nil || *entry.Status != 503 || entry.DurationMs == nil || *entry.DurationMs != 25 {
				t.Fatalf("lost/duplicate/unsafe entry %#v", entry)
			}
			seen[entry.Sequence] = true
		}
		if p.HasMore == nil || *p.HasMore != (count == 100) {
			t.Fatalf("hasMore %#v", p)
		}
		query.Set("afterCursor", *p.AfterCursor)
	}
	if len(runtimeLogPage(t, h, query).Items) != 0 {
		t.Fatal("cursor replayed consumed entries")
	}
	operationalog.NewLogger(b, "other-node", "other-session").Info("foreign event", "component", "http")
	l.Debug("debug must remain disabled")
	if p := runtimeLogPage(t, h, query); len(p.Items) != 0 {
		t.Fatalf("component/debug filter changed %#v", p)
	}
}

func TestRuntimeLogCursorIsBoundedForConfiguredInstanceIDs(t *testing.T) {
	instance := "node-" + strings.Repeat("x", 2048)
	b := operationalog.NewBuffer(2)
	h := runtimeLogFixture(b, instance, "session")
	p := runtimeLogPage(t, h, nil)
	if p.AfterCursor == nil || len(*p.AfterCursor) > 1024 {
		t.Fatalf("cursor exceeds wire bound: %#v", p.AfterCursor)
	}
	operationalog.NewLogger(b, instance, "session").Info("retained")
	p = runtimeLogPage(t, h, url.Values{"afterCursor": {*p.AfterCursor}})
	if len(p.Items) != 1 {
		t.Fatalf("bounded cursor cannot follow %#v", p)
	}
}

func TestRuntimeLogCursorValidationScopeAndTrueGap(t *testing.T) {
	b := operationalog.NewBuffer(3)
	l := operationalog.NewLogger(b, "node", "session")
	h := runtimeLogFixture(b, "node", "session")
	query := url.Values{"level": {"ERROR"}}
	p := runtimeLogPage(t, h, query)
	for i := 0; i < 4; i++ {
		l.Info("filtered out")
	}
	query.Set("afterCursor", *p.AfterCursor)
	p = runtimeLogPage(t, h, query)
	if !p.Retention.Gap || len(p.Items) != 0 || p.Retention.EarliestSequence != 2 {
		t.Fatalf("retention gap %#v", p)
	}
	query.Set("afterCursor", *p.AfterCursor)
	l.Error("error retained")
	p = runtimeLogPage(t, h, query)
	if p.Retention.Gap || len(p.Items) != 1 {
		t.Fatalf("filter sequence gap treated as loss %#v", p)
	}
	for _, test := range []struct {
		name    string
		query   url.Values
		handler http.Handler
		want    int
		code    string
	}{
		{"malformed", url.Values{"afterCursor": {"bad"}}, h, 400, "invalid_cursor"},
		{"tamper", url.Values{"level": {"ERROR"}, "afterCursor": {*p.AfterCursor + "x"}}, h, 400, "invalid_cursor"},
		{"changed filter", url.Values{"afterCursor": {*p.AfterCursor}}, h, 400, "invalid_cursor"},
		{"before and after", url.Values{"afterCursor": {*p.AfterCursor}, "beforeSequence": {"3"}}, h, 400, "invalid_cursor"},
		{"restart", query, runtimeLogFixture(operationalog.NewBuffer(3), "node", "new-session"), 409, "log_cursor_scope_changed"},
		{"different node", query, runtimeLogFixture(operationalog.NewBuffer(3), "other", "session"), 409, "log_cursor_scope_changed"},
		{"control filter", url.Values{"component": {"http\x1b"}}, h, 400, "invalid_filter"},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := runtimeLogRequest(test.handler, test.query, context.Background())
			if r.Code != test.want || !strings.Contains(r.Body.String(), test.code) || strings.Contains(r.Body.String(), `"items"`) {
				t.Fatalf("%d %s", r.Code, r.Body.String())
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r := runtimeLogRequest(h, nil, ctx); r.Code != 504 || !strings.Contains(r.Body.String(), "log_query_timeout") {
		t.Fatalf("canceled query %d %s", r.Code, r.Body.String())
	}
}

func TestRuntimeLogConcurrentAppendAndPaging(t *testing.T) {
	b := operationalog.NewBuffer(1000)
	l := operationalog.NewLogger(b, "node", "session")
	h := runtimeLogFixture(b, "node", "session")
	p := runtimeLogPage(t, h, nil)
	query := url.Values{"afterCursor": {*p.AfterCursor}, "limit": {"17"}}
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := 0; i < 300; i++ {
			l.Info("concurrent event")
		}
	})
	seen := map[int64]bool{}
	for i := 0; i < 100; i++ {
		p = runtimeLogPage(t, h, query)
		for _, entry := range p.Items {
			if seen[entry.Sequence] {
				t.Fatal("duplicate")
			}
			seen[entry.Sequence] = true
		}
		query.Set("afterCursor", *p.AfterCursor)
		if len(seen) == 300 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	wg.Wait()
	if len(seen) != 300 {
		t.Fatalf("read %d/300 entries", len(seen))
	}
}
