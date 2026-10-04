package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/operationalog"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/getkin/kin-openapi/openapi3"
)

type rawDiagnosticClient struct {
	UpstreamClient
	fetch func() (*http.Response, error)
}

func (c rawDiagnosticClient) FetchRaw(context.Context, string, repository.Member, string, http.Header) (*http.Response, error) {
	return c.fetch()
}

type rawDiagnosticBody struct {
	io.Reader
	closeErr error
}

func (b rawDiagnosticBody) Close() error { return b.closeErr }

type rawDiagnosticCoordinator struct{ rawRequestLockCoordinator }

func (*rawDiagnosticCoordinator) Release(context.Context, string, string) error {
	return errors.New("private-coordination-error")
}

// Exercise the production UpstreamClient and native HTTP router. Network hooks
// only redirect an allowed public fixture hostname to the local TLS fixture.
func TestRawFailureDiagnosticsAtHTTPBoundary(t *testing.T) {
	oldLookup, oldDial, oldProxy := rawProxyLookupIP, rawProxyDialContext, rawProxyFromEnvironment
	t.Cleanup(func() { rawProxyLookupIP, rawProxyDialContext, rawProxyFromEnvironment = oldLookup, oldDial, oldProxy })
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/transport/private-object":
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		case "/body/private-object":
			w.Header().Set("Content-Length", "100")
			_, _ = io.WriteString(w, "short-private-body")
		case "/status/private-object":
			w.Header().Set("Location", "https://private-redirect.example/private-path")
			w.WriteHeader(http.StatusFound)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer upstream.Close()
	rawProxyLookupIP = func(context.Context, string, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("93.184.216.34")}, nil
	}
	rawProxyDialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(upstream.URL, "https://"))
	}
	rawProxyFromEnvironment = func(*http.Request) (*url.URL, error) { return nil, nil }
	spec, err := openapi3.NewLoader().LoadFromFile("../../api/openapi/management-runtime-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, endpoint, code, phase string
		status                      int
		methods                     []string
	}{
		{"configuration", "https://private-user:private-credential@example.com", "upstream_configuration_invalid", "prepare", 403, []string{"GET", "HEAD"}},
		{"policy", "https://private-denied.example", "upstream_policy_rejected", "prepare", 403, []string{"GET", "HEAD"}},
		{"egress", "https://example.com/egress", "upstream_egress_failed", "egress", 502, []string{"GET", "HEAD"}},
		{"transport", "https://example.com/transport", "upstream_transport_failed", "fetch", 502, []string{"GET", "HEAD"}},
		{"status", "https://example.com/status", "upstream_status_rejected", "fetch", 502, []string{"GET", "HEAD"}},
		{"body", "https://example.com/body", "upstream_body_failed", "body", 502, []string{"GET"}},
	} {
		for _, method := range test.methods {
			for _, prefix := range []string{"/raw/", "/repository/"} {
				t.Run(test.name+"/"+method+prefix, func(t *testing.T) {
					store := repository.NewMemoryStore()
					_, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{Name: "private-repo", Format: repository.FormatRaw, Type: repository.RepositoryTypeProxy, Endpoint: test.endpoint, AllowedHosts: []string{"example.com"}})
					if err != nil {
						t.Fatal(err)
					}
					baseClient := *upstream.Client()
					baseClient.Transport = upstream.Client().Transport.(*http.Transport).Clone()
					client := &baseClient
					if test.name == "egress" {
						client.Transport.(*http.Transport).DialTLSContext = func(context.Context, string, string) (net.Conn, error) {
							return nil, errors.New("private-error-marker")
						}
					}
					b := operationalog.NewBuffer(10)
					var stdout bytes.Buffer
					d := Dependencies{LogBuffer: b, Runtime: DiagnosticRuntime{InstanceID: "node", SessionID: "session"}, AccessLog: AccessLogOptions{Mode: "full", Logger: operationalog.NewLogger(io.MultiWriter(b, &stdout), "node", "session")}}
					h := NewGatewayHandler(d, store, TestAdapter{}, testAuthenticator(), UpstreamClient{HTTPClient: client})
					r := httptest.NewRequest(method, prefix+"private-repo/private-object?token=private-query", strings.NewReader("private-request-body"))
					r.Header.Set("X-Request-ID", "failure-request")
					r.Header.Set("Cookie", "private-cookie")
					authorize(r, "resolver-secret")
					w := httptest.NewRecorder()
					h.ServeHTTP(w, r)
					if w.Code != test.status {
						t.Fatalf("status=%d want=%d", w.Code, test.status)
					}
					var line map[string]any
					if err := json.Unmarshal(stdout.Bytes(), &line); err != nil {
						t.Fatal(err)
					}
					if line["errorCode"] != test.code || line["phase"] != test.phase {
						t.Errorf("stdout diagnostics=%v want=%s/%s", line, test.code, test.phase)
					}
					q := httptest.NewRequest("GET", "/api/v2/runtime/logs?requestId=failure-request", nil)
					authorize(q, "admin-secret")
					page := httptest.NewRecorder()
					h.ServeHTTP(page, q)
					var payload map[string]any
					if page.Code != 200 || json.Unmarshal(page.Body.Bytes(), &payload) != nil {
						t.Fatalf("query=%d %s", page.Code, page.Body.String())
					}
					if err := spec.Components.Schemas["RuntimeLogPage"].Value.VisitJSON(payload); err != nil {
						t.Fatal(err)
					}
					items := payload["items"].([]any)
					if len(items) != 1 {
						t.Fatalf("items=%v", items)
					}
					item := items[0].(map[string]any)
					if item["errorCode"] != test.code || item["phase"] != test.phase || item["route"] != prefix || item["requestClass"] != "raw" {
						t.Errorf("API diagnostics=%v", item)
					}
					for _, secret := range []string{"private-", "resolver-secret", "example.com"} {
						if strings.Contains(stdout.String()+page.Body.String(), secret) {
							t.Errorf("sensitive marker %q leaked", secret)
						}
					}
				})
			}
		}
	}
}

func TestRawFailureDiagnosticsUnknownBodyAndFinalResponse(t *testing.T) {
	for _, tc := range []struct {
		name, code, phase string
		status            int
		fetch             func() (*http.Response, error)
		releaseFails      bool
	}{
		{"unknown", "unknown", "unknown", 502, func() (*http.Response, error) { return nil, errors.New("upstream_transport_failed private-error-URL") }, false},
		{"close", "upstream_body_failed", "body", 502, func() (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: rawDiagnosticBody{Reader: strings.NewReader("artifact"), closeErr: errors.New("private-close-error")}}, nil
		}, false},
		{"miss", "", "", 404, func() (*http.Response, error) {
			return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("private-body"))}, nil
		}, false},
		{"gone", "", "", 404, func() (*http.Response, error) {
			return &http.Response{StatusCode: 410, Body: io.NopCloser(strings.NewReader("private-body"))}, nil
		}, false},
		{"success", "", "", 200, func() (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("artifact"))}, nil
		}, false},
		{"release overrides fetch", "", "", 503, func() (*http.Response, error) { return nil, errors.New("private-fetch-error") }, true},
		{"release overrides status", "", "", 503, func() (*http.Response, error) {
			return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader("private-body"))}, nil
		}, true},
		{"release overrides body", "", "", 503, func() (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: rawDiagnosticBody{Reader: strings.NewReader("artifact"), closeErr: errors.New("private-close-error")}}, nil
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := repository.NewMemoryStore()
			_, _ = store.CreateHostedRepository(context.Background(), repository.HostedRepository{Name: "private-repo", Format: repository.FormatRaw, Type: repository.RepositoryTypeProxy, Endpoint: "https://example.com", AllowedHosts: []string{"example.com"}})
			var stdout bytes.Buffer
			d := Dependencies{AccessLog: AccessLogOptions{Mode: "full", Logger: operationalog.NewLogger(&stdout, "node", "session")}}
			var cache *RawCache
			if tc.releaseFails {
				cache = NewDefaultRawCache(NewMemoryOCIObjectStore(), nil).WithCoordinator(&rawDiagnosticCoordinator{})
			}
			h := NewGatewayHandlerWithRawCache(d, store, TestAdapter{}, testAuthenticator(), nil, nil, cache, nil, rawDiagnosticClient{fetch: tc.fetch})
			r := httptest.NewRequest("GET", "/raw/private-repo/private-object", nil)
			authorize(r, "resolver-secret")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d want=%d", w.Code, tc.status)
			}
			var line map[string]any
			if err := json.Unmarshal(stdout.Bytes(), &line); err != nil {
				t.Fatal(err)
			}
			if tc.code == "" {
				if line["errorCode"] != nil || line["phase"] != nil {
					t.Fatalf("unrelated response carries upstream cause=%v", line)
				}
			} else if line["errorCode"] != tc.code || line["phase"] != tc.phase {
				t.Fatalf("diagnostics=%v want=%s/%s", line, tc.code, tc.phase)
			}
			if strings.Contains(stdout.String(), "private-") {
				t.Fatal("private error leaked")
			}
		})
	}
}

func TestRawFailureDiagnosticsGroupsAndAuthorization(t *testing.T) {
	for _, firstStatus := range []int{404, 410, 500} {
		for _, method := range []string{"GET", "HEAD"} {
			t.Run(method+http.StatusText(firstStatus), func(t *testing.T) {
				store := repository.NewMemoryStore()
				var members []repository.GroupMember
				for _, name := range []string{"private-first", "private-second"} {
					repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{Name: name, Format: repository.FormatRaw, Type: repository.RepositoryTypeProxy, Endpoint: "https://example.com", AllowedHosts: []string{"example.com"}})
					if err != nil {
						t.Fatal(err)
					}
					members = append(members, repository.GroupMember{RepositoryID: repo.ID, Position: len(members)})
				}
				createV2Group(t, store, "private-group", repository.FormatRaw, members...)
				calls := 0
				client := rawDiagnosticClient{fetch: func() (*http.Response, error) {
					calls++
					status := 200
					if calls == 1 {
						status = firstStatus
					}
					return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("artifact"))}, nil
				}}
				var output bytes.Buffer
				handler := NewGatewayHandler(Dependencies{AccessLog: AccessLogOptions{Mode: "full", Logger: operationalog.NewLogger(&output, "node", "session")}}, store, TestAdapter{}, testAuthenticator(), client)
				request := httptest.NewRequest(method, "/repository/private-group/private-object", nil)
				authorize(request, "resolver-secret")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				wantStatus, wantCalls := 200, 2
				if firstStatus == 500 {
					wantStatus, wantCalls = 502, 1
				}
				if response.Code != wantStatus || calls != wantCalls {
					t.Fatalf("status=%d calls=%d", response.Code, calls)
				}
				var line map[string]any
				if json.Unmarshal(output.Bytes(), &line) != nil {
					t.Fatal("invalid stdout")
				}
				if firstStatus == 500 {
					if line["errorCode"] != "upstream_status_rejected" || line["phase"] != "fetch" {
						t.Fatal(line)
					}
				} else if line["errorCode"] != nil || line["phase"] != nil {
					t.Fatalf("successful fallback had cause=%v", line)
				}
			})
		}
	}
	for _, tc := range []struct {
		name, token, path string
		status            int
		permissive        bool
	}{
		{"unauthenticated", "", "/raw/private-repo/private-object", 401, true},
		{"unauthorized", "resolver-secret", "/raw/private-repo/private-object", 401, false},
		{"unresolved", "resolver-secret", "/repository/private-missing/private-object", 404, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := repository.NewMemoryStore()
			_, _ = store.CreateHostedRepository(context.Background(), repository.HostedRepository{Name: "private-repo", Format: repository.FormatRaw, Type: repository.RepositoryTypeProxy, Endpoint: "https://private-user:private-credential@example.com"})
			var output bytes.Buffer
			calls := 0
			auth := testAuthenticator()
			auth.LegacyReadPermissive = tc.permissive
			if tc.name == "unauthorized" {
				repo, err := store.GetHostedRepositoryByName(context.Background(), "private-repo")
				if err != nil {
					t.Fatal(err)
				}
				// Native Raw has its own legacy authenticated fallback. An explicit
				// resource grant must decide before invalid endpoint preflight.
				if _, err := store.ReplaceRepositoryGrants(context.Background(), repo.ID, []repository.RepositoryGrant{{Principal: "build-agent", Scopes: []string{"repositories:read"}, ResourcePrefix: "permitted/"}}, repo.Version); err != nil {
					t.Fatal(err)
				}
			}
			h := NewGatewayHandler(Dependencies{AccessLog: AccessLogOptions{Mode: "full", Logger: operationalog.NewLogger(&output, "node", "session")}}, store, TestAdapter{}, auth, rawDiagnosticClient{fetch: func() (*http.Response, error) { calls++; return nil, errors.New("private-error") }})
			r := httptest.NewRequest("GET", tc.path, nil)
			if tc.token != "" {
				authorize(r, tc.token)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status || calls != 0 {
				t.Fatalf("authorization status=%d calls=%d", w.Code, calls)
			}
			var line map[string]any
			if json.Unmarshal(output.Bytes(), &line) != nil || line["errorCode"] != nil || line["phase"] != nil {
				t.Fatalf("denial diagnostics=%s", output.String())
			}
		})
	}
}

func TestRawFailureDiagnosticsIncludeLocalStagingLimits(t *testing.T) {
	for _, tc := range []string{"size limit", "temporary file"} {
		t.Run(tc, func(t *testing.T) {
			store := repository.NewMemoryStore()
			_, _ = store.CreateHostedRepository(context.Background(), repository.HostedRepository{Name: "private-repo", Format: repository.FormatRaw, Type: repository.RepositoryTypeProxy, Endpoint: "https://example.com", AllowedHosts: []string{"example.com"}})
			cache := NewDefaultRawCache(NewMemoryOCIObjectStore(), nil)
			if tc == "size limit" {
				cache.WithMaxObjectBytes(3)
			} else {
				t.Setenv("TMPDIR", t.TempDir()+"/private-missing-directory")
			}
			var output bytes.Buffer
			h := NewGatewayHandlerWithRawCache(Dependencies{AccessLog: AccessLogOptions{Mode: "full", Logger: operationalog.NewLogger(&output, "node", "session")}}, store, TestAdapter{}, testAuthenticator(), nil, nil, cache, nil, rawDiagnosticClient{fetch: func() (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("artifact"))}, nil
			}})
			r := httptest.NewRequest("GET", "/raw/private-repo/private-object", nil)
			authorize(r, "resolver-secret")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			var line map[string]any
			if w.Code != 502 || json.Unmarshal(output.Bytes(), &line) != nil || line["errorCode"] != "upstream_body_failed" || line["phase"] != "body" {
				t.Fatalf("staging=%d %s", w.Code, output.String())
			}
			if strings.Contains(output.String(), "private-") {
				t.Fatal("temporary user path leaked")
			}
		})
	}
}
