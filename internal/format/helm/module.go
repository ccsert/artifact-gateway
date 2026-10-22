// Package helm implements the opt-in traditional HTTP Helm Hosted preview.
package helm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	formatmodule "github.com/ccsert/artifact-gateway/internal/format"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	helmprotocol "github.com/ccsert/artifact-gateway/internal/protocol/helm"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

type RepositoryStore interface {
	GetHostedRepository(context.Context, string) (repository.HostedRepository, error)
	GetHostedRepositoryByName(context.Context, string) (repository.HostedRepository, error)
	CreateHostedRepositoryIdempotently(context.Context, repository.HostedRepository, string, string, string) (repository.HostedRepository, bool, error)
}
type Auditor interface {
	RecordAudit(context.Context, repository.AuditRecord) error
}
type AnonymousPolicy interface {
	GetAnonymousAccessPolicy(context.Context) (repository.AnonymousAccessPolicy, error)
}
type CapacityStore interface {
	GetRepositoryCapacity(context.Context, string) (repository.RepositoryCapacity, error)
	ReplaceRepositoryCapacityQuota(context.Context, string, int64) (repository.RepositoryCapacity, error)
}
type Options struct {
	Store         repository.NativeHelmStore
	Repositories  RepositoryStore
	Objects       objectstore.Store
	Authenticator authorization.Authenticator
	Authorizer    authorization.RepositoryAuthorizer
	Audit         Auditor
	Anonymous     AnonymousPolicy
	Capacities    CapacityStore
	MaxChartBytes int64
}
type Module struct {
	store      repository.NativeHelmStore
	repos      RepositoryStore
	objects    objectstore.Store
	auth       authorization.Authenticator
	access     authorization.RepositoryAuthorizer
	audit      Auditor
	anonymous  AnonymousPolicy
	capacities CapacityStore
	maxBytes   int64
}

func nilPort(v any) bool {
	if v == nil {
		return true
	}
	switch reflect.ValueOf(v).Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Func, reflect.Slice, reflect.Map, reflect.Chan:
		return reflect.ValueOf(v).IsNil()
	}
	return false
}
func New(o Options) (*Module, error) {
	for _, port := range []any{o.Store, o.Repositories, o.Audit, o.Anonymous, o.Capacities, o.Authorizer.Grants} {
		if nilPort(port) {
			return nil, errors.New("helm preview requires explicit publication, repository, authorization, audit, anonymous policy and capacity ports")
		}
	}
	if err := objectstore.Validate(o.Objects); err != nil {
		return nil, err
	}
	if o.MaxChartBytes == 0 {
		o.MaxChartBytes = helmprotocol.MaxChartBytes
	}
	if o.MaxChartBytes < 1 || o.MaxChartBytes > helmprotocol.MaxChartBytes {
		return nil, errors.New("helm compressed chart limit must be between 1 and 64 MiB")
	}
	return &Module{store: o.Store, repos: o.Repositories, objects: o.Objects, auth: o.Authenticator, access: o.Authorizer, audit: o.Audit, anonymous: o.Anonymous, capacities: o.Capacities, maxBytes: o.MaxChartBytes}, nil
}
func (*Module) Format() repository.Format { return repository.FormatHelm }
func (m *Module) Routes() []formatmodule.Route {
	return []formatmodule.Route{{Pattern: "/helm/", Handler: m}}
}
func (m *Module) Preview() http.Handler { return http.HandlerFunc(m.servePreview) }
func (m *Module) principal(r *http.Request) (authorization.Principal, bool) {
	if _, password, basic := r.BasicAuth(); basic {
		return m.auth.AuthenticateContext(r.Context(), "Bearer "+password)
	}
	return m.auth.AuthenticateContext(r.Context(), r.Header.Get("Authorization"))
}
func (m *Module) authorize(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, operation authorization.RepositoryOperation, resource string, collection bool) (authorization.Principal, bool) {
	p, ok := m.principal(r)
	if !ok && r.Header.Get("Authorization") == "" && operation == authorization.RepositoryRead && repo.AnonymousRead {
		policy, err := m.anonymous.GetAnonymousAccessPolicy(r.Context())
		if err == nil && policy.Enabled {
			return authorization.Principal{Actor: "anonymous"}, true
		}
	}
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway Helm"`)
		m.problem(w, 401, "authentication_required")
		return p, false
	}
	decision := m.access.AuthorizeResource(r.Context(), p, repo, operation, resource)
	if !decision.Allowed && collection && p.AccountStateReason() == "" {
		if set, err := m.access.Grants.GetRepositoryGrants(r.Context(), repo.ID); err == nil {
			for _, grant := range set.Grants {
				if grant.Principal == p.Actor && grant.ResourcePrefix != "" {
					candidate := m.access.AuthorizeResource(r.Context(), p, repo, operation, grant.ResourcePrefix)
					if candidate.Allowed {
						decision = candidate
						break
					}
				}
			}
		}
	}
	if !decision.Allowed {
		_ = m.audit.RecordAudit(r.Context(), repository.AuditRecord{Actor: p.Actor, Repository: repo.Name, GroupName: repo.Name, Format: string(repository.FormatHelm), Resource: resource, Operation: string(operation), Outcome: repository.AuditAccessDenied, Status: 403, OccurredAt: time.Now().UTC(), AuthorizationSource: decision.Source, AuthorizationReason: decision.Reason})
		code := "access_denied"
		if reason := p.AccountStateReason(); reason != "" {
			code = reason
		}
		m.problem(w, 403, code)
		return p, false
	}
	return p, true
}
func (m *Module) allowed(ctx context.Context, p authorization.Principal, repo repository.HostedRepository, name string) bool {
	return p.Actor == "anonymous" && p.AuthenticationKind == "" || m.access.AuthorizeResource(ctx, p, repo, authorization.RepositoryRead, name).Allowed
}
func (m *Module) repository(ctx context.Context, key string, byID bool) (repository.HostedRepository, error) {
	var repo repository.HostedRepository
	var err error
	if byID {
		repo, err = m.repos.GetHostedRepository(ctx, key)
	} else {
		repo, err = m.repos.GetHostedRepositoryByName(ctx, key)
	}
	if err != nil {
		return repository.HostedRepository{}, err
	}
	if repo.Format != repository.FormatHelm || repo.Type != repository.RepositoryTypeHosted || repo.State != repository.RepositoryActive {
		return repository.HostedRepository{}, repository.ErrNotFound
	}
	return repo, nil
}
func (m *Module) problem(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": code, "status": status, "code": code, "detail": "Helm Hosted preview operation failed"})
}
func (m *Module) failure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		m.problem(w, 404, "not_found")
	case errors.Is(err, repository.ErrInvalidHelmPublication), errors.Is(err, helmprotocol.ErrInvalidChart):
		m.problem(w, 400, "invalid_chart")
	case errors.Is(err, repository.ErrHelmPublicationConflict), errors.Is(err, repository.ErrIdempotencyConflict), errors.Is(err, repository.ErrNameExists):
		m.problem(w, 409, "helm_identity_conflict")
	case errors.Is(err, repository.ErrQuotaExceeded), repository.IsQuotaExceeded(err):
		m.problem(w, 409, "quota_exceeded")
	case errors.Is(err, repository.ErrHelmSessionExpired):
		m.problem(w, 410, "helm_session_expired")
	case errors.Is(err, errInventoryLimit):
		m.problem(w, 422, "helm_preview_inventory_limit")
	default:
		m.problem(w, 503, "helm_unavailable")
	}
}
func (m *Module) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/helm/") || strings.ContainsAny(r.URL.Path, "\\\x00") {
		m.problem(w, 404, "not_found")
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/helm/"), "/")
	if len(parts) < 2 {
		m.problem(w, 404, "not_found")
		return
	}
	repo, err := m.repository(r.Context(), parts[0], false)
	if err != nil {
		m.failure(w, err)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(405)
		return
	}
	switch {
	case len(parts) == 2 && parts[1] == "index.yaml":
		m.index(w, r, repo)
	case len(parts) == 5 && parts[1] == "charts":
		m.asset(w, r, repo, parts[2], parts[3], parts[4])
	default:
		m.problem(w, 404, "not_found")
	}
}
