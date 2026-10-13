package agctl

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	clientMaxRetries    = 3
	clientRetryBaseWait = 200 * time.Millisecond
	clientRetryMaxWait  = 5 * time.Second
	clientMaxBody       = 16 << 20
	clientMaxErrorText  = 200
)

// sensitiveQuery is the redaction pattern for verbose URLs and error
// messages. Credentials must never appear in either.
var sensitiveQuery = regexp.MustCompile(`(?i)(token|secret|password|credential|key)`)

type apiClient struct {
	baseURL    *url.URL
	token      string
	httpClient *http.Client
	userAgent  string
	timeout    time.Duration
	verbose    io.Writer
	maxRetries int
	sleep      func(context.Context, time.Duration) error
}

type clientOptions struct {
	timeout   time.Duration
	verbose   io.Writer
	userAgent string
}

type requestOptions struct {
	query      url.Values
	idempotent bool
}

type apiResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

func newAPIClient(settings profileSettings, options clientOptions) (*apiClient, error) {
	server := strings.TrimSpace(settings.Server)
	if server == "" {
		return nil, &UsageError{
			Message: "no server configured",
			Hint:    "pass --server, set AGCTL_SERVER, or add a profile to ~/.config/agctl/config.yaml",
		}
	}
	parsed, err := url.Parse(server)
	if err != nil || !parsed.IsAbs() || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, &UsageError{
			Message: fmt.Sprintf("server URL %q is not a valid http(s) URL", server),
			Hint:    "use the Gateway root URL, for example https://gateway.example.com",
		}
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if settings.Insecure {
		// --insecure is an explicit, documented opt-in (docs/cli.md).
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	timeout := options.timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &apiClient{
		baseURL:    parsed,
		token:      settings.Token,
		httpClient: &http.Client{Transport: transport},
		userAgent:  options.userAgent,
		timeout:    timeout,
		verbose:    options.verbose,
		maxRetries: clientMaxRetries,
		sleep: func(ctx context.Context, wait time.Duration) error {
			timer := time.NewTimer(wait)
			defer timer.Stop()
			select {
			case <-timer.C:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}, nil
}

func (c *apiClient) resolveURL(path string, query url.Values) *url.URL {
	target := *c.baseURL
	target.Path = strings.TrimSuffix(target.Path, "/") + path
	if len(query) > 0 {
		target.RawQuery = query.Encode()
	}
	return &target
}

// do sends one request, retrying only idempotent operations: transport
// failures, 429, and 502/503/504 responses. Non-2xx responses are returned
// to the caller, which converts them into an APIError.
func (c *apiClient) do(ctx context.Context, method, path string, options requestOptions) (*apiResponse, error) {
	requestURL := c.resolveURL(path, options.query)
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	maxAttempts := 1
	if options.idempotent {
		maxAttempts += c.maxRetries
	}
	var lastResponse *apiResponse
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			wait := c.retryWait(lastResponse, attempt)
			if c.verbose != nil {
				_, _ = fmt.Fprintf(c.verbose, "  waiting %s before retry\n", wait.Round(time.Millisecond))
			}
			if err := c.sleep(ctx, wait); err != nil {
				return nil, c.contextError(ctx, requestURL)
			}
		}
		request, err := http.NewRequestWithContext(ctx, method, requestURL.String(), nil)
		if err != nil {
			return nil, &UsageError{Message: fmt.Sprintf("cannot build request: %v", err)}
		}
		request.Header.Set("Accept", "application/json")
		if c.userAgent != "" {
			request.Header.Set("User-Agent", c.userAgent)
		}
		if c.token != "" {
			request.Header.Set("Authorization", "Bearer "+c.token)
		}
		if c.verbose != nil {
			_, _ = fmt.Fprintf(c.verbose, "> %s %s\n", method, redactURL(requestURL))
		}
		response, err := c.httpClient.Do(request)
		lastErr = err
		lastResponse = nil
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, clientMaxBody))
			_ = response.Body.Close()
			if readErr != nil {
				lastErr = readErr
			} else {
				if c.verbose != nil {
					_, _ = fmt.Fprintf(c.verbose, "< %d\n", response.StatusCode)
				}
				result := &apiResponse{StatusCode: response.StatusCode, Header: response.Header, Body: body}
				if response.StatusCode < 300 {
					return result, nil
				}
				if !retryableStatus(response.StatusCode) || attempt == maxAttempts-1 {
					return result, nil
				}
				lastResponse = result
				continue
			}
		}
		if ctx.Err() != nil {
			return nil, c.contextError(ctx, requestURL)
		}
		if attempt == maxAttempts-1 {
			return nil, c.transportError(lastErr, requestURL)
		}
	}
	return nil, c.transportError(lastErr, requestURL)
}

func retryableStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

func (c *apiClient) retryWait(lastResponse *apiResponse, attempt int) time.Duration {
	if lastResponse != nil {
		if wait, ok := parseRetryAfter(lastResponse.Header.Get("Retry-After"), time.Now()); ok {
			if wait > clientRetryMaxWait {
				return clientRetryMaxWait
			}
			return wait
		}
	}
	wait := clientRetryBaseWait << (attempt - 1)
	if wait > clientRetryMaxWait {
		return clientRetryMaxWait
	}
	return wait
}

func (c *apiClient) contextError(ctx context.Context, requestURL *url.URL) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &ConnectionError{
			Message: fmt.Sprintf("request to %s timed out after %s", requestURL.Host, c.timeout),
			Hint:    "raise --timeout or check connectivity to the server",
		}
	}
	return &ConnectionError{
		Message: fmt.Sprintf("request to %s was canceled", requestURL.Host),
		Hint:    "interrupted before the server answered",
	}
}

func (c *apiClient) transportError(err error, requestURL *url.URL) error {
	detail := "connection failed"
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) && urlErr.Err != nil {
			detail = urlErr.Err.Error()
		} else {
			detail = err.Error()
		}
	}
	return &ConnectionError{
		Message: fmt.Sprintf("cannot reach %s: %s", requestURL.Host, detail),
		Hint:    "check --server and network connectivity to the Gateway",
	}
}

// getJSON performs an idempotent GET and decodes a 2xx JSON body. Non-2xx
// responses become structured APIError values.
func (c *apiClient) getJSON(ctx context.Context, path string, query url.Values, target any) error {
	response, err := c.do(ctx, http.MethodGet, path, requestOptions{query: query, idempotent: true})
	if err != nil {
		return err
	}
	if response.StatusCode >= 300 {
		return parseAPIError(response)
	}
	if target == nil || len(response.Body) == 0 {
		return nil
	}
	if err := json.Unmarshal(response.Body, target); err != nil {
		return &APIError{
			Code:       "invalid_response",
			HTTPStatus: response.StatusCode,
			Message:    fmt.Sprintf("server returned an unreadable response: %v", err),
			RequestID:  response.Header.Get("X-Request-ID"),
		}
	}
	return nil
}

// parseAPIError converts a non-2xx response into an APIError. The management
// API answers with a problem document carrying code, message, and requestId;
// responses without one fall back to a truncated text body.
func parseAPIError(response *apiResponse) *APIError {
	var problem struct {
		Title     string `json:"title"`
		Status    int    `json:"status"`
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"requestId"`
	}
	api := &APIError{HTTPStatus: response.StatusCode, Hint: hintForStatus(response.StatusCode)}
	if err := json.Unmarshal(response.Body, &problem); err == nil && (problem.Code != "" || problem.Message != "" || problem.Title != "") {
		api.Code = problem.Code
		api.Message = problem.Message
		if api.Message == "" {
			api.Message = problem.Title
		}
		api.RequestID = problem.RequestID
	} else {
		api.Message = strings.TrimSpace(string(response.Body))
		if len(api.Message) > clientMaxErrorText {
			api.Message = api.Message[:clientMaxErrorText]
		}
	}
	if api.Code == "" {
		api.Code = "api_error"
	}
	if api.RequestID == "" {
		api.RequestID = response.Header.Get("X-Request-ID")
	}
	if api.Message == "" {
		api.Message = fmt.Sprintf("server returned HTTP %d", response.StatusCode)
	}
	return api
}

// parseRetryAfter understands the delay-seconds and HTTP-date forms.
func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			return 0, true
		}
		return time.Duration(seconds) * time.Second, true
	}
	if moment, err := http.ParseTime(value); err == nil {
		wait := moment.Sub(now)
		if wait < 0 {
			wait = 0
		}
		return wait, true
	}
	return 0, false
}

// redactURL hides credential-bearing URL parts before they reach any writer.
func redactURL(target *url.URL) string {
	sanitized := *target
	if sanitized.User != nil {
		if _, hasPassword := sanitized.User.Password(); hasPassword {
			sanitized.User = url.UserPassword(sanitized.User.Username(), "REDACTED")
		}
	}
	query := sanitized.Query()
	for key := range query {
		if sensitiveQuery.MatchString(key) {
			query.Set(key, "REDACTED")
		}
	}
	sanitized.RawQuery = query.Encode()
	return sanitized.String()
}
