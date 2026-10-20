// Package nuget implements the explicitly provisioned NuGet Hosted preview.
// Protocol publication and metadata stay here; no Gateway or SQL implementation
// is reachable through this module.
package nuget

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	formatmodule "github.com/ccsert/artifact-gateway/internal/format"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
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
	Store           repository.NativeNuGetStore
	Repositories    RepositoryStore
	Objects         objectstore.Store
	Authenticator   authorization.Authenticator
	Authorizer      authorization.RepositoryAuthorizer
	Audit           Auditor
	Anonymous       AnonymousPolicy
	Capacities      CapacityStore
	MaxPackageBytes int64
}
type Module struct {
	store      repository.NativeNuGetStore
	repos      RepositoryStore
	objects    objectstore.Store
	auth       authorization.Authenticator
	access     authorization.RepositoryAuthorizer
	audit      Auditor
	anonymous  AnonymousPolicy
	capacities CapacityStore
	maxBytes   int64
}

func nilPort(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Func, reflect.Slice, reflect.Chan:
		return v.IsNil()
	}
	return false
}
func New(o Options) (*Module, error) {
	if nilPort(o.Store) || nilPort(o.Repositories) || nilPort(o.Audit) {
		return nil, errors.New("NuGet preview requires publication, repository and audit ports")
	}
	for _, port := range []any{o.Anonymous, o.Capacities, o.Authorizer.Grants} {
		if port != nil && nilPort(port) {
			return nil, errors.New("NuGet optional capability is typed nil")
		}
	}
	if err := objectstore.Validate(o.Objects); err != nil {
		return nil, err
	}
	if o.MaxPackageBytes == 0 {
		o.MaxPackageBytes = 256 << 20
	}
	if o.MaxPackageBytes < 1 || o.MaxPackageBytes > 256<<20 {
		return nil, errors.New("NuGet package limit must be between 1 and 256 MiB")
	}
	return &Module{store: o.Store, repos: o.Repositories, objects: o.Objects, auth: o.Authenticator, access: o.Authorizer, audit: o.Audit, anonymous: o.Anonymous, capacities: o.Capacities, maxBytes: o.MaxPackageBytes}, nil
}
func (*Module) Format() repository.Format { return repository.FormatNuGet }
func (m *Module) Routes() []formatmodule.Route {
	return []formatmodule.Route{{Pattern: "/nuget/", Handler: m}}
}

func (m *Module) principal(r *http.Request) (authorization.Principal, bool) {
	header := r.Header.Get("Authorization")
	key := r.Header.Get("X-NuGet-ApiKey")
	var p authorization.Principal
	var ok bool
	if _, password, basic := r.BasicAuth(); basic {
		p, ok = m.auth.AuthenticateContext(r.Context(), "Bearer "+password)
	} else if header != "" {
		p, ok = m.auth.AuthenticateContext(r.Context(), header)
	}
	if key != "" {
		kp, kok := m.auth.AuthenticateContext(r.Context(), "Bearer "+key)
		if !kok || header != "" && (!ok || kp.Actor != p.Actor) {
			return authorization.Principal{}, false
		}
		p, ok = kp, kok
	}
	return p, ok
}

func (m *Module) authorize(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, operation authorization.RepositoryOperation, resource string) (authorization.Principal, bool) {
	return m.authorizeRequest(w, r, repo, operation, resource, false)
}

// Collection endpoints expose protocol locations or individually filtered
// entries. A package-prefix grant is sufficient to discover those endpoints;
// every package read or write still checks its complete canonical identity.
func (m *Module) authorizeCollection(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, operation authorization.RepositoryOperation) (authorization.Principal, bool) {
	return m.authorizeRequest(w, r, repo, operation, "", true)
}

func (m *Module) authorizeRequest(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, operation authorization.RepositoryOperation, resource string, collection bool) (authorization.Principal, bool) {
	p, ok := m.principal(r)
	if !ok && r.Header.Get("Authorization") == "" && r.Header.Get("X-NuGet-ApiKey") == "" && operation == authorization.RepositoryRead && repo.AnonymousRead && m.anonymous != nil {
		policy, err := m.anonymous.GetAnonymousAccessPolicy(r.Context())
		if err == nil && policy.Enabled {
			return authorization.Principal{Actor: "anonymous"}, true
		}
	}
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway NuGet"`)
		m.problem(w, 401, "authentication_required")
		return p, false
	}
	decision := m.access.AuthorizeResource(r.Context(), p, repo, operation, resource)
	if !decision.Allowed && collection && p.AccountStateReason() == "" && m.access.Grants != nil {
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
		_ = m.audit.RecordAudit(r.Context(), repository.AuditRecord{Actor: p.Actor, Repository: repo.Name, GroupName: repo.Name, Format: string(repository.FormatNuGet), Resource: resource, Operation: string(operation), Outcome: repository.AuditAccessDenied, Status: 403, OccurredAt: time.Now().UTC(), AuthorizationSource: decision.Source, AuthorizationReason: decision.Reason})
		code := "access_denied"
		if reason := p.AccountStateReason(); reason != "" {
			code = reason
		}
		m.problem(w, 403, code)
		return p, false
	}
	return p, true
}

func (m *Module) repository(ctx context.Context, idOrName string, byID bool) (repository.HostedRepository, error) {
	var repo repository.HostedRepository
	var err error
	if byID {
		repo, err = m.repos.GetHostedRepository(ctx, idOrName)
	} else {
		repo, err = m.repos.GetHostedRepositoryByName(ctx, idOrName)
	}
	if err != nil || repo.Format != repository.FormatNuGet || repo.Type != repository.RepositoryTypeHosted || repo.State != repository.RepositoryActive {
		return repository.HostedRepository{}, repository.ErrNotFound
	}
	return repo, nil
}

func origin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return (&url.URL{Scheme: scheme, Host: r.Host}).String()
}
func sourceBase(r *http.Request, repo repository.HostedRepository) string {
	return origin(r) + "/nuget/" + url.PathEscape(repo.Name) + "/v3/"
}
func packageURL(r *http.Request, repo repository.HostedRepository, id, version string) string {
	return sourceBase(r, repo) + "flat/" + id + "/" + version + "/" + id + "." + version + ".nupkg"
}
func (m *Module) problem(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/problem+json")
	writeJSON(w, nil, status, map[string]any{"type": "about:blank", "title": code, "status": status, "code": code, "detail": "NuGet Hosted preview operation failed"})
}
func writeJSON(w http.ResponseWriter, r *http.Request, status int, value any) {
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		w.Header().Set("Cache-Control", "private, no-cache")
	}
	w.WriteHeader(status)
	if r == nil || r.Method != http.MethodHead {
		_ = json.NewEncoder(w).Encode(value)
	}
}
func (m *Module) failure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		m.problem(w, 404, "not_found")
	case errors.Is(err, repository.ErrNuGetPublicationConflict), errors.Is(err, repository.ErrIdempotencyConflict), errors.Is(err, repository.ErrNameExists):
		m.problem(w, 409, "nuget_identity_conflict")
	case errors.Is(err, repository.ErrQuotaExceeded), repository.IsQuotaExceeded(err):
		m.problem(w, 409, "quota_exceeded")
	case errors.Is(err, repository.ErrNuGetSessionExpired):
		m.problem(w, 410, "nuget_session_expired")
	case errors.Is(err, repository.ErrNuGetSessionState):
		m.problem(w, 409, "nuget_session_not_staged")
	case errors.Is(err, repository.ErrInvalidNuGetPublication):
		m.problem(w, 400, "invalid_package")
	case errors.Is(err, errMetadataLimit):
		m.problem(w, 422, "nuget_preview_metadata_limit")
	default:
		m.problem(w, 503, "nuget_unavailable")
	}
}

func (m *Module) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/nuget/"), "/")
	if !strings.HasPrefix(r.URL.Path, "/nuget/") || len(parts) < 2 || strings.ContainsAny(r.URL.RawPath, "\\") {
		m.problem(w, 404, "not_found")
		return
	}
	repo, err := m.repository(r.Context(), parts[0], false)
	if err != nil {
		m.failure(w, err)
		return
	}
	if len(parts) == 5 && parts[1] == "api" && parts[2] == "v2" && parts[3] == "package" && parts[4] == "" {
		m.push(w, r, repo)
		return
	}
	if parts[1] != "v3" || len(parts) < 3 {
		m.problem(w, 404, "not_found")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(405)
		return
	}
	m.read(w, r, repo, parts[2:])
}
