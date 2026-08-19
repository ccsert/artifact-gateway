package repository

import (
	"sort"
	"strings"
	"time"
)

func normalizeGoModulePublication(publication GoModulePublication, now time.Time) (GoModulePublication, error) {
	version := publication.Version
	if version.RepositoryID == "" || version.Module == "" || version.Version == "" || version.PublishedAt.IsZero() || len(publication.Assets) != 3 {
		return GoModulePublication{}, ErrInvalidGoModulePublication
	}
	seen := make(map[string]bool, 3)
	assets := append([]GoModuleAsset(nil), publication.Assets...)
	for index := range assets {
		asset := &assets[index]
		if asset.RepositoryID != version.RepositoryID || asset.Module != version.Module || asset.Version != version.Version ||
			(asset.Kind != "info" && asset.Kind != "mod" && asset.Kind != "zip") || seen[asset.Kind] ||
			!validSHA256Digest(asset.Digest) || asset.ObjectKey == "" || asset.Size <= 0 {
			return GoModulePublication{}, ErrInvalidGoModulePublication
		}
		seen[asset.Kind] = true
		if asset.CachedAt.IsZero() {
			asset.CachedAt = now
		}
		if asset.CreatedAt.IsZero() {
			asset.CreatedAt = now
		}
	}
	if !seen["info"] || !seen["mod"] || !seen["zip"] {
		return GoModulePublication{}, ErrInvalidGoModulePublication
	}
	if version.CachedAt.IsZero() {
		version.CachedAt = now
	}
	if version.CreatedAt.IsZero() {
		version.CreatedAt = now
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].Kind < assets[j].Kind })
	return GoModulePublication{Version: version, Assets: assets}, nil
}

func goModulePublicationMatches(existing []GoModuleAsset, incoming []GoModuleAsset) bool {
	if len(existing) != 3 || len(incoming) != 3 {
		return false
	}
	existingByKind := make(map[string]GoModuleAsset, len(existing))
	for _, asset := range existing {
		existingByKind[asset.Kind] = asset
	}
	// The info timestamp is generated at first publication. A retry generated
	// later is still the same immutable module when its source ZIP and go.mod
	// bytes are identical.
	for _, asset := range incoming {
		if asset.Kind == "info" {
			continue
		}
		stored, ok := existingByKind[asset.Kind]
		if !ok || stored.Digest != asset.Digest || stored.Size != asset.Size || stored.ObjectKey != asset.ObjectKey {
			return false
		}
	}
	_, hasInfo := existingByKind["info"]
	return hasInfo
}

func validSHA256Digest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
