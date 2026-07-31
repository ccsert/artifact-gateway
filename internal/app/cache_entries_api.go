package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// CacheEntry describes one live proxy cache index record for operations
// listings. Repository carries the artifact coordinate when the format's cache
// index persists one (the OCI repository path, e.g. library/alpine, for OCI;
// the artifact path for Maven, Raw, and Conan), and falls back to the proxy
// group name for index records written before the coordinate was persisted.
type CacheEntry struct {
	Repository  string `json:"repository"`
	Digest      string `json:"digest,omitempty"`
	Size        int64  `json:"size,omitempty"`
	ContentType string `json:"contentType,omitempty"`
	Member      string `json:"member,omitempty"`
	Endpoint    string `json:"endpoint,omitempty"`
	Format      string `json:"format"`
}

type cacheEntriesHandler struct {
	store         GatewayStore
	maintenance   *CacheMaintenance
	authenticator Authenticator
}

func (h cacheEntriesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
		return
	}
	if !principal.Admin {
		writeError(w, http.StatusForbidden, "forbidden", "administrator permission required")
		return
	}
	groupName := strings.TrimSpace(r.URL.Query().Get("repository"))
	if groupName == "" {
		writeError(w, http.StatusBadRequest, "invalid_repository", "repository is required")
		return
	}
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		writeError(w, http.StatusBadRequest, "invalid_format", "format is required")
		return
	}
	entries, err := h.entries(r.Context(), groupName, format)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "group_not_found", "group not found")
		return
	}
	if errors.Is(err, errUnsupportedCacheFormat) {
		writeError(w, http.StatusBadRequest, "invalid_format", "format must be one of oci, maven, raw, conan")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "unable to inspect cache")
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

var errUnsupportedCacheFormat = errors.New("unsupported cache format")

func (h cacheEntriesHandler) entries(ctx context.Context, groupName, format string) ([]CacheEntry, error) {
	switch format {
	case string(repository.FormatOCI):
		group, err := h.store.GetGroup(ctx, groupName)
		if err != nil {
			endpoints, proxyErr := h.proxyRepositoryEndpoints(ctx, groupName, repository.FormatOCI)
			if proxyErr != nil {
				return nil, err
			}
			return h.listOCI(ctx, endpoints)
		}
		return h.listOCI(ctx, memberEndpoints(group))
	case string(repository.FormatMaven):
		group, err := h.store.GetMavenGroup(ctx, groupName)
		if err != nil {
			endpoints, proxyErr := h.proxyRepositoryEndpoints(ctx, groupName, repository.FormatMaven)
			if proxyErr != nil {
				return nil, err
			}
			return h.listMaven(ctx, endpoints)
		}
		return h.listMaven(ctx, memberEndpoints(group))
	case string(repository.FormatRaw):
		group, err := h.store.GetRawGroup(ctx, groupName)
		if err != nil {
			endpoints, proxyErr := h.proxyRepositoryEndpoints(ctx, groupName, repository.FormatRaw)
			if proxyErr != nil {
				return nil, err
			}
			return h.listRaw(ctx, endpoints)
		}
		return h.listRaw(ctx, memberEndpoints(group))
	case string(repository.FormatConan):
		if _, err := h.store.GetConanGroup(ctx, groupName); err != nil {
			return nil, err
		}
		return h.listConan(ctx, groupName)
	default:
		return nil, errUnsupportedCacheFormat
	}
}

func (h cacheEntriesHandler) proxyRepositoryEndpoints(ctx context.Context, name string, format repository.Format) (map[string]bool, error) {
	repo, err := h.store.GetHostedRepositoryByName(ctx, name)
	if err != nil {
		return nil, err
	}
	if repo.Format != format || repo.Type != repository.RepositoryTypeProxy || strings.TrimSpace(repo.Endpoint) == "" {
		return nil, repository.ErrNotFound
	}
	return map[string]bool{repo.Endpoint: true}, nil
}

// memberEndpoints matches index records to a group because OCI, Maven, and
// Raw cache keys hash the group name instead of storing it in the index.
func memberEndpoints(group repository.Group) map[string]bool {
	endpoints := make(map[string]bool, len(group.Members))
	for _, member := range group.Members {
		if member.Endpoint != "" {
			endpoints[member.Endpoint] = true
		}
	}
	return endpoints
}

// cacheIndexRecord mirrors the shared fields of every format's cache index.
// OCI/Maven/Raw encode with snake_case tags while Conan encodes plain field
// names, so both spellings are listed per field.
type cacheIndexRecord struct {
	Repository        string    `json:"repository"`
	RepositoryPascal  string    `json:"Repository"`
	Digest            string    `json:"digest"`
	DigestPascal      string    `json:"Digest"`
	Size              int64     `json:"size"`
	SizePascal        int64     `json:"Size"`
	ContentType       string    `json:"content_type"`
	ContentTypePascal string    `json:"ContentType"`
	Member            string    `json:"member"`
	MemberPascal      string    `json:"Member"`
	Endpoint          string    `json:"endpoint"`
	EndpointPascal    string    `json:"Endpoint"`
	Group             string    `json:"Group"`
	Path              string    `json:"path"`
	PathPascal        string    `json:"Path"`
	ExpiresAt         time.Time `json:"expires_at"`
	ExpiresAtPascal   time.Time `json:"ExpiresAt"`
	Negative          bool      `json:"negative"`
	NegativePascal    bool      `json:"Negative"`
}

func (i cacheIndexRecord) repository() string {
	if i.Repository != "" {
		return i.Repository
	}
	return i.RepositoryPascal
}

func (i cacheIndexRecord) path() string {
	if i.Path != "" {
		return i.Path
	}
	return i.PathPascal
}

// coordinate prefers the artifact coordinate persisted in the index (OCI
// repository path, Maven/Raw/Conan artifact path) and falls back to the proxy
// group name for records written before the coordinate was persisted.
func (i cacheIndexRecord) coordinate() string {
	if path := i.path(); path != "" {
		return path
	}
	return i.repository()
}

func (i cacheIndexRecord) digest() string {
	if i.Digest != "" {
		return i.Digest
	}
	return i.DigestPascal
}

func (i cacheIndexRecord) size() int64 {
	if i.Size != 0 {
		return i.Size
	}
	return i.SizePascal
}

func (i cacheIndexRecord) contentType() string {
	if i.ContentType != "" {
		return i.ContentType
	}
	return i.ContentTypePascal
}

func (i cacheIndexRecord) member() string {
	if i.Member != "" {
		return i.Member
	}
	return i.MemberPascal
}

func (i cacheIndexRecord) endpoint() string {
	if i.Endpoint != "" {
		return i.Endpoint
	}
	return i.EndpointPascal
}

func (i cacheIndexRecord) expiresAt() time.Time {
	if !i.ExpiresAt.IsZero() {
		return i.ExpiresAt
	}
	return i.ExpiresAtPascal
}

func (i cacheIndexRecord) negative() bool { return i.Negative || i.NegativePascal }

func (h cacheEntriesHandler) list(ctx context.Context, prefix, format string, keep func(cacheIndexRecord) bool) ([]CacheEntry, error) {
	keys, err := h.maintenance.store.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	entries := make([]CacheEntry, 0, len(keys))
	for _, key := range keys {
		encoded, err := h.maintenance.store.Get(ctx, key)
		if err != nil {
			continue
		}
		var index cacheIndexRecord
		if json.Unmarshal(encoded, &index) != nil {
			continue
		}
		if index.negative() || !now.Before(index.expiresAt()) || !keep(index) {
			continue
		}
		entries = append(entries, CacheEntry{
			Repository:  index.coordinate(),
			Digest:      index.digest(),
			Size:        index.size(),
			ContentType: index.contentType(),
			Member:      index.member(),
			Endpoint:    index.endpoint(),
			Format:      format,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Repository != entries[j].Repository {
			return entries[i].Repository < entries[j].Repository
		}
		return entries[i].Digest < entries[j].Digest
	})
	return entries, nil
}

func (h cacheEntriesHandler) listOCI(ctx context.Context, endpoints map[string]bool) ([]CacheEntry, error) {
	return h.list(ctx, "oci/index/", string(repository.FormatOCI), func(index cacheIndexRecord) bool {
		return endpoints[index.endpoint()]
	})
}

func (h cacheEntriesHandler) listMaven(ctx context.Context, endpoints map[string]bool) ([]CacheEntry, error) {
	return h.listMavenForRepository(ctx, "", endpoints)
}

func (h cacheEntriesHandler) listMavenForRepository(ctx context.Context, repositoryName string, endpoints map[string]bool) ([]CacheEntry, error) {
	return h.list(ctx, "maven/index/", string(repository.FormatMaven), func(index cacheIndexRecord) bool {
		if repositoryName != "" && index.repository() != repositoryName {
			return false
		}
		return endpoints[index.endpoint()]
	})
}

func (h cacheEntriesHandler) listRaw(ctx context.Context, endpoints map[string]bool) ([]CacheEntry, error) {
	return h.list(ctx, "raw/index/", string(repository.FormatRaw), func(index cacheIndexRecord) bool {
		return endpoints[index.endpoint()]
	})
}

func (h cacheEntriesHandler) listConan(ctx context.Context, groupName string) ([]CacheEntry, error) {
	return h.list(ctx, "conan/index/", string(repository.FormatConan), func(index cacheIndexRecord) bool {
		return index.Group == groupName
	})
}
