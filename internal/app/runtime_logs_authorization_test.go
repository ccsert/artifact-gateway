package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/operationalog"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

const protectedRuntimeLogMarker = "synthetic-protected-runtime-event"

func TestRuntimeLogQueryRejectsAuthenticatedNonAdministrators(t *testing.T) {
	store := repository.NewMemoryStore()
	if _, err := store.CreateUser(context.Background(), repository.User{
		ID: uuid.NewString(), Name: "log-member", Role: string(RoleMember), SecretHash: "synthetic-password-hash",
	}); err != nil {
		t.Fatal(err)
	}
	auth := testAuthenticatorWithUsers(store)
	buffer := operationalog.NewBuffer(1)
	operationalog.NewLogger(buffer, "synthetic-node", "synthetic-session").Info(protectedRuntimeLogMarker)

	for _, credential := range []struct {
		name, token, actor string
	}{
		{"resolver", auth.ResolverToken, "build-agent"},
		{"member", auth.IssueToken("user:log-member"), "user:log-member"},
	} {
		t.Run(credential.name, func(t *testing.T) {
			principal, ok := auth.Authenticate("Bearer " + credential.token)
			if !ok || principal.Admin || principal.MustChangePassword || principal.Actor != credential.actor {
				t.Fatalf("fixture must authenticate as the expected non-administrator: ok=%t principal=%+v", ok, principal)
			}
			for _, state := range []struct {
				name   string
				buffer *operationalog.Buffer
			}{
				{"enabled", buffer},
				{"disabled", nil},
			} {
				t.Run(state.name, func(t *testing.T) {
					handler := NewGatewayHandler(Dependencies{LogBuffer: state.buffer, Runtime: DiagnosticRuntime{InstanceID: "synthetic-node", SessionID: "synthetic-session"}}, store, TestAdapter{}, auth)
					response := requestRuntimeLogs(handler, credential.token)
					assertRuntimeLogPermissionDenied(t, response, "access_denied")
				})
			}
		})
	}

	// The protected event exists and the endpoint is available to administrators.
	handler := NewGatewayHandler(Dependencies{LogBuffer: buffer, Runtime: DiagnosticRuntime{InstanceID: "synthetic-node", SessionID: "synthetic-session"}}, store, TestAdapter{}, auth)
	response := requestRuntimeLogs(handler, auth.AdminToken)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), protectedRuntimeLogMarker) {
		t.Fatalf("administrator query=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRuntimeLogQueryRequiresPasswordChangeWithoutGrantingAdministration(t *testing.T) {
	for _, role := range []Role{RoleAdmin, RoleMember} {
		t.Run(string(role), func(t *testing.T) {
			ctx := context.Background()
			store := repository.NewMemoryStore()
			user, err := store.CreateUser(ctx, repository.User{
				ID: uuid.NewString(), Name: "log-reset", Role: string(role), SecretHash: "synthetic-password-hash", MustChangePassword: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			auth := testAuthenticatorWithUsers(store)
			token := auth.IssueToken("user:log-reset")
			principal, ok := auth.Authenticate("Bearer " + token)
			if !ok || !principal.MustChangePassword || principal.Actor != "user:log-reset" {
				t.Fatalf("fixture must authenticate with a forced password change: ok=%t principal=%+v", ok, principal)
			}
			buffer := operationalog.NewBuffer(1)
			operationalog.NewLogger(buffer, "synthetic-node", "synthetic-session").Info(protectedRuntimeLogMarker)
			states := []struct {
				name   string
				buffer *operationalog.Buffer
			}{
				{"enabled", buffer},
				{"disabled", nil},
			}
			for _, state := range states {
				t.Run("before-reset/"+state.name, func(t *testing.T) {
					handler := NewGatewayHandler(Dependencies{LogBuffer: state.buffer, Runtime: DiagnosticRuntime{InstanceID: "synthetic-node", SessionID: "synthetic-session"}}, store, TestAdapter{}, auth)
					assertRuntimeLogPermissionDenied(t, requestRuntimeLogs(handler, token), "password_change_required")
				})
			}

			// Match the existing repository-surface regression: clearing the flag
			// restores only the authority the account already held.
			if _, err := store.UpdateUserPassword(ctx, user.ID, "synthetic-updated-password-hash", user.Version, false); err != nil {
				t.Fatal(err)
			}
			principal, ok = auth.Authenticate("Bearer " + token)
			if !ok || principal.MustChangePassword || principal.Admin != (role == RoleAdmin) {
				t.Fatalf("reset fixture authority changed unexpectedly: ok=%t principal=%+v", ok, principal)
			}
			for _, state := range states {
				t.Run("after-reset/"+state.name, func(t *testing.T) {
					handler := NewGatewayHandler(Dependencies{LogBuffer: state.buffer, Runtime: DiagnosticRuntime{InstanceID: "synthetic-node", SessionID: "synthetic-session"}}, store, TestAdapter{}, auth)
					response := requestRuntimeLogs(handler, token)
					if role == RoleMember {
						assertRuntimeLogPermissionDenied(t, response, "access_denied")
						return
					}
					if state.buffer == nil {
						if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"log_buffer_unavailable"`) || strings.Contains(response.Body.String(), protectedRuntimeLogMarker) {
							t.Fatalf("disabled administrator query=%d body=%s", response.Code, response.Body.String())
						}
						return
					}
					if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), protectedRuntimeLogMarker) {
						t.Fatalf("reset administrator query=%d body=%s", response.Code, response.Body.String())
					}
				})
			}
		})
	}
}

func requestRuntimeLogs(handler http.Handler, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/api/v2/runtime/logs", nil)
	authorize(request, token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertRuntimeLogPermissionDenied(t *testing.T, response *httptest.ResponseRecorder, code string) {
	t.Helper()
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"`+code+`"`) {
		t.Fatalf("permission denial=%d body=%s, want 403 %s", response.Code, response.Body.String(), code)
	}
	if strings.Contains(response.Body.String(), protectedRuntimeLogMarker) || strings.Contains(response.Body.String(), `"items"`) {
		t.Fatalf("denied query returned runtime log data: %s", response.Body.String())
	}
}
