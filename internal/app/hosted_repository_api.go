package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

var hostedRepositoryName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// hostedRepositoryAPIHandler is the versioned management surface described by
// native-hosted-v1.json. It intentionally does not reuse the V2 Group routes.
type hostedRepositoryAPIHandler struct {
	store         repository.HostedRepositoryStore
	authenticator Authenticator
}

type createHostedRepositoryRequest struct {
	Name   string            `json:"name"`
	Format repository.Format `json:"format"`
}

type repositoryPage struct {
	Items         []repository.HostedRepository `json:"items"`
	NextPageToken string                        `json:"nextPageToken,omitempty"`
}

func (h hostedRepositoryAPIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok || !principal.Admin {
		writeHostedProblem(w, http.StatusUnauthorized, "access_denied", "administrator authentication is required")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v2/repositories")
	if path == "" || path == "/" {
		switch r.Method {
		case http.MethodGet:
			h.list(w, r)
		case http.MethodPost:
			h.create(w, r)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	if strings.Count(strings.Trim(path, "/"), "/") != 0 {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
		return
	}
	id := strings.Trim(path, "/")
	switch r.Method {
	case http.MethodGet:
		h.get(w, r, id)
	case http.MethodDelete:
		h.disable(w, r, id)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h hostedRepositoryAPIHandler) create(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	var request createHostedRepositoryRequest
	if err := decoder.Decode(&request); err != nil || !validHostedRepository(request) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "name and format must be valid")
		return
	}
	repo, err := h.store.CreateHostedRepository(r.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: request.Name, Format: request.Format})
	if errors.Is(err, repository.ErrNameExists) {
		writeHostedProblem(w, http.StatusConflict, "idempotency_conflict", "repository name already exists")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "create repository failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(repo)
}

func (h hostedRepositoryAPIHandler) list(w http.ResponseWriter, r *http.Request) {
	pageSize := 50
	if raw := r.URL.Query().Get("pageSize"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_page_token", "pageSize must be between 1 and 100")
			return
		}
		pageSize = parsed
	}
	items, next, err := h.store.ListHostedRepositories(r.Context(), pageSize, r.URL.Query().Get("pageToken"))
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_page_token", "page token is invalid")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list repositories failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(repositoryPage{Items: items, NextPageToken: next})
}

func (h hostedRepositoryAPIHandler) get(w http.ResponseWriter, r *http.Request, id string) {
	repo, err := h.store.GetHostedRepository(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get repository failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(repo)
}

func (h hostedRepositoryAPIHandler) disable(w http.ResponseWriter, r *http.Request, id string) {
	_, err := h.store.DisableHostedRepository(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found or already disabled")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "disable repository failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"id": id, "state": "pending"})
}

func validHostedRepository(request createHostedRepositoryRequest) bool {
	return hostedRepositoryName.MatchString(request.Name) && (request.Format == repository.FormatRaw || request.Format == repository.FormatOCI || request.Format == repository.FormatMaven)
}

func writeHostedProblem(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": http.StatusText(status), "status": status, "code": code, "message": message, "requestId": ""})
}
