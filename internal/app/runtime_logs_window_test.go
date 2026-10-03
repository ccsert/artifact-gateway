package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/operationalog"
)

func TestRuntimeLogRolling24HourWindowAndCursor(t *testing.T) {
	b := operationalog.NewBuffer(10)
	logger := operationalog.NewLogger(b, "node", "session")
	record := slog.NewRecord(time.Now().Add(-23*time.Hour), slog.LevelInfo, "older than default hour", 0)
	if err := logger.Handler().Handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	h := runtimeLogFixture(b, "node", "session")
	query := url.Values{"windowSeconds": {"86400"}}
	initial := runtimeLogPage(t, h, query)
	if len(initial.Items) != 1 {
		t.Fatalf("rolling 24h excluded legitimate record: %#v", initial)
	}
	logger.Info("new event after initial snapshot")
	query.Set("afterCursor", *initial.AfterCursor)
	next := runtimeLogPage(t, h, query)
	if len(next.Items) != 1 || next.Items[0].Message != "new event after initial snapshot" {
		t.Fatalf("follow: %#v", next)
	}
	query.Set("windowSeconds", "3600")
	bad := runtimeLogRequest(h, query, context.Background())
	if bad.Code != 400 {
		t.Fatalf("cursor accepted changed window: %d %s", bad.Code, bad.Body.String())
	}
}

func TestRuntimeLogRollingWindowRejectsInvalidBoundsWithoutData(t *testing.T) {
	b := operationalog.NewBuffer(1)
	operationalog.NewLogger(b, "node", "session").Info("protected synthetic record")
	h := runtimeLogFixture(b, "node", "session")
	for _, q := range []url.Values{
		{"windowSeconds": {"0"}}, {"windowSeconds": {"86401"}}, {"windowSeconds": {"-1"}},
		{"windowSeconds": {"3600"}, "from": {time.Now().Add(-time.Hour).Format(time.RFC3339Nano)}},
		{"windowSeconds": {"3600"}, "to": {time.Now().Format(time.RFC3339Nano)}},
	} {
		w := runtimeLogRequest(h, q, context.Background())
		if w.Code != 400 {
			t.Fatalf("accepted invalid query %v: %d %s", q, w.Code, w.Body.String())
		}
		assertRuntimeWindowProblem(t, w)
	}
}

func assertRuntimeWindowProblem(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	var problem map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem["code"] != "invalid_time_window" || problem["items"] != nil || problem["afterCursor"] != nil || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("window rejection leaked data or omitted code: %s", w.Body.String())
	}
}
