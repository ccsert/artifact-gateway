package nexussource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sort"

	pypi "github.com/ccsert/artifact-gateway/internal/protocol/pypi"
)

// PyPISimple contains canonical, bounded file selection facts, never source
// credentials or caller-controlled download URLs.
type PyPISimple struct{ Body []byte }

func (s *Service) ReadPyPISimple(ctx context.Context, id, version, repository, project string) (PyPISimple, error) {
	if !sourceName.MatchString(repository) || !pypi.ValidName(project) {
		return PyPISimple{}, fail("invalid_request")
	}
	remote, source, done, err := s.connect(ctx, id)
	if err != nil {
		return PyPISimple{}, err
	}
	defer done()
	if source.Version != version {
		return PyPISimple{}, fail("source_version_changed")
	}
	prefix := remote.base.Path + "/repository/" + repository + "/"
	u := url.URL{Scheme: remote.base.Scheme, Host: remote.base.Host, Path: prefix + "simple/" + project + "/"}
	request, err := http.NewRequestWithContext(remote.ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return PyPISimple{}, fail("invalid_request")
	}
	request.Header.Set("Accept", "text/html, application/vnd.pypi.simple.v1+html")
	if remote.credential.Username != "" {
		request.SetBasicAuth(remote.credential.Username, remote.credential.Password)
	}
	resp, err := remote.client.Do(request)
	if err != nil {
		return PyPISimple{}, sourceRequestError(remote.ctx, err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case 200:
	case 401, 403:
		return PyPISimple{}, fail("source_unauthorized")
	case 404:
		return PyPISimple{}, fail("source_missing")
	default:
		return PyPISimple{}, fail("source_download_failed")
	}
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || (media != "text/html" && media != "application/vnd.pypi.simple.v1+html") {
		return PyPISimple{}, fail("pypi_simple_invalid")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, pypi.ImportMetadataLimit+1))
	if sourceTimedOut(remote.ctx, err) {
		return PyPISimple{}, fail("source_timeout")
	}
	if err != nil || len(body) > pypi.ImportMetadataLimit {
		return PyPISimple{}, fail("source_limit_exceeded")
	}
	snapshot, err := pypi.ParseSimple(body, project, &u, prefix)
	if err != nil {
		return PyPISimple{}, fail("pypi_simple_invalid")
	}
	canonical, err := snapshot.Canonical()
	if err != nil || len(canonical) > pypi.ImportMetadataLimit {
		return PyPISimple{}, fail("source_limit_exceeded")
	}
	return PyPISimple{Body: canonical}, nil
}

// Components carry explicit identity and their Assets records. The durable
// inventory reads every page twice and checks the full identity/asset digest.
func (s *session) pypiInventoryPage(repo Repository, cursor string) (InventoryPage, error) {
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
	for _, c := range *raw.Items {
		name := pypi.NormalizeName(c.Name)
		if c.Repository != repo.Name || c.Format != "pypi" || c.Group != "" || !pypi.ValidName(name) || !pypi.ValidVersion(c.Version) || len(c.Assets) < 1 || len(c.Assets) > pypi.ImportFileCountLimit {
			return out, fail("pypi_component_identity_invalid")
		}
		for _, rawAsset := range c.Assets {
			a, err := s.asset(repo, rawAsset)
			if err != nil {
				return out, err
			}
			_, _, reason := assetRoute("pypi", a.Path)
			if reason != "" {
				return out, fail("pypi_source_shape_unsupported")
			}
			a.PackageName, a.Version = name, c.Version
			if seen[a.Path] {
				return out, fail("source_duplicate_asset")
			}
			seen[a.Path] = true
			out.Assets = append(out.Assets, a)
		}
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
