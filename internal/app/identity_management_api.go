package app

import (
	"context"
	"net/http"

	"github.com/ccsert/artifact-gateway/internal/repository"
)

// Identity management does not receive authentication lookups, login mutation,
// session creation, or expiry jobs merely because their records share a store.
type identityUserStore interface {
	CreateUser(context.Context, repository.User) (repository.User, error)
	ListUsers(context.Context, repository.UserListQuery) (repository.UserPage, error)
	GetUser(context.Context, string) (repository.User, error)
	UpdateUser(context.Context, repository.UserUpdate, string) (repository.User, error)
	DeleteUser(context.Context, string) error
	UpdateUserPassword(context.Context, string, string, string, bool) (repository.User, error)
	RevokeUserSessions(context.Context, string, string) (repository.User, error)
	ListUserSessions(context.Context, string, bool) ([]repository.UserSession, error)
	RevokeUserSession(context.Context, string, string) (repository.UserSession, error)
	ListUserIdentities(context.Context, string) ([]repository.UserIdentity, error)
	CreateUserIdentity(context.Context, repository.UserIdentity) (repository.UserIdentity, error)
	DeleteUserIdentity(context.Context, string, string) error
}

type identityAPIKeyStore interface {
	CreateAPIKey(context.Context, repository.APIKey) (repository.APIKey, error)
	ListAPIKeys(context.Context) ([]repository.APIKey, error)
	RevokeAPIKey(context.Context, string) (repository.APIKey, error)
}

type identityServiceAccountStore interface {
	CreateServiceAccount(context.Context, repository.ServiceAccount) (repository.ServiceAccount, error)
	ListServiceAccounts(context.Context, int, string) ([]repository.ServiceAccount, error)
	UpdateServiceAccount(context.Context, repository.ServiceAccountUpdate, string) (repository.ServiceAccount, error)
	CreateServiceAccountCredential(context.Context, repository.APIKey) (repository.APIKey, error)
	ListServiceAccountCredentials(context.Context, string, int, string) ([]repository.APIKey, error)
	RevokeServiceAccountCredential(context.Context, string, string) (repository.APIKey, error)
}

type identityOIDCSettings interface {
	Settings(context.Context) (OIDCSettingsView, error)
	Replace(context.Context, OIDCSettingsUpdate, string) (OIDCSettingsView, error)
	Test(context.Context) (OIDCConnectionTest, error)
}

type identityManagementOptions struct {
	Scope           managementScope
	Users           identityUserStore
	APIKeys         identityAPIKeyStore
	ServiceAccounts identityServiceAccountStore
	OIDC            identityOIDCSettings
}

// identityManagementHandler owns identity administration; repository content,
// lifecycle, operations and site persistence are not its dependencies.
type identityManagementHandler struct {
	managementScope
	users           identityUserStore
	apiKeys         identityAPIKeyStore
	serviceAccounts identityServiceAccountStore
	oidcRuntime     identityOIDCSettings
}

func newIdentityManagementHandler(options identityManagementOptions) identityManagementHandler {
	return identityManagementHandler{
		managementScope: options.Scope,
		users:           options.Users,
		apiKeys:         options.APIKeys,
		serviceAccounts: options.ServiceAccounts,
		oidcRuntime:     options.OIDC,
	}
}

func (h identityManagementHandler) GetCurrentIdentity(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	writeNativeMavenJSON(w, http.StatusOK, currentIdentityResponse(principal))
}
