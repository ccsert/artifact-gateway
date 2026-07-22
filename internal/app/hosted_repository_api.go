package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

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

type repositoryPageCursor struct {
	Endpoint, ID string
	ExpiresAt    int64
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
			h.create(w, r, principal)
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

func (h hostedRepositoryAPIHandler) create(w http.ResponseWriter, r *http.Request, principal Principal) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 128 {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "Idempotency-Key is required and must be at most 128 characters")
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	var request createHostedRepositoryRequest
	if err := decoder.Decode(&request); err != nil || !validHostedRepository(request) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "name and format must be valid")
		return
	}
	payload, _ := json.Marshal(request)
	digest := sha256.Sum256(payload)
	repo, _, err := h.store.CreateHostedRepositoryIdempotently(r.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: request.Name, Format: request.Format}, principal.Actor, key, base64.RawURLEncoding.EncodeToString(digest[:]))
	if errors.Is(err, repository.ErrIdempotencyConflict) {
		writeHostedProblem(w, http.StatusConflict, "idempotency_conflict", "Idempotency-Key was already used with a different request")
		return
	}
	if errors.Is(err, repository.ErrNameExists) {
		writeHostedProblem(w, http.StatusConflict, "version_conflict", "repository name already exists")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "create repository failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	// Returning the same documented response on a successful replay makes a
	// lost client response safe to retry without introducing another outcome.
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(repo)
}

func (h hostedRepositoryAPIHandler) list(w http.ResponseWriter, r *http.Request) {
	pageSize := 50
	if raw := r.URL.Query().Get("pageSize"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 200 {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "pageSize must be between 1 and 200")
			return
		}
		pageSize = parsed
	}
	after, err := h.decodeCursor(r.URL.Query().Get("pageToken"))
	if err != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_page_token", "page token is invalid or expired")
		return
	}
	items, next, err := h.store.ListHostedRepositories(r.Context(), pageSize, after)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_page_token", "page token is invalid")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list repositories failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	nextToken := ""
	if next != "" {
		nextToken = h.encodeCursor(next)
	}
	_ = json.NewEncoder(w).Encode(repositoryPage{Items: items, NextPageToken: nextToken})
}

func (h hostedRepositoryAPIHandler) encodeCursor(id string) string {
	payload, _ := json.Marshal(repositoryPageCursor{Endpoint: "repositories", ID: id, ExpiresAt: time.Now().UTC().Add(15 * time.Minute).Unix()})
	mac := hmac.New(sha256.New, []byte(h.authenticator.AdminToken))
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(append(payload, mac.Sum(nil)...))
}

func (h hostedRepositoryAPIHandler) decodeCursor(token string) (string, error) {
	if token == "" {
		return "", nil
	}
	encoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(encoded) <= sha256.Size {
		return "", errors.New("invalid cursor")
	}
	payload, signature := encoded[:len(encoded)-sha256.Size], encoded[len(encoded)-sha256.Size:]
	mac := hmac.New(sha256.New, []byte(h.authenticator.AdminToken))
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return "", errors.New("invalid cursor")
	}
	var cursor repositoryPageCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.Endpoint != "repositories" || cursor.ID == "" || time.Now().UTC().Unix() >= cursor.ExpiresAt {
		return "", errors.New("invalid cursor")
	}
	return cursor.ID, nil
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
