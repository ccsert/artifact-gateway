package raw

import (
	"context"
	"net/url"
	"strings"

	"github.com/ccsert/artifact-gateway/internal/artifactquery"
	formatmodule "github.com/ccsert/artifact-gateway/internal/format"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/v2contract"
)

func validPrefix(value string) bool {
	if len(value) > v2contract.MaxRawPathBytes || strings.HasPrefix(value, "/") || strings.ContainsRune(value, '\\') || strings.IndexFunc(value, artifactquery.InvalidRune) >= 0 {
		return false
	}
	segments := strings.Split(value, "/")
	for index, segment := range segments {
		if segment == "." || segment == ".." || segment == "" && index < len(segments)-1 {
			return false
		}
	}
	return true
}

func searchQuery(query repository.ArtifactSearchQuery) repository.ArtifactSearchQuery {
	if query.Mode != repository.ArtifactSearchByCoordinate || query.Value == "" {
		return query
	}
	segments := strings.Split(query.Value, "/")
	canonical := true
	for _, segment := range segments {
		decoded, err := url.PathUnescape(segment)
		if err != nil || url.PathEscape(decoded) != segment {
			canonical = false
			break
		}
	}
	if canonical {
		return query
	}
	for index, segment := range segments {
		segments[index] = url.PathEscape(segment)
	}
	query.Value = strings.Join(segments, "/")
	return query
}

func (*Module) ValidCoordinate(value string) bool { return value != "" && validPrefix(value) }
func (*Module) ValidPrefix(value string) bool     { return validPrefix(value) }
func (*Module) ValidBrowseParent(parent repository.ArtifactBrowseParent) bool {
	return parent.Kind == repository.BrowseNodeDirectory && parent.Path != "" && !strings.HasPrefix(parent.Path, "/")
}
func (*Module) BrowseName(name string) string {
	if decoded, err := url.PathUnescape(name); err == nil {
		return decoded
	}
	return name
}
func (*Module) SearchQuery(query repository.ArtifactSearchQuery) repository.ArtifactSearchQuery {
	return searchQuery(query)
}
func (*Module) UsageKey(resource string) string { return resource }
func (m *Module) Search(ctx context.Context, repositoryID string, query repository.ArtifactSearchQuery, limit int, after string) ([]formatmodule.ArtifactSummary, error) {
	if m.store == nil {
		return nil, repository.ErrDisabled
	}
	assets, err := m.store.ListRawAssets(ctx, repositoryID, query.Value, limit, after, query.Match)
	if err != nil {
		return nil, err
	}
	items := make([]formatmodule.ArtifactSummary, 0, len(assets))
	for _, asset := range assets {
		items = append(items, formatmodule.ArtifactSummary{Coordinate: asset.Path, Digest: asset.Digest, ContentType: asset.ContentType, Size: asset.Size, CreatedAt: asset.UpdatedAt})
	}
	return items, nil
}

func (m *Module) ValidKeyword(value string, match repository.ArtifactSearchMatch) bool {
	if match != repository.ArtifactSearchContains {
		return m.ValidPrefix(value)
	}
	return artifactquery.ValidKeyword(value, 4096)
}
