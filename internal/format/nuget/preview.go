package nuget

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	nugetprotocol "github.com/ccsert/artifact-gateway/internal/protocol/nuget"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/uploadspool"
	"github.com/google/uuid"
)

func (m *Module) Preview() http.Handler { return http.HandlerFunc(m.servePreview) }

type previewRepository struct {
	ID           string                     `json:"id"`
	Name         string                     `json:"name"`
	Format       string                     `json:"format"`
	Preview      bool                       `json:"preview"`
	SourceURL    string                     `json:"sourceUrl"`
	Type         repository.RepositoryType  `json:"type"`
	Version      string                     `json:"version"`
	Endpoint     string                     `json:"endpoint,omitempty"`
	AllowedHosts []string                   `json:"allowedHosts,omitempty"`
	UpstreamAuth *upstreamStatus            `json:"upstreamAuth,omitempty"`
	EgressMode   repository.EgressProxyMode `json:"egressMode,omitempty"`
}
type previewSession struct {
	ID           string     `json:"id"`
	RepositoryID string     `json:"repositoryId"`
	PackageID    string     `json:"packageId"`
	Version      string     `json:"version"`
	Digest       string     `json:"digest"`
	Size         int64      `json:"size"`
	State        string     `json:"state"`
	CreatedAt    time.Time  `json:"createdAt"`
	ExpiresAt    time.Time  `json:"expiresAt"`
	CommittedAt  *time.Time `json:"committedAt,omitempty"`
}
type previewPackage struct {
	GovernanceVersion       string    `json:"governanceVersion"`
	RepositoryID            string    `json:"repositoryId"`
	PackageID               string    `json:"packageId"`
	Version                 string    `json:"version"`
	Digest                  string    `json:"digest"`
	Size                    int64     `json:"size"`
	PublishedAt             time.Time `json:"publishedAt"`
	PackageURL              string    `json:"packageUrl"`
	RegistrationURL         string    `json:"registrationUrl"`
	SourceVersion           string    `json:"sourceVersion,omitempty"`
	UpstreamPackageURL      string    `json:"upstreamPackageUrl,omitempty"`
	UpstreamRegistrationURL string    `json:"upstreamRegistrationUrl,omitempty"`
}

func repositoryDTO(r *http.Request, repo repository.HostedRepository) previewRepository {
	out := previewRepository{ID: repo.ID, Name: repo.Name, Format: "nuget", Preview: true, SourceURL: sourceBase(r, repo) + "index.json", Type: repo.Type, Version: repo.Version, Endpoint: repo.Endpoint, AllowedHosts: repo.AllowedHosts}
	if repo.Type == repository.RepositoryTypeProxy {
		out.UpstreamAuth = &upstreamStatus{Scheme: repository.UpstreamAuthSchemeNone}
		if repo.UpstreamAuth != nil {
			out.UpstreamAuth = &upstreamStatus{Scheme: repo.UpstreamAuth.Scheme, Username: repo.UpstreamAuth.Username, CredentialsConfigured: repo.UpstreamAuth.Secret != ""}
		}
		if repo.EgressProxy != nil {
			out.EgressMode = repo.EgressProxy.Mode
		}
	}
	return out
}
func sessionDTO(in repository.NuGetSession) previewSession {
	out := previewSession{ID: in.ID, RepositoryID: in.RepositoryID, PackageID: in.PackageID, Version: in.Version, Digest: in.Digest, Size: in.Size, State: in.State, CreatedAt: in.CreatedAt, ExpiresAt: in.ExpiresAt}
	if !in.CommittedAt.IsZero() {
		out.CommittedAt = &in.CommittedAt
	}
	return out
}
func packageDTO(r *http.Request, repo repository.HostedRepository, in repository.NuGetPublication) previewPackage {
	return previewPackage{GovernanceVersion: in.Governance.Version, RepositoryID: in.RepositoryID, PackageID: in.PackageID, Version: in.Version, Digest: in.Digest, Size: in.Size, PublishedAt: in.PublishedAt, PackageURL: packageURL(r, repo, in.PackageID, in.Version), RegistrationURL: sourceBase(r, repo) + "registration/" + in.PackageID + "/" + in.Version + ".json", SourceVersion: in.SourceVersion, UpstreamPackageURL: in.SourceURL, UpstreamRegistrationURL: in.RegistrationURL}
}
func decodeBody(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return false
	}
	return true
}
func requestKey(r *http.Request) (string, bool) {
	key := r.Header.Get("Idempotency-Key")
	return key, key != "" && len(key) <= 200 && !strings.ContainsAny(key, "\x00\r\n")
}

var previewRepositoryName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func (m *Module) servePreview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	p, ok := m.auth.AuthenticateContext(r.Context(), r.Header.Get("Authorization"))
	if !ok {
		m.problem(w, 401, "authentication_required")
		return
	}
	if reason := p.AccountStateReason(); reason != "" {
		m.problem(w, 403, reason)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/v2/previews/nuget/replication-jobs") {
		m.previewReplication(w, r, p)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/v2/previews/nuget/groups") {
		m.previewGroups(w, r, p)
		return
	}
	const prefix = "/api/v2/previews/nuget/repositories"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		m.problem(w, 404, "not_found")
		return
	}
	tail := strings.TrimPrefix(r.URL.Path, prefix)
	if tail == "" {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		if !p.Admin {
			m.problem(w, 403, "access_denied")
			return
		}
		var input repositoryInput
		if !decodeBody(w, r, &input) || !previewRepositoryName.MatchString(input.Name) {
			m.problem(w, 400, "invalid_repository")
			return
		}
		key, hasKey := requestKey(r)
		if !hasKey {
			if r.Header.Get("Idempotency-Key") != "" {
				m.problem(w, 400, "invalid_idempotency_key")
				return
			}
			key = uuid.NewString()
		}
		configured, err := m.configureRepository(input, repository.HostedRepository{})
		if err != nil {
			m.problem(w, 400, "invalid_source_configuration")
			return
		}
		ctx, release, err := m.nameLock(r.Context(), input.Name, "repository")
		if err != nil {
			m.failure(w, err)
			return
		}
		defer release()
		body, _ := json.Marshal(input)
		sum := sha256.Sum256(body)
		configured.ID = uuid.NewString()
		repo, replay, err := m.repos.CreateHostedRepositoryIdempotently(ctx, configured, p.Actor, "nuget-preview:"+key, hex.EncodeToString(sum[:]))
		if err != nil {
			m.failure(w, err)
			return
		}
		if !replay {
			_ = m.audit.RecordAudit(r.Context(), repository.AuditRecord{Actor: p.Actor, Repository: repo.Name, Format: string(repository.FormatNuGet), Resource: repo.ID, Operation: "nuget.preview.create", Outcome: repository.AuditResolved, Status: 201, OccurredAt: time.Now().UTC()})
		}
		w.Header().Set("Location", prefix+"/"+repo.ID)
		writeJSON(w, r, 201, repositoryDTO(r, repo))
		return
	}
	if !strings.HasPrefix(tail, "/") {
		m.problem(w, 404, "not_found")
		return
	}
	parts := strings.Split(strings.TrimPrefix(tail, "/"), "/")
	if _, err := uuid.Parse(parts[0]); err != nil {
		m.problem(w, 404, "not_found")
		return
	}
	repo, err := m.repository(r.Context(), parts[0], true)
	if err != nil {
		m.failure(w, err)
		return
	}
	if len(parts) == 1 {
		if r.Method == http.MethodPut {
			m.replacePreviewRepository(w, r, repo, p)
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(405)
			return
		}
		if _, ok = m.authorizeCollection(w, r, repo, authorization.RepositoryRead); !ok {
			return
		}
		writeJSON(w, r, 200, repositoryDTO(r, repo))
		return
	}
	if parts[1] == "packages" {
		if len(parts) == 5 && parts[4] == "governance" {
			m.previewGovernance(w, r, repo, parts[2:4])
			return
		}
		m.previewPackages(w, r, repo, parts[2:])
		return
	}
	if parts[1] == "quarantine-read-policy" && len(parts) == 2 {
		m.previewQuarantineReadPolicy(w, r, repo)
		return
	}
	if parts[1] == "capacity" && len(parts) == 2 {
		m.previewCapacity(w, r, repo)
		return
	}
	if parts[1] != "publication-sessions" {
		m.problem(w, 404, "not_found")
		return
	}
	if repo.Type != repository.RepositoryTypeHosted {
		m.problem(w, 405, "read_only_source")
		return
	}
	if len(parts) == 2 {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		var input publicationRequest
		key, validKey := requestKey(r)
		if !validKey || !decodeBody(w, r, &input) {
			m.problem(w, 400, "invalid_request")
			return
		}
		id, err := nugetprotocol.NormalizeID(input.PackageID)
		if err != nil {
			m.problem(w, 400, "invalid_package")
			return
		}
		if _, ok = m.authorize(w, r, repo, authorization.RepositoryWrite, id); !ok {
			return
		}
		session, err := m.reserve(r.Context(), repo, p.Actor, key, input)
		if err != nil {
			m.failure(w, err)
			return
		}
		if session.Actor != p.Actor {
			m.problem(w, 409, "nuget_session_owner_conflict")
			return
		}
		writeJSON(w, r, 201, sessionDTO(session))
		return
	}
	if len(parts) > 4 {
		m.problem(w, 404, "not_found")
		return
	}
	session, err := m.store.GetNuGetSession(r.Context(), parts[2])
	if err != nil || session.RepositoryID != repo.ID {
		m.problem(w, 404, "not_found")
		return
	}
	operation := authorization.RepositoryWrite
	if r.Method == http.MethodGet {
		operation = authorization.RepositoryRead
	}
	if _, ok = m.authorize(w, r, repo, operation, session.PackageID); !ok {
		return
	}
	if session.Actor != p.Actor && !p.Admin {
		m.problem(w, 403, "session_owner_required")
		return
	}
	if len(parts) == 3 {
		if r.Method != http.MethodGet {
			w.WriteHeader(405)
			return
		}
		writeJSON(w, r, 200, sessionDTO(session))
		return
	}
	if session.Actor != p.Actor {
		m.problem(w, 403, "session_owner_required")
		return
	}
	ctx, unlock, err := m.store.LockNuGetPublication(r.Context(), repo.ID, session.PackageID, session.Version)
	if err != nil {
		m.failure(w, err)
		return
	}
	defer unlock()
	session, err = m.store.GetNuGetSession(ctx, session.ID)
	if err != nil {
		m.failure(w, err)
		return
	}
	switch {
	case parts[3] == "package" && r.Method == http.MethodPut:
		r.Body = http.MaxBytesReader(w, r.Body, m.maxBytes)
		spool, err := uploadspool.Read(r.Body, m.maxBytes)
		if err != nil {
			m.problem(w, 413, "package_too_large")
			return
		}
		defer func() { _ = spool.Close() }()
		session, err = m.stage(ctx, session, spool)
		if err != nil {
			m.failure(w, err)
			return
		}
		writeJSON(w, r, 200, sessionDTO(session))
	case parts[3] == "commit" && r.Method == http.MethodPost:
		publication, err := m.commit(ctx, session, p.Actor)
		if err != nil {
			m.failure(w, err)
			return
		}
		writeJSON(w, r, 200, packageDTO(r, repo, publication))
	default:
		w.WriteHeader(405)
	}
}

func (m *Module) previewCapacity(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository) {
	operation := authorization.RepositoryRead
	if r.Method == http.MethodPut {
		operation = authorization.RepositoryAdmin
	} else if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	principal, ok := m.authorize(w, r, repo, operation, "")
	if !ok {
		return
	}
	if nilPort(m.capacities) {
		m.problem(w, 503, "nuget_capacity_unavailable")
		return
	}
	capacity, err := m.capacities.GetRepositoryCapacity(r.Context(), repo.ID)
	if err == nil && r.Method == http.MethodPut {
		var input struct {
			QuotaBytes int64 `json:"quotaBytes"`
		}
		if !decodeBody(w, r, &input) || input.QuotaBytes < 0 {
			m.problem(w, 400, "invalid_quota")
			return
		}
		capacity, err = m.capacities.ReplaceRepositoryCapacityQuota(r.Context(), repo.ID, input.QuotaBytes)
	}
	if err != nil {
		m.failure(w, err)
		return
	}
	if r.Method == http.MethodPut {
		_ = m.audit.RecordAudit(r.Context(), repository.AuditRecord{Actor: principal.Actor, Repository: repo.Name, Format: string(repository.FormatNuGet), Resource: repo.ID, Operation: "nuget.preview.quota.configure", Outcome: repository.AuditResolved, Status: 200, OccurredAt: time.Now().UTC()})
	}
	writeJSON(w, r, 200, map[string]any{"repositoryId": repo.ID, "usedBytes": capacity.UsedBytes, "objectCount": capacity.ObjectCount, "quotaBytes": capacity.QuotaBytes})
}

func (m *Module) previewPackages(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, parts []string) {
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	if len(parts) == 2 {
		id, err := nugetprotocol.NormalizeID(parts[0])
		if err != nil {
			m.problem(w, 404, "not_found")
			return
		}
		version, err := nugetprotocol.NormalizeVersion(parts[1])
		if err != nil {
			m.problem(w, 404, "not_found")
			return
		}
		if _, ok := m.authorize(w, r, repo, authorization.RepositoryRead, id); !ok {
			return
		}
		publication, err := m.store.GetNuGetPublication(r.Context(), repo.ID, id, strings.ToLower(version))
		if err != nil {
			m.failure(w, err)
			return
		}
		writeJSON(w, r, 200, packageDTO(r, repo, publication))
		return
	}
	if len(parts) != 0 {
		m.problem(w, 404, "not_found")
		return
	}
	principal, ok := m.authorizeCollection(w, r, repo, authorization.RepositoryRead)
	if !ok {
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			m.problem(w, 400, "invalid_page")
			return
		}
		limit = n
	}
	cursor := ""
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		if len(raw) > 1024 {
			m.problem(w, 400, "invalid_cursor")
			return
		}
		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil {
			m.problem(w, 400, "invalid_cursor")
			return
		}
		cursor = string(decoded)
	}
	items := []previewPackage{}
	next := ""
	for len(items) < limit {
		page, err := m.store.ListNuGetPublications(r.Context(), repo.ID, "", min(64, limit-len(items)), cursor)
		if err != nil {
			m.failure(w, err)
			return
		}
		if len(page) == 0 {
			break
		}
		for _, in := range page {
			cursor = in.PackageID + "@" + in.Version
			if m.access.AuthorizeResource(r.Context(), principal, repo, authorization.RepositoryRead, in.PackageID).Allowed {
				items = append(items, packageDTO(r, repo, in))
			}
		}
		if len(items) == limit {
			next = base64.RawURLEncoding.EncodeToString([]byte(cursor))
			break
		}
	}
	writeJSON(w, r, 200, map[string]any{"items": items, "nextCursor": next})
}
