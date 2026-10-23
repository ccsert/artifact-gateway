package rubygems

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

var repositoryName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	return d.Decode(v) == nil && d.Decode(&struct{}{}) == io.EOF
}
func jsonResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func repositoryDTO(r repository.HostedRepository) any {
	return map[string]any{"id": r.ID, "name": r.Name, "format": "rubygems", "type": "hosted", "preview": true, "version": r.Version, "anonymousRead": r.AnonymousRead, "sourceUrl": "/rubygems/" + r.Name + "/", "pushHost": "/rubygems/" + r.Name, "publicationUrl": "/rubygems/" + r.Name + "/api/v1/gems"}
}
func (m *Module) servePreview(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != path.Clean(r.URL.Path) || r.URL.RawQuery != "" || strings.ContainsAny(r.URL.Path, "\\\x00\r\n") {
		m.problem(w, 404, "not_found")
		return
	}
	p, ok := m.auth.AuthenticateContext(r.Context(), r.Header.Get("Authorization"))
	if !ok {
		m.problem(w, 401, "authentication_required")
		return
	}
	if reason := p.AccountStateReason(); reason != "" {
		m.problem(w, 403, reason)
		return
	}
	const prefix = "/api/v2/previews/rubygems/repositories"
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
			Name          string `json:"name"`
			AnonymousRead bool   `json:"anonymousRead"`
		}
		if !decode(w, r, &in) || !repositoryName.MatchString(in.Name) {
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
		data, _ := json.Marshal(in)
		sum := sha256.Sum256(data)
		repo, _, e := m.repos.CreateHostedRepositoryIdempotently(r.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: in.Name, Format: repository.FormatRubyGems, Type: repository.RepositoryTypeHosted, AnonymousRead: in.AnonymousRead}, p.Actor, "rubygems-preview:"+key, hex.EncodeToString(sum[:]))
		if e != nil {
			m.failure(w, e)
			return
		}
		w.Header().Set("Location", prefix+"/"+repo.ID)
		jsonResponse(w, 201, repositoryDTO(repo))
		return
	}
	if !strings.HasPrefix(r.URL.Path, prefix+"/") {
		m.problem(w, 404, "not_found")
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, prefix+"/"), "/")
	if _, e := uuid.Parse(parts[0]); e != nil {
		m.problem(w, 404, "not_found")
		return
	}
	repo, e := m.repository(r.Context(), parts[0], true)
	if e != nil {
		m.failure(w, e)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		if _, ok = m.authorize(w, r, repo, authorization.RepositoryRead, "", true); ok {
			jsonResponse(w, 200, repositoryDTO(repo))
		}
		return
	}
	if validGovernancePath(parts) {
		m.previewGovernance(w, r, repo, parts[2], parts[3], parts[4])
		return
	}
	if len(parts) == 2 && parts[1] == "capacity" {
		if !p.Admin {
			m.problem(w, 403, "access_denied")
			return
		}
		switch r.Method {
		case http.MethodGet:
			c, e := m.store.Capacity(r.Context(), repo.ID)
			if e != nil {
				m.failure(w, e)
				return
			}
			jsonResponse(w, 200, c)
		case http.MethodPut:
			var in struct {
				QuotaBytes *int64 `json:"quotaBytes"`
			}
			if !decode(w, r, &in) || in.QuotaBytes == nil || *in.QuotaBytes < 0 {
				m.problem(w, 400, "invalid_quota")
				return
			}
			c, e := m.store.SetQuota(r.Context(), repo.ID, p.Actor, *in.QuotaBytes)
			if e != nil {
				m.failure(w, e)
				return
			}
			jsonResponse(w, 200, c)
		default:
			w.Header().Set("Allow", "GET, PUT")
			w.WriteHeader(405)
		}
		return
	}
	m.problem(w, 404, "not_found")
}
