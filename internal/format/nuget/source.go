package nuget

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ccsert/artifact-gateway/internal/opsjson"
	nugetprotocol "github.com/ccsert/artifact-gateway/internal/protocol/nuget"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/uploadspool"
	"github.com/ccsert/artifact-gateway/internal/upstreamauth"
)

var (
	errUnsupportedSource = errors.New("unsupported or ambiguous NuGet V3 metadata")
	errSourceRejected    = errors.New("NuGet source response rejected")
)

type sourceResources struct{ flat, registration, query, autocomplete string }

// sourceURL validates every discovered address before fetching it. The
// transport independently repeats egress/origin checks at every redirect.
func sourceURL(repo repository.HostedRepository, raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	origin, e := url.Parse(repo.Endpoint)
	if err != nil || e != nil || !validSourceURL(u) || !validSourceURL(origin) {
		return nil, errUnsupportedSource
	}
	if upstreamauth.SameOrigin(origin, u) {
		return u, nil
	}
	for _, host := range repo.AllowedHosts {
		candidate, err := url.Parse("https://" + host)
		if err == nil && validSourceURL(candidate) && candidate.Path == "" && candidate.RawQuery == "" && upstreamauth.SameOrigin(candidate, u) {
			return u, nil
		}
	}
	return nil, errUnsupportedSource
}
func validSourceURL(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || len(u.String()) > 4096 || strings.ContainsAny(u.String(), "\\\x00\r\n") || strings.HasSuffix(u.Hostname(), ".") {
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
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return false
		}
	}
	return true
}
func sourceJSON(body []byte, out any) error {
	var check any
	if opsjson.Decode(body, &check) != nil || json.Unmarshal(body, out) != nil {
		return errUnsupportedSource
	}
	return nil
}
func (m *Module) validateSource(ctx context.Context, repo repository.HostedRepository) error {
	if m.sources == nil || m.upstream == nil {
		return repository.ErrDisabled
	}
	if upstreamauth.Validate(repo.UpstreamAuth) != nil {
		return errSourceRejected
	}
	current, err := m.repository(ctx, repo.ID, true)
	if err != nil {
		return err
	}
	if current.Type != repository.RepositoryTypeProxy || current.Version != repo.Version {
		return repository.ErrNuGetSourceChanged
	}
	_, err = sourceURL(repo, repo.Endpoint)
	return err
}
func (m *Module) document(ctx context.Context, repo repository.HostedRepository, target string) (repository.NuGetSourceDocument, error) {
	if err := m.validateSource(ctx, repo); err != nil {
		return repository.NuGetSourceDocument{}, err
	}
	if _, err := sourceURL(repo, target); err != nil {
		return repository.NuGetSourceDocument{}, err
	}
	prior, err := m.sources.GetNuGetSourceDocument(ctx, repo.ID, repo.Version, target)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return prior, err
	}
	hasPrior := err == nil
	if hasPrior {
		sum := sha256.Sum256(prior.Body)
		if prior.Digest != "sha256:"+hex.EncodeToString(sum[:]) {
			return prior, errSourceRejected
		}
		if prior.ExpiresAt.After(time.Now()) {
			return prior, nil
		}
	}
	headers := http.Header{"Accept": []string{"application/json"}}
	if hasPrior && prior.Status == 200 {
		if prior.ETag != "" {
			headers.Set("If-None-Match", prior.ETag)
		}
		if prior.LastModified != "" {
			headers.Set("If-Modified-Since", prior.LastModified)
		}
	}
	response, err := m.upstream.FetchNuGet(ctx, repo, target, headers)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if ctx.Err() == nil && sourceOffline(err) && hasPrior && prior.Status == 200 {
			return prior, nil
		}
		return prior, err
	}
	if response == nil || response.Body == nil {
		return prior, errSourceRejected
	}
	defer func() { _ = response.Body.Close() }()
	now := time.Now().UTC()
	if response.StatusCode == 304 {
		if !hasPrior || prior.Status != 200 {
			return prior, errSourceRejected
		}
		prior.FetchedAt, prior.ExpiresAt = now, now.Add(30*time.Second)
		if err = m.sources.PutNuGetSourceDocument(ctx, prior); err != nil {
			return prior, err
		}
		return prior, nil
	}
	if response.StatusCode != 200 && response.StatusCode != 404 {
		return prior, errSourceRejected
	}
	doc := repository.NuGetSourceDocument{RepositoryID: repo.ID, SourceVersion: repo.Version, URL: target, Status: response.StatusCode, FetchedAt: now, ExpiresAt: now.Add(30 * time.Second), ETag: response.Header.Get("ETag"), LastModified: response.Header.Get("Last-Modified")}
	if doc.Status == 404 {
		doc.ExpiresAt = now.Add(10 * time.Second)
		doc.Body = []byte{}
	} else {
		media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if err != nil || media != "application/json" && media != "application/ld+json" {
			return doc, errUnsupportedSource
		}
		var body io.Reader = response.Body
		if encoding := response.Header.Get("Content-Encoding"); encoding == "gzip" {
			reader, err := gzip.NewReader(response.Body)
			if err != nil {
				return doc, errSourceRejected
			}
			defer func() { _ = reader.Close() }()
			body = reader
		} else if encoding != "" && encoding != "identity" {
			return doc, errUnsupportedSource
		}
		doc.Body, err = io.ReadAll(io.LimitReader(body, (8<<20)+1))
		if err != nil || len(doc.Body) > 8<<20 {
			return doc, errUnsupportedSource
		}
		var check any
		if sourceJSON(doc.Body, &check) != nil {
			return doc, errUnsupportedSource
		}
	}
	sum := sha256.Sum256(doc.Body)
	doc.Digest = "sha256:" + hex.EncodeToString(sum[:])
	if err = m.sources.PutNuGetSourceDocument(ctx, doc); err != nil {
		return doc, err
	}
	return doc, nil
}

// url.Error itself implements net.Error even for failed TLS verification.
// Only an unavailable dial/DNS or a transport deadline admits stale metadata;
// certificates, egress decisions, HTTP denials and malformed responses do not.
func sourceOffline(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var operation *net.OpError
	if errors.As(err, &operation) {
		// net/http wraps an unavailable egress proxy's dial failure in a
		// proxyconnect operation. Its inner cause must still be an offline
		// transport error; CONNECT denials and TLS failures are not offline.
		if operation.Op == "proxyconnect" {
			return sourceOffline(operation.Err)
		}
		return operation.Op == "dial"
	}
	var dns *net.DNSError
	return errors.As(err, &dns)
}
func (m *Module) resources(ctx context.Context, repo repository.HostedRepository) (sourceResources, error) {
	doc, err := m.document(ctx, repo, repo.Endpoint)
	if err != nil {
		return sourceResources{}, err
	}
	if doc.Status != 200 {
		return sourceResources{}, repository.ErrNotFound
	}
	var index struct {
		Version   string `json:"version"`
		Resources []struct {
			ID   string          `json:"@id"`
			Type json.RawMessage `json:"@type"`
		} `json:"resources"`
	}
	if sourceJSON(doc.Body, &index) != nil || !strings.HasPrefix(index.Version, "3.") || len(index.Resources) == 0 || len(index.Resources) > 128 {
		return sourceResources{}, errUnsupportedSource
	}
	values := map[string]string{}
	for _, resource := range index.Resources {
		var types []string
		var single string
		if json.Unmarshal(resource.Type, &single) == nil {
			types = []string{single}
		} else if json.Unmarshal(resource.Type, &types) != nil || len(types) > 8 {
			return sourceResources{}, errUnsupportedSource
		}
		for _, kind := range types {
			switch kind {
			case "PackageBaseAddress/3.0.0", "RegistrationsBaseUrl/3.6.0", "RegistrationsBaseUrl/3.4.0", "RegistrationsBaseUrl", "RegistrationsBaseUrl/3.0.0-beta", "RegistrationsBaseUrl/3.0.0-rc", "SearchQueryService/3.5.0", "SearchQueryService", "SearchQueryService/3.0.0-beta", "SearchQueryService/3.0.0-rc", "SearchAutocompleteService/3.5.0", "SearchAutocompleteService", "SearchAutocompleteService/3.0.0-beta", "SearchAutocompleteService/3.0.0-rc":
				if _, err := sourceURL(repo, resource.ID); err != nil {
					return sourceResources{}, err
				}
				if prior := values[kind]; prior != "" && prior != resource.ID {
					return sourceResources{}, errUnsupportedSource
				}
				values[kind] = resource.ID
			}
		}
	}
	first := func(kinds ...string) string {
		for _, k := range kinds {
			if values[k] != "" {
				return values[k]
			}
		}
		return ""
	}
	out := sourceResources{flat: values["PackageBaseAddress/3.0.0"], registration: first("RegistrationsBaseUrl/3.6.0", "RegistrationsBaseUrl/3.4.0", "RegistrationsBaseUrl", "RegistrationsBaseUrl/3.0.0-rc", "RegistrationsBaseUrl/3.0.0-beta"), query: first("SearchQueryService/3.5.0", "SearchQueryService", "SearchQueryService/3.0.0-rc", "SearchQueryService/3.0.0-beta"), autocomplete: first("SearchAutocompleteService/3.5.0", "SearchAutocompleteService", "SearchAutocompleteService/3.0.0-rc", "SearchAutocompleteService/3.0.0-beta")}
	if out.flat == "" || out.registration == "" {
		return out, errUnsupportedSource
	}
	for _, base := range []string{out.flat, out.registration} {
		u, _ := url.Parse(base)
		if !strings.HasSuffix(u.Path, "/") || u.RawQuery != "" {
			return out, errUnsupportedSource
		}
	}
	return out, nil
}
func (m *Module) proxyVersions(ctx context.Context, repo repository.HostedRepository, id string) ([]string, error) {
	resources, err := m.resources(ctx, repo)
	if err != nil {
		return nil, err
	}
	doc, err := m.document(ctx, repo, resources.flat+id+"/index.json")
	if err != nil {
		return nil, err
	}
	if doc.Status == 404 {
		return nil, repository.ErrNotFound
	}
	var list struct {
		Versions []string `json:"versions"`
	}
	if sourceJSON(doc.Body, &list) != nil || len(list.Versions) == 0 || len(list.Versions) > 4096 {
		return nil, errUnsupportedSource
	}
	out := make([]string, 0, len(list.Versions))
	seen := map[string]bool{}
	for _, raw := range list.Versions {
		version, err := nugetprotocol.NormalizeVersion(raw)
		version = strings.ToLower(version)
		if err != nil || seen[version] {
			return nil, errUnsupportedSource
		}
		seen[version] = true
		out = append(out, version)
	}
	sort.Slice(out, func(i, j int) bool { return nugetprotocol.CompareVersions(out[i], out[j]) < 0 })
	return out, nil
}

type registrationCatalog struct {
	ID                   string                          `json:"id"`
	Version              string                          `json:"version"`
	Authors              json.RawMessage                 `json:"authors"`
	Description          string                          `json:"description"`
	PackageContent       string                          `json:"packageContent"`
	PackageHash          string                          `json:"packageHash"`
	PackageHashAlgorithm string                          `json:"packageHashAlgorithm"`
	DependencyGroups     []nugetprotocol.DependencyGroup `json:"dependencyGroups"`
	Published            string                          `json:"published"`
	Listed               *bool                           `json:"listed"`
}
type sourceLeaf struct {
	ID             string              `json:"@id"`
	PackageContent string              `json:"packageContent"`
	Catalog        registrationCatalog `json:"catalogEntry"`
}
type sourcePage struct {
	ID    string       `json:"@id"`
	Count int          `json:"count"`
	Lower string       `json:"lower"`
	Upper string       `json:"upper"`
	Items []sourceLeaf `json:"items"`
}

func (m *Module) proxyMetadata(ctx context.Context, repo repository.HostedRepository, id string) ([]repository.NuGetPublication, error) {
	resources, err := m.resources(ctx, repo)
	if err != nil {
		return nil, err
	}
	versions, err := m.proxyVersions(ctx, repo, id)
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	for _, v := range versions {
		wanted[v] = true
	}
	indexURL := resources.registration + id + "/index.json"
	doc, err := m.document(ctx, repo, indexURL)
	if err != nil {
		return nil, err
	}
	if doc.Status != 200 {
		return nil, errSourceRejected
	}
	var index struct {
		Count int          `json:"count"`
		Items []sourcePage `json:"items"`
	}
	if sourceJSON(doc.Body, &index) != nil || index.Count != len(index.Items) || index.Count < 1 || index.Count > 64 {
		return nil, errUnsupportedSource
	}
	out := []repository.NuGetPublication{}
	seen := map[string]bool{}
	lastUpper := ""
	for _, page := range index.Items {
		low, e1 := nugetprotocol.NormalizeVersion(page.Lower)
		high, e2 := nugetprotocol.NormalizeVersion(page.Upper)
		low, high = strings.ToLower(low), strings.ToLower(high)
		if e1 != nil || e2 != nil || page.Count < 1 || page.Count > 4096 || nugetprotocol.CompareVersions(low, high) > 0 || lastUpper != "" && nugetprotocol.CompareVersions(lastUpper, low) >= 0 {
			return nil, errUnsupportedSource
		}
		lastUpper = high
		if page.Items == nil {
			if _, err := sourceURL(repo, page.ID); err != nil {
				return nil, err
			}
			document, err := m.document(ctx, repo, page.ID)
			if err != nil {
				return nil, err
			}
			if document.Status != 200 {
				return nil, errSourceRejected
			}
			var fetched sourcePage
			if sourceJSON(document.Body, &fetched) != nil || fetched.Count != page.Count || fetched.Lower != page.Lower || fetched.Upper != page.Upper || fetched.ID != "" && fetched.ID != page.ID {
				return nil, errUnsupportedSource
			}
			page.Items = fetched.Items
		}
		if len(page.Items) != page.Count {
			return nil, errUnsupportedSource
		}
		actualLower, actualUpper := "", ""
		for _, leaf := range page.Items {
			catalog := leaf.Catalog
			packageID, e1 := nugetprotocol.NormalizeID(catalog.ID)
			version, e2 := nugetprotocol.NormalizeVersion(catalog.Version)
			version = strings.ToLower(version)
			if e1 != nil || e2 != nil || packageID != id || seen[version] || !wanted[version] || nugetprotocol.CompareVersions(version, low) < 0 || nugetprotocol.CompareVersions(version, high) > 0 || len(out) >= 4096 {
				return nil, errUnsupportedSource
			}
			seen[version] = true
			if actualLower == "" || nugetprotocol.CompareVersions(version, actualLower) < 0 {
				actualLower = version
			}
			if actualUpper == "" || nugetprotocol.CompareVersions(version, actualUpper) > 0 {
				actualUpper = version
			}
			content := resources.flat + id + "/" + version + "/" + id + "." + version + ".nupkg"
			if leaf.PackageContent != content || catalog.PackageContent != "" && catalog.PackageContent != content {
				return nil, errSourceRejected
			}
			if _, err := sourceURL(repo, leaf.ID); err != nil {
				return nil, err
			}
			if len(catalog.Description) > 16384 {
				return nil, errUnsupportedSource
			}
			if _, err := nugetprotocol.CanonicalDependencyGroups(catalog.DependencyGroups); err != nil {
				return nil, errUnsupportedSource
			}
			var authors string
			if len(catalog.Authors) > 0 {
				if json.Unmarshal(catalog.Authors, &authors) != nil {
					var values []string
					if json.Unmarshal(catalog.Authors, &values) != nil || len(values) > 128 {
						return nil, errUnsupportedSource
					}
					authors = strings.Join(values, ",")
				}
			}
			if len(authors) > 16384 {
				return nil, errUnsupportedSource
			}
			if catalog.PackageHash != "" {
				hash, err := base64.StdEncoding.DecodeString(catalog.PackageHash)
				if err != nil || len(hash) != 64 || catalog.PackageHashAlgorithm != "SHA512" {
					return nil, errSourceRejected
				}
			}
			published := time.Unix(1, 0).UTC()
			if catalog.Published != "" {
				published, err = time.Parse(time.RFC3339Nano, catalog.Published)
				if err != nil {
					return nil, errUnsupportedSource
				}
			}
			meta := packageMetadata{ID: catalog.ID, Version: version, Authors: authors, Description: catalog.Description, DependencyGroups: catalog.DependencyGroups, PackageHash: catalog.PackageHash, Listed: catalog.Listed}
			encoded, err := json.Marshal(meta)
			if err != nil || len(encoded) > 256<<10 {
				return nil, errUnsupportedSource
			}
			out = append(out, repository.NuGetPublication{RepositoryID: repo.ID, PackageID: id, Version: version, MetadataJSON: encoded, PublishedAt: published, SourceVersion: repo.Version, SourceURL: content, RegistrationURL: indexURL})
		}
		if actualLower != low || actualUpper != high {
			return nil, errUnsupportedSource
		}
	}
	if len(out) != len(versions) {
		return nil, errSourceRejected
	}
	sortedVersions(out)
	return out, nil
}
func (m *Module) packageMetadata(ctx context.Context, repo repository.HostedRepository, id string) ([]repository.NuGetPublication, error) {
	if repo.Type == repository.RepositoryTypeProxy {
		return m.proxyMetadata(ctx, repo, id)
	}
	return m.all(ctx, repo.ID, id)
}

func (m *Module) proxyPackage(ctx context.Context, repo repository.HostedRepository, id, version string) (repository.NuGetPublication, error) {
	items, err := m.proxyMetadata(ctx, repo, id)
	if err != nil {
		return repository.NuGetPublication{}, err
	}
	var selected repository.NuGetPublication
	for _, p := range items {
		if p.Version == version {
			selected = p
			break
		}
	}
	if selected.Version == "" {
		return selected, repository.ErrNotFound
	}
	expected, err := metadata(selected)
	if err != nil {
		return selected, errSourceRejected
	}
	ctx, release, err := m.store.LockNuGetPublication(ctx, repo.ID, id, version)
	if err != nil {
		return selected, err
	}
	defer release()
	if err = m.validateSource(ctx, repo); err != nil {
		return selected, err
	}
	if publication, err := m.store.GetNuGetPublication(ctx, repo.ID, id, version); err == nil {
		actual, e := metadata(publication)
		if e != nil || publication.SourceURL != selected.SourceURL || publication.RegistrationURL != selected.RegistrationURL || expected.PackageHash != "" && actual.PackageHash != expected.PackageHash {
			return selected, errSourceRejected
		}
		a, e := nugetprotocol.CanonicalDependencyGroups(actual.DependencyGroups)
		b, e2 := nugetprotocol.CanonicalDependencyGroups(expected.DependencyGroups)
		if e != nil || e2 != nil || !bytes.Equal(a, b) {
			return selected, errSourceRejected
		}
		return m.verifyPublication(ctx, publication)
	} else if !errors.Is(err, repository.ErrNotFound) {
		return selected, err
	}
	response, err := m.upstream.FetchNuGet(ctx, repo, selected.SourceURL, http.Header{"Accept": []string{"application/octet-stream"}})
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return selected, err
	}
	if response == nil || response.Body == nil {
		return selected, errSourceRejected
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 200 || response.ContentLength > m.maxBytes || response.Header.Get("Content-Encoding") != "" && response.Header.Get("Content-Encoding") != "identity" {
		return selected, errSourceRejected
	}
	spool, err := uploadspool.Read(response.Body, m.maxBytes)
	if err != nil {
		return selected, errSourceRejected
	}
	defer func() { _ = spool.Close() }()
	actual, _, err := inspectSpool(spool)
	if err != nil || !strings.EqualFold(actual.ID, id) || actual.Version != version {
		return selected, errSourceRejected
	}
	if expected.PackageHash == "" {
		sidecar, e := m.upstream.FetchNuGet(ctx, repo, selected.SourceURL+".sha512", http.Header{"Accept": []string{"text/plain"}})
		if e != nil {
			if sidecar != nil && sidecar.Body != nil {
				_ = sidecar.Body.Close()
			}
			return selected, e
		}
		if sidecar == nil || sidecar.Body == nil {
			return selected, errSourceRejected
		}
		body, e := io.ReadAll(io.LimitReader(sidecar.Body, 257))
		_ = sidecar.Body.Close()
		if e != nil || sidecar.StatusCode != 200 || len(body) > 256 {
			return selected, errSourceRejected
		}
		expected.PackageHash = strings.TrimSpace(string(body))
	}
	if actual.PackageHash != expected.PackageHash {
		return selected, errSourceRejected
	}
	a, e := nugetprotocol.CanonicalDependencyGroups(actual.DependencyGroups)
	b, e2 := nugetprotocol.CanonicalDependencyGroups(expected.DependencyGroups)
	if e != nil || e2 != nil || !bytes.Equal(a, b) {
		return selected, errSourceRejected
	}
	// The store replays the active canonical identity within this source
	// namespace. A new random session ID permits recovery after an incomplete
	// reservation expires, without reusing its object key or collection fence.
	session, err := m.reserve(ctx, repo, "nuget-proxy-cache", "", publicationRequest{PackageID: id, Version: version, Digest: spool.Digest(), Size: spool.Size()})
	if err != nil {
		return selected, err
	}
	session.SourceURL, session.RegistrationURL = selected.SourceURL, selected.RegistrationURL
	session, err = m.stage(ctx, session, spool)
	if err != nil {
		return selected, err
	}
	return m.commit(ctx, session, session.Actor)
}
func (m *Module) verifyPublication(ctx context.Context, p repository.NuGetPublication) (repository.NuGetPublication, error) {
	session, err := m.store.GetNuGetSession(ctx, p.SessionID)
	if err != nil {
		return p, err
	}
	return m.commit(ctx, session, session.Actor)
}

func (m *Module) searchMetadata(ctx context.Context, repo repository.HostedRepository, query url.Values, autocomplete bool) ([]repository.NuGetPublication, error) {
	if repo.Type != repository.RepositoryTypeProxy {
		return m.all(ctx, repo.ID, "")
	}
	if id := query.Get("id"); id != "" {
		canonical, err := nugetprotocol.NormalizeID(id)
		if err != nil {
			return nil, errUnsupportedSource
		}
		return m.proxyMetadata(ctx, repo, canonical)
	}
	resources, err := m.resources(ctx, repo)
	if err != nil {
		return nil, err
	}
	base := resources.query
	if autocomplete {
		base = resources.autocomplete
	}
	if base == "" {
		return nil, errUnsupportedSource
	}
	ids := []string{}
	seen := map[string]bool{}
	total := -1
	for skip := 0; skip < 4096; skip += 100 {
		u, _ := url.Parse(base)
		q := u.Query()
		q.Set("q", query.Get("q"))
		q.Set("skip", strconv.Itoa(skip))
		q.Set("take", "100")
		q.Set("prerelease", query.Get("prerelease"))
		q.Set("semVerLevel", "2.0.0")
		u.RawQuery = q.Encode()
		doc, err := m.document(ctx, repo, u.String())
		if err != nil {
			return nil, err
		}
		if doc.Status != 200 {
			return nil, repository.ErrNotFound
		}
		var list struct {
			TotalHits int               `json:"totalHits"`
			Data      []json.RawMessage `json:"data"`
		}
		if sourceJSON(doc.Body, &list) != nil || list.TotalHits < 0 || list.TotalHits > 4096 || len(list.Data) > 100 || total >= 0 && total != list.TotalHits {
			return nil, errUnsupportedSource
		}
		total = list.TotalHits
		if len(list.Data) != min(100, max(0, total-skip)) {
			return nil, errUnsupportedSource
		}
		for _, raw := range list.Data {
			var id string
			if autocomplete {
				if json.Unmarshal(raw, &id) != nil {
					return nil, errUnsupportedSource
				}
			} else {
				var entry struct {
					ID string `json:"id"`
				}
				if sourceJSON(raw, &entry) != nil {
					return nil, errUnsupportedSource
				}
				id = entry.ID
			}
			canonical, err := nugetprotocol.NormalizeID(id)
			if err != nil || seen[canonical] {
				return nil, errUnsupportedSource
			}
			seen[canonical] = true
			ids = append(ids, canonical)
		}
		if skip+len(list.Data) >= total {
			break
		}
	}
	out := []repository.NuGetPublication{}
	for _, id := range ids {
		items, err := m.proxyMetadata(ctx, repo, id)
		if err != nil {
			return nil, err
		}
		if len(out)+len(items) > 4096 {
			return nil, errMetadataLimit
		}
		out = append(out, items...)
	}
	return out, nil
}
