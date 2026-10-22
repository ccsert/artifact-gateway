package helm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/ccsert/artifact-gateway/internal/authorization"
	helmprotocol "github.com/ccsert/artifact-gateway/internal/protocol/helm"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/uploadspool"
	"github.com/ccsert/artifact-gateway/internal/upstreamauth"
	"github.com/google/uuid"
	"go.yaml.in/yaml/v3"
)

var errUpstreamRejected = errors.New("helm upstream response rejected")

func digestBytes(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func validSourceURL(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || len(u.String()) > 4096 || strings.ContainsAny(u.String(), "\\\x00\r\n") || strings.HasSuffix(u.Hostname(), ".") || u.RawQuery != "" {
		return false
	}
	if u.RawPath != "" && u.EscapedPath() != u.Path {
		return false
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	if p := u.Port(); p != "" {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 65535 {
			return false
		}
	}
	return true
}
func sourceURL(repo repository.HostedRepository, raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	base, b := url.Parse(repo.Endpoint)
	if e != nil || b != nil || !validSourceURL(u) || !validSourceURL(base) {
		return nil, errUpstreamRejected
	}
	if upstreamauth.SameOrigin(base, u) {
		return u, nil
	}
	for _, host := range repo.AllowedHosts {
		candidate, e := url.Parse("https://" + host)
		if e == nil && validSourceURL(candidate) && candidate.Path == "" && upstreamauth.SameOrigin(candidate, u) {
			return u, nil
		}
	}
	return nil, errUpstreamRejected
}
func sourceOffline(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var op *net.OpError
	if errors.As(err, &op) {
		if op.Op == "proxyconnect" {
			return sourceOffline(op.Err)
		}
		return op.Op == "dial"
	}
	var dns *net.DNSError
	return errors.As(err, &dns)
}
func (m *Module) validateSource(ctx context.Context, repo repository.HostedRepository) error {
	if m.sources == nil || m.upstream == nil {
		return repository.ErrDisabled
	}
	if upstreamauth.Validate(repo.UpstreamAuth) != nil {
		return errUpstreamRejected
	}
	current, e := m.repository(ctx, repo.ID, true)
	if e != nil {
		return e
	}
	if current.Type != repository.RepositoryTypeProxy || current.Version != repo.Version {
		return repository.ErrHelmSourceChanged
	}
	_, e = sourceURL(repo, repo.Endpoint)
	return e
}
func (m *Module) sourceDocument(ctx context.Context, repo repository.HostedRepository) (repository.HelmSourceDocument, error) {
	if e := m.validateSource(ctx, repo); e != nil {
		return repository.HelmSourceDocument{}, e
	}
	prior, e := m.sources.GetHelmSourceDocument(ctx, repo.ID, repo.Version)
	if e != nil && !errors.Is(e, repository.ErrNotFound) {
		return prior, e
	}
	has := e == nil
	if has {
		if digestBytes(prior.Body) != prior.Digest {
			return prior, errIntegrity
		}
		if prior.ExpiresAt.After(time.Now()) {
			return prior, nil
		}
	}
	headers := http.Header{"Accept": []string{"application/yaml, text/yaml"}}
	if has {
		headers.Set("If-None-Match", prior.ETag)
		headers.Set("If-Modified-Since", prior.LastModified)
	}
	response, e := m.upstream.FetchHelm(ctx, repo, repo.Endpoint, headers)
	if e != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if ctx.Err() == nil && has && sourceOffline(e) {
			return prior, nil
		}
		return prior, e
	}
	if response == nil || response.Body == nil {
		return prior, errUpstreamRejected
	}
	defer func() { _ = response.Body.Close() }()
	now := time.Now().UTC()
	if response.StatusCode == 304 {
		if !has {
			return prior, errUpstreamRejected
		}
		prior.FetchedAt, prior.ExpiresAt = now, now.Add(30*time.Second)
		return prior, m.sources.PutHelmSourceDocument(ctx, prior)
	}
	if response.StatusCode != 200 || response.Header.Get("Content-Encoding") != "" && response.Header.Get("Content-Encoding") != "identity" {
		return prior, errUpstreamRejected
	}
	body, e := io.ReadAll(io.LimitReader(response.Body, helmprotocol.MaxIndexBytes+1))
	if e != nil || len(body) > helmprotocol.MaxIndexBytes {
		return prior, errUpstreamRejected
	}
	entries, e := helmprotocol.ParseIndex(body, repo.Endpoint)
	if e != nil {
		return prior, errUpstreamRejected
	}
	for _, entry := range entries {
		if _, e = sourceURL(repo, entry.URLs[0]); e != nil {
			return prior, e
		}
	}
	next := repository.HelmSourceDocument{RepositoryID: repo.ID, RepositoryVersion: repo.Version, Body: body, Digest: digestBytes(body), ETag: response.Header.Get("ETag"), LastModified: response.Header.Get("Last-Modified"), FetchedAt: now, ExpiresAt: now.Add(30 * time.Second)}
	if e = m.sources.PutHelmSourceDocument(ctx, next); e != nil {
		return next, e
	}
	return next, nil
}
func (m *Module) sourceEntries(ctx context.Context, repo repository.HostedRepository) ([]repository.HelmSourceEntry, error) {
	out := []repository.HelmSourceEntry{}
	if repo.Type == repository.RepositoryTypeHosted {
		all, e := m.store.ListHelmPublications(ctx, repo.ID, helmprotocol.MaxCharts+1)
		if e != nil {
			return nil, e
		}
		if len(all) > helmprotocol.MaxCharts {
			return nil, errInventoryLimit
		}
		for _, p := range all {
			out = append(out, repository.HelmSourceEntry{Name: p.Name, Version: p.Version, Digest: p.Digest, RepositoryID: repo.ID, RepositoryVersion: repo.Version, MetadataJSON: p.MetadataJSON, CreatedAt: p.PublishedAt})
		}
		return out, nil
	}
	doc, e := m.sourceDocument(ctx, repo)
	if e != nil {
		return nil, e
	}
	all, e := helmprotocol.ParseIndex(doc.Body, repo.Endpoint)
	if e != nil {
		return nil, errUpstreamRejected
	}
	for _, entry := range all {
		meta, _ := json.Marshal(entry.Metadata)
		out = append(out, repository.HelmSourceEntry{Name: entry.Name, Version: entry.Version, Digest: "sha256:" + entry.Digest, RepositoryID: repo.ID, RepositoryVersion: repo.Version, URL: entry.URLs[0], MetadataJSON: meta, CreatedAt: entry.Created})
	}
	return out, nil
}
func (m *Module) freeze(ctx context.Context, view repository.HostedRepository, entries []repository.HelmSourceEntry) (repository.HelmSourceSnapshot, error) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name+"@"+entries[i].Version < entries[j].Name+"@"+entries[j].Version
	})
	body, _ := json.Marshal(entries)
	digest := digestBytes(body)
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("helm:"+view.ID+":"+view.Version+":"+digest)).String()
	return m.sources.PutHelmSourceSnapshot(ctx, repository.HelmSourceSnapshot{ID: id, ViewID: view.ID, ViewVersion: view.Version, Digest: digest, Entries: entries})
}
func snapshotPath(view, snapshot string, e repository.HelmSourceEntry) string {
	return "/helm/" + url.PathEscape(view) + "/snapshots/" + snapshot + "/" + assetRelative(e.Name, e.Version, false)
}
func (m *Module) sourceIndex(w http.ResponseWriter, r *http.Request, view repository.HostedRepository, entries []repository.HelmSourceEntry) {
	snap, e := m.freeze(r.Context(), view, entries)
	if e != nil {
		m.failure(w, e)
		return
	}
	all := map[string][]indexEntry{}
	generated := time.Unix(0, 0).UTC()
	for _, entry := range snap.Entries {
		var meta helmprotocol.Metadata
		if json.Unmarshal(entry.MetadataJSON, &meta) != nil {
			m.failure(w, errIntegrity)
			return
		}
		all[entry.Name] = append(all[entry.Name], indexEntry{Metadata: meta, Created: entry.CreatedAt, Digest: strings.TrimPrefix(entry.Digest, "sha256:"), URLs: []string{snapshotPath(view.Name, snap.ID, entry)}})
		if entry.CreatedAt.After(generated) {
			generated = entry.CreatedAt
		}
	}
	for name := range all {
		sort.Slice(all[name], func(i, j int) bool {
			a, _ := semver.StrictNewVersion(all[name][i].Version)
			b, _ := semver.StrictNewVersion(all[name][j].Version)
			return a.GreaterThan(b)
		})
	}
	body, e := yaml.Marshal(struct {
		APIVersion string                  `yaml:"apiVersion"`
		Generated  time.Time               `yaml:"generated"`
		Entries    map[string][]indexEntry `yaml:"entries"`
	}{"v1", generated, all})
	if e != nil || len(body) > 8<<20 {
		m.failure(w, errInventoryLimit)
		return
	}
	etag := `"` + digestBytes(body) + `"`
	w.Header().Set("Content-Type", "application/yaml")
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("Vary", "Authorization")
	w.Header().Set("ETag", etag)
	w.Header().Set("X-Helm-Source-Snapshot", snap.ID)
	for _, tag := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		if strings.TrimSpace(tag) == etag || strings.TrimSpace(tag) == "*" || strings.TrimSpace(tag) == "W/"+etag {
			w.WriteHeader(304)
			return
		}
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}
func (m *Module) serveSource(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, parts []string) {
	p, ok := m.authorize(w, r, repo, authorization.RepositoryRead, "", true)
	if !ok {
		return
	}
	if len(parts) == 1 && parts[0] == "index.yaml" {
		entries, e := m.sourceEntries(r.Context(), repo)
		if e != nil {
			m.failure(w, e)
			return
		}
		filtered := []repository.HelmSourceEntry{}
		for _, entry := range entries {
			if m.allowed(r.Context(), p, repo, entry.Name) {
				filtered = append(filtered, entry)
			}
		}
		m.sourceIndex(w, r, repo, filtered)
		return
	}
	if len(parts) == 6 && parts[0] == "snapshots" {
		m.snapshotAsset(w, r, repo, nil, parts[1:])
		return
	}
	m.problem(w, 404, "not_found")
}
func (m *Module) proxyChart(ctx context.Context, repo repository.HostedRepository, entry repository.HelmSourceEntry) (repository.HelmPublication, error) {
	if e := m.validateSource(ctx, repo); e != nil {
		return repository.HelmPublication{}, e
	}
	if repo.Version != entry.RepositoryVersion {
		return repository.HelmPublication{}, repository.ErrHelmSourceChanged
	}
	ctx, release, e := m.store.LockHelmPublication(ctx, repo.ID, entry.Name, entry.Version)
	if e != nil {
		return repository.HelmPublication{}, e
	}
	defer release()
	prior, e := m.store.GetHelmPublication(ctx, repo.ID, entry.Name, entry.Version)
	if e == nil {
		if prior.Digest != entry.Digest {
			return prior, repository.ErrHelmPublicationConflict
		}
		return prior, nil
	}
	if !errors.Is(e, repository.ErrNotFound) {
		return prior, e
	}
	fetch := func(target string, limit int64, optional bool) (*uploadspool.Spool, error) {
		if _, e := sourceURL(repo, target); e != nil {
			return nil, e
		}
		response, e := m.upstream.FetchHelm(ctx, repo, target, http.Header{})
		if e != nil {
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}
			return nil, e
		}
		if response == nil || response.Body == nil {
			return nil, errUpstreamRejected
		}
		defer func() { _ = response.Body.Close() }()
		if optional && response.StatusCode == 404 {
			return nil, nil
		}
		if response.StatusCode != 200 {
			return nil, errUpstreamRejected
		}
		if response.Header.Get("Content-Encoding") != "" && response.Header.Get("Content-Encoding") != "identity" {
			return nil, errUpstreamRejected
		}
		spool, e := uploadspool.Read(response.Body, limit)
		if e != nil {
			return nil, errUpstreamRejected
		}
		return spool, nil
	}
	chart, e := fetch(entry.URL, m.maxBytes, false)
	if e != nil {
		return prior, e
	}
	defer func() { _ = chart.Close() }()
	if chart.Digest() != entry.Digest {
		return prior, errIntegrity
	}
	inspection, e := helmprotocol.Inspect(chart.Reader())
	if e != nil || inspection.Metadata.Name != entry.Name || inspection.Metadata.Version != entry.Version || !bytes.Equal(inspection.MetadataJSON, entry.MetadataJSON) {
		return prior, errIntegrity
	}
	prov, e := fetch(entry.URL+".prov", helmprotocol.MaxProvenanceBytes, true)
	if e != nil {
		return prior, e
	}
	if prov != nil {
		defer func() { _ = prov.Close() }()
		body, e := io.ReadAll(prov.Reader())
		if e != nil || helmprotocol.InspectProvenance(body, inspection, chart.Digest()) != nil {
			return prior, errIntegrity
		}
	}
	in := repository.HelmSession{ID: uuid.NewString(), RepositoryID: repo.ID, SourceVersion: repo.Version, Name: entry.Name, Version: entry.Version, Digest: chart.Digest(), Size: chart.Size(), Actor: "helm-proxy", MetadataJSON: inspection.MetadataJSON, ChartYAML: inspection.ChartYAML, ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if prov != nil {
		in.ProvenanceDigest, in.ProvenanceSize = prov.Digest(), prov.Size()
	}
	session, _, e := m.store.ReserveHelmPublication(ctx, in)
	if e != nil {
		return prior, e
	}
	if session.State != "committed" {
		if e = chart.Rewind(); e == nil {
			e = m.objects.PutVerifiedReader(ctx, session.ObjectKey, chart.Reader(), session.Size, session.Digest)
		}
		if e == nil && prov != nil {
			if e = prov.Rewind(); e == nil {
				e = m.objects.PutVerifiedReader(ctx, session.ProvenanceKey, prov.Reader(), session.ProvenanceSize, session.ProvenanceDigest)
			}
		}
		if e != nil {
			return prior, e
		}
	}
	return m.commit(ctx, session, in.Actor)
}
func (m *Module) previewCharts(r *http.Request, repo repository.HostedRepository, p authorization.Principal) ([]repository.HelmPublication, error) {
	if repo.Type == repository.RepositoryTypeHosted {
		return m.snapshot(r, repo, p)
	}
	entries, e := m.sourceEntries(r.Context(), repo)
	if e != nil {
		return nil, e
	}
	filtered := []repository.HelmSourceEntry{}
	for _, entry := range entries {
		if m.allowed(r.Context(), p, repo, entry.Name) {
			filtered = append(filtered, entry)
		}
	}
	snap, e := m.freeze(r.Context(), repo, filtered)
	if e != nil {
		return nil, e
	}
	out := []repository.HelmPublication{}
	for _, entry := range snap.Entries {
		p := repository.HelmPublication{RepositoryID: repo.ID, Name: entry.Name, Version: entry.Version, Digest: entry.Digest, MetadataJSON: entry.MetadataJSON, PublishedAt: entry.CreatedAt}
		cached, e := m.store.GetHelmPublication(r.Context(), repo.ID, entry.Name, entry.Version)
		if e == nil {
			if cached.Digest != entry.Digest {
				return nil, repository.ErrHelmPublicationConflict
			}
			p = cached
		} else if !errors.Is(e, repository.ErrNotFound) {
			return nil, e
		}
		p.SessionID = snap.ID
		out = append(out, p)
	}
	return out, nil
}
func (m *Module) previewChart(r *http.Request, repo repository.HostedRepository, name, version string) (repository.HelmPublication, error) {
	if repo.Type == repository.RepositoryTypeHosted {
		return m.store.GetHelmPublication(r.Context(), repo.ID, name, version)
	}
	p, _ := m.principal(r)
	all, e := m.previewCharts(r, repo, p)
	if e != nil {
		return repository.HelmPublication{}, e
	}
	for _, in := range all {
		if in.Name == name && in.Version == version {
			return in, nil
		}
	}
	return repository.HelmPublication{}, repository.ErrNotFound
}
