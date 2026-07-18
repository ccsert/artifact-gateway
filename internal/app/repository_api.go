package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type Adapter interface {
	Available(context.Context, repository.Member, string) bool
}

type TestAdapter struct{}

func (TestAdapter) Available(_ context.Context, member repository.Member, _ string) bool {
	return member.Endpoint != "test://unavailable"
}

type Resolver struct {
	Store   repository.Store
	Adapter Adapter
	Metrics *Metrics
}

func (r Resolver) Resolve(ctx context.Context, groupName, repositoryName, actor string) (repository.Member, error) {
	group, err := r.Store.GetGroup(ctx, groupName)
	if err != nil {
		if auditErr := r.audit(ctx, groupName, repositoryName, "", repository.AuditNotFound, actor); auditErr != nil {
			return repository.Member{}, auditErr
		}
		return repository.Member{}, err
	}
	if !group.Enabled {
		if auditErr := r.audit(ctx, groupName, repositoryName, "", repository.AuditGroupDisabled, actor); auditErr != nil {
			return repository.Member{}, auditErr
		}
		return repository.Member{}, repository.ErrDisabled
	}
	for _, member := range group.Members {
		if r.Adapter.Available(ctx, member, repositoryName) {
			if err := r.audit(ctx, groupName, repositoryName, member.Name, repository.AuditResolved, actor); err != nil {
				return repository.Member{}, err
			}
			r.Metrics.resolved.Add(1)
			return member, nil
		}
	}
	if err := r.audit(ctx, groupName, repositoryName, "", repository.AuditNotFound, actor); err != nil {
		return repository.Member{}, err
	}
	r.Metrics.failed.Add(1)
	return repository.Member{}, repository.ErrNotFound
}

func (r Resolver) audit(ctx context.Context, groupName, repositoryName, memberName string, outcome repository.AuditOutcome, actor string) error {
	if err := r.Store.RecordAudit(ctx, repository.AuditRecord{GroupName: groupName, Repository: repositoryName, MemberName: memberName, Outcome: outcome, Actor: actor, OccurredAt: time.Now().UTC()}); err != nil {
		return fmt.Errorf("record resolver audit: %w", err)
	}
	return nil
}

type Metrics struct {
	resolved atomic.Uint64
	failed   atomic.Uint64
}

func (m *Metrics) Handler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte("# TYPE artifact_gateway_resolver_requests_total counter\nartifact_gateway_resolver_requests_total{outcome=\"resolved\"} " + utoa(m.resolved.Load()) + "\nartifact_gateway_resolver_requests_total{outcome=\"failed\"} " + utoa(m.failed.Load()) + "\n"))
}

func utoa(value uint64) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for value > 0 {
		i--
		digits[i] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[i:])
}

func NewGatewayHandler(dependencies Dependencies, store repository.Store, adapter Adapter, token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /readyz", dependencies.ready)
	metrics := &Metrics{}
	resolver := Resolver{Store: store, Adapter: adapter, Metrics: metrics}
	api := apiHandler{store: store, resolver: resolver, token: token}
	mux.Handle("GET /metrics", http.HandlerFunc(metrics.Handler))
	mux.Handle("/api/v1/oci/groups", api)
	mux.Handle("/api/v1/oci/groups/", api)
	return mux
}

type apiHandler struct {
	store    repository.Store
	resolver Resolver
	token    string
}

func (a apiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.authorized(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/oci/groups")
	if path == "" || path == "/" {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		a.create(w, r)
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 1 && r.Method == http.MethodGet {
		a.get(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "disable" && r.Method == http.MethodPost {
		a.disable(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "resolve" && r.Method == http.MethodGet {
		a.resolve(w, r, parts[0], actor)
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "resource not found")
}

func (a apiHandler) authorized(r *http.Request) (string, bool) {
	if a.token == "" {
		return "anonymous", true
	}
	if r.Header.Get("Authorization") != "Bearer "+a.token {
		return "", false
	}
	return "token", true
}

func (a apiHandler) create(w http.ResponseWriter, r *http.Request) {
	defer func() { _ = r.Body.Close() }()
	var group repository.Group
	if err := json.NewDecoder(r.Body).Decode(&group); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	if err := validateGroup(group); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_group", err.Error())
		return
	}
	created, err := a.store.CreateGroup(r.Context(), group)
	if errors.Is(err, repository.ErrNameExists) {
		writeError(w, http.StatusConflict, "group_exists", "group name already exists")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "unable to create group")
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (a apiHandler) get(w http.ResponseWriter, r *http.Request, name string) {
	group, err := a.store.GetGroup(r.Context(), name)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, 404, "not_found", "group not found")
		return
	}
	if err != nil {
		writeError(w, 500, "storage_error", "unable to read group")
		return
	}
	writeJSON(w, 200, group)
}
func (a apiHandler) disable(w http.ResponseWriter, r *http.Request, name string) {
	err := a.store.DisableGroup(r.Context(), name)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, 404, "not_found", "group not found")
		return
	}
	if err != nil {
		writeError(w, 500, "storage_error", "unable to disable group")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (a apiHandler) resolve(w http.ResponseWriter, r *http.Request, name, actor string) {
	repositoryName := r.URL.Query().Get("repository")
	if repositoryName == "" {
		writeError(w, 400, "invalid_repository", "repository query parameter is required")
		return
	}
	member, err := a.resolver.Resolve(r.Context(), name, repositoryName, actor)
	if errors.Is(err, repository.ErrDisabled) {
		writeError(w, 409, "group_disabled", "group is disabled")
		return
	}
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, 404, "not_found", "no member can serve repository")
		return
	}
	if err != nil {
		writeError(w, 500, "resolver_error", "unable to resolve repository")
		return
	}
	writeJSON(w, 200, member)
}

func validateGroup(group repository.Group) error {
	if group.Name == "" || strings.Contains(group.Name, "/") {
		return errors.New("name must be a non-empty OCI namespace")
	}
	if len(group.Members) == 0 {
		return errors.New("at least one member is required")
	}
	positions := make(map[int]bool, len(group.Members))
	for _, member := range group.Members {
		if member.Name == "" || member.Endpoint == "" {
			return errors.New("member name and endpoint are required")
		}
		if member.Type != repository.MemberHosted && member.Type != repository.MemberProxy {
			return errors.New("member type must be hosted or proxy")
		}
		if member.Position < 0 || positions[member.Position] {
			return errors.New("member positions must be unique non-negative integers")
		}
		positions[member.Position] = true
	}
	for position := range group.Members {
		if !positions[position] {
			return errors.New("member positions must start at zero and be contiguous")
		}
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
