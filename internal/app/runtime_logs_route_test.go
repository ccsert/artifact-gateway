package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/operationalog"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestRuntimeLogsPreserveRegisteredRouteAndResolvedRepositoryClass(t *testing.T) {
	for _, instrument := range []bool{false, true} {
		t.Run(map[bool]string{false: "without metrics", true: "with metrics"}[instrument], func(t *testing.T) {
			store := repository.NewMemoryStore()
			if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{Name: "raw-releases", Format: repository.FormatRaw}); err != nil {
				t.Fatal(err)
			}
			buffer := operationalog.NewBuffer(8)
			var output bytes.Buffer
			logger := operationalog.NewLogger(io.MultiWriter(buffer, &output), "node", "session")
			handler := NewGatewayHandler(Dependencies{LogBuffer: buffer, Runtime: DiagnosticRuntime{InstanceID: "node", SessionID: "session"}, AccessLog: AccessLogOptions{Mode: "full", Logger: logger}}, store, TestAdapter{}, testAuthenticator())
			metrics := &Metrics{}
			if instrument {
				handler = metrics.Instrument(handler)
			}
			r := httptest.NewRequest(http.MethodGet, "/repository/raw-releases/private-artifact?token=private-query", strings.NewReader("private-body"))
			r.Header.Set("X-Request-ID", "route-regression")
			authorize(r, "resolver-secret")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != http.StatusNotFound {
				t.Fatalf("artifact status=%d", w.Code)
			}
			query := httptest.NewRequest(http.MethodGet, "/api/v2/runtime/logs?requestId=route-regression", nil)
			authorize(query, "admin-secret")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, query)
			var page struct {
				Items []map[string]any `json:"items"`
			}
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Items) != 1 {
				t.Fatalf("query status=%d body=%s", response.Code, response.Body.String())
			}
			if got := page.Items[0]; got["route"] != "/repository/" || got["requestClass"] != "raw" {
				t.Errorf("access diagnostics=%v, want registered route /repository/ and resolved raw class", got)
			}
			for _, marker := range []string{"private-artifact", "private-query", "private-body", "resolver-secret"} {
				if strings.Contains(response.Body.String()+output.String(), marker) {
					t.Errorf("sensitive marker %q in logs", marker)
				}
			}
			if instrument && (metrics.httpRequests.requests[requestClassRaw][3].Load() != 1 || metrics.httpRequests.requests[requestClassOther][3].Load() != 0 || metrics.httpRequests.inFlight[requestClassRaw].Load() != 0 || metrics.httpRequests.inFlight[requestClassOther].Load() != 0) {
				t.Error("logging changed resolved request metrics")
			}
		})
	}
}

func TestResolvedRepositoryAccessClassUsesExistingFormatCoverage(t *testing.T) {
	for _, format := range []repository.Format{repository.FormatMaven, repository.FormatRaw, repository.FormatNPM, repository.FormatPyPI, repository.FormatGo} {
		for _, instrument := range []bool{false, true} {
			t.Run(string(format)+map[bool]string{false: "/standalone", true: "/metrics"}[instrument], func(t *testing.T) {
				store := repository.NewMemoryStore()
				if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{Name: "opaque-repository", Format: format}); err != nil {
					t.Fatal(err)
				}
				var output bytes.Buffer
				d := Dependencies{AccessLog: AccessLogOptions{Mode: "full", Logger: operationalog.NewLogger(&output, "node", "session")}}
				mux := newRuntimeLogMux(nil)
				mux.Handle("/repository/", nexusRepositoryCompatibilityRouter{repositories: store, routes: map[repository.Format]nexusRepositoryCompatibilityRoute{format: {repositoryPrefix: "/native/", repositoryHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// A forwarded clone must retain the shared classification;
					// changing Pattern must not change the registered outer route.
					r.Pattern = "/private-handler-path?token=private-query"
					w.WriteHeader(http.StatusBadGateway)
				})}}})
				handler := d.requestObservability(mux)
				metrics := &Metrics{}
				if instrument {
					handler = metrics.Instrument(handler)
				}
				handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/repository/opaque-repository/private-object", nil))
				var record map[string]any
				if err := json.Unmarshal(output.Bytes(), &record); err != nil {
					t.Fatal(err)
				}
				if record["requestClass"] != string(format) || record["route"] != "/repository/" || strings.Contains(output.String(), "private-") {
					t.Fatalf("resolved log=%v", record)
				}
				if instrument {
					class, _ := requestClassForRepositoryFormat(format)
					if metrics.httpRequests.requests[class][4].Load() != 1 || metrics.httpRequests.requests[requestClassOther][4].Load() != 0 {
						t.Fatal("resolved 5xx metrics changed")
					}
					for index := range metrics.httpRequests.inFlight {
						if metrics.httpRequests.inFlight[index].Load() != 0 {
							t.Fatal("inFlight did not settle")
						}
					}
				}
			})
		}
	}
}

func TestRuntimeLogsRouteTemplatesDoNotGuessRepositoryFormat(t *testing.T) {
	buffer := operationalog.NewBuffer(8)
	handler := NewGatewayHandler(Dependencies{LogBuffer: buffer, Runtime: DiagnosticRuntime{InstanceID: "node", SessionID: "session"}, AccessLog: AccessLogOptions{Mode: "full", Logger: operationalog.NewLogger(buffer, "node", "session")}}, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator())
	for _, test := range []struct{ path, route, class string }{
		{"/repository/raw-private-name/private-object", "/repository/", "other"},
		{"/raw/private-name/private-object", "/raw/", "raw"},
		{"/cargo/private-name/private-object", "/cargo/", "cargo"},
		{"/api/v2/repositories/private-id", "GET /api/v2/repositories/{repositoryId}", "management"},
		{"/private-unmatched", "unmatched", "other"},
		{"/livez", "GET /livez", "health"},
	} {
		t.Run(test.path, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, test.path+"?token=private-query", nil)
			r.Header.Set("X-Request-ID", "template-request")
			authorize(r, "admin-secret")
			handler.ServeHTTP(httptest.NewRecorder(), r)
			page := buffer.Query(context.Background(), operationalog.Filter{RequestID: "template-request", Limit: 1, RollingFrom: true, RollingTo: true})
			if len(page.Items) != 1 || page.Items[0].Route != test.route || page.Items[0].RequestClass != test.class {
				t.Fatalf("template record=%v", page.Items)
			}
		})
	}
}

func TestRequestObservabilityRejectsUnregisteredRequestPattern(t *testing.T) {
	var output bytes.Buffer
	d := Dependencies{AccessLog: AccessLogOptions{Mode: "full", Logger: operationalog.NewLogger(&output, "node", "session")}}
	d.requestObservability(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Pattern = r.URL.String()
		w.WriteHeader(http.StatusBadGateway)
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/private-path?token=private-query", nil))
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record["route"] != "unmatched" || strings.Contains(output.String(), "private-") {
		t.Fatalf("unregistered request pattern leaked: %s", output.String())
	}
}
