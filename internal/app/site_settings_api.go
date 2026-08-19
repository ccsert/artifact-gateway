package app

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/consoletheme"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

const maxSiteLogoDataBytes = 192 << 10

func (h generatedRepositoryAPIAdapter) GetSiteSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := h.siteSettings.GetSiteSettings(r.Context())
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get site settings failed")
		return
	}
	themes, err := h.availableConsoleThemes()
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "load Console themes failed")
		return
	}
	response, err := siteSettingsResponse(settings, themes)
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "serialize Console themes failed")
		return
	}
	w.Header().Set("ETag", settings.Version)
	writeNativeMavenJSON(w, http.StatusOK, response)
}

func (h generatedRepositoryAPIAdapter) ReplaceSiteSettings(w http.ResponseWriter, r *http.Request, params adminopenapi.ReplaceSiteSettingsParams) {
	principal, ok := h.authorize(w, r)
	if !ok {
		return
	}
	var request adminopenapi.SiteSettingsUpdate
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 384<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "site settings request is invalid")
		return
	}
	themes, err := h.availableConsoleThemes()
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "load Console themes failed")
		return
	}
	settings, err := normalizeSiteSettings(request.SiteName, request.LogoUrl, request.BrandMark, request.EnabledThemeIds, request.DefaultThemeId, themes)
	if err != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	settings, err = h.siteSettings.ReplaceSiteSettings(r.Context(), settings, string(params.IfMatch))
	if errors.Is(err, repository.ErrVersionConflict) {
		writeHostedProblem(w, http.StatusPreconditionFailed, "version_conflict", "If-Match does not match current site settings version")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "replace site settings failed")
		return
	}
	if h.audit != nil {
		_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{
			Actor: principal.Actor, Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(),
			Format: "management", Resource: "site-settings", Operation: "site-settings.replace",
			Status: http.StatusOK, CacheDisposition: "bypass",
		})
	}
	w.Header().Set("ETag", settings.Version)
	response, err := siteSettingsResponse(settings, themes)
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "serialize Console themes failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusOK, response)
}

func normalizeSiteSettings(siteName, logoURL, brandMark string, enabledThemeIDs []string, defaultThemeID string, themes []consoletheme.Theme) (repository.SiteSettings, error) {
	settings := repository.SiteSettings{
		SiteName: strings.TrimSpace(siteName), LogoURL: strings.TrimSpace(logoURL), BrandMark: strings.TrimSpace(brandMark),
		EnabledThemeIDs: append([]string(nil), enabledThemeIDs...), DefaultThemeID: strings.TrimSpace(defaultThemeID),
	}
	if !validSiteIdentityText(settings.SiteName, 80) {
		return repository.SiteSettings{}, errors.New("siteName must contain 1 to 80 visible characters")
	}
	if !validSiteIdentityText(settings.BrandMark, 8) {
		return repository.SiteSettings{}, errors.New("brandMark must contain 1 to 8 visible characters")
	}
	if err := validateSiteLogoURL(settings.LogoURL); err != nil {
		return repository.SiteSettings{}, err
	}
	if err := validateThemeSelection(settings.EnabledThemeIDs, settings.DefaultThemeID, themes); err != nil {
		return repository.SiteSettings{}, err
	}
	return settings, nil
}

func validateThemeSelection(enabledThemeIDs []string, defaultThemeID string, themes []consoletheme.Theme) error {
	if len(enabledThemeIDs) == 0 || len(enabledThemeIDs) > 32 {
		return errors.New("enabledThemeIds must contain 1 to 32 themes")
	}
	available := make(map[string]struct{}, len(themes))
	for _, theme := range themes {
		available[theme.ID] = struct{}{}
	}
	enabled := make(map[string]struct{}, len(enabledThemeIDs))
	for _, id := range enabledThemeIDs {
		if _, ok := available[id]; !ok {
			return fmt.Errorf("enabled theme %q is not available", id)
		}
		if _, duplicate := enabled[id]; duplicate {
			return fmt.Errorf("enabled theme %q is duplicated", id)
		}
		enabled[id] = struct{}{}
	}
	if _, ok := enabled[defaultThemeID]; !ok {
		return errors.New("defaultThemeId must identify an enabled theme")
	}
	return nil
}

func validSiteIdentityText(value string, maximum int) bool {
	if value == "" || utf8.RuneCountInString(value) > maximum {
		return false
	}
	return !strings.ContainsAny(value, "\r\n\x00")
}

func validateSiteLogoURL(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > 262144 {
		return errors.New("logoUrl is too large")
	}
	if strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") {
		return nil
	}
	if strings.HasPrefix(value, "data:") {
		return validateSiteLogoDataURL(value)
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return errors.New("logoUrl must be an HTTPS URL, a same-origin path, or a supported image data URL")
	}
	return nil
}

func validateSiteLogoDataURL(value string) error {
	comma := strings.IndexByte(value, ',')
	if comma < 0 {
		return errors.New("logoUrl image data is invalid")
	}
	mediaType := value[:comma]
	switch mediaType {
	case "data:image/png;base64", "data:image/jpeg;base64", "data:image/webp;base64":
	default:
		return errors.New("logoUrl image must be PNG, JPEG, or WebP")
	}
	decoded, err := base64.StdEncoding.DecodeString(value[comma+1:])
	if err != nil || len(decoded) == 0 || len(decoded) > maxSiteLogoDataBytes {
		return errors.New("logoUrl image data is invalid or exceeds 192 KiB")
	}
	return nil
}

func (h generatedRepositoryAPIAdapter) availableConsoleThemes() ([]consoletheme.Theme, error) {
	if h.consoleThemes == nil {
		return consoletheme.NewRegistry("").List()
	}
	return h.consoleThemes.List()
}

func siteSettingsResponse(settings repository.SiteSettings, themes []consoletheme.Theme) (adminopenapi.SiteSettings, error) {
	available := make([]adminopenapi.ConsoleTheme, 0, len(themes))
	for _, theme := range themes {
		data, err := json.Marshal(theme)
		if err != nil {
			return adminopenapi.SiteSettings{}, err
		}
		var item adminopenapi.ConsoleTheme
		if err := json.Unmarshal(data, &item); err != nil {
			return adminopenapi.SiteSettings{}, err
		}
		available = append(available, item)
	}
	return adminopenapi.SiteSettings{
		Version: settings.Version, SiteName: settings.SiteName, LogoUrl: settings.LogoURL,
		BrandMark: settings.BrandMark, EnabledThemeIds: append([]string(nil), settings.EnabledThemeIDs...),
		DefaultThemeId: settings.DefaultThemeID, AvailableThemes: available, UpdatedAt: settings.UpdatedAt,
	}, nil
}
