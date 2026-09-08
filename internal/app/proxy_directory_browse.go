package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// A directory expansion must query the index in storage. In particular, a
// generic object Store.List/Get fallback would scan every repository and S3.
type proxyDirectoryStore interface {
	ListGroupProxyBrowseNodes(context.Context, repository.HostedRepository, string, string, repository.ArtifactBrowseParent, int, string) ([]repository.ArtifactBrowseNode, error)
	ListProxyBrowseNodes(context.Context, repository.HostedRepository, repository.ArtifactBrowseParent, int, string) ([]repository.ArtifactBrowseNode, error)
}

func (h proxyCacheBrowseHandler) directoryStore() proxyDirectoryStore {
	if h.maintenance == nil {
		return nil
	}
	store, _ := h.maintenance.store.(proxyDirectoryStore)
	return store
}

// The live index is authoritative for cache visibility. Missing/expired,
// negative and pre-migration S3-only records never become directory evidence.
// Legacy records enter this query when the existing protocol read migrates them.
const proxyBrowseLiveSQL = `WITH live AS (
	SELECT key, updated_at,
		COALESCE(value->>'repository',value->>'Repository') AS cache_repository,
		COALESCE(value->>'path',value->>'Path') AS path,
		COALESCE(value->>'digest',value->>'Digest') AS digest,
		COALESCE(value->>'size',value->>'Size','0')::bigint AS size,
		COALESCE(value->>'content_type',value->>'ContentType','') AS content_type
	FROM cache_control_entries
	WHERE key LIKE '%s/index/%%'
		AND COALESCE(value->>'repository',value->>'Repository')=$1
		AND COALESCE(value->>'endpoint',value->>'Endpoint')=$2
		AND md5(COALESCE(value->>'repository',value->>'Repository'))=md5($1)
		AND md5(COALESCE(value->>'endpoint',value->>'Endpoint'))=md5($2)
		AND COALESCE(value->>'negative',value->>'Negative','false')='false'
		AND COALESCE(value->>'object',value->>'Object','')<>''
		AND COALESCE(value->>'digest',value->>'Digest','') ~ '^[a-f0-9]{64}$'
		AND CASE WHEN pg_input_is_valid(COALESCE(value->>'size',value->>'Size','0'),'bigint')
			THEN COALESCE(value->>'size',value->>'Size','0')::bigint>=0 ELSE false END
		AND CASE WHEN pg_input_is_valid(COALESCE(value->>'expires_at',value->>'ExpiresAt'),'timestamptz')
			THEN COALESCE(value->>'expires_at',value->>'ExpiresAt')::timestamptz>now() ELSE false END
		AND COALESCE(value->>'path',value->>'Path','')<>''
		AND COALESCE(value->>'path',value->>'Path') NOT LIKE '/%%'
		AND COALESCE(value->>'path',value->>'Path') !~ '(^|/)(\.|\.\.)(/|$)|//'
`

// A SNAPSHOT build is identified by its timestamp AND build number. A version
// node's signed Path pins its exact file prefix, so later refreshes cannot mix
// different builds, classifiers, or checksums into an expanded node.
const proxyMavenBrowseSQL = `), paths AS (
	SELECT *, regexp_match(path,'^(.+)/([^/]+)/([^/]+)/([^/]+)$') AS parts FROM live
), coordinates AS (
	SELECT *, replace(parts[1],'/','.') AS namespace, parts[2] AS component,
		parts[3] AS version, parts[4] AS filename,
		parts[1] || '/' || parts[2] || '/' || parts[3] || '/' AS directory,
		CASE WHEN right(parts[3],9)='-SNAPSHOT'
			AND left(parts[4],length(parts[2])+1)=parts[2] || '-'
			AND left(substring(parts[4] FROM length(parts[2])+2),length(parts[3])-8)=left(parts[3],length(parts[3])-8)
		THEN (regexp_match(substring(parts[4] FROM length(parts[2])+length(parts[3])-6),
			'^([0-9]{8}\.[0-9]{6}-[1-9][0-9]{0,8})([.-])'))[1] END AS snapshot
	FROM paths WHERE parts IS NOT NULL
), assets AS (
	SELECT *, namespace || ':' || component || ':' || version AS coordinate,
		directory || component || '-' || CASE WHEN snapshot IS NOT NULL
			THEN left(version,length(version)-8) || snapshot ELSE version END AS file_prefix,
		CASE WHEN snapshot IS NOT NULL THEN split_part(snapshot,'-',2)::integer ELSE 0 END AS build_number
	FROM coordinates WHERE snapshot IS NOT NULL OR
		(left(filename,length(component)+length(version)+1)=component || '-' || version
			AND substring(filename FROM length(component)+length(version)+2 FOR 1) IN ('.','-'))
)
`

func (s *PostgresCacheControlStore) ListProxyBrowseNodes(ctx context.Context, repo repository.HostedRepository, parent repository.ArtifactBrowseParent, limit int, after string) ([]repository.ArtifactBrowseNode, error) {
	return s.ListGroupProxyBrowseNodes(ctx, repo, "", "", parent, limit, after)
}

func (s *PostgresCacheControlStore) ListGroupProxyBrowseNodes(ctx context.Context, repo repository.HostedRepository, group, scope string, parent repository.ArtifactBrowseParent, limit int, after string) ([]repository.ArtifactBrowseNode, error) {
	if repo.Type != repository.RepositoryTypeProxy || (repo.Format != repository.FormatMaven && repo.Format != repository.FormatRaw) {
		return nil, repository.ErrUnsupportedBrowseFormat
	}
	if limit < 1 || limit > 201 {
		return nil, errors.New("proxy browse limit must be between 1 and 201")
	}
	// Replace only the server-owned format literal; all caller values are SQL parameters.
	base := strings.ReplaceAll(strings.ReplaceAll(proxyBrowseLiveSQL, "%s", string(repo.Format)), "%%", "%")
	if repo.Format == repository.FormatRaw {
		return s.listRawProxyBrowse(ctx, base, repo, parent, limit, after)
	}
	args := []any{repo.Name, repo.Endpoint}
	prefix := ""
	if parent.Namespace != "" {
		prefix = strings.ReplaceAll(parent.Namespace, ".", "/") + "/"
		if parent.Component != "" {
			prefix += parent.Component + "/"
		}
		if parent.Kind == repository.BrowseNodeVersion {
			parts := strings.Split(parent.Version, ":")
			if len(parts) != 3 {
				return nil, repository.ErrUnsupportedBrowseFormat
			}
			prefix += parts[2] + "/"
		}
		// Prefix is derived only from a signed parent. Bind it separately from
		// the navigation arguments so the path index can constrain expansions.
		// The namespace/component predicates below remain authoritative.
	}
	predicate := ""
	switch parent.Kind {
	case "":
	case repository.BrowseNodeNamespace:
		predicate = "namespace=$3"
		args = append(args, parent.Namespace)
	case repository.BrowseNodeComponent:
		predicate = "namespace=$3 AND component=$4"
		args = append(args, parent.Namespace, parent.Component)
	case repository.BrowseNodeVersion:
		predicate = "coordinate=$3 AND file_prefix=$4 AND build_number=$5"
		args = append(args, parent.Version, parent.Path, parent.BuildNumber)
	default:
		return nil, repository.ErrUnsupportedBrowseFormat
	}
	args = append(args, after, limit)
	if prefix != "" {
		pattern := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix) + "%"
		args = append(args, pattern)
		base += fmt.Sprintf(` AND COALESCE(value->>'path',value->>'Path') LIKE $%d ESCAPE '\'`, len(args))
		args = append(args, proxyBrowseIndexedPrefix(prefix))
		base += fmt.Sprintf(` AND left(COALESCE(value->>'path',value->>'Path'),256) LIKE $%d ESCAPE '\'`, len(args))
	}
	// Each select returns the same small row shape. Grouping remains in PostgreSQL.
	query := base + proxyMavenBrowseSQL
	switch parent.Kind {
	case "":
		query += `SELECT DISTINCT namespace COLLATE "C", 'namespace', namespace, true, namespace, '', '', '', '', '', 0::bigint, '', NULL::timestamptz, 0, '' FROM assets WHERE namespace COLLATE "C">$3 ORDER BY 1 LIMIT $4`
	case repository.BrowseNodeNamespace:
		query += `SELECT DISTINCT component COLLATE "C", 'component', component, true, namespace, component, '', '', '', '', 0::bigint, '', NULL::timestamptz, 0, '' FROM assets WHERE ` + predicate + ` AND component COLLATE "C">$4 ORDER BY 1 LIMIT $5`
	case repository.BrowseNodeComponent:
		query += `SELECT DISTINCT file_prefix COLLATE "C", 'version', version || CASE WHEN snapshot IS NOT NULL THEN ' · ' || snapshot ELSE '' END, true, namespace, component, version, file_prefix, coordinate, '', 0::bigint, '', NULL::timestamptz, build_number, '' FROM assets WHERE ` + predicate + ` AND file_prefix COLLATE "C">$5 ORDER BY 1 LIMIT $6`
	case repository.BrowseNodeVersion:
		query += `SELECT path COLLATE "C", 'asset', filename, false, namespace, component, version, path, coordinate, 'sha256:' || digest, size, content_type, updated_at, build_number, cache_repository FROM assets WHERE ` + predicate + ` AND path COLLATE "C">$6 ORDER BY 1 LIMIT $7`
	}
	if group != "" && scope != "" {
		query, args = groupProxyBrowseQuery(query, args, group, scope, repo.Name)
	}
	return s.scanProxyBrowseNodes(ctx, query, args...)
}

func (s *PostgresCacheControlStore) listRawProxyBrowse(ctx context.Context, base string, repo repository.HostedRepository, parent repository.ArtifactBrowseParent, limit int, after string) ([]repository.ArtifactBrowseNode, error) {
	if parent.Kind != "" && parent.Kind != repository.BrowseNodeDirectory {
		return nil, repository.ErrUnsupportedBrowseFormat
	}
	prefix := parent.Path
	if prefix != "" {
		prefix += "/"
	}
	pattern := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix) + "%"
	query := base + ` AND COALESCE(value->>'path',value->>'Path') LIKE $3 ESCAPE '\'
		AND left(COALESCE(value->>'path',value->>'Path'),256) LIKE $7 ESCAPE '\'
	), descendants AS (
		SELECT *, substring(path FROM char_length($4)+1) AS remainder FROM live
	), nodes AS (
		SELECT DISTINCT split_part(remainder,'/',1) || chr(31) || '0' AS key,
			'directory' AS kind, split_part(remainder,'/',1) AS name, true AS children,
			'' AS namespace, '' AS component, '' AS version, $4 || split_part(remainder,'/',1) AS path,
			'' AS coordinate, '' AS digest, 0::bigint AS size, '' AS content_type, NULL::timestamptz AS created_at, 0 AS build_number, ''::text AS cache_repository
		FROM descendants WHERE position('/' IN remainder)>0
		UNION ALL
		SELECT remainder || chr(31) || '1', 'asset', remainder, false, '', '', '', path, path,
			'sha256:' || digest, size, content_type, updated_at, 0, cache_repository
		FROM descendants WHERE remainder<>'' AND position('/' IN remainder)=0
	)
	SELECT * FROM nodes WHERE key COLLATE "C">$5 ORDER BY key COLLATE "C" LIMIT $6`
	return s.scanProxyBrowseNodes(ctx, query, repo.Name, repo.Endpoint, pattern, prefix, after, limit, proxyBrowseIndexedPrefix(prefix))
}

// Match the bounded index expression; the full path and exact repository /
// endpoint predicates remain authoritative even if prefixes or hashes collide.
func proxyBrowseIndexedPrefix(prefix string) string {
	runes := []rune(prefix)
	runes = runes[:min(len(runes), 256)]
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(string(runes)) + "%"
}

func (s *PostgresCacheControlStore) scanProxyBrowseNodes(ctx context.Context, query string, args ...any) ([]repository.ArtifactBrowseNode, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	nodes := make([]repository.ArtifactBrowseNode, 0)
	for rows.Next() {
		var node repository.ArtifactBrowseNode
		var cachedAt *time.Time
		if err := rows.Scan(&node.Key, &node.Kind, &node.Name, &node.HasChildren, &node.Namespace, &node.Component, &node.Version, &node.Path, &node.Coordinate, &node.Digest, &node.Size, &node.ContentType, &cachedAt, &node.BuildNumber, &node.CacheRepositoryName); err != nil {
			return nil, err
		}
		if cachedAt != nil {
			node.CreatedAt = *cachedAt
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

// The source repository and the scope owning its index are separate. A Group
// cache may contribute only while its authorized candidate scope still matches.
// Prefer the newest live evidence when direct and Group indexes share a path.
func groupProxyBrowseQuery(query string, args []any, group, scope, member string) (string, []any) {
	n := len(args)
	original := "COALESCE(value->>'repository',value->>'Repository')=$1"
	combined := fmt.Sprintf("(%s OR (COALESCE(value->>'repository',value->>'Repository')=$%d AND COALESCE(value->>'resolution_scope',value->>'ResolutionScope')=$%d AND COALESCE(value->>'member',value->>'Member')=$%d))", original, n+1, n+2, n+3)
	query = strings.Replace(query, original, combined, 1)
	query = strings.Replace(query, "AND md5(COALESCE(value->>'repository',value->>'Repository'))=md5($1)", fmt.Sprintf("AND md5(COALESCE(value->>'repository',value->>'Repository')) IN (md5($1),md5($%d))", n+1), 1)
	query = strings.Replace(query, "), paths AS (", "), deduplicated AS (SELECT DISTINCT ON (path) * FROM live ORDER BY path, updated_at DESC, key), paths AS (", 1)
	query = strings.Replace(query, "AS parts FROM live", "AS parts FROM deduplicated", 1)
	return query, append(args, group, scope, member)
}
