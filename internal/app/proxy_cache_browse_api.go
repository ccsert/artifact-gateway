package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type proxyCacheBrowseHandler struct {
	store         repository.HostedRepositoryStore
	maintenance   *CacheMaintenance
	authenticator Authenticator
	authorizer    RepositoryAuthorizer
}

type proxyCacheInvalidateRequest struct {
	Path   string `json:"path"`
	Prefix bool   `json:"prefix"`
}

type proxyCacheInvalidateResponse struct {
	Invalidated int `json:"invalidated"`
}

type proxyCacheNegativeClearResponse struct {
	Cleared int `json:"cleared"`
}

type proxyCacheBrowsePage struct {
	Items         []proxyCacheBrowseItem `json:"items"`
	NextPageToken *string                `json:"nextPageToken,omitempty"`
	TotalEstimate int                    `json:"totalEstimate"`
	GroupBy       string                 `json:"groupBy"`
}

type proxyCacheBrowseItem struct {
	Key               string            `json:"key"`
	Coordinate        string            `json:"coordinate"`
	GroupID           string            `json:"groupId,omitempty"`
	ArtifactID        string            `json:"artifactId,omitempty"`
	Version           string            `json:"version,omitempty"`
	Path              string            `json:"path,omitempty"`
	Digest            string            `json:"digest,omitempty"`
	Size              int64             `json:"size,omitempty"`
	ContentType       string            `json:"contentType,omitempty"`
	Member            string            `json:"member,omitempty"`
	AssetCount        int               `json:"assetCount,omitempty"`
	PrimaryAssetCount int               `json:"primaryAssetCount,omitempty"`
	SidecarCount      int               `json:"sidecarCount,omitempty"`
	Extensions        []string          `json:"extensions,omitempty"`
	Assets            []proxyCacheAsset `json:"assets,omitempty"`
}

type proxyCacheAsset struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	Digest      string `json:"digest,omitempty"`
	Size        int64  `json:"size,omitempty"`
	ContentType string `json:"contentType,omitempty"`
	Member      string `json:"member,omitempty"`
	Sidecar     bool   `json:"sidecar"`
}

type proxyCacheBrowseCursor struct {
	RepositoryID string `json:"repositoryId"`
	Format       string `json:"format"`
	GroupBy      string `json:"groupBy"`
	AssetFilter  string `json:"assetFilter"`
	Query        string `json:"q"`
	Offset       int    `json:"offset"`
}

type mavenCacheCoordinate struct {
	GroupID    string
	ArtifactID string
	Version    string
	FileName   string
}

func (h proxyCacheBrowseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.maintenance == nil {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "cache browsing is not enabled")
		return
	}
	repositoryID := r.PathValue("repositoryId")
	repo, err := h.store.GetHostedRepository(r.Context(), repositoryID)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get repository failed")
		return
	}
	if repo.Type != repository.RepositoryTypeProxy {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_repository", "cache browse is only available for proxy repositories")
		return
	}
	principal, ok := h.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok {
		if anonymousHostedRepositoryReadAllowed(repo, r.Method) {
			principal = anonymousPrincipal()
		} else {
			writeHostedProblem(w, http.StatusUnauthorized, "access_denied", "authentication is required")
			return
		}
	}
	if !isAnonymous(principal) {
		decision := h.authorizer.Authorize(r.Context(), principal, repo, RepositoryRead)
		if !decision.Allowed {
			writeHostedProblem(w, http.StatusForbidden, "access_denied", "repository read permission is required")
			return
		}
	}

	pageSize := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("pageSize")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 200 {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "pageSize must be between 1 and 200")
			return
		}
		pageSize = parsed
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	groupBy := strings.TrimSpace(r.URL.Query().Get("groupBy"))
	if groupBy == "" {
		groupBy = "version"
	}
	if groupBy != "version" && groupBy != "component" && groupBy != "asset" {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "groupBy must be one of version, component, asset")
		return
	}
	assetFilter := strings.TrimSpace(r.URL.Query().Get("assetFilter"))
	if assetFilter == "" {
		assetFilter = "primary"
	}
	if assetFilter != "primary" && assetFilter != "all" && assetFilter != "jar" && assetFilter != "pom" {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "assetFilter must be one of primary, all, jar, pom")
		return
	}
	offset, err := h.decodeProxyCacheCursor(r.URL.Query().Get("pageToken"), repo.ID, string(repo.Format), groupBy, assetFilter, query)
	if err != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_page_token", "page token is invalid or expired")
		return
	}

	items, err := h.proxyCacheItems(r.Context(), repo, query, groupBy, assetFilter)
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "storage_error", "unable to inspect proxy cache")
		return
	}
	total := len(items)
	if offset > total {
		offset = total
	}
	end := offset + pageSize
	if end > total {
		end = total
	}
	page := items[offset:end]
	var next *string
	if end < total {
		token := h.encodeProxyCacheCursor(proxyCacheBrowseCursor{RepositoryID: repo.ID, Format: string(repo.Format), GroupBy: groupBy, AssetFilter: assetFilter, Query: query, Offset: end})
		next = &token
	}
	writeNativeMavenJSON(w, http.StatusOK, proxyCacheBrowsePage{Items: page, NextPageToken: next, TotalEstimate: total, GroupBy: groupBy})
}

func (h proxyCacheBrowseHandler) Invalidate(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok {
		writeHostedProblem(w, http.StatusUnauthorized, "access_denied", "authentication is required")
		return
	}
	if h.maintenance == nil {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "cache operations are not enabled")
		return
	}
	repo, err := h.store.GetHostedRepository(r.Context(), r.PathValue("repositoryId"))
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get repository failed")
		return
	}
	if repo.Type != repository.RepositoryTypeProxy || repo.Format != repository.FormatMaven {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_repository", "Maven cache invalidation is only available for Maven proxy repositories")
		return
	}
	if decision := h.authorizer.Authorize(r.Context(), principal, repo, RepositoryAdmin); !decision.Allowed {
		writeHostedProblem(w, http.StatusForbidden, "access_denied", "repository admin permission is required")
		return
	}
	var request proxyCacheInvalidateRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || strings.TrimSpace(request.Path) == "" {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "path is required")
		return
	}
	path := strings.Trim(strings.TrimSpace(request.Path), "/")
	keys, err := h.maintenance.store.List(r.Context(), "maven/index/")
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "storage_error", "unable to inspect proxy cache")
		return
	}
	invalidated := 0
	for _, key := range keys {
		encoded, err := h.maintenance.store.Get(r.Context(), key)
		if err != nil {
			continue
		}
		var index cacheIndexRecord
		if json.Unmarshal(encoded, &index) != nil || index.repository() != repo.Name {
			continue
		}
		cachedPath := strings.Trim(index.path(), "/")
		matches := cachedPath == path
		if request.Prefix {
			matches = cachedPath == path || strings.HasPrefix(cachedPath, path+"/")
		}
		if matches {
			if err := h.maintenance.store.Delete(r.Context(), key); err == nil {
				invalidated++
			}
		}
	}
	writeNativeMavenJSON(w, http.StatusOK, proxyCacheInvalidateResponse{Invalidated: invalidated})
}

func (h proxyCacheBrowseHandler) ClearNegative(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok {
		writeHostedProblem(w, http.StatusUnauthorized, "access_denied", "authentication is required")
		return
	}
	if h.maintenance == nil {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "cache operations are not enabled")
		return
	}
	repo, err := h.store.GetHostedRepository(r.Context(), r.PathValue("repositoryId"))
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get repository failed")
		return
	}
	if repo.Type != repository.RepositoryTypeProxy || repo.Format != repository.FormatMaven {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_repository", "Maven negative cache clearing is only available for Maven proxy repositories")
		return
	}
	if decision := h.authorizer.Authorize(r.Context(), principal, repo, RepositoryAdmin); !decision.Allowed {
		writeHostedProblem(w, http.StatusForbidden, "access_denied", "repository admin permission is required")
		return
	}

	var request proxyCacheInvalidateRequest
	if r.Body != nil {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil && !errors.Is(err, io.EOF) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "request body is invalid")
			return
		}
	}
	path := strings.Trim(strings.TrimSpace(request.Path), "/")
	keys, err := h.maintenance.store.List(r.Context(), "maven/index/")
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "storage_error", "unable to inspect proxy cache")
		return
	}
	cleared := 0
	for _, key := range keys {
		encoded, err := h.maintenance.store.Get(r.Context(), key)
		if err != nil {
			continue
		}
		var index cacheIndexRecord
		if json.Unmarshal(encoded, &index) != nil || !index.negative() || index.repository() != repo.Name {
			continue
		}
		cachedPath := strings.Trim(index.path(), "/")
		matches := path == "" || cachedPath == path
		if request.Prefix && path != "" {
			matches = cachedPath == path || strings.HasPrefix(cachedPath, path+"/")
		}
		if matches {
			if err := h.maintenance.store.Delete(r.Context(), key); err == nil {
				cleared++
			}
		}
	}
	writeNativeMavenJSON(w, http.StatusOK, proxyCacheNegativeClearResponse{Cleared: cleared})
}

func (h proxyCacheBrowseHandler) proxyCacheItems(ctx context.Context, repo repository.HostedRepository, query, groupBy, assetFilter string) ([]proxyCacheBrowseItem, error) {
	switch repo.Format {
	case repository.FormatMaven:
		entries, err := (cacheEntriesHandler{maintenance: h.maintenance, authenticator: h.authenticator}).listMavenForRepository(ctx, repo.Name, map[string]bool{repo.Endpoint: true})
		if err != nil {
			return nil, err
		}
		return mavenProxyCacheBrowseItems(entries, query, groupBy, assetFilter), nil
	default:
		return nil, errUnsupportedCacheFormat
	}
}

func (h proxyCacheBrowseHandler) proxyCacheCapacity(ctx context.Context, repo repository.HostedRepository, capacity repository.RepositoryCapacity) (repository.RepositoryCapacity, error) {
	if h.maintenance == nil {
		return capacity, nil
	}
	switch repo.Format {
	case repository.FormatMaven:
		keys, err := h.maintenance.store.List(ctx, "maven/index/")
		if err != nil {
			return capacity, err
		}
		now := time.Now().UTC()
		capacity.UsedBytes = 0
		capacity.ObjectCount = 0
		capacity.PrimaryBytes = 0
		capacity.SidecarBytes = 0
		capacity.NegativeCount = 0
		capacity.ExpiredObjectCount = 0
		capacity.ReclaimableBytes = 0
		for _, key := range keys {
			encoded, err := h.maintenance.store.Get(ctx, key)
			if err != nil {
				continue
			}
			var index cacheIndexRecord
			if json.Unmarshal(encoded, &index) != nil || index.endpoint() != repo.Endpoint || index.repository() != repo.Name {
				continue
			}
			expired := !now.Before(index.expiresAt())
			if index.negative() {
				if expired {
					capacity.ExpiredObjectCount++
				} else {
					capacity.NegativeCount++
				}
				continue
			}
			size := index.size()
			if expired {
				capacity.ExpiredObjectCount++
				capacity.ReclaimableBytes += size
				continue
			}
			capacity.UsedBytes += size
			capacity.ObjectCount++
			parsed, ok := parseMavenCacheCoordinate(index.coordinate())
			if ok && isMavenSidecar(parsed.FileName) {
				capacity.SidecarBytes += size
			} else {
				capacity.PrimaryBytes += size
			}
		}
		return capacity, nil
	default:
		return capacity, nil
	}
}

func (h proxyCacheBrowseHandler) decodeProxyCacheCursor(raw, repositoryID, format, groupBy, assetFilter, query string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0, err
	}
	var cursor proxyCacheBrowseCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil {
		return 0, err
	}
	if cursor.RepositoryID != repositoryID || cursor.Format != format || cursor.GroupBy != groupBy || cursor.AssetFilter != assetFilter || cursor.Query != query || cursor.Offset < 0 {
		return 0, errors.New("cursor scope mismatch")
	}
	return cursor.Offset, nil
}

func (h proxyCacheBrowseHandler) encodeProxyCacheCursor(cursor proxyCacheBrowseCursor) string {
	encoded, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func mavenProxyCacheBrowseItems(entries []CacheEntry, query, groupBy, assetFilter string) []proxyCacheBrowseItem {
	q := strings.ToLower(strings.TrimSpace(query))
	assetsByVersion := make(map[string][]proxyCacheAsset)
	parsedByVersion := make(map[string]mavenCacheCoordinate)
	versionsByComponent := make(map[string]map[string]struct{})
	assets := make([]proxyCacheBrowseItem, 0, len(entries))
	for _, entry := range entries {
		parsed, ok := parseMavenCacheCoordinate(entry.Repository)
		if !ok {
			continue
		}
		coordinate := parsed.GroupID + ":" + parsed.ArtifactID + ":" + parsed.Version
		if q != "" && !strings.Contains(strings.ToLower(coordinate+" "+entry.Repository), q) {
			continue
		}
		sidecar := isMavenSidecar(parsed.FileName)
		extension := mavenPrimaryExtension(parsed.FileName)
		if assetFilter == "primary" && sidecar {
			continue
		}
		if assetFilter == "jar" && (sidecar || extension != "jar") {
			continue
		}
		if assetFilter == "pom" && (sidecar || extension != "pom") {
			continue
		}
		asset := proxyCacheAsset{Path: entry.Repository, Name: parsed.FileName, Digest: entry.Digest, Size: entry.Size, ContentType: entry.ContentType, Member: entry.Member, Sidecar: sidecar}
		assetsByVersion[coordinate] = append(assetsByVersion[coordinate], asset)
		parsedByVersion[coordinate] = parsed
		component := parsed.GroupID + ":" + parsed.ArtifactID
		if versionsByComponent[component] == nil {
			versionsByComponent[component] = make(map[string]struct{})
		}
		versionsByComponent[component][parsed.Version] = struct{}{}
		if groupBy == "asset" {
			assets = append(assets, proxyCacheBrowseItem{Key: entry.Repository, Coordinate: coordinate, GroupID: parsed.GroupID, ArtifactID: parsed.ArtifactID, Version: parsed.Version, Path: entry.Repository, Digest: entry.Digest, Size: entry.Size, ContentType: entry.ContentType, Member: entry.Member, AssetCount: 1, SidecarCount: boolInt(sidecar), Extensions: []string{mavenPrimaryExtension(parsed.FileName)}})
		}
	}
	if groupBy == "asset" {
		sort.Slice(assets, func(i, j int) bool { return assets[i].Path < assets[j].Path })
		return assets
	}
	if groupBy == "component" {
		items := make([]proxyCacheBrowseItem, 0, len(versionsByComponent))
		for component, versions := range versionsByComponent {
			parts := strings.SplitN(component, ":", 2)
			items = append(items, proxyCacheBrowseItem{Key: component, Coordinate: component, GroupID: parts[0], ArtifactID: parts[1], AssetCount: len(versions)})
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Coordinate < items[j].Coordinate })
		return items
	}
	items := make([]proxyCacheBrowseItem, 0, len(assetsByVersion))
	for coordinate, versionAssets := range assetsByVersion {
		parsed := parsedByVersion[coordinate]
		sort.Slice(versionAssets, func(i, j int) bool {
			return boolInt(versionAssets[i].Sidecar) < boolInt(versionAssets[j].Sidecar) || versionAssets[i].Name < versionAssets[j].Name
		})
		primaryBytes := int64(0)
		primaryCount := 0
		sidecarCount := 0
		extensions := map[string]struct{}{}
		for _, asset := range versionAssets {
			ext := mavenPrimaryExtension(asset.Name)
			extensions[ext] = struct{}{}
			if asset.Sidecar {
				sidecarCount++
				continue
			}
			primaryCount++
			primaryBytes += asset.Size
		}
		items = append(items, proxyCacheBrowseItem{Key: coordinate, Coordinate: coordinate, GroupID: parsed.GroupID, ArtifactID: parsed.ArtifactID, Version: parsed.Version, Size: primaryBytes, Member: versionAssets[0].Member, AssetCount: len(versionAssets), PrimaryAssetCount: primaryCount, SidecarCount: sidecarCount, Extensions: sortedStringKeys(extensions), Assets: versionAssets})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Coordinate < items[j].Coordinate })
	return items
}

func parseMavenCacheCoordinate(path string) (mavenCacheCoordinate, bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 4 {
		return mavenCacheCoordinate{}, false
	}
	fileName := parts[len(parts)-1]
	version := parts[len(parts)-2]
	artifactID := parts[len(parts)-3]
	groupParts := parts[:len(parts)-3]
	if len(groupParts) == 0 || !strings.HasPrefix(fileName, artifactID+"-"+version) {
		return mavenCacheCoordinate{}, false
	}
	return mavenCacheCoordinate{GroupID: strings.Join(groupParts, "."), ArtifactID: artifactID, Version: version, FileName: fileName}, true
}

func isMavenSidecar(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".sha1") || strings.HasSuffix(lower, ".sha256") || strings.HasSuffix(lower, ".sha512") || strings.HasSuffix(lower, ".md5") || strings.HasSuffix(lower, ".asc")
}

func mavenPrimaryExtension(name string) string {
	for _, suffix := range []string{".sha512", ".sha256", ".sha1", ".md5", ".asc"} {
		name = strings.TrimSuffix(name, suffix)
	}
	if index := strings.LastIndex(name, "."); index >= 0 && index < len(name)-1 {
		return name[index+1:]
	}
	return "file"
}

func sortedStringKeys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
