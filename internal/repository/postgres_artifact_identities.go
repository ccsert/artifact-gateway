package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	protocolidentity "github.com/artifact-gateway/artifact-gateway/internal/protocol/identity"
)

func (s *PostgresStore) ListArtifactIdentities(ctx context.Context, repositoryID string, format Format, purpose ArtifactIdentityPurpose, query string, limit int) ([]ArtifactIdentity, error) {
	base, err := postgresArtifactIdentityQuery(format, purpose)
	if err != nil {
		return nil, err
	}
	limit = artifactIdentityLimit(limit)
	query = strings.ToLower(strings.TrimSpace(query))
	rows, err := s.db.QueryContext(ctx, `
		WITH raw_identities AS (`+base+`), identities AS (
			SELECT DISTINCT ON (coordinate,digest) coordinate,digest,size,published_at
			FROM raw_identities ORDER BY coordinate,digest,published_at DESC
		)
		SELECT identities.coordinate,identities.digest,identities.size,identities.published_at,
		       CASE WHEN ai.repository_id IS NULL THEN NULL ELSE jsonb_build_object(
		         'signatureCount', jsonb_array_length(ai.signatures),
		         'sbomCount', jsonb_array_length(ai.sboms),
		         'licenseCount', jsonb_array_length(ai.licenses),
		         'vulnerabilityStatus', ai.vulnerability->>'status',
		         'critical', COALESCE((ai.vulnerability->>'critical')::int, 0),
		         'high', COALESCE((ai.vulnerability->>'high')::int, 0),
		         'medium', COALESCE((ai.vulnerability->>'medium')::int, 0),
		         'low', COALESCE((ai.vulnerability->>'low')::int, 0),
		         'unknown', COALESCE((ai.vulnerability->>'unknown')::int, 0)
		       ) END AS intelligence
		FROM identities
		LEFT JOIN artifact_intelligence ai
		  ON ai.repository_id::text=$1 AND ai.format=$4 AND ai.coordinate=identities.coordinate AND ai.digest=identities.digest
		WHERE $2='' OR position($2 in lower(identities.coordinate))>0 OR lower(identities.digest)=$2
		ORDER BY identities.published_at DESC,identities.coordinate,identities.digest DESC
		LIMIT $3`, repositoryID, query, limit, format)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	identities := make([]ArtifactIdentity, 0, limit)
	for rows.Next() {
		var identity ArtifactIdentity
		var size sql.NullInt64
		var intelligence []byte
		if err := rows.Scan(&identity.Coordinate, &identity.Digest, &size, &identity.PublishedAt, &intelligence); err != nil {
			return nil, err
		}
		if size.Valid {
			value := size.Int64
			identity.Size = &value
		}
		if len(intelligence) > 0 && string(intelligence) != "null" {
			identity.Intelligence = &ArtifactIntelligenceSummary{}
			if err := json.Unmarshal(intelligence, identity.Intelligence); err != nil {
				return nil, err
			}
		}
		identities = append(identities, identity)
	}
	return identities, rows.Err()
}

func postgresArtifactIdentityQuery(format Format, purpose ArtifactIdentityPurpose) (string, error) {
	if purpose != ArtifactIdentityScan && purpose != ArtifactIdentityDistribution {
		return "", fmt.Errorf("unsupported artifact identity purpose %q", purpose)
	}
	switch format {
	case FormatMaven:
		return `SELECT coordinate,digest,NULL::bigint AS size,created_at AS published_at
			FROM native_maven_artifacts WHERE repository_id::text=$1 AND state='visible' AND digest ~ '^sha256:[a-f0-9]{64}$'`, nil
	case FormatOCI:
		return `SELECT name AS coordinate,digest,size,created_at AS published_at
			FROM native_oci_manifests WHERE repository_id::text=$1 AND object_key<>'' AND digest ~ '^sha256:[a-f0-9]{64}$'`, nil
	case FormatRaw:
		return `SELECT a.path AS coordinate,a.digest,o.size,a.updated_at AS published_at
			FROM native_raw_assets a JOIN native_raw_objects o ON o.digest=a.digest
			WHERE a.repository_id::text=$1 AND o.object_key<>'' AND a.digest ~ '^sha256:[a-f0-9]{64}$'`, nil
	case FormatNPM:
		return `SELECT ` + protocolidentity.PostgreSQLNPMVersion("v.package_name", "v.version") + ` AS coordinate,v.digest,v.size,v.created_at AS published_at
			FROM native_npm_versions v JOIN native_npm_packages p ON p.repository_id=v.repository_id AND p.name=v.package_name
			WHERE v.repository_id::text=$1 AND v.state='visible' AND v.object_key<>'' AND v.digest ~ '^sha256:[a-f0-9]{64}$' AND NOT p.negative`, nil
	case FormatPyPI:
		return `SELECT ` + protocolidentity.PostgreSQLPyPIVersion("project", "version") + ` AS coordinate,digest,size,created_at AS published_at
			FROM native_pypi_files WHERE repository_id::text=$1 AND state='visible' AND object_key<>'' AND digest ~ '^sha256:[a-f0-9]{64}$'`, nil
	case FormatGo:
		if purpose != ArtifactIdentityScan {
			return "", fmt.Errorf("format %q does not support distribution identities", format)
		}
		return `SELECT ` + protocolidentity.PostgreSQLGoVersion("v.module_path", "v.version") + ` AS coordinate,a.digest,a.size,v.created_at AS published_at
			FROM native_go_versions v
			JOIN LATERAL (
				SELECT digest,size FROM native_go_assets a
				WHERE a.repository_id=v.repository_id AND a.module_path=v.module_path AND a.version=v.version AND a.object_key<>'' AND a.digest ~ '^sha256:[a-f0-9]{64}$'
				ORDER BY CASE a.kind WHEN 'zip' THEN 0 WHEN 'mod' THEN 1 ELSE 2 END LIMIT 1
			) a ON true
			WHERE v.repository_id::text=$1`, nil
	case FormatConan:
		recipes := `SELECT ` + protocolidentity.PostgreSQLConanRecipe("reference", "revision") + ` AS coordinate,digest,NULL::bigint AS size,created_at AS published_at
			FROM native_conan_recipe_revisions r
			WHERE repository_id::text=$1 AND state='visible' AND digest ~ '^sha256:[a-f0-9]{64}$'
			  AND EXISTS (SELECT 1 FROM native_conan_assets a JOIN native_conan_object_intents i ON i.object_key=a.object_key
			              WHERE a.repository_id=r.repository_id AND a.reference=r.reference AND a.recipe_revision=r.revision
			                AND a.package_id='' AND a.package_revision='' AND i.collected_at IS NULL)`
		if purpose == ArtifactIdentityDistribution {
			return recipes, nil
		}
		return recipes + ` UNION ALL
			SELECT ` + protocolidentity.PostgreSQLConanPackage("p.reference", "p.recipe_revision", "p.package_id", "p.revision") + ` AS coordinate,
			       p.digest,NULL::bigint AS size,p.created_at AS published_at
			FROM native_conan_package_revisions p
			JOIN native_conan_recipe_revisions r ON r.repository_id=p.repository_id AND r.reference=p.reference AND r.revision=p.recipe_revision AND r.state='visible'
			WHERE p.repository_id::text=$1 AND p.state='visible' AND p.digest ~ '^sha256:[a-f0-9]{64}$'
			  AND EXISTS (SELECT 1 FROM native_conan_assets a JOIN native_conan_object_intents i ON i.object_key=a.object_key
			              WHERE a.repository_id=p.repository_id AND a.reference=p.reference AND a.recipe_revision=p.recipe_revision
			                AND a.package_id=p.package_id AND a.package_revision=p.revision AND i.collected_at IS NULL)`, nil
	default:
		return "", fmt.Errorf("format %q does not support artifact identities", format)
	}
}
