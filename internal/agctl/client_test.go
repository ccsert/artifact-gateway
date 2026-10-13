package agctl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestClient(t *testing.T, settings profileSettings, options clientOptions) *apiClient {
	t.Helper()
	if options.timeout == 0 {
		options.timeout = 5 * time.Second
	}
	if options.userAgent == "" {
		options.userAgent = "agctl/test"
	}
	client, err := newAPIClient(settings, options)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func recordSleeps(client *apiClient) *[]time.Duration {
	waits := &[]time.Duration{}
	client.sleep = func(_ context.Context, wait time.Duration) error {
		*waits = append(*waits, wait)
		return nil
	}
	return waits
}

func countingHandler(responses []int, body, retryAfter string) (*httptest.Server, *int) {
	var mu sync.Mutex
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		index := attempts
		attempts++
		mu.Unlock()
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		status := responses[len(responses)-1]
		if index < len(responses) {
			status = responses[index]
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	return server, &attempts
}

func TestClientRetriesIdempotentRequestsWithBackoff(t *testing.T) {
	server, attempts := countingHandler([]int{503, 503, 502, 200}, `{"ok":true}`, "")
	defer server.Close()
	client := newTestClient(t, profileSettings{Server: server.URL}, clientOptions{})
	waits := recordSleeps(client)

	response, err := client.do(context.Background(), http.MethodGet, "/probe", requestOptions{idempotent: true})
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	if response.StatusCode != 200 || *attempts != 4 {
		t.Fatalf("status = %d, attempts = %d", response.StatusCode, *attempts)
	}
	want := []time.Duration{200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond}
	if len(*waits) != len(want) {
		t.Fatalf("waits = %v", *waits)
	}
	for i, wait := range *waits {
		if wait != want[i] {
			t.Fatalf("wait[%d] = %s, want %s", i, wait, want[i])
		}
	}
}

func TestClientHonorsRetryAfterAndCapsIt(t *testing.T) {
	server, attempts := countingHandler([]int{429, 429, 200}, `{}`, "7")
	defer server.Close()
	client := newTestClient(t, profileSettings{Server: server.URL}, clientOptions{})
	waits := recordSleeps(client)

	if _, err := client.do(context.Background(), http.MethodGet, "/probe", requestOptions{idempotent: true}); err != nil {
		t.Fatalf("do: %v", err)
	}
	if *attempts != 3 {
		t.Fatalf("attempts = %d", *attempts)
	}
	for i, wait := range *waits {
		if wait != clientRetryMaxWait {
			t.Fatalf("wait[%d] = %s, want the %s cap", i, wait, clientRetryMaxWait)
		}
	}
}

func TestClientStopsRetryingAtTheLimit(t *testing.T) {
	server, attempts := countingHandler([]int{503}, `unavailable`, "")
	defer server.Close()
	client := newTestClient(t, profileSettings{Server: server.URL}, clientOptions{})
	recordSleeps(client)

	response, err := client.do(context.Background(), http.MethodGet, "/probe", requestOptions{idempotent: true})
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	if response.StatusCode != 503 || *attempts != 1+clientMaxRetries {
		t.Fatalf("status = %d, attempts = %d", response.StatusCode, *attempts)
	}
}

func TestClientDoesNotRetryNonIdempotentRequests(t *testing.T) {
	server, attempts := countingHandler([]int{503}, `unavailable`, "")
	defer server.Close()
	client := newTestClient(t, profileSettings{Server: server.URL}, clientOptions{})

	response, err := client.do(context.Background(), http.MethodGet, "/probe", requestOptions{})
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	if response.StatusCode != 503 || *attempts != 1 {
		t.Fatalf("status = %d, attempts = %d", response.StatusCode, *attempts)
	}
}

func TestClientDoesNotRetryClientErrors(t *testing.T) {
	server, attempts := countingHandler([]int{400}, `{"code":"invalid_request","message":"bad input"}`, "")
	defer server.Close()
	client := newTestClient(t, profileSettings{Server: server.URL}, clientOptions{})

	err := client.getJSON(context.Background(), "/probe", nil, nil)
	var api *APIError
	if !errors.As(err, &api) || api.HTTPStatus != 400 || api.Code != "invalid_request" || api.Message != "bad input" {
		t.Fatalf("err = %#v", err)
	}
	if *attempts != 1 {
		t.Fatalf("attempts = %d", *attempts)
	}
}

func TestClientReportsTimeoutAsConnectionError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client := newTestClient(t, profileSettings{Server: server.URL}, clientOptions{timeout: 50 * time.Millisecond})

	_, err := client.do(context.Background(), http.MethodGet, "/probe", requestOptions{idempotent: true})
	var connection *ConnectionError
	if !errors.As(err, &connection) || !strings.Contains(connection.Message, "timed out") {
		t.Fatalf("err = %v", err)
	}
	if exitCodeFor(err) != ExitConnection {
		t.Fatalf("exit code = %d", exitCodeFor(err))
	}
}

func TestClientReportsCanceledRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client := newTestClient(t, profileSettings{Server: server.URL}, clientOptions{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.do(ctx, http.MethodGet, "/probe", requestOptions{idempotent: true})
	var connection *ConnectionError
	if !errors.As(err, &connection) || !strings.Contains(connection.Message, "canceled") {
		t.Fatalf("err = %v", err)
	}
}

func TestClientReportsUnreachableServer(t *testing.T) {
	client := newTestClient(t, profileSettings{Server: "http://127.0.0.1:1"}, clientOptions{timeout: time.Second})
	_, err := client.do(context.Background(), http.MethodGet, "/probe", requestOptions{idempotent: true})
	var connection *ConnectionError
	if !errors.As(err, &connection) || !strings.Contains(connection.Message, "127.0.0.1:1") {
		t.Fatalf("err = %v", err)
	}
	if exitCodeFor(err) != ExitConnection {
		t.Fatalf("exit code = %d", exitCodeFor(err))
	}
}

func TestClientSendsUserAgentAndAuthorization(t *testing.T) {
	var userAgent, authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userAgent = r.Header.Get("User-Agent")
		authorization = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := newTestClient(t, profileSettings{Server: server.URL, Token: "secret-token"}, clientOptions{})
	if _, err := client.do(context.Background(), http.MethodGet, "/probe", requestOptions{idempotent: true}); err != nil {
		t.Fatal(err)
	}
	if userAgent != "agctl/test" || authorization != "Bearer secret-token" {
		t.Fatalf("user agent = %q, authorization = %q", userAgent, authorization)
	}

	anonymous := newTestClient(t, profileSettings{Server: server.URL}, clientOptions{})
	if _, err := anonymous.do(context.Background(), http.MethodGet, "/probe", requestOptions{idempotent: true}); err != nil {
		t.Fatal(err)
	}
	if authorization != "" {
		t.Fatalf("anonymous request sent authorization %q", authorization)
	}
}

func TestClientVerboseOutputRedactsCredentials(t *testing.T) {
	server, _ := countingHandler([]int{200}, `{}`, "")
	defer server.Close()
	var verbose strings.Builder
	client := newTestClient(t, profileSettings{Server: server.URL, Token: "secret-token"}, clientOptions{verbose: &verbose})

	if _, err := client.do(context.Background(), http.MethodGet, "/probe", requestOptions{
		query:      url.Values{"token": {"query-secret"}, "limit": {"10"}},
		idempotent: true,
	}); err != nil {
		t.Fatal(err)
	}
	output := verbose.String()
	if strings.Contains(output, "secret-token") || strings.Contains(output, "query-secret") {
		t.Fatalf("verbose output leaked a credential: %s", output)
	}
	if !strings.Contains(output, "REDACTED") || !strings.Contains(output, "limit=10") {
		t.Fatalf("verbose output = %s", output)
	}
}

func TestParseAPIErrorVariants(t *testing.T) {
	problem := &apiResponse{
		StatusCode: 403,
		Header:     http.Header{},
		Body:       []byte(`{"type":"about:blank","title":"Forbidden","status":403,"code":"access_denied","message":"not allowed","requestId":"req-1"}`),
	}
	api := parseAPIError(problem)
	if api.Code != "access_denied" || api.Message != "not allowed" || api.RequestID != "req-1" || api.HTTPStatus != 403 || api.Hint == "" {
		t.Fatalf("problem = %#v", api)
	}

	withHeader := &apiResponse{
		StatusCode: 500,
		Header:     http.Header{"X-Request-Id": {"req-header"}},
		Body:       []byte(`{"code":"internal_error","message":"boom"}`),
	}
	if api := parseAPIError(withHeader); api.RequestID != "req-header" {
		t.Fatalf("header fallback = %#v", api)
	}

	plain := &apiResponse{StatusCode: 502, Header: http.Header{}, Body: []byte(strings.Repeat("x", 500))}
	api = parseAPIError(plain)
	if api.Code != "api_error" || len(api.Message) != clientMaxErrorText {
		t.Fatalf("plain body = %#v", api)
	}

	empty := &apiResponse{StatusCode: 503, Header: http.Header{}, Body: nil}
	api = parseAPIError(empty)
	if !strings.Contains(api.Message, "503") {
		t.Fatalf("empty body = %#v", api)
	}
}

func TestGetJSONToleratesUnknownFieldsAndValues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"actor":"svc","administrator":false,"kind":"future-kind","extra":true,"nested":{"x":1}}`)
	}))
	defer server.Close()
	client := newTestClient(t, profileSettings{Server: server.URL}, clientOptions{})

	var identity struct {
		Actor         string `json:"actor"`
		Administrator bool   `json:"administrator"`
		Kind          string `json:"kind"`
	}
	if err := client.getJSON(context.Background(), "/api/v2/identity", nil, &identity); err != nil {
		t.Fatalf("getJSON: %v", err)
	}
	if identity.Actor != "svc" || identity.Kind != "future-kind" {
		t.Fatalf("identity = %#v", identity)
	}
}

func TestGetJSONReportsUnreadableBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "not json")
	}))
	defer server.Close()
	client := newTestClient(t, profileSettings{Server: server.URL}, clientOptions{})

	var target map[string]any
	err := client.getJSON(context.Background(), "/probe", nil, &target)
	var api *APIError
	if !errors.As(err, &api) || api.Code != "invalid_response" {
		t.Fatalf("err = %#v", err)
	}
}

func TestResolveURLJoinsBasePath(t *testing.T) {
	client := newTestClient(t, profileSettings{Server: "https://gateway.example.com/gw/"}, clientOptions{})
	target := client.resolveURL("/api/v2/identity", url.Values{"a": {"1"}})
	if target.String() != "https://gateway.example.com/gw/api/v2/identity?a=1" {
		t.Fatalf("url = %s", target)
	}
	plain := client.resolveURL("/livez", nil)
	if plain.String() != "https://gateway.example.com/gw/livez" {
		t.Fatalf("url = %s", plain)
	}
}

func TestNewAPIClientRejectsInvalidServers(t *testing.T) {
	if _, err := newAPIClient(profileSettings{}, clientOptions{}); err == nil {
		t.Fatal("empty server accepted")
	}
	if _, err := newAPIClient(profileSettings{Server: "gateway.example.com"}, clientOptions{}); err == nil {
		t.Fatal("scheme-less server accepted")
	}
	if _, err := newAPIClient(profileSettings{Server: "ftp://gateway.example.com"}, clientOptions{}); err == nil {
		t.Fatal("non-http server accepted")
	}
}

func TestRedactURLMasksUserinfoAndSensitiveQuery(t *testing.T) {
	target, err := url.Parse("https://user:pw@gateway.example.com/x?apiKey=abc&limit=2&Secret=z")
	if err != nil {
		t.Fatal(err)
	}
	redacted := redactURL(target)
	if strings.Contains(redacted, "pw") {
		t.Fatalf("redacted url leaked password: %s", redacted)
	}
	if strings.Contains(redacted, "abc") || strings.Contains(redacted, "=z") {
		t.Fatalf("redacted url leaked query secret: %s", redacted)
	}
	if !strings.Contains(redacted, "limit=2") {
		t.Fatalf("redacted url dropped harmless query: %s", redacted)
	}
}

func TestFlagType(t *testing.T) {
	seen := map[string]string{}
	for _, cmd := range registry() {
		for _, info := range commandFlagInfos(cmd, 10*time.Second) {
			seen[info.Name] = info.Type
		}
	}
	for name, want := range map[string]string{"json": "bool", "yes": "bool", "timeout": "duration", "token": "string", "server": "string", "o": "string"} {
		if seen[name] != want {
			t.Fatalf("flag %s type = %q, want %q", name, seen[name], want)
		}
	}
}

func TestCommandInfosExposeContractMetadata(t *testing.T) {
	encoded, err := json.Marshal(commandInfos(30 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, field := range []string{`"resource"`, `"scenarios"`, `"stage"`, `"endpoints"`, `"mutating"`, `"minimumRole"`, `"dryRun"`, `"retry"`, `"wait"`, `"pagination"`} {
		if !strings.Contains(text, field) {
			t.Fatalf("command tree missing %s: %s", field, text)
		}
	}
	if !strings.Contains(text, `"endpoints":["GET /livez"`) {
		t.Fatalf("info endpoints missing: %s", text)
	}
	if !strings.Contains(text, `"stage":"m0"`) {
		t.Fatalf("stage missing: %s", text)
	}
}
