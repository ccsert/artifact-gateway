package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
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
	return r.resolve(ctx, groupName, repositoryName, actor, func(member repository.Member) bool {
		return r.Adapter.Available(ctx, member, repositoryName)
	})
}

func (r Resolver) ResolveOCIMembers(ctx context.Context, groupName, repositoryName, actor string) ([]repository.Member, error) {
	resolved := false
	defer func() {
		if !resolved {
			r.Metrics.failed.Add(1)
		}
	}()
	group, err := r.loadActiveGroup(ctx, groupName, repositoryName, actor)
	if err != nil {
		return nil, err
	}
	var hosted, proxy []repository.Member
	for _, member := range group.Members {
		if !r.Adapter.Available(ctx, member, repositoryName) {
			continue
		}
		switch member.Type {
		case repository.MemberHosted:
			hosted = append(hosted, member)
		case repository.MemberProxy:
			proxy = append(proxy, member)
		}
	}
	if len(hosted)+len(proxy) == 0 {
		if err := r.audit(ctx, groupName, repositoryName, "", repository.AuditNotFound, actor); err != nil {
			return nil, err
		}
		return nil, repository.ErrNotFound
	}
	resolved = true
	return append(hosted, proxy...), nil
}

func (r Resolver) RecordOCIResolution(ctx context.Context, groupName, repositoryName, memberName, actor string) error {
	if err := r.audit(ctx, groupName, repositoryName, memberName, repository.AuditResolved, actor); err != nil {
		r.Metrics.failed.Add(1)
		return err
	}
	r.Metrics.resolved.Add(1)
	return nil
}

func (r Resolver) RecordOCIFailure(ctx context.Context, groupName, repositoryName, memberName, actor string, outcome repository.AuditOutcome) error {
	return r.audit(ctx, groupName, repositoryName, memberName, outcome, actor)
}

func (r Resolver) RecordOCIRequestFailure() {
	r.Metrics.failed.Add(1)
}

func (r Resolver) resolve(ctx context.Context, groupName, repositoryName, actor string, eligible func(repository.Member) bool) (repository.Member, error) {
	resolved := false
	defer func() {
		if !resolved {
			r.Metrics.failed.Add(1)
		}
	}()
	group, err := r.loadActiveGroup(ctx, groupName, repositoryName, actor)
	if err != nil {
		return repository.Member{}, err
	}
	for _, member := range group.Members {
		if eligible(member) {
			if err := r.audit(ctx, groupName, repositoryName, member.Name, repository.AuditResolved, actor); err != nil {
				return repository.Member{}, err
			}
			r.Metrics.resolved.Add(1)
			resolved = true
			return member, nil
		}
	}
	if err := r.audit(ctx, groupName, repositoryName, "", repository.AuditNotFound, actor); err != nil {
		return repository.Member{}, err
	}
	return repository.Member{}, repository.ErrNotFound
}

func (r Resolver) loadActiveGroup(ctx context.Context, groupName, repositoryName, actor string) (repository.Group, error) {
	group, err := r.Store.GetGroup(ctx, groupName)
	if err != nil {
		outcome := repository.AuditStorageError
		if errors.Is(err, repository.ErrNotFound) {
			outcome = repository.AuditNotFound
		}
		if auditErr := r.audit(ctx, groupName, repositoryName, "", outcome, actor); auditErr != nil {
			return repository.Group{}, auditErr
		}
		return repository.Group{}, err
	}
	if !group.Enabled {
		if auditErr := r.audit(ctx, groupName, repositoryName, "", repository.AuditGroupDisabled, actor); auditErr != nil {
			return repository.Group{}, auditErr
		}
		return repository.Group{}, repository.ErrDisabled
	}
	return group, nil
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

type Principal struct {
	Actor string
	Admin bool
}

type Authenticator struct {
	AdminToken    string
	ResolverToken string
	AdminActor    string
	ResolverActor string
}

func (a Authenticator) Authenticate(header string) (Principal, bool) {
	const bearer = "Bearer "
	if !strings.HasPrefix(header, bearer) {
		return Principal{}, false
	}
	token := strings.TrimPrefix(header, bearer)
	if tokenMatches(token, a.AdminToken) {
		return Principal{Actor: a.AdminActor, Admin: true}, true
	}
	if tokenMatches(token, a.ResolverToken) {
		return Principal{Actor: a.ResolverActor}, true
	}
	if actor, ok := a.tokenActor(token); ok {
		return Principal{Actor: actor}, true
	}
	return Principal{}, false
}

func (a Authenticator) IssueToken(actor string) string {
	expiresAt := time.Now().UTC().Add(5 * time.Minute).Unix()
	payload := "v1." + base64.RawURLEncoding.EncodeToString([]byte(actor)) + "." + strconv.FormatInt(expiresAt, 10)
	mac := hmac.New(sha256.New, []byte(a.ResolverToken))
	_, _ = mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a Authenticator) tokenActor(token string) (string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != "v1" || a.ResolverToken == "" {
		return "", false
	}
	actor, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(actor) == 0 {
		return "", false
	}
	expiresAt, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || time.Now().UTC().Unix() >= expiresAt {
		return "", false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, []byte(a.ResolverToken))
	_, _ = mac.Write([]byte(strings.Join(parts[:3], ".")))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return "", false
	}
	return string(actor), true
}

func tokenMatches(value, expected string) bool {
	return expected != "" && len(value) == len(expected) && subtle.ConstantTimeCompare([]byte(value), []byte(expected)) == 1
}

type GatewayStore interface {
	repository.Store
	repository.MavenStore
}

func NewGatewayHandler(dependencies Dependencies, store GatewayStore, adapter Adapter, authenticator Authenticator, ociClients ...OCIClient) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /readyz", dependencies.ready)
	metrics := &Metrics{}
	resolver := Resolver{Store: store, Adapter: adapter, Metrics: metrics}
	api := apiHandler{store: store, resolver: resolver, authenticator: authenticator}
	ociClient := OCIClient(GiteaClient{})
	if len(ociClients) > 0 {
		ociClient = ociClients[0]
	}
	mavenClient := MavenClient(GiteaClient{})
	if client, ok := ociClient.(MavenClient); ok {
		mavenClient = client
	}
	oci := OCIHandler{Resolver: resolver, Client: ociClient, Authenticator: authenticator}
	mux.Handle("GET /metrics", http.HandlerFunc(metrics.Handler))
	mux.Handle("/api/v1/oci/groups", api)
	mux.Handle("/api/v1/oci/groups/", api)
	mux.Handle("/api/v1/maven/groups", mavenAPIHandler{store: store, authenticator: authenticator})
	mux.Handle("/api/v1/maven/groups/", mavenAPIHandler{store: store, authenticator: authenticator})
	mux.Handle("/v2/", oci)
	mux.Handle("/maven/", MavenHandler{Store: store, Authenticator: authenticator, Client: mavenClient, Metrics: metrics})
	mux.HandleFunc("GET /auth/token", oci.Token)
	return mux
}

type mavenAPIHandler struct {
	store         repository.MavenStore
	authenticator Authenticator
}

func (a mavenAPIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
		return
	}
	if !principal.Admin {
		writeError(w, http.StatusForbidden, "forbidden", "administrator permission required")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/maven/groups")
	if path == "" || path == "/" {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
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
		created, err := a.store.CreateMavenGroup(r.Context(), group)
		if errors.Is(err, repository.ErrNameExists) {
			writeError(w, http.StatusConflict, "group_exists", "group name already exists")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "unable to create group")
			return
		}
		writeJSON(w, http.StatusCreated, created)
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 1 && r.Method == http.MethodGet {
		group, err := a.store.GetMavenGroup(r.Context(), parts[0])
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "group not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "unable to read group")
			return
		}
		writeJSON(w, http.StatusOK, group)
		return
	}
	if len(parts) == 2 && parts[1] == "disable" && r.Method == http.MethodPost {
		err := a.store.DisableMavenGroup(r.Context(), parts[0])
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "group not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "unable to disable group")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "resource not found")
}

type apiHandler struct {
	store         repository.Store
	resolver      Resolver
	authenticator Authenticator
}

func (a apiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.authenticator.Authenticate(r.Header.Get("Authorization"))
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
		if !a.requireAdmin(w, principal) {
			return
		}
		a.create(w, r)
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 1 && r.Method == http.MethodGet {
		if !a.requireAdmin(w, principal) {
			return
		}
		a.get(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "disable" && r.Method == http.MethodPost {
		if !a.requireAdmin(w, principal) {
			return
		}
		a.disable(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "resolve" && r.Method == http.MethodGet {
		a.resolve(w, r, parts[0], principal.Actor)
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "resource not found")
}

func (a apiHandler) requireAdmin(w http.ResponseWriter, principal Principal) bool {
	if principal.Admin {
		return true
	}
	writeError(w, http.StatusForbidden, "forbidden", "administrator permission required")
	return false
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
		a.resolver.Metrics.failed.Add(1)
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
