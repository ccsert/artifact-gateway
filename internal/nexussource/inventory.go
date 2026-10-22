package nexussource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"reflect"
	"sort"
	"strings"
)

// InventoryPage is one bounded, validated Assets API response. Its cursor is
// private execution state, never a management cursor or an authentication token.
type InventoryPage struct {
	Assets            []Asset
	UnsupportedAssets []UnsupportedAsset
	NextCursor        string
	MetadataBytes     int64
	MetadataSHA256    string
}

func validInventoryCursor(cursor string) bool {
	return len(cursor) <= 4096 && !strings.ContainsAny(cursor, "\r\n\x00")
}

func (s *session) inventoryRepositories(names []string) ([]Repository, error) {
	all, err := s.repositories()
	if err != nil {
		return nil, err
	}
	repos, err := selectRepositories(all, names)
	if err != nil {
		return nil, err
	}
	for i := range repos {
		repos[i], err = s.inspect(repos[i])
		if err != nil {
			return nil, err
		}
		if repos[i].Status != "supported" || repos[i].Type != "hosted" {
			return nil, fail("source_repository_unsupported")
		}
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].Name < repos[j].Name })
	return repos, nil
}

// InventoryRepositories fixes only the explicitly selected, supported Hosted
// shapes. Each call validates the current connection version independently.
func (s *Service) InventoryRepositories(ctx context.Context, id, expected string, names []string) ([]Repository, error) {
	if len(names) < 1 || len(names) > 10 {
		return nil, fail("invalid_request")
	}
	remote, source, done, err := s.connect(ctx, id)
	if err != nil {
		return nil, err
	}
	defer done()
	if source.Version != expected {
		return nil, fail("source_version_changed")
	}
	return remote.inventoryRepositories(names)
}

// ReadInventoryPage renews source/transport/shape checks for every bounded page.
// No operation-wide page/asset ceiling is inherited from the preview endpoint.
func (s *Service) ReadInventoryPage(ctx context.Context, id, expected string, repo Repository, cursor string) (InventoryPage, error) {
	if !validInventoryCursor(cursor) || !sourceName.MatchString(repo.Name) {
		return InventoryPage{}, fail("invalid_request")
	}
	remote, source, done, err := s.connect(ctx, id)
	if err != nil {
		return InventoryPage{}, err
	}
	defer done()
	if source.Version != expected {
		return InventoryPage{}, fail("source_version_changed")
	}
	current, err := remote.inventoryRepositories([]string{repo.Name})
	if err != nil {
		return InventoryPage{}, err
	}
	if !reflect.DeepEqual(current[0], repo) {
		return InventoryPage{}, fail("source_changed")
	}
	page, err := remote.inventoryPage(repo, cursor)
	if err != nil {
		return InventoryPage{}, err
	}
	// Canonical paths and the connection version are sufficient for later reads;
	// a source-supplied URL is not persisted in the migration ledger.
	for i := range page.Assets {
		page.Assets[i].DownloadURL = ""
	}
	return page, nil
}

func (s *session) inventoryPage(repo Repository, cursor string) (InventoryPage, error) {
	if repo.Format == "npm" {
		return s.npmInventoryPage(repo, cursor)
	}
	query := url.Values{"repository": {repo.Name}}
	if cursor != "" {
		query.Set("continuationToken", cursor)
	}
	var raw struct {
		Items             *[]assetRaw     `json:"items"`
		ContinuationToken json.RawMessage `json:"continuationToken"`
	}
	before := s.stats.ResponseBytes
	if err := s.read("assets", query, &raw); err != nil {
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
	page := InventoryPage{Assets: []Asset{}, UnsupportedAssets: []UnsupportedAsset{}, MetadataBytes: s.stats.ResponseBytes - before}
	if next != nil {
		page.NextCursor = *next
	}
	if !validInventoryCursor(page.NextCursor) || page.NextCursor != "" && page.NextCursor == cursor {
		return InventoryPage{}, fail("source_pagination_cycle")
	}
	seen := map[string]bool{}
	metadata := make([]Asset, 0, len(*raw.Items))
	for _, item := range *raw.Items {
		asset, err := s.asset(repo, item)
		if err != nil {
			return InventoryPage{}, err
		}
		if seen[asset.Path] {
			return InventoryPage{}, fail("source_duplicate_asset")
		}
		seen[asset.Path] = true
		observed := asset
		observed.DownloadURL = ""
		metadata = append(metadata, observed)
		format, _ := NativeFormat(repo.Format)
		_, _, reason := assetRoute(format, asset.Path)
		if reason == "" {
			reason = mavenUnsupportedPath(repo, asset.Path)
		}
		if reason != "" {
			page.UnsupportedAssets = append(page.UnsupportedAssets, UnsupportedAsset{Repository: repo.Name, Path: asset.Path, Reason: reason})
		} else {
			page.Assets = append(page.Assets, asset)
		}
	}
	// Include size/checksums of unsupported shapes too: observing their drift
	// must not silently preserve a supposedly complete inventory fingerprint.
	sort.Slice(metadata, func(i, j int) bool { return metadata[i].Path < metadata[j].Path })
	canonical, err := json.Marshal(metadata)
	if err != nil {
		return InventoryPage{}, err
	}
	sum := sha256.Sum256(canonical)
	page.MetadataSHA256 = hex.EncodeToString(sum[:])
	sort.Slice(page.Assets, func(i, j int) bool { return page.Assets[i].Path < page.Assets[j].Path })
	sort.Slice(page.UnsupportedAssets, func(i, j int) bool { return page.UnsupportedAssets[i].Path < page.UnsupportedAssets[j].Path })
	return page, nil
}
