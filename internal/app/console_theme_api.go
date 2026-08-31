package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/consoletheme"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type consoleThemeCatalogItem struct {
	Theme   consoletheme.Theme
	Source  adminopenapi.ConsoleThemeSource
	Version string
}

func (h generatedRepositoryAPIAdapter) availableConsoleThemes(ctx context.Context) ([]consoleThemeCatalogItem, error) {
	registry := h.consoleThemes
	if registry == nil {
		registry = consoletheme.NewRegistry("")
	}
	baseThemes, err := registry.List()
	if err != nil {
		return nil, err
	}
	builtinThemes, err := consoletheme.Builtins()
	if err != nil {
		return nil, err
	}
	builtinIDs := make(map[string]struct{}, len(builtinThemes))
	for _, theme := range builtinThemes {
		builtinIDs[theme.ID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(baseThemes))
	catalog := make([]consoleThemeCatalogItem, 0, len(baseThemes))
	for _, theme := range baseThemes {
		source := adminopenapi.ConsoleThemeSourceDirectory
		if _, builtin := builtinIDs[theme.ID]; builtin {
			source = adminopenapi.ConsoleThemeSourceBuiltin
		}
		seen[theme.ID] = struct{}{}
		catalog = append(catalog, consoleThemeCatalogItem{Theme: theme, Source: source})
	}
	if h.consoleThemePackages != nil {
		packages, listErr := h.consoleThemePackages.ListConsoleThemePackages(ctx)
		if listErr != nil {
			return nil, listErr
		}
		for _, stored := range packages {
			theme, parseErr := consoletheme.Parse(stored.Payload)
			if parseErr != nil {
				return nil, fmt.Errorf("load managed Console theme %s: %w", stored.ID, parseErr)
			}
			if theme.ID != stored.ID {
				return nil, fmt.Errorf("managed Console theme id %q does not match payload id %q", stored.ID, theme.ID)
			}
			if _, duplicate := seen[theme.ID]; duplicate {
				return nil, fmt.Errorf("duplicate Console theme id %q", theme.ID)
			}
			seen[theme.ID] = struct{}{}
			catalog = append(catalog, consoleThemeCatalogItem{Theme: theme, Source: adminopenapi.ConsoleThemeSourceManaged, Version: stored.Version})
		}
	}
	return catalog, nil
}

func consoleThemeCatalogResponse(catalog consoleThemeCatalogItem) (adminopenapi.ConsoleTheme, error) {
	data, err := json.Marshal(catalog.Theme)
	if err != nil {
		return adminopenapi.ConsoleTheme{}, err
	}
	var response adminopenapi.ConsoleTheme
	if err := json.Unmarshal(data, &response); err != nil {
		return adminopenapi.ConsoleTheme{}, err
	}
	response.Source = &catalog.Source
	if catalog.Version != "" {
		response.Version = &catalog.Version
	}
	return response, nil
}

func consoleThemePackageResponse(theme consoletheme.Theme) (adminopenapi.ConsoleThemePackage, error) {
	data, err := json.Marshal(theme)
	if err != nil {
		return adminopenapi.ConsoleThemePackage{}, err
	}
	var response adminopenapi.ConsoleThemePackage
	if err := json.Unmarshal(data, &response); err != nil {
		return adminopenapi.ConsoleThemePackage{}, err
	}
	return response, nil
}

func readConsoleThemePackage(r *http.Request) (consoletheme.Theme, []byte, error) {
	data, err := io.ReadAll(io.LimitReader(r.Body, consoletheme.MaxFileBytes+1))
	if err != nil {
		return consoletheme.Theme{}, nil, err
	}
	theme, err := consoletheme.Parse(data)
	if err != nil {
		return consoletheme.Theme{}, nil, err
	}
	canonical, err := json.Marshal(theme)
	if err != nil {
		return consoletheme.Theme{}, nil, err
	}
	return theme, canonical, nil
}

func findConsoleTheme(catalog []consoleThemeCatalogItem, id string) (consoleThemeCatalogItem, bool) {
	for _, item := range catalog {
		if item.Theme.ID == id {
			return item, true
		}
	}
	return consoleThemeCatalogItem{}, false
}

func (h generatedRepositoryAPIAdapter) ValidateConsoleThemePackage(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	theme, _, err := readConsoleThemePackage(r)
	if err != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_theme_package", err.Error())
		return
	}
	packageResponse, err := consoleThemePackageResponse(theme)
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "serialize Console theme failed")
		return
	}
	validation := adminopenapi.ConsoleThemePackageValidation{Theme: packageResponse, Status: adminopenapi.Available}
	catalog, err := h.availableConsoleThemes(r.Context())
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "load Console themes failed")
		return
	}
	if existing, found := findConsoleTheme(catalog, theme.ID); found {
		source := adminopenapi.ConsoleThemePackageValidationExistingSource(existing.Source)
		validation.ExistingSource = &source
		if existing.Source == adminopenapi.ConsoleThemeSourceManaged {
			validation.Status = adminopenapi.Replaceable
			validation.ExistingVersion = &existing.Version
		} else {
			validation.Status = adminopenapi.Reserved
		}
	}
	writeNativeMavenJSON(w, http.StatusOK, validation)
}

func (h generatedRepositoryAPIAdapter) InstallConsoleThemePackage(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authorize(w, r)
	if !ok {
		return
	}
	if h.consoleThemePackages == nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "managed Console themes are unavailable")
		return
	}
	theme, payload, err := readConsoleThemePackage(r)
	if err != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_theme_package", err.Error())
		return
	}
	catalog, err := h.availableConsoleThemes(r.Context())
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "load Console themes failed")
		return
	}
	if existing, found := findConsoleTheme(catalog, theme.ID); found {
		message := "theme ID is reserved by an immutable package"
		if existing.Source == adminopenapi.ConsoleThemeSourceManaged {
			message = "managed theme already exists; replace it with If-Match"
		}
		writeHostedProblem(w, http.StatusConflict, "console_theme_exists", message)
		return
	}
	stored, err := h.consoleThemePackages.CreateConsoleThemePackage(r.Context(), repository.ConsoleThemePackage{ID: theme.ID, Payload: payload})
	if errors.Is(err, repository.ErrConsoleThemeExists) {
		writeHostedProblem(w, http.StatusConflict, "console_theme_exists", "managed theme already exists")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "install Console theme failed")
		return
	}
	response, err := consoleThemeCatalogResponse(consoleThemeCatalogItem{Theme: theme, Source: adminopenapi.ConsoleThemeSourceManaged, Version: stored.Version})
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "serialize Console theme failed")
		return
	}
	h.recordConsoleThemeAudit(r.Context(), principal, theme.ID, "console-theme.install", http.StatusCreated)
	w.Header().Set("ETag", stored.Version)
	writeNativeMavenJSON(w, http.StatusCreated, response)
}

func (h generatedRepositoryAPIAdapter) ReplaceConsoleThemePackage(w http.ResponseWriter, r *http.Request, themeID string, params adminopenapi.ReplaceConsoleThemePackageParams) {
	principal, ok := h.authorize(w, r)
	if !ok {
		return
	}
	if h.consoleThemePackages == nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "managed Console themes are unavailable")
		return
	}
	theme, payload, err := readConsoleThemePackage(r)
	if err != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_theme_package", err.Error())
		return
	}
	if theme.ID != themeID {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_theme_package", "theme package id must match the request path")
		return
	}
	stored, err := h.consoleThemePackages.ReplaceConsoleThemePackage(r.Context(), repository.ConsoleThemePackage{ID: theme.ID, Payload: payload}, string(params.IfMatch))
	switch {
	case errors.Is(err, repository.ErrConsoleThemeNotFound):
		writeHostedProblem(w, http.StatusNotFound, "console_theme_not_found", "managed Console theme not found")
		return
	case errors.Is(err, repository.ErrVersionConflict):
		writeHostedProblem(w, http.StatusPreconditionFailed, "version_conflict", "If-Match does not match the current theme version")
		return
	case err != nil:
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "replace Console theme failed")
		return
	}
	response, err := consoleThemeCatalogResponse(consoleThemeCatalogItem{Theme: theme, Source: adminopenapi.ConsoleThemeSourceManaged, Version: stored.Version})
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "serialize Console theme failed")
		return
	}
	h.recordConsoleThemeAudit(r.Context(), principal, theme.ID, "console-theme.replace", http.StatusOK)
	w.Header().Set("ETag", stored.Version)
	writeNativeMavenJSON(w, http.StatusOK, response)
}

func (h generatedRepositoryAPIAdapter) DeleteConsoleThemePackage(w http.ResponseWriter, r *http.Request, themeID string, params adminopenapi.DeleteConsoleThemePackageParams) {
	principal, ok := h.authorize(w, r)
	if !ok {
		return
	}
	if h.consoleThemePackages == nil || h.siteSettings == nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "managed Console themes are unavailable")
		return
	}
	settings, err := h.siteSettings.GetSiteSettings(r.Context())
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "load site settings failed")
		return
	}
	if slices.Contains(settings.EnabledThemeIDs, themeID) {
		writeHostedProblem(w, http.StatusConflict, "console_theme_in_use", "disable the theme in site settings before deleting it")
		return
	}
	err = h.consoleThemePackages.DeleteConsoleThemePackage(r.Context(), themeID, string(params.IfMatch))
	switch {
	case errors.Is(err, repository.ErrConsoleThemeNotFound):
		writeHostedProblem(w, http.StatusNotFound, "console_theme_not_found", "managed Console theme not found")
		return
	case errors.Is(err, repository.ErrVersionConflict):
		writeHostedProblem(w, http.StatusPreconditionFailed, "version_conflict", "If-Match does not match the current theme version")
		return
	case err != nil:
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "delete Console theme failed")
		return
	}
	h.recordConsoleThemeAudit(r.Context(), principal, themeID, "console-theme.delete", http.StatusNoContent)
	w.WriteHeader(http.StatusNoContent)
}

func (h generatedRepositoryAPIAdapter) recordConsoleThemeAudit(ctx context.Context, principal Principal, themeID, operation string, status int) {
	if h.audit == nil {
		return
	}
	_ = h.audit.RecordAudit(ctx, repository.AuditRecord{
		Actor: principal.Actor, Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(),
		Format: "management", Resource: "console-theme:" + themeID, Operation: operation,
		Status: status, CacheDisposition: "bypass",
	})
}
