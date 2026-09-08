package repository

import (
	"context"
	"sort"
	"strconv"
	"strings"
)

// Group browse uses one protocol-path sort order across Hosted and Proxy
// streams. Repository-local version cursors keep their existing grammar.
func groupMavenVersionNode(node ArtifactBrowseNode) ArtifactBrowseNode {
	prefix := strings.ReplaceAll(node.Namespace, ".", "/") + "/" + node.Component + "/" + node.Version + "/" + node.Component + "-" + node.Version
	if node.BuildNumber > 0 {
		build := node.CreatedAt.UTC().Format("20060102.150405") + "-" + strconv.Itoa(node.BuildNumber)
		prefix = strings.ReplaceAll(node.Namespace, ".", "/") + "/" + node.Component + "/" + node.Version + "/" + node.Component + "-" + strings.TrimSuffix(node.Version, "-SNAPSHOT") + "-" + build
		node.Name = node.Version + " · " + build
	}
	node.Path, node.Key = prefix, prefix
	return node
}

func (s *MemoryStore) ListGroupArtifactBrowseNodes(ctx context.Context, repositoryID string, format Format, parent ArtifactBrowseParent, limit int, after string) ([]ArtifactBrowseNode, error) {
	if format != FormatMaven || parent.Kind != BrowseNodeComponent {
		return s.ListArtifactBrowseNodes(ctx, repositoryID, format, parent, limit, after)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	nodes := s.listMemoryMavenBrowseNodes(repositoryID, parent)
	byKey := make(map[string]ArtifactBrowseNode)
	for _, node := range nodes {
		node = groupMavenVersionNode(node)
		if node.Key > after {
			byKey[node.Key] = node
		}
	}
	nodes = browseNodeMapValues(byKey)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Key < nodes[j].Key })
	if len(nodes) > limit {
		nodes = nodes[:limit]
	}
	return nodes, nil
}

func (s *PostgresStore) ListGroupArtifactBrowseNodes(ctx context.Context, repositoryID string, format Format, parent ArtifactBrowseParent, limit int, after string) ([]ArtifactBrowseNode, error) {
	if format == FormatRaw {
		return s.listPostgresRawBrowseNodesOrdered(ctx, repositoryID, parent, limit, after, true)
	}
	if format != FormatMaven || parent.Kind != BrowseNodeComponent {
		return s.ListArtifactBrowseNodes(ctx, repositoryID, format, parent, limit, after)
	}
	rows, err := s.db.QueryContext(ctx, `WITH versions AS (
  SELECT coordinate,digest,created_at,build_number,
   row_number() OVER (PARTITION BY coordinate ORDER BY build_number DESC,created_at DESC) AS rank
  FROM native_maven_artifacts
  WHERE repository_id=$1::uuid AND state='visible'
   AND split_part(coordinate,':',1)=$2 AND split_part(coordinate,':',2)=$3
 ), paths AS (
  SELECT *, replace($2,'.','/') || '/' || $3 || '/' || split_part(coordinate,':',3) || '/' || $3 || '-' ||
   CASE WHEN build_number>0 THEN left(split_part(coordinate,':',3),length(split_part(coordinate,':',3))-9) || '-' || to_char(created_at AT TIME ZONE 'UTC','YYYYMMDD.HH24MISS') || '-' || build_number::text
   ELSE split_part(coordinate,':',3) END AS path
  FROM versions WHERE rank=1
 ) SELECT coordinate,digest,created_at,build_number FROM paths
 WHERE path COLLATE "C">$4 ORDER BY path COLLATE "C" LIMIT $5`, repositoryID, parent.Namespace, parent.Component, after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	nodes := make([]ArtifactBrowseNode, 0)
	for rows.Next() {
		node := ArtifactBrowseNode{Kind: BrowseNodeVersion, HasChildren: true, Namespace: parent.Namespace, Component: parent.Component}
		if err := rows.Scan(&node.Coordinate, &node.Digest, &node.CreatedAt, &node.BuildNumber); err != nil {
			return nil, err
		}
		node.Version = strings.Split(node.Coordinate, ":")[2]
		node.Name = node.Version
		nodes = append(nodes, groupMavenVersionNode(node))
	}
	return nodes, rows.Err()
}
