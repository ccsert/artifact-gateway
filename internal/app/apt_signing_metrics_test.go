package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAPTSigningMetricsExposeBoundedOutcomesAndLatency(t *testing.T) {
	metrics := &Metrics{}
	metrics.RecordAPTSigning("success", 200*time.Millisecond)
	metrics.RecordAPTSigning("untrusted_signer", 2*time.Second)
	metrics.RecordAPTSigning("actor@example.test", time.Second)

	response := httptest.NewRecorder()
	metrics.Handler(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := response.Body.String()
	for _, expected := range []string{
		`artifact_gateway_apt_signing_requests_total{outcome="success"} 1`,
		`artifact_gateway_apt_signing_requests_total{outcome="untrusted_signer"} 1`,
		`artifact_gateway_apt_signing_requests_total{outcome="invalid_signature"} 0`,
		`artifact_gateway_apt_signing_duration_seconds_bucket{le="0.1"} 0`,
		`artifact_gateway_apt_signing_duration_seconds_bucket{le="0.25"} 1`,
		`artifact_gateway_apt_signing_duration_seconds_bucket{le="2.5"} 2`,
		`artifact_gateway_apt_signing_duration_seconds_bucket{le="+Inf"} 2`,
		`artifact_gateway_apt_signing_duration_seconds_count 2`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("metrics missing %q:\n%s", expected, body)
		}
	}
	if strings.Contains(body, "actor@example.test") {
		t.Fatalf("unbounded signing label leaked into metrics:\n%s", body)
	}
}
