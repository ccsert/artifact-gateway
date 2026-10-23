package helm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	helmprotocol "github.com/ccsert/artifact-gateway/internal/protocol/helm"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

var repositoryName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func repositoryDTO(repo repository.HostedRepository) any {
	out := map[string]any{"id": repo.ID, "name": repo.Name, "format": "helm", "type": repo.Type, "preview": true, "version": repo.Version, "anonymousRead": repo.AnonymousRead, "sourceUrl": "/helm/" + url.PathEscape(repo.Name) + "/index.yaml", "provenanceTrust": "consumer-public-keyring"}
	status := upstreamStatus{Scheme: repository.UpstreamAuthSchemeNone}
	if repo.UpstreamAuth != nil {
		status.Scheme = repo.UpstreamAuth.Scheme
		status.Username = repo.UpstreamAuth.Username
		status.CredentialsConfigured = repo.UpstreamAuth.Secret != ""
	}
	out["upstreamAuth"] = status
	if repo.Type == repository.RepositoryTypeHosted {
		out["publicationApi"] = "/api/v2/previews/helm/repositories/" + repo.ID + "/charts/{chartName}/{version}"
	} else {
		out["endpoint"] = repo.Endpoint
		out["allowedHosts"] = append([]string{}, repo.AllowedHosts...)
		if repo.EgressProxy != nil {
			out["egressMode"] = repo.EgressProxy.Mode
		}
	}
	return out
}
func chartDTO(repo repository.HostedRepository, p repository.HelmPublication) any {
	cached := p.Size > 0
	out := map[string]any{"repositoryId": p.RepositoryID, "name": p.Name, "version": p.Version, "digest": p.Digest, "cached": cached, "publishedAt": p.PublishedAt, "chartUrl": assetPath(repo, p, false)}
	if cached {
		out["size"] = p.Size
		out["provenanceSize"] = p.ProvenanceSize
	}
	if repo.Type == repository.RepositoryTypeProxy {
		out["chartUrl"] = snapshotPath(repo.Name, p.SessionID, repository.HelmSourceEntry{Name: p.Name, Version: p.Version})
	}
	if cached && p.ProvenanceSize > 0 {
		out["provenanceDigest"] = p.ProvenanceDigest
		out["provenanceUrl"] = assetPath(repo, p, true)
		if repo.Type == repository.RepositoryTypeProxy {
			out["provenanceUrl"] = snapshotPath(repo.Name, p.SessionID, repository.HelmSourceEntry{Name: p.Name, Version: p.Version}) + ".prov"
		}
	}
	return out
}
func writeJSON(w http.ResponseWriter, r *http.Request, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_ = json.NewEncoder(w).Encode(value)
	}
}
func decodeBody(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(target) == nil && decoder.Decode(&struct{}{}) == io.EOF
}
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
	if strings.HasPrefix(r.URL.Path, "/api/v2/previews/helm/groups") {
		m.previewGroups(w, r, p)
		return
	}
	const prefix = "/api/v2/previews/helm/repositories"
	if r.URL.Path == prefix {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			w.WriteHeader(405)
			return
		}
		if !p.Admin {
			m.problem(w, 403, "access_denied")
			return
		}
		var in repositoryInput
		if !decodeBody(w, r, &in) || in.Version != "" {
			m.problem(w, 400, "invalid_repository")
			return
		}
		next, e := m.configureRepository(in, repository.HostedRepository{})
		if e != nil {
			m.problem(w, 400, "invalid_repository")
			return
		}
		ctx, release, e := m.nameLock(r.Context(), in.Name, "repository")
		if e != nil {
			m.failure(w, e)
			return
		}
		defer release()
		key := r.Header.Get("Idempotency-Key")
		if len(key) > 200 || strings.ContainsAny(key, "\x00\r\n") {
			m.problem(w, 400, "invalid_idempotency_key")
			return
		}
		if key == "" {
			key = uuid.NewString()
		}
		body, _ := json.Marshal(in)
		sum := sha256.Sum256(body)
		repo, replay, err := m.repos.CreateHostedRepositoryIdempotently(ctx, withRepositoryID(next), p.Actor, "helm-preview:"+key, hex.EncodeToString(sum[:]))
		if err != nil {
			m.failure(w, err)
			return
		}
		if !replay {
			_ = m.audit.RecordAudit(r.Context(), repository.AuditRecord{Actor: p.Actor, Repository: repo.Name, Format: string(repository.FormatHelm), Resource: repo.ID, Operation: "helm.preview.create", Outcome: repository.AuditResolved, Status: 201, OccurredAt: time.Now().UTC()})
		}
		w.Header().Set("Location", prefix+"/"+repo.ID)
		writeJSON(w, r, 201, repositoryDTO(repo))
		return
	}
	if !strings.HasPrefix(r.URL.Path, prefix+"/") {
		m.problem(w, 404, "not_found")
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, prefix+"/"), "/")
	if _, err := uuid.Parse(parts[0]); err != nil {
		m.problem(w, 404, "not_found")
		return
	}
	repo, err := m.repository(r.Context(), parts[0], true)
	if err != nil {
		m.failure(w, err)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodPut {
		m.replacePreviewRepository(w, r, repo, p)
		return
	}
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			w.WriteHeader(405)
			return
		}
		if _, ok = m.authorize(w, r, repo, authorization.RepositoryRead, "", true); !ok {
			return
		}
		writeJSON(w, r, 200, repositoryDTO(repo))
		return
	}
	if len(parts) == 2 && parts[1] == "capacity" {
		m.capacity(w, r, repo)
		return
	}
	if len(parts) == 2 && parts[1] == "quarantine-read-policy" {
		m.previewQuarantineReadPolicy(w, r, repo)
		return
	}
	if len(parts) == 5 && parts[1] == "charts" && parts[4] == "governance" {
		m.previewGovernance(w, r, repo, parts[2], parts[3])
		return
	}
	if parts[1] != "charts" {
		m.problem(w, 404, "not_found")
		return
	}
	if len(parts) == 2 && r.Method == http.MethodGet {
		principal, allowed := m.authorize(w, r, repo, authorization.RepositoryRead, "", true)
		if !allowed {
			return
		}
		all, e := m.previewCharts(r, repo, principal)
		if e != nil {
			m.failure(w, e)
			return
		}
		items := []any{}
		for _, in := range all {
			items = append(items, chartDTO(repo, in))
		}
		writeJSON(w, r, 200, map[string]any{"items": items})
		return
	}
	if len(parts) != 4 || !helmprotocol.ValidIdentity(parts[2], parts[3]) {
		m.problem(w, 404, "not_found")
		return
	}
	name, version := parts[2], parts[3]
	operation := authorization.RepositoryRead
	if r.Method == http.MethodPut {
		operation = authorization.RepositoryWrite
	} else if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	if _, ok = m.authorize(w, r, repo, operation, name, false); !ok {
		return
	}
	if r.Method == http.MethodPut {
		if repo.Type != repository.RepositoryTypeHosted {
			w.Header().Set("Allow", "GET")
			m.problem(w, 405, "read_only_source")
			return
		}
		m.upload(w, r, repo, name, version, p.Actor)
		return
	}
	in, err := m.previewChart(r, repo, name, version)
	if err != nil {
		m.failure(w, err)
		return
	}
	writeJSON(w, r, 200, chartDTO(repo, in))
}
func (m *Module) capacity(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository) {
	operation := authorization.RepositoryRead
	if r.Method == http.MethodPut {
		operation = authorization.RepositoryAdmin
	} else if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	p, ok := m.authorize(w, r, repo, operation, "", false)
	if !ok {
		return
	}
	var capacity repository.RepositoryCapacity
	var err error
	if r.Method == http.MethodPut {
		var in struct {
			QuotaBytes *int64 `json:"quotaBytes"`
		}
		if !decodeBody(w, r, &in) || in.QuotaBytes == nil || *in.QuotaBytes < 0 {
			m.problem(w, 400, "invalid_quota")
			return
		}
		capacity, err = m.capacities.ReplaceRepositoryCapacityQuota(r.Context(), repo.ID, *in.QuotaBytes)
	} else {
		capacity, err = m.capacities.GetRepositoryCapacity(r.Context(), repo.ID)
	}
	if err != nil {
		m.failure(w, err)
		return
	}
	if r.Method == http.MethodPut {
		_ = m.audit.RecordAudit(r.Context(), repository.AuditRecord{Actor: p.Actor, Repository: repo.Name, Format: string(repository.FormatHelm), Resource: repo.ID, Operation: "helm.preview.quota.configure", Outcome: repository.AuditResolved, Status: 200, OccurredAt: time.Now().UTC()})
	}
	writeJSON(w, r, 200, map[string]any{"repositoryId": repo.ID, "usedBytes": capacity.UsedBytes, "objectCount": capacity.ObjectCount, "quotaBytes": capacity.QuotaBytes})
}

func withRepositoryID(in repository.HostedRepository) repository.HostedRepository {
	in.ID = uuid.NewString()
	return in
}
