package agctl

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInfoRendersOnlyTheEffectiveServerURL(t *testing.T) {
	isolatedCLI(t)
	fake := newFakeGateway(t, "configured-token")
	server := strings.Replace(fake.server.URL, "://", "://private-user:private-password@", 1) + "?token=query-secret#fragment-secret"
	for _, format := range []string{"table", "json"} {
		t.Run(format, func(t *testing.T) {
			code, stdout, stderr := runCLI(t, "info", "--server", server, "--token", "configured-token", "--verbose", "-o", format)
			if code != ExitOK {
				t.Fatalf("code = %d", code)
			}
			for _, secret := range []string{"private-user", "private-password", "query-secret", "fragment-secret", "configured-token"} {
				if strings.Contains(stdout+stderr, secret) {
					t.Fatalf("%s output contains a credential", format)
				}
			}
			if format == "json" {
				var report infoReport
				if err := json.Unmarshal(decodeEnvelope(t, stdout).Data, &report); err != nil {
					t.Fatal(err)
				}
				if report.Server != fake.server.URL {
					t.Fatalf("server = %q, want effective endpoint %q", report.Server, fake.server.URL)
				}
			}
		})
	}
}

func TestInvalidServerDoesNotEchoCredentials(t *testing.T) {
	isolatedCLI(t)
	server := "http://private-user:private-password@[::1?token=query-secret"
	for _, format := range []string{"table", "json"} {
		code, stdout, stderr := runCLI(t, "info", "--server", server, "-o", format)
		if code != ExitUsage {
			t.Fatalf("code = %d", code)
		}
		for _, secret := range []string{"private-user", "private-password", "query-secret"} {
			if strings.Contains(stdout+stderr, secret) {
				t.Fatalf("%s error contains a credential", format)
			}
		}
		if format == "json" {
			if report := decodeEnvelope(t, stdout); report.OK || report.Error.Code != "usage_error" {
				t.Fatalf("unexpected failure envelope: %#v", report)
			}
		}
	}
}

func TestInfoAuthFailuresKeepSafeDiagnostics(t *testing.T) {
	isolatedCLI(t)
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/livez" || r.URL.Path == "/readyz" {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				w.Header().Set("X-Request-ID", "request-header")
				writeProblem(w, status, "access_denied", "Authorization: Bearer configured-token; password=echoed-password")
			}))
			defer server.Close()
			for _, format := range []string{"table", "json"} {
				code, stdout, stderr := runCLI(t, "info", "--server", server.URL, "--token", "configured-token", "--verbose", "-o", format)
				if code != ExitOK {
					t.Fatalf("code = %d", code)
				}
				for _, secret := range []string{"configured-token", "echoed-password"} {
					if strings.Contains(stdout+stderr, secret) {
						t.Fatalf("%s auth diagnostics contain a credential", format)
					}
				}
				if format == "json" {
					var report infoReport
					if err := json.Unmarshal(decodeEnvelope(t, stdout).Data, &report); err != nil {
						t.Fatal(err)
					}
					for _, probe := range []probeResult{report.Identity.probeResult, report.Formats.probeResult} {
						if probe.Code != "access_denied" || probe.RequestID != "req-test" || probe.Hint == "" || probe.HTTPStatus != status {
							t.Fatalf("missing safe diagnostics: %#v", probe)
						}
					}
				} else {
					for _, want := range []string{"access_denied", "req-test", "hint:"} {
						if !strings.Contains(stdout, want) {
							t.Fatalf("missing %q in human report", want)
						}
					}
				}
			}
		})
	}
}

func TestInfoHealthFailuresDoNotExposeRawBodies(t *testing.T) {
	isolatedCLI(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", "health-request")
		w.Header().Set("Retry-After", "0")
		switch r.URL.Path {
		case "/livez":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = fmt.Fprint(w, "plain-body-private-credential")
		case "/readyz":
			writeProblem(w, http.StatusServiceUnavailable, "dependency_unavailable", "backend password=health-password; configured-token")
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()
	for _, format := range []string{"table", "json"} {
		code, stdout, stderr := runCLI(t, "info", "--server", server.URL, "--token", "configured-token", "--verbose", "-o", format)
		if code != ExitOK {
			t.Fatalf("code = %d", code)
		}
		for _, secret := range []string{"plain-body-private-credential", "health-password", "configured-token"} {
			if strings.Contains(stdout+stderr, secret) {
				t.Fatalf("%s health diagnostics contain a credential", format)
			}
		}
		if format == "json" {
			var report infoReport
			if err := json.Unmarshal(decodeEnvelope(t, stdout).Data, &report); err != nil {
				t.Fatal(err)
			}
			if report.Livez.Status != "error" || report.Livez.RequestID != "health-request" || report.Readyz.Status != "not_ready" || report.Readyz.Code != "dependency_unavailable" {
				t.Fatalf("health diagnostics = %#v / %#v", report.Livez, report.Readyz)
			}
		}
	}
}
