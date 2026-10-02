package backupops

import (
	"context"
	"errors"
)

// Published byte references, rather than upload/GC intents or historical path
// mappings. Pending intents can legitimately precede their physical bytes.
const publishedReferences = `
 SELECT o.object_key FROM native_raw_assets a JOIN native_raw_objects o USING(digest)
 UNION SELECT object_key FROM native_oci_blobs
 UNION SELECT object_key FROM native_oci_manifests
 UNION SELECT a.object_key FROM native_maven_assets a JOIN native_maven_object_references r USING(object_key)
 UNION SELECT a.object_key FROM native_conan_assets a
 JOIN native_conan_recipe_revisions r ON r.repository_id=a.repository_id AND r.reference=a.reference AND r.revision=a.recipe_revision
 LEFT JOIN native_conan_package_revisions p ON p.repository_id=a.repository_id AND p.reference=a.reference AND p.recipe_revision=a.recipe_revision AND p.package_id=a.package_id AND p.revision=a.package_revision
 WHERE r.state='visible' AND (a.package_id='' OR p.state='visible')
 UNION SELECT v.object_key FROM native_npm_versions v JOIN native_npm_packages p ON p.repository_id=v.repository_id AND p.name=v.package_name WHERE v.state='visible' AND NOT p.negative AND v.object_key<>''
 UNION SELECT object_key FROM native_pypi_files WHERE state='visible' AND object_key<>''
 UNION SELECT a.object_key FROM native_go_assets a WHERE a.collecting_at IS NULL AND a.collected_at IS NULL AND NOT EXISTS (SELECT 1 FROM artifact_tombstones t WHERE t.repository_id=a.repository_id AND t.format='go' AND t.coordinate=a.module_path||'@'||a.version)
 UNION SELECT object_key FROM native_apt_assets
 UNION SELECT object_key FROM native_apt_package_revisions
 UNION SELECT a.object_key FROM native_apt_snapshot_assets a JOIN native_apt_repository_snapshots s ON s.id=a.snapshot_id WHERE s.state NOT IN('failed','pruned')
 UNION SELECT COALESCE(value->>'object',value->>'Object') FROM cache_control_entries
 WHERE (key LIKE 'oci/index/%' OR key LIKE 'maven/index/%' OR key LIKE 'raw/index/%' OR key LIKE 'conan/index/%')
 AND COALESCE((COALESCE(value->>'negative',value->>'Negative'))::boolean,false)=false
 AND (COALESCE(value->>'expires_at',value->>'ExpiresAt'))::timestamptz > CURRENT_TIMESTAMP
`

const publishedCargoReferences = `SELECT p.object_key FROM native_cargo_publications p JOIN native_cargo_identity_reservations r USING(repository_id,collision_key,version_key) JOIN native_cargo_names n USING(repository_id,collision_key) WHERE p.collecting_at IS NULL AND p.collected_at IS NULL AND NOT EXISTS(SELECT 1 FROM artifact_tombstones t WHERE t.repository_id=p.repository_id AND t.format='cargo' AND t.coordinate=n.name||'@'||r.version)
 UNION SELECT object_key FROM native_cargo_proxy_crates`

func (p Postgres) WalkReferences(ctx context.Context, visit func(string) error) error {
	conn, err := p.connect(ctx)
	if err != nil {
		return errors.New("reference database unavailable")
	}
	defer func() { _ = conn.Close(ctx) }()
	// v0.4.2 predates Cargo tables. An entirely absent Cargo schema has no
	// Cargo references; a partial schema is rejected rather than silently skipped.
	var cargoTables int
	if conn.QueryRow(ctx, "SELECT count(*) FROM unnest(ARRAY['native_cargo_publications','native_cargo_identity_reservations','native_cargo_names','native_cargo_proxy_crates']) t(name) WHERE to_regclass('public.'||name) IS NOT NULL").Scan(&cargoTables) != nil || (cargoTables != 0 && cargoTables != 4) {
		return errors.New("reference schema unsupported")
	}
	query := publishedReferences
	if cargoTables == 4 {
		query += " UNION " + publishedCargoReferences
	}
	query = "SELECT object_key FROM (" + query + ") references_to_bytes WHERE object_key<>'' ORDER BY object_key COLLATE \"C\""
	rows, err := conn.Query(ctx, query)
	if err != nil {
		return errors.New("reference snapshot unavailable")
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		if rows.Scan(&key) != nil || len(key) > 32<<10 {
			return errors.New("reference invalid")
		}
		if err = visit(key); err != nil {
			return err
		}
	}
	if rows.Err() != nil {
		return errors.New("reference snapshot unavailable")
	}
	return nil
}
