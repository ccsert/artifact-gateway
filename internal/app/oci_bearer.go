package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

const ociBearerMaxChallenge = 8 * 1024
const ociBearerMaxBody = 64 * 1024
const ociBearerMaxToken = 8 * 1024

var errOCIBearerConfiguration = errors.New("OCI upstream bearer configuration invalid")
var errOCIBearerChallenge = errors.New("OCI upstream bearer challenge rejected")
var errOCIBearerToken = errors.New("OCI upstream bearer token rejected")
var errOCIBearerExchange = errors.New("OCI upstream bearer exchange failed")
var errOCIBearerRetry = errors.New("OCI upstream bearer authorization failed")

var ociBearerName = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*$`)

func (c UpstreamClient) fetchOCIWithBearer(ctx context.Context, member repository.Member, repositoryName string, request *http.Request) (*http.Response, error) {
	if member.OCIBearer == nil {
		return c.doProxyUpstream(ctx, member, request)
	}
	endpoint, err := url.Parse(member.Endpoint)
	if member.OCIBearer.Validate() != nil || err != nil || !proxyURLValid(endpoint, false) || request == nil || !proxyURLValid(request.URL, false) || proxyOrigin(request.URL) != proxyOrigin(endpoint) || len(repositoryName) > 255 || !ociBearerName.MatchString(repositoryName) {
		return nil, errOCIBearerConfiguration
	}
	ctx, cancel := context.WithTimeout(ctx, ociSharedWorkTimeout)
	keepBody := false
	defer func() {
		if !keepBody {
			cancel()
		}
	}()
	content := request.Clone(ctx)
	content.Header.Del("Authorization")
	content.Header.Del("Cookie")
	content.Header.Del("Proxy-Authorization")
	scope := "repository:" + repositoryName + ":pull"
	key, err := c.ociBearerCacheKey(member, scope)
	if err != nil {
		return nil, errOCIBearerConfiguration
	}
	var cached ociBearerToken
	if c.OCIBearerCache != nil {
		cached = c.OCIBearerCache.lookup(key)
		if cached.value != "" {
			content.Header.Set("Authorization", "Bearer "+cached.value)
		}
	}
	response, err := c.doProxyUpstream(ctx, member, content)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusUnauthorized {
		if cached.value != "" {
			c.OCIBearerCache.invalidate(key, cached.version)
		}
		validOrigin := response.Request != nil && proxyOrigin(response.Request.URL) == proxyOrigin(endpoint)
		challengeErr := validateOCIBearerChallenge(response.Header.Values("WWW-Authenticate"), member.OCIBearer, scope)
		_ = response.Body.Close()
		if !validOrigin || challengeErr != nil {
			return nil, errOCIBearerChallenge
		}
		var token ociBearerToken
		var err error
		if c.OCIBearerCache == nil {
			token, err = c.exchangeOCIBearer(ctx, member, scope)
		} else {
			token, err = c.OCIBearerCache.get(ctx, key, func(work context.Context) (ociBearerToken, error) { return c.exchangeOCIBearer(work, member, scope) })
		}
		if err != nil {
			return nil, err
		}
		content.Header.Set("Authorization", "Bearer "+token.value)
		response, err = c.doProxyUpstream(ctx, member, content)
		if err != nil {
			return nil, err
		}
		if response.StatusCode == http.StatusUnauthorized {
			_ = response.Body.Close()
			if c.OCIBearerCache != nil {
				c.OCIBearerCache.invalidate(key, token.version)
			}
			return nil, errOCIBearerRetry
		}
	}
	response.Body = &proxyResponseBody{ReadCloser: response.Body, cancel: cancel, release: func() {}}
	keepBody = true
	return response, nil
}

// Only one unambiguous Bearer challenge is accepted. The small scanner handles
// quoted commas and escapes without accepting another challenge or duplicates.
func validateOCIBearerChallenge(values []string, binding *repository.OCIBearer, scope string) error {
	if len(values) != 1 || len(values[0]) > ociBearerMaxChallenge {
		return errOCIBearerChallenge
	}
	input := strings.TrimSpace(values[0])
	schemeEnd := strings.IndexAny(input, " \t")
	if schemeEnd < 0 || !strings.EqualFold(input[:schemeEnd], "Bearer") {
		return errOCIBearerChallenge
	}
	input = strings.TrimSpace(input[schemeEnd:])
	params := make(map[string]string, 3)
	for len(input) != 0 {
		keyEnd := strings.IndexByte(input, '=')
		if keyEnd < 1 || len(params) >= 3 {
			return errOCIBearerChallenge
		}
		key := strings.ToLower(strings.TrimSpace(input[:keyEnd]))
		if key != "realm" && key != "service" && key != "scope" {
			return errOCIBearerChallenge
		}
		if _, exists := params[key]; exists {
			return errOCIBearerChallenge
		}
		input = strings.TrimSpace(input[keyEnd+1:])
		if len(input) == 0 || input[0] != '"' {
			return errOCIBearerChallenge
		}
		input = input[1:]
		var value strings.Builder
		closed := false
		for len(input) != 0 {
			char := input[0]
			input = input[1:]
			if char == '"' {
				closed = true
				break
			}
			if char == '\\' {
				if len(input) == 0 || (input[0] != '\\' && input[0] != '"') {
					return errOCIBearerChallenge
				}
				char, input = input[0], input[1:]
			}
			if char < 0x20 || char > 0x7e {
				return errOCIBearerChallenge
			}
			value.WriteByte(char)
		}
		if !closed {
			return errOCIBearerChallenge
		}
		params[key] = value.String()
		input = strings.TrimSpace(input)
		if len(input) == 0 {
			break
		}
		if input[0] != ',' {
			return errOCIBearerChallenge
		}
		input = strings.TrimSpace(input[1:])
		if len(input) == 0 {
			return errOCIBearerChallenge
		}
	}
	if params["realm"] != binding.Realm || params["service"] != binding.Service {
		return errOCIBearerChallenge
	}
	if supplied, exists := params["scope"]; exists && supplied != scope {
		return errOCIBearerChallenge
	}
	return nil
}

func (c UpstreamClient) exchangeOCIBearer(ctx context.Context, member repository.Member, scope string) (ociBearerToken, error) {
	realm, err := url.Parse(member.OCIBearer.Realm)
	if err != nil || !proxyURLValid(realm, false) {
		return ociBearerToken{}, errOCIBearerConfiguration
	}
	query := realm.Query()
	query.Set("service", member.OCIBearer.Service)
	query.Set("scope", scope)
	realm.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, realm.String(), nil)
	if err != nil {
		return ociBearerToken{}, errOCIBearerConfiguration
	}
	request.Header.Set("Accept", "application/json")
	response, err := c.doProxyUpstream(ctx, member, request)
	if err != nil {
		return ociBearerToken{}, safeOCIBearerError(ctx, errOCIBearerExchange, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return ociBearerToken{}, errOCIBearerExchange
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, ociBearerMaxBody+1))
	if err != nil {
		return ociBearerToken{}, safeOCIBearerError(ctx, errOCIBearerExchange, err)
	}
	if len(body) > ociBearerMaxBody {
		return ociBearerToken{}, errOCIBearerToken
	}
	var result struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
		ExpiresIn   *int64 `json:"expires_in"`
		IssuedAt    string `json:"issued_at"`
	}
	if json.Unmarshal(body, &result) != nil || (result.Token != "" && result.AccessToken != "" && result.Token != result.AccessToken) {
		return ociBearerToken{}, errOCIBearerToken
	}
	token := result.Token
	if token == "" {
		token = result.AccessToken
	}
	if token == "" || len(token) > ociBearerMaxToken || strings.IndexFunc(token, func(r rune) bool { return r <= 0x20 || r >= 0x7f }) >= 0 {
		return ociBearerToken{}, errOCIBearerToken
	}
	ttl := int64(60)
	if result.ExpiresIn != nil {
		ttl = *result.ExpiresIn
	}
	if ttl <= 0 {
		return ociBearerToken{}, errOCIBearerToken
	}
	now := time.Now()
	issued := now
	if result.IssuedAt != "" {
		var err error
		issued, err = time.Parse(time.RFC3339, result.IssuedAt)
		if err != nil || issued.After(now.Add(30*time.Second)) {
			return ociBearerToken{}, errOCIBearerToken
		}
	}
	// Compare elapsed seconds before converting the bounded remainder to a
	// duration, so large issuer lifetimes cannot overflow time.Duration.
	elapsed := now.Sub(issued).Seconds()
	remaining := float64(ttl) - elapsed
	if remaining <= 0 {
		return ociBearerToken{}, errOCIBearerToken
	}
	if remaining > 300 {
		remaining = 300
	}
	expires := now.Add(time.Duration(remaining * float64(time.Second)))
	return ociBearerToken{value: token, expires: expires}, nil
}

func safeOCIBearerError(ctx context.Context, kind, err error) error {
	if ctx.Err() != nil {
		return errors.Join(kind, ctx.Err())
	}
	if errors.Is(err, context.Canceled) {
		return errors.Join(kind, context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.Join(kind, context.DeadlineExceeded)
	}
	return kind
}
