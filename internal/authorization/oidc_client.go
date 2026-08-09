package authorization

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const oidcDiscoveryCacheTTL = 15 * time.Minute

type OIDCClientConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scopes       []string
}

type OIDCProviderMetadata struct {
	AuthorizationEndpoint string
	TokenEndpoint         string
	EndSessionEndpoint    string
}

type OIDCTokenResponse struct {
	AccessToken  string
	IDToken      string
	RefreshToken string
	TokenType    string
	ExpiresIn    int64
}

// OIDCClient owns the browser-facing Authorization Code flow. Provider
// metadata is discovered at runtime so the Console never needs provider URLs
// baked into its frontend bundle.
type OIDCClient struct {
	config OIDCClientConfig
	client *http.Client

	mu       sync.Mutex
	metadata OIDCProviderMetadata
	expires  time.Time
}

func NewOIDCClient(config OIDCClientConfig) *OIDCClient {
	config.Issuer = strings.TrimRight(strings.TrimSpace(config.Issuer), "/")
	config.ClientID = strings.TrimSpace(config.ClientID)
	config.RedirectURL = strings.TrimSpace(config.RedirectURL)
	config.Scopes = normalizedOIDCScopes(config.Scopes)
	if config.Issuer == "" || config.ClientID == "" || config.RedirectURL == "" {
		return nil
	}
	return &OIDCClient{config: config, client: &http.Client{Timeout: 10 * time.Second}}
}

func (c *OIDCClient) Issuer() string { return c.config.Issuer }

func (c *OIDCClient) ClientID() string { return c.config.ClientID }

func (c *OIDCClient) RedirectURL() string { return c.config.RedirectURL }

func (c *OIDCClient) Scopes() []string { return append([]string(nil), c.config.Scopes...) }

func (c *OIDCClient) Metadata(ctx context.Context) (OIDCProviderMetadata, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().UTC().Before(c.expires) && c.metadata.AuthorizationEndpoint != "" && c.metadata.TokenEndpoint != "" {
		return c.metadata, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.config.Issuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		return OIDCProviderMetadata{}, err
	}
	response, err := c.client.Do(request)
	if err != nil {
		return OIDCProviderMetadata{}, fmt.Errorf("discover OIDC provider: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return OIDCProviderMetadata{}, fmt.Errorf("discover OIDC provider: HTTP %d", response.StatusCode)
	}
	var document struct {
		Issuer                string `json:"issuer"`
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
		EndSessionEndpoint    string `json:"end_session_endpoint"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&document); err != nil {
		return OIDCProviderMetadata{}, fmt.Errorf("decode OIDC discovery: %w", err)
	}
	if strings.TrimRight(document.Issuer, "/") != c.config.Issuer || document.AuthorizationEndpoint == "" || document.TokenEndpoint == "" {
		return OIDCProviderMetadata{}, errors.New("OIDC discovery metadata is incomplete or has an unexpected issuer")
	}
	if err := validateOIDCProviderEndpoint(c.config.Issuer, document.AuthorizationEndpoint); err != nil {
		return OIDCProviderMetadata{}, fmt.Errorf("invalid OIDC authorization endpoint: %w", err)
	}
	if err := validateOIDCProviderEndpoint(c.config.Issuer, document.TokenEndpoint); err != nil {
		return OIDCProviderMetadata{}, fmt.Errorf("invalid OIDC token endpoint: %w", err)
	}
	if document.EndSessionEndpoint != "" {
		if err := validateOIDCProviderEndpoint(c.config.Issuer, document.EndSessionEndpoint); err != nil {
			return OIDCProviderMetadata{}, fmt.Errorf("invalid OIDC end-session endpoint: %w", err)
		}
	}
	c.metadata = OIDCProviderMetadata{
		AuthorizationEndpoint: document.AuthorizationEndpoint,
		TokenEndpoint:         document.TokenEndpoint,
		EndSessionEndpoint:    document.EndSessionEndpoint,
	}
	c.expires = time.Now().UTC().Add(oidcDiscoveryCacheTTL)
	return c.metadata, nil
}

func validateOIDCProviderEndpoint(issuer, endpoint string) error {
	issuerURL, issuerErr := url.Parse(issuer)
	endpointURL, endpointErr := url.Parse(endpoint)
	if issuerErr != nil || endpointErr != nil || endpointURL.Host == "" {
		return errors.New("endpoint must be an absolute URL")
	}
	if issuerURL.Scheme == "https" && endpointURL.Scheme != "https" {
		return errors.New("endpoint must use HTTPS")
	}
	if endpointURL.Scheme != "https" && endpointURL.Scheme != "http" {
		return errors.New("endpoint must use HTTP or HTTPS")
	}
	return nil
}

func (c *OIDCClient) AuthorizationURL(ctx context.Context, state, nonce, codeChallenge string) (string, error) {
	metadata, err := c.Metadata(ctx)
	if err != nil {
		return "", err
	}
	values := url.Values{
		"client_id":             {c.config.ClientID},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
		"nonce":                 {nonce},
		"redirect_uri":          {c.config.RedirectURL},
		"response_type":         {"code"},
		"scope":                 {strings.Join(c.config.Scopes, " ")},
		"state":                 {state},
	}
	separator := "?"
	if strings.Contains(metadata.AuthorizationEndpoint, "?") {
		separator = "&"
	}
	return metadata.AuthorizationEndpoint + separator + values.Encode(), nil
}

func (c *OIDCClient) Exchange(ctx context.Context, code, verifier string) (OIDCTokenResponse, error) {
	metadata, err := c.Metadata(ctx)
	if err != nil {
		return OIDCTokenResponse{}, err
	}
	values := url.Values{
		"client_id":     {c.config.ClientID},
		"code":          {code},
		"code_verifier": {verifier},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {c.config.RedirectURL},
	}
	if c.config.ClientSecret != "" {
		values.Set("client_secret", c.config.ClientSecret)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, metadata.TokenEndpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return OIDCTokenResponse{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.client.Do(request)
	if err != nil {
		return OIDCTokenResponse{}, fmt.Errorf("exchange OIDC code: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return OIDCTokenResponse{}, fmt.Errorf("exchange OIDC code: HTTP %d", response.StatusCode)
	}
	var payload struct {
		AccessToken  string `json:"access_token"`
		IDToken      string `json:"id_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return OIDCTokenResponse{}, fmt.Errorf("decode OIDC token response: %w", err)
	}
	if payload.IDToken == "" {
		return OIDCTokenResponse{}, errors.New("OIDC token response is missing id_token")
	}
	return OIDCTokenResponse{
		AccessToken:  payload.AccessToken,
		IDToken:      payload.IDToken,
		RefreshToken: payload.RefreshToken,
		TokenType:    payload.TokenType,
		ExpiresIn:    payload.ExpiresIn,
	}, nil
}

func normalizedOIDCScopes(values []string) []string {
	seen := map[string]bool{"openid": true}
	scopes := []string{"openid"}
	for _, value := range values {
		for _, scope := range strings.Fields(value) {
			if scope == "" || seen[scope] {
				continue
			}
			seen[scope] = true
			scopes = append(scopes, scope)
		}
	}
	return scopes
}
