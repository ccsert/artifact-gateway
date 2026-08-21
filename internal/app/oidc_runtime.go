package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/authorization"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/secrets"
)

const (
	oidcSettingsCacheTTL    = 5 * time.Second
	oidcClientSecretPurpose = "oidc.client-secret"
)

var errOIDCNotEnabled = errors.New("OIDC authentication is not enabled")
var errInvalidOIDCSettings = errors.New("invalid OIDC settings")

type OIDCRuntimeConfig struct {
	Enabled       bool
	Issuer        string
	Audience      string
	JWKSURL       string
	ClientID      string
	ClientSecret  string
	RedirectURL   string
	Scopes        []string
	AdminSubjects []string
	Roles         authorization.OIDCRoleMapping
}

type OIDCSettingsView struct {
	Version                string    `json:"version"`
	Source                 string    `json:"source"`
	Enabled                bool      `json:"enabled"`
	Issuer                 string    `json:"issuer"`
	Audience               string    `json:"audience"`
	JWKSURL                string    `json:"jwksUrl,omitempty"`
	ClientID               string    `json:"clientId"`
	ClientSecretConfigured bool      `json:"clientSecretConfigured"`
	RedirectURL            string    `json:"redirectUrl"`
	Scopes                 []string  `json:"scopes"`
	AdminSubjects          []string  `json:"adminSubjects"`
	ReaderRoles            []string  `json:"readerRoles"`
	WriterRoles            []string  `json:"writerRoles"`
	AdminRoles             []string  `json:"adminRoles"`
	ProvisioningMode       string    `json:"provisioningMode"`
	EmailLinkingEnabled    bool      `json:"emailLinkingEnabled"`
	JITDefaultRole         string    `json:"jitDefaultRole"`
	UpdatedAt              time.Time `json:"updatedAt,omitempty"`
}

type OIDCSettingsUpdate struct {
	Enabled             bool
	Issuer              string
	Audience            string
	JWKSURL             string
	ClientID            string
	ClientSecret        *string
	ClearClientSecret   bool
	RedirectURL         string
	Scopes              []string
	AdminSubjects       []string
	ReaderRoles         []string
	WriterRoles         []string
	AdminRoles          []string
	ProvisioningMode    string
	EmailLinkingEnabled bool
	JITDefaultRole      string
}

type OIDCConnectionTest struct {
	Reachable             bool      `json:"reachable"`
	Issuer                string    `json:"issuer"`
	AuthorizationEndpoint string    `json:"authorizationEndpoint,omitempty"`
	TokenEndpoint         string    `json:"tokenEndpoint,omitempty"`
	JWKSURL               string    `json:"jwksUrl,omitempty"`
	LatencyMs             int64     `json:"latencyMs"`
	CheckedAt             time.Time `json:"checkedAt"`
}

type oidcRuntimeState struct {
	view            OIDCSettingsView
	clientSecret    string
	discoveryClient *authorization.OIDCClient
	browserClient   *authorization.OIDCClient
	mu              sync.Mutex
	apiValidator    *authorization.OIDCValidator
	loginValidator  *authorization.OIDCValidator
}

type OIDCRuntime struct {
	store     repository.OIDCSettingsStore
	bootstrap OIDCRuntimeConfig

	mu      sync.Mutex
	cached  *oidcRuntimeState
	expires time.Time
}

func NewOIDCRuntime(store repository.OIDCSettingsStore, bootstrap OIDCRuntimeConfig) *OIDCRuntime {
	return &OIDCRuntime{store: store, bootstrap: bootstrap}
}

func (o *OIDCRuntime) Settings(ctx context.Context) (OIDCSettingsView, error) {
	state, err := o.current(ctx)
	if err != nil {
		return OIDCSettingsView{}, err
	}
	return cloneOIDCSettingsView(state.view), nil
}

func (o *OIDCRuntime) Replace(ctx context.Context, update OIDCSettingsUpdate, expectedVersion string) (OIDCSettingsView, error) {
	if o.store == nil {
		return OIDCSettingsView{}, errors.New("OIDC settings storage is unavailable")
	}
	normalized, err := normalizeOIDCSettingsUpdate(update)
	if err != nil {
		return OIDCSettingsView{}, fmt.Errorf("%w: %w", errInvalidOIDCSettings, err)
	}
	current, err := o.current(ctx)
	if err != nil {
		return OIDCSettingsView{}, err
	}
	clientSecret := current.clientSecret
	if normalized.ClientSecret != nil && strings.TrimSpace(*normalized.ClientSecret) != "" {
		clientSecret = strings.TrimSpace(*normalized.ClientSecret)
	} else if normalized.ClearClientSecret {
		clientSecret = ""
	}
	encryptedSecret, err := secrets.Seal(oidcClientSecretPurpose, clientSecret)
	if err != nil {
		return OIDCSettingsView{}, err
	}
	stored, err := o.store.ReplaceOIDCSettings(ctx, repository.OIDCSettings{
		Enabled: normalized.Enabled, Issuer: normalized.Issuer, Audience: normalized.Audience,
		JWKSURL: normalized.JWKSURL, ClientID: normalized.ClientID, ClientSecret: encryptedSecret,
		RedirectURL: normalized.RedirectURL, Scopes: normalized.Scopes, AdminSubjects: normalized.AdminSubjects,
		ReaderRoles: normalized.ReaderRoles, WriterRoles: normalized.WriterRoles, AdminRoles: normalized.AdminRoles,
		ProvisioningMode: normalized.ProvisioningMode, EmailLinkingEnabled: normalized.EmailLinkingEnabled,
		JITDefaultRole: normalized.JITDefaultRole,
	}, expectedVersion)
	if err != nil {
		return OIDCSettingsView{}, err
	}
	o.invalidate()
	state, err := o.stateFromStored(stored, "database")
	if err != nil {
		return OIDCSettingsView{}, err
	}
	o.mu.Lock()
	o.cached = state
	o.expires = time.Now().UTC().Add(oidcSettingsCacheTTL)
	o.mu.Unlock()
	return cloneOIDCSettingsView(state.view), nil
}

func (o *OIDCRuntime) Browser(ctx context.Context) (*authorization.OIDCClient, *authorization.OIDCValidator, error) {
	state, err := o.current(ctx)
	if err != nil {
		return nil, nil, err
	}
	if !state.view.Enabled || state.browserClient == nil {
		return nil, nil, errOIDCNotEnabled
	}
	metadata, err := state.browserClient.Metadata(ctx)
	if err != nil {
		return nil, nil, err
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.loginValidator == nil {
		state.loginValidator = authorization.NewOIDCValidator(o.validatorConfig(state, state.view.ClientID, metadata.JWKSURI))
	}
	return state.browserClient, state.loginValidator, nil
}

func (o *OIDCRuntime) OIDCValidator(ctx context.Context) (*authorization.OIDCValidator, error) {
	state, err := o.current(ctx)
	if err != nil {
		return nil, err
	}
	if !state.view.Enabled || state.discoveryClient == nil || state.view.Audience == "" {
		return nil, nil
	}
	metadata, err := state.discoveryClient.Metadata(ctx)
	if err != nil {
		return nil, err
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.apiValidator == nil {
		state.apiValidator = authorization.NewOIDCValidator(o.validatorConfig(state, state.view.Audience, metadata.JWKSURI))
	}
	return state.apiValidator, nil
}

func (o *OIDCRuntime) Test(ctx context.Context) (OIDCConnectionTest, error) {
	state, err := o.current(ctx)
	if err != nil {
		return OIDCConnectionTest{}, err
	}
	if !state.view.Enabled || state.discoveryClient == nil {
		return OIDCConnectionTest{}, errOIDCNotEnabled
	}
	started := time.Now()
	metadata, err := state.discoveryClient.Metadata(ctx)
	result := OIDCConnectionTest{
		Issuer: state.view.Issuer, LatencyMs: time.Since(started).Milliseconds(), CheckedAt: time.Now().UTC(),
	}
	if err != nil {
		return result, err
	}
	result.Reachable = true
	result.AuthorizationEndpoint = metadata.AuthorizationEndpoint
	result.TokenEndpoint = metadata.TokenEndpoint
	result.JWKSURL = metadata.JWKSURI
	return result, nil
}

func (o *OIDCRuntime) current(ctx context.Context) (*oidcRuntimeState, error) {
	now := time.Now().UTC()
	o.mu.Lock()
	if o.cached != nil && now.Before(o.expires) {
		cached := o.cached
		o.mu.Unlock()
		return cached, nil
	}
	previous := o.cached
	o.mu.Unlock()

	if o.store == nil {
		state := o.stateFromBootstrap()
		o.mu.Lock()
		o.cached = state
		o.expires = now.Add(oidcSettingsCacheTTL)
		o.mu.Unlock()
		return state, nil
	}
	stored, err := o.store.GetOIDCSettings(ctx)
	source := "database"
	if errors.Is(err, repository.ErrNotFound) {
		state := o.stateFromBootstrap()
		o.mu.Lock()
		o.cached = state
		o.expires = now.Add(oidcSettingsCacheTTL)
		o.mu.Unlock()
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	if previous != nil && previous.view.Source == source && previous.view.Version == stored.Version {
		o.mu.Lock()
		o.expires = now.Add(oidcSettingsCacheTTL)
		o.mu.Unlock()
		return previous, nil
	}
	state, err := o.stateFromStored(stored, source)
	if err != nil {
		return nil, err
	}
	o.mu.Lock()
	o.cached = state
	o.expires = now.Add(oidcSettingsCacheTTL)
	o.mu.Unlock()
	return state, nil
}

func (o *OIDCRuntime) stateFromBootstrap() *oidcRuntimeState {
	config := o.bootstrap
	view := OIDCSettingsView{
		Version: "0", Source: "environment", Enabled: config.Enabled, Issuer: config.Issuer,
		Audience: config.Audience, JWKSURL: config.JWKSURL, ClientID: config.ClientID,
		ClientSecretConfigured: config.ClientSecret != "", RedirectURL: config.RedirectURL,
		Scopes: normalizeStrings(config.Scopes), AdminSubjects: normalizeStrings(config.AdminSubjects),
		ReaderRoles: normalizeStrings(config.Roles.Reader), WriterRoles: normalizeStrings(config.Roles.Writer),
		AdminRoles:       normalizeStrings(config.Roles.Admin),
		ProvisioningMode: "disabled", JITDefaultRole: "reader",
	}
	return buildOIDCRuntimeState(view, config.ClientSecret)
}

func (o *OIDCRuntime) stateFromStored(stored repository.OIDCSettings, source string) (*oidcRuntimeState, error) {
	clientSecret, err := secrets.Open(oidcClientSecretPurpose, stored.ClientSecret)
	if err != nil {
		return nil, err
	}
	view := OIDCSettingsView{
		Version: stored.Version, Source: source, Enabled: stored.Enabled, Issuer: stored.Issuer,
		Audience: stored.Audience, JWKSURL: stored.JWKSURL, ClientID: stored.ClientID,
		ClientSecretConfigured: stored.ClientSecret != "", RedirectURL: stored.RedirectURL,
		Scopes: stored.Scopes, AdminSubjects: stored.AdminSubjects, ReaderRoles: stored.ReaderRoles,
		WriterRoles: stored.WriterRoles, AdminRoles: stored.AdminRoles, UpdatedAt: stored.UpdatedAt,
		ProvisioningMode: stored.ProvisioningMode, EmailLinkingEnabled: stored.EmailLinkingEnabled,
		JITDefaultRole: stored.JITDefaultRole,
	}
	return buildOIDCRuntimeState(view, clientSecret), nil
}

func buildOIDCRuntimeState(view OIDCSettingsView, clientSecret string) *oidcRuntimeState {
	var discoveryClient, browserClient *authorization.OIDCClient
	if view.Enabled {
		discoveryClient = authorization.NewOIDCDiscoveryClient(view.Issuer)
		browserClient = authorization.NewOIDCClient(authorization.OIDCClientConfig{
			Issuer: view.Issuer, ClientID: view.ClientID, ClientSecret: clientSecret,
			RedirectURL: view.RedirectURL, Scopes: view.Scopes,
		})
		if browserClient != nil {
			discoveryClient = browserClient
		}
	}
	return &oidcRuntimeState{
		view: cloneOIDCSettingsView(view), clientSecret: clientSecret,
		discoveryClient: discoveryClient, browserClient: browserClient,
	}
}

func (o *OIDCRuntime) validatorConfig(state *oidcRuntimeState, audience, discoveredJWKS string) authorization.OIDCConfig {
	jwksURL := state.view.JWKSURL
	if jwksURL == "" {
		jwksURL = discoveredJWKS
	}
	return authorization.OIDCConfig{
		Issuer: state.view.Issuer, Audience: audience, JWKSURL: jwksURL,
		AdminSubjects: state.view.AdminSubjects,
		Roles: authorization.OIDCRoleMapping{
			Reader: state.view.ReaderRoles, Writer: state.view.WriterRoles, Admin: state.view.AdminRoles,
		},
	}
}

func (o *OIDCRuntime) invalidate() {
	o.mu.Lock()
	o.cached = nil
	o.expires = time.Time{}
	o.mu.Unlock()
}

func normalizeOIDCSettingsUpdate(update OIDCSettingsUpdate) (OIDCSettingsUpdate, error) {
	update.Issuer = strings.TrimRight(strings.TrimSpace(update.Issuer), "/")
	update.Audience = strings.TrimSpace(update.Audience)
	update.JWKSURL = strings.TrimSpace(update.JWKSURL)
	update.ClientID = strings.TrimSpace(update.ClientID)
	update.RedirectURL = strings.TrimSpace(update.RedirectURL)
	update.Scopes = normalizeStrings(append([]string{"openid"}, update.Scopes...))
	update.AdminSubjects = normalizeStrings(update.AdminSubjects)
	update.ReaderRoles = normalizeStrings(update.ReaderRoles)
	update.WriterRoles = normalizeStrings(update.WriterRoles)
	update.AdminRoles = normalizeStrings(update.AdminRoles)
	if update.ProvisioningMode == "" {
		update.ProvisioningMode = "disabled"
	}
	if update.JITDefaultRole == "" {
		update.JITDefaultRole = "reader"
	}
	if update.ProvisioningMode != "disabled" && update.ProvisioningMode != "jit" {
		return update, errors.New("provisioningMode must be disabled or jit")
	}
	if update.JITDefaultRole != "admin" && update.JITDefaultRole != "writer" && update.JITDefaultRole != "reader" {
		return update, errors.New("jitDefaultRole must be admin, writer, or reader")
	}
	if !update.Enabled {
		return update, nil
	}
	if update.Issuer == "" || update.Audience == "" {
		return update, errors.New("issuer and audience are required when OIDC is enabled")
	}
	if (update.ClientID == "") != (update.RedirectURL == "") {
		return update, errors.New("clientId and redirectUrl must be configured together")
	}
	if update.ClientSecret != nil && strings.TrimSpace(*update.ClientSecret) != "" && update.ClientID == "" {
		return update, errors.New("clientId is required when clientSecret is configured")
	}
	if !secureOIDCURL(update.Issuer) {
		return update, errors.New("issuer must use HTTPS outside localhost")
	}
	if update.JWKSURL != "" && !secureOIDCURL(update.JWKSURL) {
		return update, errors.New("jwksUrl must use HTTPS outside localhost")
	}
	if update.RedirectURL != "" && !secureOIDCURL(update.RedirectURL) {
		return update, errors.New("redirectUrl must use HTTPS outside localhost")
	}
	return update, nil
}

func secureOIDCURL(value string) bool {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Host == "" {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	if parsed.Scheme != "http" {
		return false
	}
	return slices.Contains([]string{"localhost", "127.0.0.1", "::1"}, parsed.Hostname())
}

func normalizeStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		normalized = append(normalized, value)
	}
	return normalized
}

func cloneOIDCSettingsView(view OIDCSettingsView) OIDCSettingsView {
	view.Scopes = append([]string{}, view.Scopes...)
	view.AdminSubjects = append([]string{}, view.AdminSubjects...)
	view.ReaderRoles = append([]string{}, view.ReaderRoles...)
	view.WriterRoles = append([]string{}, view.WriterRoles...)
	view.AdminRoles = append([]string{}, view.AdminRoles...)
	return view
}
