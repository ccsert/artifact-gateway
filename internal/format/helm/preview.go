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
	return map[string]any{"id": repo.ID, "name": repo.Name, "format": "helm", "type": "hosted", "preview": true, "version": repo.Version, "sourceUrl": "/helm/" + url.PathEscape(repo.Name) + "/index.yaml", "publicationApi": "/api/v2/previews/helm/repositories/" + repo.ID + "/charts/{chartName}/{version}", "provenanceTrust": "consumer-public-keyring"}
}
func chartDTO(repo repository.HostedRepository, p repository.HelmPublication) any {
	out := map[string]any{"repositoryId": p.RepositoryID, "name": p.Name, "version": p.Version, "digest": p.Digest, "size": p.Size, "publishedAt": p.PublishedAt, "chartUrl": assetPath(repo, p, false), "provenanceSize": p.ProvenanceSize}
	if p.ProvenanceSize > 0 {
		out["provenanceDigest"] = p.ProvenanceDigest
		out["provenanceUrl"] = assetPath(repo, p, true)
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
		var in struct {
			Name string `json:"name"`
		}
		if !decodeBody(w, r, &in) || !repositoryName.MatchString(in.Name) {
			m.problem(w, 400, "invalid_repository")
			return
		}
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
		repo, replay, err := m.repos.CreateHostedRepositoryIdempotently(r.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: in.Name, Format: repository.FormatHelm, Type: repository.RepositoryTypeHosted}, p.Actor, "helm-preview:"+key, hex.EncodeToString(sum[:]))
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
	if parts[1] != "charts" {
		m.problem(w, 404, "not_found")
		return
	}
	if len(parts) == 2 && r.Method == http.MethodGet {
		principal, allowed := m.authorize(w, r, repo, authorization.RepositoryRead, "", true)
		if !allowed {
			return
		}
		all, e := m.snapshot(r, repo, principal)
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
		m.upload(w, r, repo, name, version, p.Actor)
		return
	}
	in, err := m.store.GetHelmPublication(r.Context(), repo.ID, name, version)
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
