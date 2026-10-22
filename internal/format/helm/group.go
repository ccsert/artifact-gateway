package helm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	helmprotocol "github.com/ccsert/artifact-gateway/internal/protocol/helm"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func groupRepository(g repository.HostedGroup) repository.HostedRepository {
	return repository.HostedRepository{ID: g.ID, Name: g.Name, Version: g.Version, Format: repository.FormatHelm, Type: repository.RepositoryTypeProxy, State: repository.RepositoryActive, AnonymousRead: g.AnonymousRead}
}
func (m *Module) groupMembers(ctx context.Context, g repository.HostedGroup) ([]repository.HostedRepository, error) {
	if m.sources == nil || m.groups == nil || len(g.Members) == 0 || len(g.Members) > 32 {
		return nil, repository.ErrDisabled
	}
	ordered := append([]repository.GroupMember(nil), g.Members...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Position < ordered[j].Position })
	out := []repository.HostedRepository{}
	seen := map[string]bool{}
	for _, member := range ordered {
		if seen[member.RepositoryID] {
			return nil, repository.ErrHelmSourceChanged
		}
		seen[member.RepositoryID] = true
		repo, e := m.repository(ctx, member.RepositoryID, true)
		if e != nil {
			return nil, repository.ErrHelmSourceChanged
		}
		out = append(out, repo)
	}
	return out, nil
}
func (m *Module) authorizeGroup(w http.ResponseWriter, r *http.Request, g repository.HostedGroup, members []repository.HostedRepository, name string, collection bool) (authorization.Principal, bool) {
	p, ok := m.principal(r)
	if !ok {
		if r.Header.Get("Authorization") != "" || !g.AnonymousRead {
			w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway Helm"`)
			m.problem(w, 401, "authentication_required")
			return p, false
		}
		policy, e := m.anonymous.GetAnonymousAccessPolicy(r.Context())
		if e != nil || !policy.Enabled {
			m.problem(w, 401, "authentication_required")
			return p, false
		}
		p = authorization.Principal{Actor: "anonymous"}
	}
	for _, repo := range members {
		if _, ok = m.authorize(w, r, repo, authorization.RepositoryRead, name, collection); !ok {
			return p, false
		}
	}
	return p, true
}
func (m *Module) groupEntries(ctx context.Context, g repository.HostedGroup, members []repository.HostedRepository, readable func(string) bool) ([]repository.HelmSourceEntry, error) {
	first := map[string]repository.HelmSourceEntry{}
	all := map[string]map[string]repository.HelmSourceEntry{}
	for _, repo := range members {
		entries, e := m.sourceEntries(ctx, repo)
		if e != nil {
			return nil, e
		}
		for _, entry := range entries {
			k := entry.Name + "@" + entry.Version
			if all[k] == nil {
				if len(first) >= helmprotocol.MaxCharts {
					return nil, errInventoryLimit
				}
				all[k] = map[string]repository.HelmSourceEntry{}
				first[k] = entry
			}
			all[k][repo.ID] = entry
		}
	}
	keys := []string{}
	for key := range first {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := []repository.HelmSourceEntry{}
	for _, key := range keys {
		entry := first[key]
		if readable != nil && !readable(entry.Name) {
			continue
		}
		owner, e := m.sources.BindHelmGroupAuthority(ctx, g.Version, repository.HelmGroupAuthority{GroupID: g.ID, Entry: entry})
		if e != nil {
			return nil, e
		}
		current, ok := all[key][owner.Entry.RepositoryID]
		if !ok {
			return nil, repository.ErrHelmSourceChanged
		}
		if current.Digest != owner.Entry.Digest {
			return nil, repository.ErrHelmPublicationConflict
		}
		out = append(out, owner.Entry)
	}
	return out, nil
}
func (m *Module) serveGroup(w http.ResponseWriter, r *http.Request, g repository.HostedGroup, parts []string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		m.problem(w, 405, "read_only_source")
		return
	}
	members, e := m.groupMembers(r.Context(), g)
	if e != nil {
		m.failure(w, e)
		return
	}
	name := ""
	if len(parts) == 6 && parts[0] == "snapshots" {
		name = parts[3]
	}
	p, ok := m.authorizeGroup(w, r, g, members, name, name == "")
	if !ok {
		return
	}
	if len(parts) == 1 && parts[0] == "index.yaml" {
		entries, e := m.groupEntries(r.Context(), g, members, func(name string) bool {
			for _, repo := range members {
				if !m.allowed(r.Context(), p, repo, name) {
					return false
				}
			}
			return true
		})
		if e != nil {
			m.failure(w, e)
			return
		}
		m.sourceIndex(w, r, groupRepository(g), entries)
		return
	}
	if len(parts) == 6 && parts[0] == "snapshots" {
		m.snapshotAsset(w, r, groupRepository(g), &g, parts[1:])
		return
	}
	m.problem(w, 404, "not_found")
}
func (m *Module) snapshotAsset(w http.ResponseWriter, r *http.Request, view repository.HostedRepository, group *repository.HostedGroup, parts []string) {
	if m.sources == nil || len(parts) != 5 || parts[1] != "charts" || !helmprotocol.ValidIdentity(parts[2], parts[3]) {
		m.problem(w, 404, "not_found")
		return
	}
	if _, e := uuid.Parse(parts[0]); e != nil {
		m.problem(w, 404, "not_found")
		return
	}
	snap, e := m.sources.GetHelmSourceSnapshot(r.Context(), view.ID, parts[0])
	if e != nil {
		m.failure(w, e)
		return
	}
	body, _ := json.Marshal(snap.Entries)
	if digestBytes(body) != snap.Digest {
		m.failure(w, errIntegrity)
		return
	}
	var entry repository.HelmSourceEntry
	for _, candidate := range snap.Entries {
		if candidate.Name == parts[2] && candidate.Version == parts[3] {
			entry = candidate
			break
		}
	}
	if entry.Name == "" {
		m.problem(w, 404, "not_found")
		return
	}
	expected := helmprotocol.Filename(entry.Name, entry.Version)
	prov := parts[4] == expected+".prov"
	if parts[4] != expected && !prov {
		m.problem(w, 404, "not_found")
		return
	}
	source, e := m.repository(r.Context(), entry.RepositoryID, true)
	if e != nil {
		m.failure(w, e)
		return
	}
	if source.Version != entry.RepositoryVersion {
		m.failure(w, repository.ErrHelmSourceChanged)
		return
	}
	var principal authorization.Principal
	var allowed bool
	if group == nil {
		if source.ID != view.ID {
			m.failure(w, errIntegrity)
			return
		}
		if principal, allowed = m.authorize(w, r, source, authorization.RepositoryRead, entry.Name, false); !allowed {
			return
		}
	} else {
		found := false
		for _, member := range group.Members {
			if member.RepositoryID == source.ID {
				found = true
			}
		}
		if !found {
			m.failure(w, repository.ErrHelmSourceChanged)
			return
		}
		members, e := m.groupMembers(r.Context(), *group)
		if e != nil {
			m.failure(w, e)
			return
		}
		if principal, allowed = m.authorizeGroup(w, r, *group, members, entry.Name, false); !allowed {
			return
		}
	}
	result := &sourceReadResult{ResponseWriter: w, status: http.StatusOK}
	w = result
	defer func() {
		outcome := repository.AuditResolved
		switch {
		case result.status == 404:
			outcome = repository.AuditNotFound
		case result.status == 502:
			outcome = repository.AuditUpstreamError
		case result.status >= 500:
			outcome = repository.AuditStorageError
		case result.status >= 400:
			outcome = repository.AuditProxyDenied
		}
		_ = m.audit.RecordAudit(r.Context(), repository.AuditRecord{Actor: principal.Actor, Repository: view.Name, GroupName: view.Name, Format: "helm", Resource: entry.Name + "@" + entry.Version, Operation: "helm.source.resolve", Outcome: outcome, Status: result.status, Bytes: result.bytes, OccurredAt: time.Now().UTC(), Evidence: map[string]string{"repositoryId": source.ID, "repositoryVersion": source.Version, "digest": entry.Digest, "snapshotId": snap.ID}})
	}()
	var p repository.HelmPublication
	if source.Type == repository.RepositoryTypeProxy {
		p, e = m.proxyChart(r.Context(), source, entry)
	} else {
		p, e = m.store.GetHelmPublication(r.Context(), source.ID, entry.Name, entry.Version)
	}
	if e != nil {
		m.failure(w, e)
		return
	}
	if p.Digest != entry.Digest {
		m.failure(w, repository.ErrHelmPublicationConflict)
		return
	}
	w.Header().Set("X-Helm-Source-Repository", source.ID)
	w.Header().Set("X-Helm-Source-Snapshot", snap.ID)
	m.publicationAsset(w, r, p, parts[4], prov)

}

// sourceReadResult observes the actual protocol response, including conditional
// and range statuses; it never labels an error response as a successful read.
type sourceReadResult struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func (w *sourceReadResult) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *sourceReadResult) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, e := w.ResponseWriter.Write(body)
	w.bytes += int64(n)
	return n, e
}
func (w *sourceReadResult) Unwrap() http.ResponseWriter { return w.ResponseWriter }

var _ io.Writer = (*sourceReadResult)(nil)
