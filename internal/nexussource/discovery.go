package nexussource

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/ccsert/artifact-gateway/internal/egress"
	"github.com/ccsert/artifact-gateway/internal/secrets"
)

const (
	maxRepositories  = 500
	maxRequests      = 128
	maxResponseBytes = 2 << 20
	maxTotalBytes    = 16 << 20
)

type session struct {
	ctx        context.Context
	client     *http.Client
	base       *url.URL
	credential Credentials
	stats      Stats
}

func (s *Service) connect(ctx context.Context, id string) (*session, Source, context.CancelFunc, error) {
	timeout := s.options.OperationTimeout
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	bad := func(err error) (*session, Source, context.CancelFunc, error) {
		cancel()
		return nil, Source{}, func() {}, err
	}
	source, err := s.store.Get(ctx, id)
	if err != nil {
		return bad(err)
	}
	base, ok := validBase(source.BaseURL)
	if !ok {
		return bad(fail("invalid_source"))
	}
	var credential Credentials
	if source.CredentialCiphertext != "" {
		plaintext, err := secrets.Open("nexus-source:"+source.ID, source.CredentialCiphertext)
		if err != nil || json.Unmarshal([]byte(plaintext), &credential) != nil {
			return bad(fail("encryption_key_unavailable"))
		}
	}
	hooks := egress.DefaultHooks()
	if s.options.EgressHooks != nil {
		hooks = *s.options.EgressHooks
	}
	hooks.PrivateEndpointCIDRs = s.options.PrivateEndpoints
	client, err := egress.ApplyContext(ctx, s.options.Client, nil, base.String(), hooks)
	if err != nil {
		if sourceTimedOut(ctx, err) {
			return bad(fail("source_timeout"))
		}
		return bad(fail("source_egress_denied"))
	}
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	client.Timeout = timeout
	return &session{ctx: ctx, client: client, base: base, credential: credential}, source, func() { client.CloseIdleConnections(); cancel() }, nil
}
func (s *session) read(endpoint string, query url.Values, out any) error {
	if sourceTimedOut(s.ctx, nil) {
		return fail("source_timeout")
	}
	if s.stats.Requests >= maxRequests {
		return fail("source_limit_exceeded")
	}
	u := *s.base
	u.Path += "/service/rest/v1/" + endpoint
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fail("source_invalid_response")
	}
	req.Header.Set("Accept", "application/json")
	if s.credential.Username != "" {
		req.SetBasicAuth(s.credential.Username, s.credential.Password)
	}
	s.stats.Requests++
	resp, err := s.client.Do(req)
	if err != nil {
		return sourceRequestError(s.ctx, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if sourceTimedOut(s.ctx, nil) {
		return fail("source_timeout")
	}
	switch {
	case resp.StatusCode == 401:
		return fail("source_unauthorized")
	case resp.StatusCode == 403:
		return fail("source_forbidden")
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return fail("source_redirect_denied")
	case resp.StatusCode == 404:
		return fail("source_api_unsupported")
	case resp.StatusCode != 200:
		return fail("source_http_error")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	s.stats.ResponseBytes += int64(len(body))
	if sourceTimedOut(s.ctx, err) {
		return fail("source_timeout")
	}
	if err != nil {
		return fail("source_invalid_response")
	}
	if len(body) > maxResponseBytes || s.stats.ResponseBytes > maxTotalBytes {
		return fail("source_limit_exceeded")
	}
	decodeErr := json.Unmarshal(body, out)
	if sourceTimedOut(s.ctx, nil) {
		return fail("source_timeout")
	}
	if decodeErr != nil {
		return fail("source_invalid_response")
	}
	return nil
}

type repoRaw struct{ Name, Format, Type string }

func (s *session) repositories() ([]Repository, error) {
	var values []repoRaw
	if err := s.read("repositories", nil, &values); err != nil {
		return nil, err
	}
	if values == nil {
		return nil, fail("source_invalid_response")
	}
	if len(values) > maxRepositories {
		return nil, fail("source_limit_exceeded")
	}
	out := make([]Repository, 0, len(values))
	seen := map[string]bool{}
	for _, v := range values {
		if !sourceName.MatchString(v.Name) || !sourceName.MatchString(v.Format) || !sourceName.MatchString(v.Type) || seen[v.Name] {
			return nil, fail("source_invalid_response")
		}
		seen[v.Name] = true
		r := Repository{Name: v.Name, Format: v.Format, Type: v.Type, Members: []string{}, Fields: []Field{}, Status: "unsupported", Reason: "format_unsupported"}
		if _, supported := NativeFormat(v.Format); supported {
			if v.Type == "hosted" || v.Type == "proxy" || v.Type == "group" {
				r.Status = "preview_only"
				r.Reason = "configuration_not_read"
			} else {
				r.Reason = "type_unsupported"
			}
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (s *session) inspect(r Repository) (Repository, error) {
	if r.Status == "unsupported" {
		r.Fields = []Field{{"format", "unsupported", r.Reason}, {"security", "unsupported", "permissions_and_credentials_are_not_migrated"}}
		return r, nil
	}
	format := r.Format
	if format == "maven2" {
		format = "maven"
	}
	var raw map[string]json.RawMessage
	if err := s.read("repositories/"+format+"/"+r.Type+"/"+r.Name, nil, &raw); err != nil {
		return r, err
	}
	if raw == nil {
		return r, fail("source_invalid_response")
	}
	var details struct {
		Name  string `json:"name"`
		Maven *struct {
			VersionPolicy string `json:"versionPolicy"`
		} `json:"maven"`
		Group *struct {
			MemberNames []string `json:"memberNames"`
		} `json:"group"`
		Proxy *struct {
			RemoteURL string `json:"remoteUrl"`
		} `json:"proxy"`
	}
	encoded, _ := json.Marshal(raw)
	if json.Unmarshal(encoded, &details) != nil || details.Name != r.Name {
		return r, fail("source_invalid_response")
	}
	r.Status, r.Reason = "supported", "hosted_read_only_inventory"
	if r.Type == "group" {
		if details.Group == nil || details.Group.MemberNames == nil || len(details.Group.MemberNames) > maxRepositories {
			return r, fail("source_invalid_response")
		}
		seen := map[string]bool{}
		for _, n := range details.Group.MemberNames {
			if !sourceName.MatchString(n) || seen[n] {
				return r, fail("source_invalid_response")
			}
			seen[n] = true
		}
		r.Members = details.Group.MemberNames
		r.Status, r.Reason = "preview_only", "group_configuration_only"
	}
	if r.Type == "proxy" {
		if details.Proxy == nil {
			return r, fail("source_invalid_response")
		}
		if u, ok := validBase(details.Proxy.RemoteURL); ok {
			r.RemoteURL = u.String()
			r.Status, r.Reason = "preview_only", "proxy_configuration_only"
		} else {
			r.Status, r.Reason = "unsupported", "upstream_url_unsupported"
		}
	}
	if r.Format == "maven2" && r.Type != "group" {
		if details.Maven == nil {
			return r, fail("source_invalid_response")
		}
		switch details.Maven.VersionPolicy {
		case "RELEASE", "SNAPSHOT", "MIXED":
			r.VersionPolicy = details.Maven.VersionPolicy
		default:
			r.VersionPolicy = "UNKNOWN"
		}
		if r.VersionPolicy != "RELEASE" && r.VersionPolicy != "MIXED" {
			r.Status, r.Reason = "unsupported", "maven_release_policy_required"
		}
	}
	r.Fields = classifyFields(raw, r)
	return r, nil
}
func classifyFields(raw map[string]json.RawMessage, r Repository) []Field {
	fields := map[string]Field{}
	var visit func(map[string]json.RawMessage, string, int)
	visit = func(values map[string]json.RawMessage, prefix string, depth int) {
		for key, value := range values {
			if !sourceName.MatchString(key) || len(key) > 64 {
				continue
			}
			key = prefix + key
			var child map[string]json.RawMessage
			if depth < 2 && json.Unmarshal(value, &child) == nil && len(child) > 0 {
				visit(child, key+".", depth+1)
				continue
			}
			disposition, reason := "unsupported", "source_specific_configuration"
			switch key {
			case "name", "format", "type", "group.memberNames", "proxy.remoteUrl":
				disposition, reason = "expressible", "native_repository_configuration"
			case "maven.versionPolicy":
				if r.VersionPolicy == "RELEASE" || r.VersionPolicy == "MIXED" {
					disposition, reason = "expressible", "maven_release_policy"
				} else {
					reason = "maven_release_policy_required"
				}
			case "online", "storage.blobStoreName", "storage.strictContentTypeValidation", "storage.writePolicy", "cleanup.policyNames", "negativeCache.enabled", "negativeCache.timeToLive", "maven.layoutPolicy", "maven.contentDisposition", "proxy.contentMaxAge", "proxy.metadataMaxAge", "httpClient.blocked", "httpClient.autoBlock":
				disposition, reason = "manual", "configure_native_target_policy_explicitly"
			}
			fields[key] = Field{key, disposition, reason}
		}
	}
	visit(raw, "", 0)
	fields["security"] = Field{"security", "unsupported", "permissions_and_credentials_are_not_migrated"}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]Field, 0, len(keys))
	for _, key := range keys {
		out = append(out, fields[key])
	}
	return out
}
func selectRepositories(all []Repository, names []string) ([]Repository, error) {
	if len(names) < 1 || len(names) > 10 {
		return nil, fail("invalid_request")
	}
	indexed := map[string]Repository{}
	for _, r := range all {
		indexed[r.Name] = r
	}
	out := make([]Repository, 0, len(names))
	seen := map[string]bool{}
	for _, n := range names {
		if !sourceName.MatchString(n) || seen[n] {
			return nil, fail("invalid_request")
		}
		seen[n] = true
		r, ok := indexed[n]
		if !ok {
			return nil, fail("source_repository_not_visible")
		}
		out = append(out, r)
	}
	return out, nil
}
func (s *Service) Preflight(ctx context.Context, id string) (Preflight, error) {
	remote, source, done, err := s.connect(ctx, id)
	if err != nil {
		return Preflight{}, err
	}
	defer done()
	repos, err := remote.repositories()
	if err != nil {
		return Preflight{}, err
	}
	// Inspect is explicitly bounded, even when the source has hundreds of repositories.
	if len(repos) > 100 {
		return Preflight{}, fail("source_limit_exceeded")
	}
	for i, r := range repos {
		repos[i], err = remote.inspect(r)
		if err != nil {
			return Preflight{}, err
		}
	}
	return Preflight{source.ID, source.Version, repos, remote.stats}, nil
}
func (s *Service) Discover(ctx context.Context, id string, in DiscoveryInput) (Discovery, error) {
	if in.MaxPages == 0 {
		in.MaxPages = 25
	}
	if in.MaxAssets == 0 {
		in.MaxAssets = 10000
	}
	if in.MaxPages < 1 || in.MaxPages > 100 || in.MaxAssets < 1 || in.MaxAssets > 10000 {
		return Discovery{}, fail("invalid_request")
	}
	if _, err := selectRepositories(nil, in.Repositories); err != nil {
		var f *Failure
		if !errors.As(err, &f) || f.Code != "source_repository_not_visible" {
			return Discovery{}, err
		}
	}
	remote, source, done, err := s.connect(ctx, id)
	if err != nil {
		return Discovery{}, err
	}
	defer done()
	all, err := remote.repositories()
	if err != nil {
		return Discovery{}, err
	}
	repos, err := selectRepositories(all, in.Repositories)
	if err != nil {
		return Discovery{}, err
	}
	out := Discovery{Preflight: Preflight{SourceID: source.ID, SourceVersion: source.Version}, Assets: []Asset{}, UnsupportedAssets: []UnsupportedAsset{}}
	for i, r := range repos {
		repos[i], err = remote.inspect(r)
		if err != nil {
			return Discovery{}, err
		}
		if repos[i].Status != "supported" || r.Type != "hosted" {
			continue
		}
		assets, unsupported, err := remote.assets(repos[i], in.MaxPages, in.MaxAssets-len(out.Assets)-len(out.UnsupportedAssets))
		if err != nil {
			return Discovery{}, err
		}
		out.Assets = append(out.Assets, assets...)
		out.UnsupportedAssets = append(out.UnsupportedAssets, unsupported...)
	}
	out.Repositories, out.Stats = repos, remote.stats
	return out, nil
}

type assetRaw struct {
	Repository, Format, Path, DownloadURL string
	FileSize                              *int64 `json:"fileSize"`
	Checksum                              map[string]string
}

func (s *session) assets(repo Repository, maxPages, maxAssets int) ([]Asset, []UnsupportedAsset, error) {
	out, unsupported := []Asset{}, []UnsupportedAsset{}
	token := ""
	seenTokens, seenPaths := map[string]bool{}, map[string]bool{}
	for {
		if s.stats.Pages >= maxPages {
			return nil, nil, fail("source_limit_exceeded")
		}
		page, err := s.inventoryPage(repo, token)
		if err != nil {
			return nil, nil, err
		}
		if len(page.Assets)+len(page.UnsupportedAssets) > maxAssets-len(out)-len(unsupported) {
			return nil, nil, fail("source_limit_exceeded")
		}
		paths := make([]string, 0, len(page.Assets)+len(page.UnsupportedAssets))
		for _, a := range page.Assets {
			paths = append(paths, a.Path)
		}
		for _, a := range page.UnsupportedAssets {
			paths = append(paths, a.Path)
		}
		for _, p := range paths {
			if seenPaths[p] {
				return nil, nil, fail("source_duplicate_asset")
			}
			seenPaths[p] = true
		}
		out, unsupported = append(out, page.Assets...), append(unsupported, page.UnsupportedAssets...)
		if page.NextCursor == "" {
			return out, unsupported, nil
		}
		token = page.NextCursor
		if seenTokens[token] {
			return nil, nil, fail("source_pagination_cycle")
		}
		seenTokens[token] = true
	}
}
func (s *session) asset(repo Repository, raw assetRaw) (Asset, error) {
	format, supported := NativeFormat(repo.Format)
	canonical, wire, _ := assetRoute(format, raw.Path)
	if raw.Repository != repo.Name || raw.Format != repo.Format || raw.FileSize == nil || *raw.FileSize < 0 || !supported || canonical == "" {
		return Asset{}, fail("source_invalid_response")
	}
	if repo.Format == "maven2" && repo.VersionPolicy == "RELEASE" && strings.Contains(raw.Path, "-SNAPSHOT/") {
		return Asset{}, fail("source_snapshot_unsupported")
	}
	u, err := url.Parse(raw.DownloadURL)
	expected := s.base.Path + "/repository/" + repo.Name + "/" + wire
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, s.base.Host) || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Path != expected || u.Opaque != "" {
		return Asset{}, fail("source_download_url_denied")
	}
	checksum := map[string]string{}
	for algorithm, value := range raw.Checksum {
		size := 0
		switch algorithm {
		case "sha256":
			size = 64
		case "sha1":
			size = 40
		case "md5":
			size = 32
		case "sha512":
			size = 128
		default:
			continue
		}
		if len(value) != size || strings.Trim(value, "0123456789abcdefABCDEF") != "" {
			return Asset{}, fail("source_invalid_response")
		}
		checksum[algorithm] = strings.ToLower(value)
	}
	// Equivalent source escaping (such as %2B or %2b for a literal +) is
	// acceptable only after the decoded path exactly matches safe metadata.
	// Reconstruct the URL so source RawPath aliases never cross this boundary.
	downloadURL := (&url.URL{Scheme: s.base.Scheme, Host: s.base.Host, Path: expected}).String()
	return Asset{repo.Name, canonical, downloadURL, *raw.FileSize, checksum}, nil
}
func validAssetPath(value string) bool {
	if value == "" || len(value) > 2048 || strings.HasPrefix(value, "/") || path.Clean(value) != value || strings.ContainsAny(value, "\\\r\n\x00?#%") {
		return false
	}
	for _, p := range strings.Split(value, "/") {
		if p == "." || p == ".." || p == "" {
			return false
		}
	}
	return true
}

func mavenUnsupportedPath(repo Repository, value string) string {
	if repo.Format != "maven2" || repo.VersionPolicy != "MIXED" {
		return ""
	}
	parts := strings.Split(value, "/")
	for _, part := range parts[:len(parts)-1] {
		if strings.HasSuffix(strings.ToUpper(part), "-SNAPSHOT") {
			return "maven_snapshot_unsupported"
		}
	}
	if strings.HasPrefix(parts[len(parts)-1], "maven-metadata.xml") {
		return "maven_mixed_metadata_requires_regeneration"
	}
	if len(parts) < 4 {
		return "maven_path_unclassifiable"
	}
	artifact, version, filename := parts[len(parts)-3], parts[len(parts)-2], parts[len(parts)-1]
	if artifact == "" || version == "" || !strings.HasPrefix(filename, artifact+"-"+version) || len(filename) == len(artifact)+len(version)+1 {
		return "maven_path_unclassifiable"
	}
	return ""
}
