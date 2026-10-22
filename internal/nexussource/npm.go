package nexussource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	npmprotocol "github.com/ccsert/artifact-gateway/internal/protocol/npm"
)

// NPMPackument is a bounded authenticated observation. Paths are reconstructed
// only after every dist URL passes the configured source/repository boundary.
type NPMPackument struct {
	Body         []byte
	Validator    string
	TarballPaths map[string]string
}

func (s *Service) ReadNPMPackument(ctx context.Context, id, version, repository, name string) (NPMPackument, error) {
	if !sourceName.MatchString(repository) || !npmprotocol.ValidPackageName(name) {
		return NPMPackument{}, fail("invalid_request")
	}
	remote, source, done, err := s.connect(ctx, id)
	if err != nil {
		return NPMPackument{}, err
	}
	defer done()
	if source.Version != version {
		return NPMPackument{}, fail("source_version_changed")
	}
	u := url.URL{Scheme: remote.base.Scheme, Host: remote.base.Host, Path: remote.base.Path + "/repository/" + repository + "/" + name}
	req, err := http.NewRequestWithContext(remote.ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return NPMPackument{}, fail("source_invalid_response")
	}
	req.Header.Set("Accept", "application/json")
	if remote.credential.Username != "" {
		req.SetBasicAuth(remote.credential.Username, remote.credential.Password)
	}
	resp, err := remote.client.Do(req)
	if err != nil {
		return NPMPackument{}, sourceRequestError(remote.ctx, err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case 200:
	case 401, 403:
		return NPMPackument{}, fail("source_unauthorized")
	case 404:
		return NPMPackument{}, fail("source_missing")
	default:
		return NPMPackument{}, fail("source_download_failed")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, npmprotocol.ImportPackumentLimit+1))
	if sourceTimedOut(remote.ctx, err) {
		return NPMPackument{}, fail("source_timeout")
	}
	if err != nil || len(body) > npmprotocol.ImportPackumentLimit {
		return NPMPackument{}, fail("source_limit_exceeded")
	}
	p, err := npmprotocol.DecodeImportPackument(body, name)
	if err != nil {
		return NPMPackument{}, fail("npm_packument_invalid")
	}
	out := NPMPackument{Body: body, TarballPaths: map[string]string{}}
	prefix := remote.base.Path + "/repository/" + repository + "/"
	for version, raw := range p.Versions {
		var v npmprotocol.ImportVersion
		_ = json.Unmarshal(raw, &v)
		target, err := url.Parse(v.Dist.Tarball)
		if err != nil || target.Scheme != "https" || !strings.EqualFold(target.Host, remote.base.Host) || target.User != nil || target.RawQuery != "" || target.ForceQuery || target.Fragment != "" || target.Opaque != "" || !strings.HasPrefix(target.Path, prefix) {
			return NPMPackument{}, fail("source_download_url_denied")
		}
		path := strings.TrimPrefix(target.Path, prefix)
		canonical, _, reason := assetRoute("npm", path)
		if reason != "" || canonical != path || !strings.HasPrefix(path, name+"/-/") {
			return NPMPackument{}, fail("source_download_url_denied")
		}
		out.TarballPaths[version] = path
	}
	etag := resp.Header.Get("ETag")
	if strings.HasPrefix(etag, "\"") && strings.HasSuffix(etag, "\"") && len(etag) <= 512 && !strings.ContainsAny(etag, "\r\n\x00") {
		out.Validator = "etag:" + etag
	}
	return out, nil
}

// npm pages use Components REST so the inventory identity never relies on a
// filename convention. The tarball must be the component's only binary asset.
func (s *session) npmInventoryPage(repo Repository, cursor string) (InventoryPage, error) {
	query := url.Values{"repository": {repo.Name}}
	if cursor != "" {
		query.Set("continuationToken", cursor)
	}
	var raw struct {
		Items *[]struct {
			Repository, Format, Name, Group, Version string
			Assets                                   []assetRaw
		}
		ContinuationToken json.RawMessage
	}
	before := s.stats.ResponseBytes
	if err := s.read("components", query, &raw); err != nil {
		return InventoryPage{}, err
	}
	s.stats.Pages++
	if raw.Items == nil || len(raw.ContinuationToken) == 0 {
		return InventoryPage{}, fail("source_invalid_response")
	}
	var next *string
	if json.Unmarshal(raw.ContinuationToken, &next) != nil {
		return InventoryPage{}, fail("source_invalid_response")
	}
	out := InventoryPage{Assets: []Asset{}, UnsupportedAssets: []UnsupportedAsset{}, MetadataBytes: s.stats.ResponseBytes - before}
	if next != nil {
		out.NextCursor = *next
	}
	if !validInventoryCursor(out.NextCursor) || out.NextCursor != "" && out.NextCursor == cursor {
		return out, fail("source_pagination_cycle")
	}
	seen := map[string]bool{}
	for _, component := range *raw.Items {
		name := component.Name
		if component.Group != "" {
			name = strings.TrimSuffix(component.Group, "/") + "/" + name
			if !strings.HasPrefix(name, "@") {
				name = "@" + name
			}
		}
		if component.Repository != repo.Name || component.Format != "npm" || !npmprotocol.ValidPackageName(name) || !npmprotocol.ValidVersion(component.Version) || len(component.Assets) != 1 {
			return out, fail("npm_component_identity_invalid")
		}
		a, err := s.asset(repo, component.Assets[0])
		if err != nil {
			return out, err
		}
		if !strings.HasPrefix(a.Path, name+"/-/") || seen[a.Path] {
			return out, fail("npm_component_identity_invalid")
		}
		a.PackageName, a.Version = name, component.Version
		seen[a.Path] = true
		out.Assets = append(out.Assets, a)
	}
	sort.Slice(out.Assets, func(i, j int) bool { return out.Assets[i].Path < out.Assets[j].Path })
	metadata := append([]Asset(nil), out.Assets...)
	for i := range metadata {
		metadata[i].DownloadURL = ""
	}
	canonical, _ := json.Marshal(metadata)
	sum := sha256.Sum256(canonical)
	out.MetadataSHA256 = hex.EncodeToString(sum[:])
	return out, nil
}
