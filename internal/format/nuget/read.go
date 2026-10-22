package nuget

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ccsert/artifact-gateway/internal/artifacthttp"
	"github.com/ccsert/artifact-gateway/internal/authorization"
	nugetprotocol "github.com/ccsert/artifact-gateway/internal/protocol/nuget"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

var errMetadataLimit = errors.New("NuGet metadata inventory exceeds preview bound")

func (m *Module) read(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, path []string) {
	resource := ""
	if len(path) > 1 && (path[0] == "flat" || path[0] == "registration") {
		resource = strings.ToLower(path[1])
	}
	var p authorization.Principal
	var ok bool
	if resource == "" {
		p, ok = m.authorizeCollection(w, r, repo, authorization.RepositoryRead)
	} else {
		p, ok = m.authorize(w, r, repo, authorization.RepositoryRead, resource)
	}
	if !ok {
		return
	}
	if len(path) == 1 && path[0] == "index.json" {
		if repo.Type == repository.RepositoryTypeProxy {
			if _, err := m.resources(r.Context(), repo); err != nil {
				m.failure(w, err)
				return
			}
		}
		m.serviceIndex(w, r, repo)
		return
	}
	if path[0] == "flat" {
		m.flat(w, r, repo, path[1:], p.Actor)
		return
	}
	if path[0] == "registration" {
		m.registration(w, r, repo, path[1:])
		return
	}
	if len(path) == 1 && (path[0] == "query" || path[0] == "autocomplete") {
		m.search(w, r, repo, p, path[0] == "autocomplete")
		return
	}
	m.problem(w, 404, "not_found")
}
func (m *Module) serviceIndex(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository) {
	base := sourceBase(r, repo)
	resources := []map[string]string{
		{"@id": base + "flat/", "@type": "PackageBaseAddress/3.0.0"},
		{"@id": base + "registration/", "@type": "RegistrationsBaseUrl/3.6.0"},
		{"@id": base + "query", "@type": "SearchQueryService/3.5.0"},
		{"@id": base + "autocomplete", "@type": "SearchAutocompleteService/3.5.0"},
	}
	if repo.Type == repository.RepositoryTypeHosted {
		resources = append(resources, map[string]string{"@id": origin(r) + "/nuget/" + repo.Name + "/api/v2/package/", "@type": "PackagePublish/2.0.0"})
	}
	writeJSON(w, r, 200, map[string]any{"version": "3.0.0", "resources": resources})
}

func (m *Module) all(ctx context.Context, repo, id string) ([]repository.NuGetPublication, error) {
	out := []repository.NuGetPublication{}
	cursor := ""
	metadataBytes := 0
	for {
		page, err := m.store.ListNuGetPublications(ctx, repo, id, 64, cursor)
		if err != nil {
			return nil, err
		}
		for _, p := range page {
			metadataBytes += len(p.MetadataJSON)
			if len(out) >= 4096 || metadataBytes > 8<<20 {
				return nil, errMetadataLimit
			}
			out = append(out, p)
		}
		if len(page) < 64 {
			return out, nil
		}
		last := page[len(page)-1]
		cursor = last.PackageID + "@" + last.Version
	}
}
func metadata(p repository.NuGetPublication) (packageMetadata, error) {
	var meta packageMetadata
	err := json.Unmarshal(p.MetadataJSON, &meta)
	return meta, err
}
func sortedVersions(items []repository.NuGetPublication) {
	sort.Slice(items, func(i, j int) bool { return nugetprotocol.CompareVersions(items[i].Version, items[j].Version) < 0 })
}

func (m *Module) flat(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, parts []string, actor string) {
	if len(parts) != 2 && len(parts) != 3 {
		m.problem(w, 404, "not_found")
		return
	}
	id, err := nugetprotocol.NormalizeID(parts[0])
	if err != nil {
		m.problem(w, 404, "not_found")
		return
	}
	if len(parts) == 2 && parts[1] == "index.json" {
		if repo.Type == repository.RepositoryTypeProxy {
			versions, err := m.proxyVersions(r.Context(), repo, id)
			if err != nil {
				m.failure(w, err)
				return
			}
			items := make([]repository.NuGetPublication, 0, len(versions))
			for _, version := range versions {
				items = append(items, repository.NuGetPublication{RepositoryID: repo.ID, PackageID: id, Version: version, SourceVersion: repo.Version})
			}
			items, err = m.visiblePublications(r.Context(), items)
			if err != nil {
				m.failure(w, err)
				return
			}
			if len(items) == 0 {
				m.problem(w, 404, "not_found")
				return
			}
			visible := make([]string, 0, len(items))
			for _, p := range items {
				visible = append(visible, p.Version)
			}
			m.metadataJSON(w, r, map[string]any{"versions": visible})
			return
		}
		items, err := m.all(r.Context(), repo.ID, id)
		if err == nil {
			items, err = m.visiblePublications(r.Context(), items)
		}
		if err != nil {
			m.failure(w, err)
			return
		}
		if len(items) == 0 {
			m.problem(w, 404, "not_found")
			return
		}
		sortedVersions(items)
		versions := make([]string, 0, len(items))
		for _, p := range items {
			versions = append(versions, p.Version)
		}
		writeJSON(w, r, 200, map[string]any{"versions": versions})
		return
	}
	if len(parts) != 3 {
		m.problem(w, 404, "not_found")
		return
	}
	version, err := nugetprotocol.NormalizeVersion(parts[1])
	if err != nil {
		m.problem(w, 404, "not_found")
		return
	}
	version = strings.ToLower(version)
	if parts[2] != id+"."+version+".nupkg" && parts[2] != id+".nuspec" && parts[2] != id+"."+version+".nupkg.sha512" {
		m.problem(w, 404, "not_found")
		return
	}
	if repo.Type == repository.RepositoryTypeProxy {
		if cached, lookupErr := m.store.GetNuGetPublication(r.Context(), repo.ID, id, version); lookupErr == nil {
			_, blocked, governanceErr := m.governedPublication(r.Context(), cached)
			if governanceErr != nil {
				m.failure(w, governanceErr)
				return
			}
			if blocked {
				m.problem(w, 404, "not_found")
				return
			}
		} else if !errors.Is(lookupErr, repository.ErrNotFound) {
			m.failure(w, lookupErr)
			return
		}
		if _, err = m.proxyPackage(r.Context(), repo, id, version); err != nil {
			m.failure(w, err)
			return
		}
	}
	p, err := m.store.GetNuGetPublication(r.Context(), repo.ID, id, version)
	if err != nil {
		m.failure(w, err)
		return
	}
	m.packageFile(w, r, repo, p, parts[2], actor)
}
func (m *Module) packageFile(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, p repository.NuGetPublication, file, actor string) {
	ctx, release, err := m.store.LockNuGetPublication(r.Context(), p.RepositoryID, p.PackageID, p.Version)
	if err != nil {
		m.failure(w, err)
		return
	}
	defer release()
	current, err := m.store.GetNuGetPublication(ctx, p.RepositoryID, p.PackageID, p.Version)
	if err != nil || current.Digest != p.Digest {
		if err == nil {
			err = repository.ErrNuGetPublicationConflict
		}
		m.failure(w, err)
		return
	}
	p, blocked, err := m.governedPublication(ctx, current)
	if err != nil {
		m.failure(w, err)
		return
	}
	if blocked {
		m.problem(w, 404, "not_found")
		return
	}
	id, version := p.PackageID, p.Version
	switch file {
	case id + "." + version + ".nupkg":
		result := artifacthttp.ServeObject(w, r, file, artifacthttp.Object{Digest: p.Digest, ObjectKey: p.ObjectKey, ContentType: "application/octet-stream", Size: p.Size}, m.objects)
		if result.Status == 200 || result.Status == 206 {
			_ = m.audit.RecordAudit(r.Context(), repository.AuditRecord{Repository: repo.Name, GroupName: repo.Name, Actor: actor, Format: string(repository.FormatNuGet), Resource: id + "@" + version, Representation: "nupkg", Operation: "download", Outcome: repository.AuditResolved, Status: result.Status, Bytes: result.Bytes, OccurredAt: time.Now().UTC()})
		}
	case id + ".nuspec":
		w.Header().Set("Content-Type", "application/xml")
		inlineHeaders(w, p.Nuspec)
		http.ServeContent(w, r, file, p.PublishedAt, bytes.NewReader(p.Nuspec))
	case id + "." + version + ".nupkg.sha512":
		meta, err := metadata(p)
		if err != nil {
			m.failure(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		inlineHeaders(w, []byte(meta.PackageHash))
		http.ServeContent(w, r, file, p.PublishedAt, strings.NewReader(meta.PackageHash))
	default:
		m.problem(w, 404, "not_found")
	}
}

func inlineHeaders(w http.ResponseWriter, body []byte) {
	digest := sha256.Sum256(body)
	w.Header().Set("ETag", `"sha256:`+hex.EncodeToString(digest[:])+`"`)
	w.Header().Set("Cache-Control", "private, no-cache")
}

func (m *Module) leaf(r *http.Request, repo repository.HostedRepository, p repository.NuGetPublication) (map[string]any, error) {
	meta, err := metadata(p)
	if err != nil {
		return nil, err
	}
	base := sourceBase(r, repo)
	leafURL := base + "registration/" + p.PackageID + "/" + p.Version + ".json"
	content := packageURL(r, repo, p.PackageID, p.Version)
	listed := listedPublication(p, meta)
	groups := make([]map[string]any, 0, len(meta.DependencyGroups))
	for _, g := range meta.DependencyGroups {
		deps := make([]map[string]any, 0, len(g.Dependencies))
		for _, dep := range g.Dependencies {
			item := map[string]any{"id": dep.ID, "registration": base + "registration/" + strings.ToLower(dep.ID) + "/index.json"}
			if dep.Range != "" {
				item["range"] = dep.Range
			}
			deps = append(deps, item)
		}
		group := map[string]any{"dependencies": deps}
		if g.TargetFramework != "" {
			group["targetFramework"] = g.TargetFramework
		}
		groups = append(groups, group)
	}
	return map[string]any{"@id": leafURL, "listed": listed, "packageContent": content, "published": p.PublishedAt.UTC().Format(time.RFC3339Nano), "registration": base + "registration/" + p.PackageID + "/index.json", "catalogEntry": map[string]any{"@id": leafURL + "#catalogEntry", "id": meta.ID, "version": p.Version, "authors": meta.Authors, "description": meta.Description, "listed": listed, "published": p.PublishedAt.UTC().Format(time.RFC3339Nano), "dependencyGroups": groups, "packageContent": content, "packageHash": meta.PackageHash, "packageHashAlgorithm": "SHA512"}}, nil
}
func (m *Module) registration(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, parts []string) {
	if len(parts) < 2 {
		m.problem(w, 404, "not_found")
		return
	}
	id, err := nugetprotocol.NormalizeID(parts[0])
	if err != nil {
		m.problem(w, 404, "not_found")
		return
	}
	items, err := m.packageMetadata(r.Context(), repo, id)
	if err == nil {
		items, err = m.visiblePublications(r.Context(), items)
	}
	if err != nil {
		m.failure(w, err)
		return
	}
	if len(items) == 0 {
		m.problem(w, 404, "not_found")
		return
	}
	m.registrationItems(w, r, repo, parts, items)
}
func (m *Module) registrationItems(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, parts []string, items []repository.NuGetPublication) {
	id := items[0].PackageID
	sortedVersions(items)
	if len(parts) == 2 && parts[1] != "index.json" {
		version := strings.TrimSuffix(parts[1], ".json")
		if version == parts[1] {
			m.problem(w, 404, "not_found")
			return
		}
		for _, p := range items {
			if p.Version == strings.ToLower(version) {
				leaf, err := m.leaf(r, repo, p)
				if err != nil {
					m.failure(w, err)
					return
				}
				m.gzipJSON(w, r, leaf)
				return
			}
		}
		m.problem(w, 404, "not_found")
		return
	}
	pages := []map[string]any{}
	indexURL := sourceBase(r, repo) + "registration/" + id + "/index.json"
	for start := 0; start < len(items); start += 64 {
		end := min(start+64, len(items))
		chunk := items[start:end]
		low, high := chunk[0].Version, chunk[len(chunk)-1].Version
		pageURL := sourceBase(r, repo) + "registration/" + id + "/page/" + low + "/" + high + ".json"
		page := map[string]any{"@id": pageURL, "parent": indexURL, "count": len(chunk), "lower": low, "upper": high}
		isPage := len(parts) == 4 && parts[1] == "page" && parts[2] == low && parts[3] == high+".json"
		if len(items) < 128 || isPage {
			leaves := []map[string]any{}
			for _, p := range chunk {
				leaf, err := m.leaf(r, repo, p)
				if err != nil {
					m.failure(w, err)
					return
				}
				leaves = append(leaves, leaf)
			}
			page["items"] = leaves
		}
		if isPage {
			m.gzipJSON(w, r, page)
			return
		}
		pages = append(pages, page)
	}
	if len(parts) != 2 || parts[1] != "index.json" {
		m.problem(w, 404, "not_found")
		return
	}
	m.gzipJSON(w, r, map[string]any{"@id": indexURL, "count": len(pages), "items": pages})
}

func (m *Module) metadataJSON(w http.ResponseWriter, r *http.Request, value any) {
	body, err := json.Marshal(value)
	if err != nil || len(body) > 8<<20 {
		m.failure(w, errMetadataLimit)
		return
	}
	inlineHeaders(w, body)
	w.Header().Set("Content-Type", "application/json")
	http.ServeContent(w, r, "metadata.json", time.Time{}, bytes.NewReader(body))
}
func (m *Module) gzipJSON(w http.ResponseWriter, r *http.Request, value any) {
	body, err := json.Marshal(value)
	if err != nil || len(body) > 8<<20 {
		m.failure(w, errMetadataLimit)
		return
	}
	var encoded bytes.Buffer
	writer := gzip.NewWriter(&encoded)
	_, err = writer.Write(body)
	closeErr := writer.Close()
	if err != nil || closeErr != nil {
		m.problem(w, 503, "nuget_unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("Content-Length", strconv.Itoa(encoded.Len()))
	inlineHeaders(w, encoded.Bytes())
	if r.Header.Get("If-None-Match") == w.Header().Get("ETag") {
		w.Header().Del("Content-Length")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(200)
	if r.Method != http.MethodHead {
		_, _ = w.Write(encoded.Bytes())
	}
}

func (m *Module) search(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, principal authorization.Principal, autocomplete bool) {
	skip, take, err := searchParameters(r)
	if err != nil {
		m.problem(w, 400, "invalid_search_query")
		return
	}
	items, err := m.searchMetadata(r.Context(), repo, r.URL.Query(), autocomplete)
	if err != nil {
		m.failure(w, err)
		return
	}
	m.searchItems(w, r, repo, principal, autocomplete, items, skip, take, true)
}
func (m *Module) searchItems(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, principal authorization.Principal, autocomplete bool, items []repository.NuGetPublication, skip, take int, filterAuthorization bool) {
	query := r.URL.Query()
	all := map[string][]repository.NuGetPublication{}
	pre := strings.EqualFold(query.Get("prerelease"), "true")
	q := strings.ToLower(query.Get("q"))
	filterID := strings.ToLower(query.Get("id"))
	for _, p := range items {
		if filterAuthorization && principal.Actor != "anonymous" && !m.access.AuthorizeResource(r.Context(), principal, repo, authorization.RepositoryRead, p.PackageID).Allowed {
			continue
		}
		p, blocked, err := m.governedPublication(r.Context(), p)
		if err != nil {
			m.failure(w, err)
			return
		}
		if blocked {
			continue
		}
		if !pre && strings.Contains(p.Version, "-") {
			continue
		}
		if filterID != "" && p.PackageID != filterID {
			continue
		}
		meta, err := metadata(p)
		if err != nil {
			m.failure(w, err)
			return
		}
		if !listedPublication(p, meta) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(meta.ID+" "+meta.Description), q) {
			continue
		}
		all[p.PackageID] = append(all[p.PackageID], p)
	}
	ids := make([]string, 0, len(all))
	for id := range all {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	total := len(ids)
	if autocomplete {
		data := []string{}
		if filterID != "" {
			values := all[filterID]
			sortedVersions(values)
			for _, p := range values {
				data = append(data, p.Version)
			}
		} else {
			data = ids
		}
		total = len(data)
		start := min(skip, total)
		end := min(start+take, total)
		m.metadataJSON(w, r, map[string]any{"totalHits": total, "data": data[start:end]})
		return
	}
	data := []map[string]any{}
	for _, id := range ids[min(skip, total):min(skip+take, total)] {
		versions := all[id]
		sortedVersions(versions)
		latest := versions[len(versions)-1]
		meta, err := metadata(latest)
		if err != nil {
			m.failure(w, err)
			return
		}
		vs := []map[string]any{}
		for _, p := range versions {
			vs = append(vs, map[string]any{"version": p.Version, "downloads": 0, "@id": sourceBase(r, repo) + "registration/" + id + "/" + p.Version + ".json"})
		}
		data = append(data, map[string]any{"id": meta.ID, "version": latest.Version, "description": meta.Description, "authors": []string{meta.Authors}, "registration": sourceBase(r, repo) + "registration/" + id + "/index.json", "versions": vs})
	}
	m.metadataJSON(w, r, map[string]any{"totalHits": total, "data": data})
}
