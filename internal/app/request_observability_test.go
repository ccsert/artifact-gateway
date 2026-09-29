package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/operationalog"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestRequestObservabilityCorrelatesAuditAndBoundsAccessLog(t *testing.T) {
	var output bytes.Buffer
	store := repository.NewMemoryStore()
	dependencies := Dependencies{AccessLog: AccessLogOptions{Mode: "limited", SlowThreshold: time.Hour, Logger: operationalog.NewLogger(&output, "node-1", "session-1")}}
	handler := dependencies.requestObservability(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := store.RecordAudit(r.Context(), repository.AuditRecord{GroupName: "repository", Repository: "repository", Outcome: repository.AuditResolved}); err != nil {
			t.Fatal(err)
		}
		if r.URL.Path == "/error" {
			http.Error(w, "private body", http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	ok := httptest.NewRequest(http.MethodGet, "/ok?password=private-query", nil)
	ok.Header.Set("X-Request-ID", "request-42")
	ok.Header.Set("Authorization", "Bearer private-token")
	okResponse := httptest.NewRecorder()
	handler.ServeHTTP(okResponse, ok)
	if okResponse.Code != http.StatusNoContent || okResponse.Header().Get("X-Request-ID") != "request-42" || output.Len() != 0 {
		t.Fatalf("successful request status=%d header=%q log=%s", okResponse.Code, okResponse.Header().Get("X-Request-ID"), output.String())
	}
	if len(store.Audits) != 1 || store.Audits[0].RequestID != "request-42" || store.Audits[0].TraceID != okResponse.Header().Get("X-Trace-ID") {
		t.Fatalf("audit correlation=%#v", store.Audits)
	}

	failed := httptest.NewRequest(http.MethodGet, "/error?token=private-query", nil)
	failed.Header.Set("X-Request-ID", "unsafe?token=private-header")
	failedResponse := httptest.NewRecorder()
	handler.ServeHTTP(failedResponse, failed)
	if failedResponse.Header().Get("X-Request-ID") == "unsafe?token=private-header" || len(store.Audits) != 2 {
		t.Fatalf("unsafe ID echoed or audit missing: %q %#v", failedResponse.Header().Get("X-Request-ID"), store.Audits)
	}
	if store.Audits[1].RequestID != failedResponse.Header().Get("X-Request-ID") || store.Audits[1].TraceID != failedResponse.Header().Get("X-Trace-ID") {
		t.Fatalf("failed request correlation=%#v", store.Audits[1])
	}
	var logRecord map[string]any
	if err := json.Unmarshal(output.Bytes(), &logRecord); err != nil {
		t.Fatal(err)
	}
	if logRecord["level"] != "ERROR" || logRecord["status"] != float64(http.StatusBadGateway) || logRecord["requestId"] != store.Audits[1].RequestID || logRecord["traceId"] != store.Audits[1].TraceID || logRecord["operation"] != "http.request" {
		t.Fatalf("access log=%#v", logRecord)
	}
	for _, secret := range [][]byte{[]byte("private-query"), []byte("private-header"), []byte("private-token"), []byte("private body")} {
		if bytes.Contains(output.Bytes(), secret) {
			t.Fatalf("access log includes secret: %s", output.String())
		}
	}
}

func TestRequestObservabilityLogsSlowAndFullRequests(t *testing.T) {
	for _, test := range []struct {
		name      string
		mode      string
		threshold time.Duration
		wantLevel string
	}{
		{name: "slow", mode: "limited", threshold: time.Nanosecond, wantLevel: "WARN"},
		{name: "full", mode: "full", threshold: time.Hour, wantLevel: "INFO"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			deps := Dependencies{AccessLog: AccessLogOptions{Mode: test.mode, SlowThreshold: test.threshold, Logger: operationalog.NewLogger(&output, "node", "session")}}
			response := httptest.NewRecorder()
			deps.requestObservability(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			var record map[string]any
			if err := json.Unmarshal(output.Bytes(), &record); err != nil {
				t.Fatal(err)
			}
			if record["level"] != test.wantLevel || record["requestClass"] != "health" {
				t.Fatalf("access log=%#v", record)
			}
		})
	}
}
