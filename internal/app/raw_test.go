package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"go.opentelemetry.io/otel/trace"
)

type rawFixtureClient struct {
	mu        sync.Mutex
	calls     []string
	responses map[string]int
	body      []byte
}

func (c *rawFixtureClient) Fetch(_ context.Context, _ string, _ repository.Member, _, _, _ string, _ http.Header) (*http.Response, error) {
	return nil, errors.New("OCI fetch is not used by Raw tests")
}

func (c *rawFixtureClient) FetchRaw(_ context.Context, _ string, member repository.Member, _ string, _ http.Header) (*http.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, member.Name)
	status := c.responses[member.Name]
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"text/plain"}}, Body: io.NopCloser(bytes.NewReader(c.body))}, nil
}

func (c *rawFixtureClient) Calls() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.calls...)
}

func rawRequest(method, path string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set("Authorization", "Bearer resolver-secret")
	return r
}

func TestRawHostedFirstCacheAndRange(t *testing.T) {
	store := repository.NewMemoryStore()
	_, err := store.CreateRawGroup(context.Background(), repository.Group{Name: "downloads", Members: []repository.Member{
		{Name: "hosted", Type: repository.MemberHosted, Endpoint: "http://legacy.local", Position: 0},
		{Name: "proxy", Type: repository.MemberProxy, Endpoint: "https://proxy.example", Position: 1, AllowedHosts: []string{"proxy.example"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	client := &rawFixtureClient{responses: map[string]int{"hosted": http.StatusNotFound, "proxy": http.StatusOK}, body: []byte("artifact")}
	h := RawHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Metrics: &Metrics{}, Cache: NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, []string{"proxy.example"})}
	for range 2 {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, rawRequest(http.MethodGet, "/raw/downloads/release/app.txt"))
		if w.Code != http.StatusOK || w.Body.String() != "artifact" || w.Header().Get("ETag") == "" || w.Header().Get("Digest") == "" {
			t.Fatalf("response=%d headers=%v body=%q", w.Code, w.Header(), w.Body.String())
		}
	}
	if got := client.Calls(); len(got) != 2 || got[0] != "hosted" || got[1] != "proxy" {
		t.Fatalf("calls=%v", got)
	}
	r := rawRequest(http.MethodGet, "/raw/downloads/release/app.txt")
	r.Header.Set("Range", "bytes=1-3")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusPartialContent || w.Body.String() != "rti" {
		t.Fatalf("range=%d %q", w.Code, w.Body.String())
	}
}

func TestRawRejectsMultipartRangeOverHTTP(t *testing.T) {
	store := repository.NewMemoryStore()
	_, err := store.CreateRawGroup(context.Background(), repository.Group{Name: "downloads", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "http://legacy.local"}}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(RawHandler{Store: store, Authenticator: testAuthenticator(), Client: &rawFixtureClient{responses: map[string]int{"hosted": http.StatusOK}, body: []byte("artifact")}, Metrics: &Metrics{}, Cache: NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, nil)})
	defer server.Close()
	for _, values := range [][]string{{"bytes=0-1,3-4"}, {"bytes=0-1", "bytes=3-4"}} {
		request, err := http.NewRequest(http.MethodGet, server.URL+"/raw/downloads/release/app.txt", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer resolver-secret")
		for _, value := range values {
			request.Header.Add("Range", value)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusRequestedRangeNotSatisfiable {
			t.Fatalf("multipart ranges %v status = %d", values, response.StatusCode)
		}
	}
}

func TestRawHeadConditionalAndChecksumSidecars(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateRawGroup(context.Background(), repository.Group{Name: "downloads", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "http://legacy.local"}}})
	client := &rawFixtureClient{responses: map[string]int{"hosted": http.StatusOK}, body: []byte("artifact")}
	h := RawHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Metrics: &Metrics{}, Cache: NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, nil)}

	head := rawRequest(http.MethodHead, "/raw/downloads/release/app.txt")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, head)
	if w.Code != http.StatusOK || w.Body.Len() != 0 || w.Header().Get("Content-Length") != "8" || w.Header().Get("ETag") == "" {
		t.Fatalf("HEAD = %d headers=%v body=%q", w.Code, w.Header(), w.Body.String())
	}

	conditional := rawRequest(http.MethodGet, "/raw/downloads/release/app.txt")
	conditional.Header.Set("If-None-Match", w.Header().Get("ETag"))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, conditional)
	if w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Fatalf("conditional = %d body=%q", w.Code, w.Body.String())
	}

	client.body = []byte("not-a-checksum")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, rawRequest(http.MethodGet, "/raw/downloads/release/app.sha256"))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("invalid sidecar = %d", w.Code)
	}

	sum := sha256.Sum256([]byte("artifact"))
	client.body = []byte(hex.EncodeToString(sum[:]) + "\n")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, rawRequest(http.MethodGet, "/raw/downloads/release/app.sha256"))
	if w.Code != http.StatusOK {
		t.Fatalf("valid sidecar = %d", w.Code)
	}
}

func TestRawWritesV2AuditFieldsForRequestOutcomes(t *testing.T) {
	store := repository.NewMemoryStore()
	for _, group := range []repository.Group{
		{Name: "hosted", Members: []repository.Member{{Name: "legacy", Type: repository.MemberHosted, Endpoint: "https://legacy.example:8443"}}},
		{Name: "negative", Members: []repository.Member{{Name: "proxy", Type: repository.MemberProxy, Endpoint: "https://proxy.example", AllowedHosts: []string{"proxy.example"}}}},
		{Name: "fallback", Members: []repository.Member{{Name: "hosted-miss", Type: repository.MemberHosted, Endpoint: "https://hosted.example", Position: 0}, {Name: "proxy-ok", Type: repository.MemberProxy, Endpoint: "https://proxy-ok.example", Position: 1, AllowedHosts: []string{"proxy-ok.example"}}}},
		{Name: "blocked", Members: []repository.Member{{Name: "blocked-proxy", Type: repository.MemberProxy, Endpoint: "https://user:secret@blocked.example"}}},
		{Name: "outage", Members: []repository.Member{{Name: "offline", Type: repository.MemberHosted, Endpoint: "https://offline.example"}}},
	} {
		if _, err := store.CreateRawGroup(context.Background(), group); err != nil {
			t.Fatal(err)
		}
	}
	client := &rawFixtureClient{responses: map[string]int{"legacy": http.StatusOK, "proxy": http.StatusNotFound, "hosted-miss": http.StatusNotFound, "proxy-ok": http.StatusOK, "offline": http.StatusServiceUnavailable}, body: []byte("artifact")}
	h := RawHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Cache: NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, nil)}
	request := func(method, path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, rawRequest(method, path))
		return w
	}
	last := func() repository.AuditRecord { return store.Audits[len(store.Audits)-1] }

	resolvedResponse := request(http.MethodGet, "/raw/hosted/release/app.txt")
	resolved := last()
	if resolved.Format != "raw" || resolved.Resource != "release/app.txt" || resolved.Representation != "body" || resolved.MemberName != "legacy" || resolved.MemberType != "hosted" || resolved.UpstreamHost != "legacy.example" || resolved.Operation != "get" || resolved.Status != http.StatusOK || resolved.CacheDisposition != "miss" || resolved.Bytes != 8 {
		t.Fatalf("resolved audit=%#v", resolved)
	}

	request(http.MethodHead, "/raw/hosted/release/app.txt")
	head := last()
	if head.Operation != "head" || head.Status != http.StatusOK || head.CacheDisposition != "hit" || head.Bytes != 0 {
		t.Fatalf("HEAD audit=%#v", head)
	}
	rangeRequest := rawRequest(http.MethodGet, "/raw/hosted/release/app.txt")
	rangeRequest.Header.Set("Range", "bytes=0-1")
	h.ServeHTTP(httptest.NewRecorder(), rangeRequest)
	rangeAudit := last()
	if rangeAudit.Status != http.StatusPartialContent || rangeAudit.CacheDisposition != "hit" || rangeAudit.Bytes != 2 {
		t.Fatalf("range audit=%#v", rangeAudit)
	}
	conditional := rawRequest(http.MethodGet, "/raw/hosted/release/app.txt")
	conditional.Header.Set("If-None-Match", resolvedResponse.Header().Get("ETag"))
	h.ServeHTTP(httptest.NewRecorder(), conditional)
	if conditionalAudit := last(); conditionalAudit.Status != http.StatusNotModified || conditionalAudit.Bytes != 0 || conditionalAudit.CacheDisposition != "hit" {
		t.Fatalf("conditional audit=%#v", conditionalAudit)
	}

	request(http.MethodGet, "/raw/negative/missing")
	request(http.MethodGet, "/raw/negative/missing")
	negative := last()
	if negative.Outcome != repository.AuditNotFound || negative.MemberName != "proxy" || negative.MemberType != "proxy" || negative.UpstreamHost != "proxy.example" || negative.Status != http.StatusNotFound || negative.CacheDisposition != "hit" || negative.Bytes != 0 {
		t.Fatalf("negative-cache audit=%#v", negative)
	}

	request(http.MethodGet, "/raw/fallback/artifact")
	fallback := last()
	if fallback.Outcome != repository.AuditResolved || fallback.MemberName != "proxy-ok" || fallback.MemberType != "proxy" || fallback.UpstreamHost != "proxy-ok.example" || fallback.Status != http.StatusOK || fallback.CacheDisposition != "miss" || fallback.Bytes != 8 {
		t.Fatalf("group fallback audit=%#v", fallback)
	}

	request(http.MethodGet, "/raw/blocked/artifact")
	blocked := last()
	if blocked.Outcome != repository.AuditProxyDenied || blocked.MemberName != "blocked-proxy" || blocked.UpstreamHost != "blocked.example" || blocked.Status != http.StatusForbidden || blocked.CacheDisposition != "bypass" {
		t.Fatalf("proxy-denied audit=%#v", blocked)
	}

	request(http.MethodGet, "/raw/outage/artifact")
	outage := last()
	if outage.Outcome != repository.AuditUpstreamError || outage.MemberName != "offline" || outage.Status != http.StatusBadGateway || outage.CacheDisposition != "bypass" {
		t.Fatalf("upstream-error audit=%#v", outage)
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/raw/hosted/release/app.txt", nil))
	denied := last()
	if denied.Actor != "anonymous" || denied.Outcome != repository.AuditAccessDenied || denied.Status != http.StatusUnauthorized || denied.Format != "raw" || denied.Resource != "release/app.txt" || denied.Operation != "get" || denied.CacheDisposition != "bypass" {
		t.Fatalf("authentication-denied audit=%#v", denied)
	}

	methodDenied := httptest.NewRecorder()
	h.ServeHTTP(methodDenied, rawRequest(http.MethodPost, "/raw/hosted/release/app.txt"))
	if methodDenied.Code != http.StatusMethodNotAllowed || last().Actor != "build-agent" || last().Status != http.StatusMethodNotAllowed || last().Operation != "post" {
		t.Fatalf("method-denied response=%d audit=%#v", methodDenied.Code, last())
	}

	invalidRange := rawRequest(http.MethodGet, "/raw/hosted/release/app.txt")
	invalidRange.Header.Set("Range", "bytes=99-100")
	invalidRangeResponse := httptest.NewRecorder()
	h.ServeHTTP(invalidRangeResponse, invalidRange)
	if invalidRangeResponse.Code != http.StatusRequestedRangeNotSatisfiable || last().Status != http.StatusRequestedRangeNotSatisfiable || last().CacheDisposition != "hit" || last().Bytes != 0 {
		t.Fatalf("invalid-range response=%d audit=%#v", invalidRangeResponse.Code, last())
	}
}

func TestRawAuditsCorrelationAndExportsMetrics(t *testing.T) {
	store := repository.NewMemoryStore()
	for _, group := range []repository.Group{
		{Name: "hosted", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "https://hosted.example"}}},
		{Name: "negative", Members: []repository.Member{{Name: "negative", Type: repository.MemberHosted, Endpoint: "https://negative.example"}}},
		{Name: "blocked", Members: []repository.Member{{Name: "blocked", Type: repository.MemberProxy, Endpoint: "https://blocked.example"}}},
		{Name: "outage", Members: []repository.Member{{Name: "outage", Type: repository.MemberHosted, Endpoint: "https://outage.example"}}},
	} {
		if _, err := store.CreateRawGroup(context.Background(), group); err != nil {
			t.Fatal(err)
		}
	}
	client := &rawFixtureClient{responses: map[string]int{
		"hosted": http.StatusOK, "negative": http.StatusNotFound, "outage": http.StatusBadGateway,
	}, body: []byte("artifact")}
	metrics := &Metrics{}
	handler := RawHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Metrics: metrics, Cache: NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, nil)}

	traceID := trace.TraceID{1}
	spanID := trace.SpanID{1}
	request := rawRequest(http.MethodGet, "/raw/hosted/release/app.txt?access_token=do-not-record")
	request.Header.Set("X-Request-ID", "request-42")
	request = request.WithContext(trace.ContextWithSpanContext(request.Context(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled})))
	handler.ServeHTTP(httptest.NewRecorder(), request)
	handler.ServeHTTP(httptest.NewRecorder(), rawRequest(http.MethodGet, "/raw/hosted/release/app.txt"))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/raw/hosted/release/app.txt", nil))
	handler.ServeHTTP(httptest.NewRecorder(), rawRequest(http.MethodPost, "/raw/hosted/release/app.txt"))
	handler.ServeHTTP(httptest.NewRecorder(), rawRequest(http.MethodGet, "/raw/negative/missing"))
	handler.ServeHTTP(httptest.NewRecorder(), rawRequest(http.MethodGet, "/raw/negative/missing"))
	handler.ServeHTTP(httptest.NewRecorder(), rawRequest(http.MethodGet, "/raw/blocked/artifact"))
	handler.ServeHTTP(httptest.NewRecorder(), rawRequest(http.MethodGet, "/raw/outage/artifact"))
	handler.ServeHTTP(httptest.NewRecorder(), rawRequest(http.MethodGet, "/raw/hosted/release/app.sha256"))
	handler.ServeHTTP(httptest.NewRecorder(), rawRequest(http.MethodGet, "/raw/hosted/%2e%2e/secret?token=do-not-record"))

	if len(store.Audits) == 0 {
		t.Fatal("no Raw audit records")
	}
	for _, audit := range store.Audits {
		if audit.RequestID == "" || audit.TraceID == "" {
			t.Fatalf("missing correlation fields: %#v", audit)
		}
		if strings.Contains(audit.Resource, "token") || strings.Contains(audit.Resource, "secret") {
			t.Fatalf("audit resource exposes request secret: %#v", audit)
		}
	}
	first := store.Audits[0]
	if first.RequestID != "request-42" || first.TraceID != traceID.String() {
		t.Fatalf("propagated correlation = %#v", first)
	}
	last := store.Audits[len(store.Audits)-1]
	if last.Status != http.StatusBadRequest || last.Resource != "" {
		t.Fatalf("malformed request audit = %#v", last)
	}
	if metrics.rawGetRequests.Load() != 9 || metrics.rawOtherRequests.Load() != 1 || metrics.rawAuthorizationDenied.Load() != 2 || metrics.rawCacheHit.Load() != 1 || metrics.rawCacheMiss.Load() != 5 || metrics.rawNegativeHit.Load() != 1 || metrics.rawProxyDenied.Load() != 1 || metrics.rawChecksumFailure.Load() != 1 || metrics.rawUpstreamFailure.Load() != 1 || metrics.rawResponseBytes.Load() != 16 {
		t.Fatalf("Raw metrics = %#v", metrics)
	}
	response := httptest.NewRecorder()
	metrics.Handler(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, want := range []string{
		"artifact_gateway_raw_requests_total{method=\"get\"} 9",
		"artifact_gateway_raw_authorization_denials_total 2",
		"artifact_gateway_raw_cache_requests_total{outcome=\"hit\"} 1",
		"artifact_gateway_raw_cache_requests_total{outcome=\"miss\"} 5",
		"artifact_gateway_raw_negative_cache_hits_total 1",
		"artifact_gateway_raw_proxy_denied_total 1",
		"artifact_gateway_raw_checksum_failures_total 1",
		"artifact_gateway_raw_upstream_failures_total 1",
		"artifact_gateway_raw_response_bytes_total 16",
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("metrics missing %q:\n%s", want, response.Body.String())
		}
	}
}

func TestGatewayRoutesRawRequests(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateRawGroup(context.Background(), repository.Group{Name: "downloads", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "http://legacy.local"}}})
	client := &rawFixtureClient{responses: map[string]int{"hosted": http.StatusOK}, body: []byte("artifact")}
	handler := NewGatewayHandlerWithRawCache(Dependencies{}, store, TestAdapter{}, testAuthenticator(), NewDefaultOCICache(NewMemoryOCIObjectStore(), nil), nil, NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, nil), nil, client)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, rawRequest(http.MethodGet, "/raw/downloads/release/app.txt"))
	if w.Code != http.StatusOK || w.Body.String() != "artifact" {
		t.Fatalf("route = %d %q", w.Code, w.Body.String())
	}
}

func TestLegacyRawClientDecodesCanonicalPath(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/a b" {
			t.Fatalf("path=%q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	client := UpstreamClient{}
	response, err := client.FetchRaw(context.Background(), http.MethodGet, repository.Member{Type: repository.MemberHosted, Endpoint: upstream.URL}, "a%20b", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
}

func TestRawStandardHTTPClientE2E(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateRawGroup(context.Background(), repository.Group{Name: "downloads", Members: []repository.Member{
		{Name: "hosted", Type: repository.MemberHosted, Endpoint: "http://legacy.local", Position: 0},
		{Name: "proxy", Type: repository.MemberProxy, Endpoint: "https://proxy.example", Position: 1, AllowedHosts: []string{"proxy.example"}},
	}})
	_, _ = store.CreateRawGroup(context.Background(), repository.Group{Name: "outage", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "http://legacy.local"}}})
	client := &rawFixtureClient{responses: map[string]int{"hosted": http.StatusNotFound, "proxy": http.StatusOK}, body: []byte("artifact")}
	handler := RawHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Metrics: &Metrics{}, Cache: NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, []string{"proxy.example"})}
	server := httptest.NewServer(handler)
	defer server.Close()

	do := func(method, path string, headers http.Header) *http.Response {
		t.Helper()
		r, err := http.NewRequest(method, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		for name, values := range headers {
			r.Header[name] = values
		}
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	authorized := http.Header{"Authorization": []string{"Bearer resolver-secret"}}

	response := do(http.MethodGet, "/raw/downloads/release/app.txt", authorized)
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "artifact" || response.Header.Get("Content-Type") != "text/plain" || response.Header.Get("Content-Length") != "8" {
		t.Fatalf("GET status=%d headers=%v body=%q", response.StatusCode, response.Header, body)
	}
	if got := client.Calls(); len(got) != 2 || got[0] != "hosted" || got[1] != "proxy" {
		t.Fatalf("resolution calls=%v", got)
	}

	response = do(http.MethodHead, "/raw/downloads/release/app.txt", authorized)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Length") != "8" {
		t.Fatalf("HEAD status=%d headers=%v", response.StatusCode, response.Header)
	}
	rangeHeaders := authorized.Clone()
	rangeHeaders.Set("Range", "bytes=1-3")
	response = do(http.MethodGet, "/raw/downloads/release/app.txt", rangeHeaders)
	body, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusPartialContent || string(body) != "rti" {
		t.Fatalf("range status=%d body=%q", response.StatusCode, body)
	}
	if got := client.Calls(); len(got) != 2 {
		t.Fatalf("cache did not isolate upstream: calls=%v", got)
	}

	response = do(http.MethodGet, "/raw/downloads/release/app.txt", http.Header{})
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d", response.StatusCode)
	}

	deniedHandler := RawHandler{Store: store, Authenticator: Authenticator{ResolverToken: "resolver-secret", RepositoryReaders: map[string][]string{"reader": {"other"}}}, Client: client, Metrics: &Metrics{}, Cache: handler.Cache}
	denied := httptest.NewServer(deniedHandler)
	defer denied.Close()
	r, _ := http.NewRequest(http.MethodGet, denied.URL+"/raw/downloads/release/app.txt", nil)
	r.Header.Set("Authorization", "Bearer resolver-secret")
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("permission status=%d", response.StatusCode)
	}

	client.responses["hosted"] = http.StatusBadGateway
	response = do(http.MethodGet, "/raw/outage/release/app.txt", authorized)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("upstream status=%d", response.StatusCode)
	}
}

func TestRawLegacyGroupStandardHTTPClientE2E(t *testing.T) {
	var upstreamCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		if r.URL.Path != "/release/app.txt" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("hosted-artifact"))
	}))
	defer upstream.Close()
	store := repository.NewMemoryStore()
	_, _ = store.CreateRawGroup(context.Background(), repository.Group{Name: "hosted", Members: []repository.Member{{Name: "legacy", Type: repository.MemberHosted, Endpoint: upstream.URL}}})
	handler := RawHandler{Store: store, Authenticator: testAuthenticator(), Client: UpstreamClient{}, Metrics: &Metrics{}, Cache: NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, nil)}
	gateway := httptest.NewServer(handler)
	defer gateway.Close()

	request, err := http.NewRequest(http.MethodGet, gateway.URL+"/raw/hosted/release/app.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer resolver-secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "hosted-artifact" || response.Header.Get("Content-Length") != "15" {
		t.Fatalf("hosted response=%d headers=%v body=%q", response.StatusCode, response.Header, body)
	}

	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || upstreamCalls != 1 {
		t.Fatalf("hosted cache response=%d upstream calls=%d", response.StatusCode, upstreamCalls)
	}
}

func TestRawFetchesCanonicalRepresentationAndBoundsObjectSize(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateRawGroup(context.Background(), repository.Group{Name: "downloads", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "http://legacy.local"}}})
	cache := NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, nil).WithMaxObjectBytes(8)
	client := &rawFixtureClient{responses: map[string]int{"hosted": http.StatusOK}, body: []byte("artifact")}
	h := RawHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Metrics: &Metrics{}, Cache: cache}
	r := rawRequest(http.MethodGet, "/raw/downloads/release/app.txt")
	r.Header.Set("Range", "bytes=0-1")
	r.Header.Set("Accept", "application/not-cached")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusPartialContent || w.Body.String() != "ar" {
		t.Fatalf("range response=%d body=%q", w.Code, w.Body.String())
	}

	client.body = []byte("different")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, rawRequest(http.MethodGet, "/raw/downloads/release/app.txt"))
	if w.Code != http.StatusOK || w.Body.String() != "artifact" || len(client.Calls()) != 1 {
		t.Fatalf("cached response=%d body=%q calls=%v", w.Code, w.Body.String(), client.Calls())
	}

	_, _ = store.CreateRawGroup(context.Background(), repository.Group{Name: "oversize", Members: []repository.Member{{Name: "large", Type: repository.MemberHosted, Endpoint: "http://legacy.local"}}})
	client.responses["large"] = http.StatusOK
	client.body = []byte("too-large")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, rawRequest(http.MethodGet, "/raw/oversize/release/app.txt"))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("oversize response=%d", w.Code)
	}
}
