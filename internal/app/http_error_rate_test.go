package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/httperrorrate"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/getkin/kin-openapi/openapi3"
)

func TestHTTPErrorRateActualHTTPAndDiagnosticsContract(t *testing.T) {
	m := &Metrics{}
	o := httperrorrate.New("synthetic-http-node", "synthetic-http-session")
	now := time.Now().UTC()
	m.sampleHTTPErrorRate(o, "synthetic-http-session", now.Add(-5*time.Minute))
	server := httptest.NewServer(m.Instrument(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/failure", "/livez", "/readyz", "/metrics":
			w.WriteHeader(503)
		case "/raw/missing":
			w.WriteHeader(404)
		default:
			w.WriteHeader(200)
		}
	})))
	t.Cleanup(server.Close)
	for path, count := range map[string]int{"/api/failure": 2, "/raw/missing": 8, "/other": 10, "/livez": 6, "/readyz": 6, "/metrics": 6} {
		for range count {
			r, err := server.Client().Get(server.URL + path)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, r.Body)
			_ = r.Body.Close()
		}
	}
	for i := 1; i <= 20; i++ {
		m.sampleHTTPErrorRate(o, "synthetic-http-session", now.Add(time.Duration(i-20)*15*time.Second))
	}
	deps := Dependencies{HTTPErrorRate: o, Runtime: DiagnosticRuntime{InstanceID: "synthetic-http-node", SessionID: "synthetic-http-session", Roles: []string{"api"}}}
	handler := NewGatewayHandler(deps, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator())
	response := capacityResponse(handler, "admin-secret")
	if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("diagnostics %d", response.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	rate := body["httpErrorRate"].(map[string]any)
	if rate["requests"] != float64(20) || rate["errors"] != float64(2) || rate["ratio"] != 0.1 || rate["state"] != "available" || rate["instanceId"] != "synthetic-http-node" || rate["coverageSeconds"] != float64(300) {
		t.Fatalf("rate: %#v", rate)
	}
	spec, err := openapi3.NewLoader().LoadFromFile(filepath.Join("..", "..", "api", "openapi", "management-runtime-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := spec.Paths.Find("/diagnostics").Get.Responses.Status(200).Value.Content["application/json"].Schema.Value.VisitJSON(body); err != nil {
		t.Fatal(err)
	}
	// Repeated diagnostics reads do not sample or move the counter boundaries.
	for range 8 {
		r := capacityResponse(handler, "admin-secret")
		var next map[string]any
		_ = json.Unmarshal(r.Body.Bytes(), &next)
		n := next["httpErrorRate"].(map[string]any)
		if n["sampleAt"] != rate["sampleAt"] || n["requests"] != rate["requests"] {
			t.Fatal("diagnostics refreshed counter sample")
		}
	}
	for _, token := range []string{"", "reader-secret"} {
		r := capacityResponse(handler, token)
		if r.Code == 200 || strings.Contains(r.Body.String(), "httpErrorRate") {
			t.Fatal("diagnostics leaked across administrator gate")
		}
	}
}

func TestHTTPErrorRateFallbackAndMisboundObserver(t *testing.T) {
	runtime := DiagnosticRuntime{InstanceID: "node", SessionID: "session"}
	for _, o := range []*httperrorrate.Observer{nil, httperrorrate.New("other-node", "other-session")} {
		r := diagnosticHTTPErrorRate(o, runtime, time.Now())
		if r.State != "unknown" || r.Reason == nil || *r.Reason != "source_unavailable" || r.Ratio != nil || r.Requests != nil || r.InstanceId != "node" {
			t.Fatalf("fallback: %#v", r)
		}
	}
}

func TestHTTPErrorRateSamplerFollowsRuntimeCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	o, done := (&Metrics{}).StartHTTPErrorRateSampling(ctx, "node", "session")
	before := o.Snapshot(time.Now().UTC())
	if before.SampleAt == nil || before.Ratio != nil || before.Reason != "warming_up" {
		t.Fatal("initial baseline")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sampler did not stop")
	}
	if after := o.Snapshot(time.Now().UTC()); !after.SampleAt.Equal(*before.SampleAt) {
		t.Fatal("cancelled sampler changed sample")
	}
}
