package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type catalogLockObservingStore struct {
	*repository.MemoryStore
	observationMu          sync.Mutex
	lockHeld               bool
	lockCount              int
	replacedSettingsLocked bool
	deletedThemeLocked     bool
}

func (s *catalogLockObservingStore) LockConsoleThemeCatalog(_ context.Context) (func(), error) {
	s.observationMu.Lock()
	s.lockHeld = true
	s.lockCount++
	s.observationMu.Unlock()
	return func() {
		s.observationMu.Lock()
		s.lockHeld = false
		s.observationMu.Unlock()
	}, nil
}

func (s *catalogLockObservingStore) ReplaceSiteSettings(ctx context.Context, settings repository.SiteSettings, expectedVersion string) (repository.SiteSettings, error) {
	s.observationMu.Lock()
	s.replacedSettingsLocked = s.lockHeld
	s.observationMu.Unlock()
	return s.MemoryStore.ReplaceSiteSettings(ctx, settings, expectedVersion)
}

func (s *catalogLockObservingStore) DeleteConsoleThemePackage(ctx context.Context, id, expectedVersion string) error {
	s.observationMu.Lock()
	s.deletedThemeLocked = s.lockHeld
	s.observationMu.Unlock()
	return s.MemoryStore.DeleteConsoleThemePackage(ctx, id, expectedVersion)
}

const acmeDarkTheme = `{
  "$schema":"https://artifact-gateway.local/schemas/console-theme-v1.json",
  "schemaVersion":1,
  "id":"acme-dark",
  "name":"Acme Dark",
  "description":"A managed test theme",
  "mode":"dark",
  "token":{
    "colorPrimary":"#7C3AED",
    "colorSuccess":"#16A34A",
    "colorWarning":"#D97706",
    "colorError":"#DC2626",
    "colorInfo":"#2563EB",
    "colorTextBase":"#F5F3FF",
    "colorBgBase":"#111018"
  }
}`

func TestConsoleThemePackageManagementLifecycle(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := func(method, path, body, token, ifMatch string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			authorize(r, token)
		}
		if ifMatch != "" {
			r.Header.Set("If-Match", ifMatch)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	if got := request(http.MethodPost, "/api/v2/console-themes:validate", acmeDarkTheme, "writer-secret", ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("non-admin validation = %d %s", got.Code, got.Body.String())
	}
	validated := request(http.MethodPost, "/api/v2/console-themes:validate", acmeDarkTheme, "admin-secret", "")
	if validated.Code != http.StatusOK || !strings.Contains(validated.Body.String(), `"status":"available"`) || !strings.Contains(validated.Body.String(), `"$schema"`) {
		t.Fatalf("validated = %d %s", validated.Code, validated.Body.String())
	}

	installed := request(http.MethodPost, "/api/v2/console-themes", acmeDarkTheme, "admin-secret", "")
	if installed.Code != http.StatusCreated || installed.Header().Get("ETag") != "1" || !strings.Contains(installed.Body.String(), `"source":"managed"`) {
		t.Fatalf("installed = %d etag=%q %s", installed.Code, installed.Header().Get("ETag"), installed.Body.String())
	}
	replaceable := request(http.MethodPost, "/api/v2/console-themes:validate", acmeDarkTheme, "admin-secret", "")
	if replaceable.Code != http.StatusOK || !strings.Contains(replaceable.Body.String(), `"status":"replaceable"`) || !strings.Contains(replaceable.Body.String(), `"existingVersion":"1"`) {
		t.Fatalf("replaceable = %d %s", replaceable.Code, replaceable.Body.String())
	}
	listed := request(http.MethodGet, "/api/v2/site-settings", "", "", "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"id":"acme-dark"`) || !strings.Contains(listed.Body.String(), `"version":"1"`) {
		t.Fatalf("catalog = %d %s", listed.Code, listed.Body.String())
	}

	replacement := strings.Replace(acmeDarkTheme, "Acme Dark", "Acme Midnight", 1)
	replaced := request(http.MethodPut, "/api/v2/console-themes/acme-dark", replacement, "admin-secret", "1")
	if replaced.Code != http.StatusOK || replaced.Header().Get("ETag") != "2" || !strings.Contains(replaced.Body.String(), "Acme Midnight") {
		t.Fatalf("replaced = %d etag=%q %s", replaced.Code, replaced.Header().Get("ETag"), replaced.Body.String())
	}
	stale := request(http.MethodPut, "/api/v2/console-themes/acme-dark", replacement, "admin-secret", "1")
	if stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale replace = %d %s", stale.Code, stale.Body.String())
	}

	settings, err := store.GetSiteSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	settings.EnabledThemeIDs = append(settings.EnabledThemeIDs, "acme-dark")
	if _, err = store.ReplaceSiteSettings(t.Context(), settings, settings.Version); err != nil {
		t.Fatal(err)
	}
	inUse := request(http.MethodDelete, "/api/v2/console-themes/acme-dark", "", "admin-secret", "2")
	if inUse.Code != http.StatusConflict || !strings.Contains(inUse.Body.String(), "console_theme_in_use") {
		t.Fatalf("in-use delete = %d %s", inUse.Code, inUse.Body.String())
	}
	settings, err = store.GetSiteSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	settings.EnabledThemeIDs = settings.EnabledThemeIDs[:len(settings.EnabledThemeIDs)-1]
	if _, err = store.ReplaceSiteSettings(t.Context(), settings, settings.Version); err != nil {
		t.Fatal(err)
	}
	staleDelete := request(http.MethodDelete, "/api/v2/console-themes/acme-dark", "", "admin-secret", "1")
	if staleDelete.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale delete = %d %s", staleDelete.Code, staleDelete.Body.String())
	}
	deleted := request(http.MethodDelete, "/api/v2/console-themes/acme-dark", "", "admin-secret", "2")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("deleted = %d %s", deleted.Code, deleted.Body.String())
	}
	missing := request(http.MethodDelete, "/api/v2/console-themes/acme-dark", "", "admin-secret", "2")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing delete = %d %s", missing.Code, missing.Body.String())
	}
	if len(store.Audits) != 3 || store.Audits[0].Operation != "console-theme.install" || store.Audits[1].Operation != "console-theme.replace" || store.Audits[2].Operation != "console-theme.delete" {
		t.Fatalf("audits=%#v", store.Audits)
	}
}

func TestConsoleThemePackageRejectsInvalidAndReservedPackages(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := func(path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		authorize(r, "admin-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	invalid := request("/api/v2/console-themes:validate", strings.Replace(acmeDarkTheme, `"name":"Acme Dark"`, `"name":"Acme Dark","arbitraryCss":"body{}"`, 1))
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "invalid_theme_package") {
		t.Fatalf("invalid = %d %s", invalid.Code, invalid.Body.String())
	}
	malformedColorBody := strings.Replace(acmeDarkTheme, `"colorPrimary":"#7C3AED"`, `"colorPrimary":"rgb(,,,)"`, 1)
	malformedColor := request("/api/v2/console-themes:validate", malformedColorBody)
	if malformedColor.Code != http.StatusBadRequest || !strings.Contains(malformedColor.Body.String(), "invalid_theme_package") {
		t.Fatalf("malformed color = %d %s", malformedColor.Code, malformedColor.Body.String())
	}
	reservedBody := strings.Replace(acmeDarkTheme, `"id":"acme-dark"`, `"id":"gateway-dark"`, 1)
	reserved := request("/api/v2/console-themes:validate", reservedBody)
	if reserved.Code != http.StatusOK || !strings.Contains(reserved.Body.String(), `"status":"reserved"`) || !strings.Contains(reserved.Body.String(), `"existingSource":"builtin"`) {
		t.Fatalf("reserved validation = %d %s", reserved.Code, reserved.Body.String())
	}
	conflict := request("/api/v2/console-themes", reservedBody)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("reserved install = %d %s", conflict.Code, conflict.Body.String())
	}
}

func TestConsoleThemeCatalogMutationsShareOneLock(t *testing.T) {
	store := &catalogLockObservingStore{MemoryStore: repository.NewMemoryStore()}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())

	settingsBody := `{"siteName":"Artifact Gateway","logoUrl":"","brandMark":"AG","enabledThemeIds":["gateway-dark","gateway-light","aerok-dark","aerok-light"],"defaultThemeId":"gateway-dark"}`
	settingsRequest := httptest.NewRequest(http.MethodPut, "/api/v2/site-settings", strings.NewReader(settingsBody))
	authorize(settingsRequest, "admin-secret")
	settingsRequest.Header.Set("If-Match", "1")
	settingsResponse := httptest.NewRecorder()
	handler.ServeHTTP(settingsResponse, settingsRequest)
	if settingsResponse.Code != http.StatusOK {
		t.Fatalf("replace settings = %d %s", settingsResponse.Code, settingsResponse.Body.String())
	}

	if _, err := store.CreateConsoleThemePackage(t.Context(), repository.ConsoleThemePackage{ID: "acme-dark", Payload: []byte(acmeDarkTheme)}); err != nil {
		t.Fatal(err)
	}
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/v2/console-themes/acme-dark", nil)
	authorize(deleteRequest, "admin-secret")
	deleteRequest.Header.Set("If-Match", "1")
	deleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete theme = %d %s", deleteResponse.Code, deleteResponse.Body.String())
	}

	store.observationMu.Lock()
	defer store.observationMu.Unlock()
	if store.lockCount != 2 || !store.replacedSettingsLocked || !store.deletedThemeLocked {
		t.Fatalf("lock observations = count:%d replace:%t delete:%t", store.lockCount, store.replacedSettingsLocked, store.deletedThemeLocked)
	}
}
