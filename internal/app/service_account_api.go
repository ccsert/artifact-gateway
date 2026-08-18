package app

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/authorization"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

type serviceAccountPageCursor struct {
	Endpoint, AccountID, ID string
	ExpiresAt               int64
}

func (h generatedRepositoryAPIAdapter) serviceAccountPageBounds(
	w http.ResponseWriter,
	pageSizeParam *int,
	pageToken *string,
	endpoint string,
	accountID string,
) (int, string, bool) {
	pageSize := 50
	if pageSizeParam != nil {
		pageSize = *pageSizeParam
	}
	if pageSize < 1 || pageSize > 200 {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "pageSize must be between 1 and 200")
		return 0, "", false
	}
	if pageToken == nil || *pageToken == "" {
		return pageSize, "", true
	}
	var cursor serviceAccountPageCursor
	if decodeSignedCursor(h.authenticator.AdminToken, *pageToken, &cursor) != nil ||
		cursor.Endpoint != endpoint || cursor.AccountID != accountID || cursor.ID == "" ||
		time.Now().UTC().Unix() >= cursor.ExpiresAt {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_page_token", "page token is invalid or expired")
		return 0, "", false
	}
	return pageSize, cursor.ID, true
}

func (h generatedRepositoryAPIAdapter) serviceAccountNextPageToken(endpoint, accountID, id string) *string {
	token := encodeSignedCursor(h.authenticator.AdminToken, serviceAccountPageCursor{
		Endpoint: endpoint, AccountID: accountID, ID: id,
		ExpiresAt: time.Now().UTC().Add(15 * time.Minute).Unix(),
	})
	return &token
}

func (h generatedRepositoryAPIAdapter) ListServiceAccounts(w http.ResponseWriter, r *http.Request, params adminopenapi.ListServiceAccountsParams) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	pageSize, afterID, ok := h.serviceAccountPageBounds(
		w, params.PageSize, params.PageToken, "service-accounts", "",
	)
	if !ok {
		return
	}
	accounts, err := h.serviceAccounts.ListServiceAccounts(r.Context(), pageSize+1, afterID)
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list service accounts failed")
		return
	}
	var nextPageToken *string
	if len(accounts) > pageSize {
		accounts = accounts[:pageSize]
		nextPageToken = h.serviceAccountNextPageToken("service-accounts", "", accounts[len(accounts)-1].ID)
	}
	items := make([]adminopenapi.ServiceAccount, 0, len(accounts))
	for _, account := range accounts {
		items = append(items, serviceAccountResponse(account))
	}
	writeNativeMavenJSON(w, http.StatusOK, adminopenapi.ServiceAccountList{Items: items, NextPageToken: nextPageToken})
}

func (h generatedRepositoryAPIAdapter) CreateServiceAccount(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	var request adminopenapi.CreateServiceAccount
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "invalid service account request")
		return
	}
	name := strings.TrimSpace(request.Name)
	description := ""
	if request.Description != nil {
		description = strings.TrimSpace(*request.Description)
	}
	if name == "" || utf8.RuneCountInString(name) > 128 || utf8.RuneCountInString(description) > 1024 {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "service account name or description is invalid")
		return
	}
	account, err := h.serviceAccounts.CreateServiceAccount(r.Context(), repository.ServiceAccount{
		ID:          uuid.NewString(),
		Name:        name,
		Description: description,
	})
	if errors.Is(err, repository.ErrNameExists) {
		writeHostedProblem(w, http.StatusConflict, "name_exists", "service account name already exists")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "create service account failed")
		return
	}
	h.recordServiceAccountAudit(r, "service-accounts/"+account.ID, "service_account.create", http.StatusCreated)
	writeNativeMavenJSON(w, http.StatusCreated, serviceAccountResponse(account))
}

func (h generatedRepositoryAPIAdapter) UpdateServiceAccount(w http.ResponseWriter, r *http.Request, serviceAccountID openapi_types.UUID, params adminopenapi.UpdateServiceAccountParams) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	var request adminopenapi.UpdateServiceAccount
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.Description == nil && request.State == nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "service account update is required")
		return
	}
	update := repository.ServiceAccountUpdate{ID: serviceAccountID.String()}
	if request.Description != nil {
		description := strings.TrimSpace(*request.Description)
		if utf8.RuneCountInString(description) > 1024 {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "service account description is invalid")
			return
		}
		update.Description = &description
	}
	if request.State != nil {
		state := repository.ServiceAccountState(*request.State)
		if state != repository.ServiceAccountActive && state != repository.ServiceAccountDisabled {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "service account state is invalid")
			return
		}
		update.State = &state
	}
	account, err := h.serviceAccounts.UpdateServiceAccount(r.Context(), update, string(params.IfMatch))
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "service account not found")
		return
	}
	if errors.Is(err, repository.ErrVersionConflict) {
		writeHostedProblem(w, http.StatusPreconditionFailed, "version_conflict", "service account version changed")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "update service account failed")
		return
	}
	h.recordServiceAccountAudit(r, "service-accounts/"+account.ID, "service_account.update", http.StatusOK)
	writeNativeMavenJSON(w, http.StatusOK, serviceAccountResponse(account))
}

func (h generatedRepositoryAPIAdapter) ListServiceAccountCredentials(
	w http.ResponseWriter,
	r *http.Request,
	serviceAccountID openapi_types.UUID,
	params adminopenapi.ListServiceAccountCredentialsParams,
) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	accountID := serviceAccountID.String()
	pageSize, afterID, ok := h.serviceAccountPageBounds(
		w, params.PageSize, params.PageToken, "service-account-credentials", accountID,
	)
	if !ok {
		return
	}
	credentials, err := h.serviceAccounts.ListServiceAccountCredentials(r.Context(), accountID, pageSize+1, afterID)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "service account not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list service account credentials failed")
		return
	}
	var nextPageToken *string
	if len(credentials) > pageSize {
		credentials = credentials[:pageSize]
		nextPageToken = h.serviceAccountNextPageToken(
			"service-account-credentials", accountID, credentials[len(credentials)-1].ID,
		)
	}
	items := make([]adminopenapi.ServiceAccountCredential, 0, len(credentials))
	for _, credential := range credentials {
		items = append(items, serviceAccountCredentialResponse(credential))
	}
	writeNativeMavenJSON(w, http.StatusOK, adminopenapi.ServiceAccountCredentialList{
		Items: items, NextPageToken: nextPageToken,
	})
}

func (h generatedRepositoryAPIAdapter) CreateServiceAccountCredential(w http.ResponseWriter, r *http.Request, serviceAccountID openapi_types.UUID) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	var request adminopenapi.CreateServiceAccountCredential
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "invalid service account credential request")
		return
	}
	name := strings.TrimSpace(request.Name)
	if name == "" || utf8.RuneCountInString(name) > 128 {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "credential name is invalid")
		return
	}
	now := time.Now().UTC()
	expiresAt := now.Add(90 * 24 * time.Hour)
	if request.ExpiresAt != nil {
		expiresAt = request.ExpiresAt.UTC()
	}
	if !expiresAt.After(now) || expiresAt.After(now.Add(365*24*time.Hour)) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "credential expiry must be in the future and no more than 365 days away")
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "generate service account credential failed")
		return
	}
	token := "agc_" + base64.RawURLEncoding.EncodeToString(raw)
	credential, err := h.serviceAccounts.CreateServiceAccountCredential(r.Context(), repository.APIKey{
		ID:               uuid.NewString(),
		ServiceAccountID: serviceAccountID.String(),
		Name:             name,
		SecretHash:       authorization.HashAPIKey(token),
		Roles:            []string{},
		ExpiresAt:        &expiresAt,
	})
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "service account not found")
		return
	}
	if errors.Is(err, repository.ErrServiceAccountDisabled) {
		writeHostedProblem(w, http.StatusConflict, "service_account_disabled", "service account is disabled")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "create service account credential failed")
		return
	}
	response := serviceAccountCredentialResponse(credential)
	h.recordServiceAccountAudit(r, "service-accounts/"+serviceAccountID.String()+"/credentials/"+credential.ID, "service_account.credential.create", http.StatusCreated)
	writeNativeMavenJSON(w, http.StatusCreated, adminopenapi.CreatedServiceAccountCredential{
		Id:               response.Id,
		ServiceAccountId: response.ServiceAccountId,
		Name:             response.Name,
		CreatedAt:        response.CreatedAt,
		ExpiresAt:        response.ExpiresAt,
		LastUsedAt:       response.LastUsedAt,
		RevokedAt:        response.RevokedAt,
		Token:            token,
	})
}

func (h generatedRepositoryAPIAdapter) RevokeServiceAccountCredential(w http.ResponseWriter, r *http.Request, serviceAccountID, credentialID openapi_types.UUID) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	credential, err := h.serviceAccounts.RevokeServiceAccountCredential(r.Context(), serviceAccountID.String(), credentialID.String())
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "service account credential not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "revoke service account credential failed")
		return
	}
	h.recordServiceAccountAudit(r, "service-accounts/"+serviceAccountID.String()+"/credentials/"+credential.ID, "service_account.credential.revoke", http.StatusOK)
	writeNativeMavenJSON(w, http.StatusOK, serviceAccountCredentialResponse(credential))
}

func (h generatedRepositoryAPIAdapter) recordServiceAccountAudit(r *http.Request, resource, operation string, status int) {
	if h.audit == nil {
		return
	}
	_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{
		Actor:            h.auditActor(r),
		Outcome:          repository.AuditResolved,
		OccurredAt:       time.Now().UTC(),
		Format:           "management",
		Resource:         resource,
		Operation:        operation,
		Status:           status,
		CacheDisposition: "bypass",
	})
}

func serviceAccountResponse(account repository.ServiceAccount) adminopenapi.ServiceAccount {
	return adminopenapi.ServiceAccount{
		Id:          uuid.MustParse(account.ID),
		Name:        account.Name,
		Description: account.Description,
		State:       adminopenapi.ServiceAccountState(account.State),
		CreatedAt:   account.CreatedAt,
		UpdatedAt:   account.UpdatedAt,
		Version:     account.Version,
	}
}

func serviceAccountCredentialResponse(credential repository.APIKey) adminopenapi.ServiceAccountCredential {
	return adminopenapi.ServiceAccountCredential{
		Id:               uuid.MustParse(credential.ID),
		ServiceAccountId: uuid.MustParse(credential.ServiceAccountID),
		Name:             credential.Name,
		CreatedAt:        credential.CreatedAt,
		ExpiresAt:        credential.ExpiresAt,
		LastUsedAt:       credential.LastUsedAt,
		RevokedAt:        credential.RevokedAt,
	}
}
