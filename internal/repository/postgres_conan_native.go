package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *PostgresStore) ListReclaimableConanObjects(ctx context.Context, before time.Time, limit int) ([]ConanObjectIntent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT i.repository_id::text,i.object_key,i.digest,i.size,i.created_at,i.claimed_at,i.collected_at FROM native_conan_object_intents i WHERE i.collected_at IS NULL AND EXISTS (SELECT 1 FROM native_conan_assets a JOIN artifact_tombstones t ON t.repository_id=a.repository_id AND t.format='conan' AND t.coordinate=a.reference || '#' || a.recipe_revision WHERE a.object_key=i.object_key AND t.tombstoned_at <= $1) AND NOT EXISTS (SELECT 1 FROM native_conan_assets a JOIN native_conan_recipe_revisions r ON r.repository_id=a.repository_id AND r.reference=a.reference AND r.revision=a.recipe_revision LEFT JOIN native_conan_package_revisions p ON p.repository_id=a.repository_id AND p.reference=a.reference AND p.recipe_revision=a.recipe_revision AND p.package_id=a.package_id AND p.revision=a.package_revision WHERE a.object_key=i.object_key AND r.state='visible' AND (a.package_id='' OR p.state='visible')) ORDER BY i.created_at LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ConanObjectIntent{}
	for rows.Next() {
		var item ConanObjectIntent
		var claimedAt, collectedAt sql.NullTime
		if err := rows.Scan(&item.RepositoryID, &item.ObjectKey, &item.Digest, &item.Size, &item.CreatedAt, &claimedAt, &collectedAt); err != nil {
			return nil, err
		}
		if claimedAt.Valid {
			item.ClaimedAt = claimedAt.Time
		}
		if collectedAt.Valid {
			item.CollectedAt = collectedAt.Time
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func (s *PostgresStore) ConanObjectHasVisibleReference(ctx context.Context, key string) (bool, error) {
	var yes bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM native_conan_assets a JOIN native_conan_recipe_revisions r ON r.repository_id=a.repository_id AND r.reference=a.reference AND r.revision=a.recipe_revision LEFT JOIN native_conan_package_revisions p ON p.repository_id=a.repository_id AND p.reference=a.reference AND p.recipe_revision=a.recipe_revision AND p.package_id=a.package_id AND p.revision=a.package_revision WHERE a.object_key=$1 AND r.state='visible' AND (a.package_id='' OR p.state='visible'))`, key).Scan(&yes)
	return yes, err
}
func (s *PostgresStore) MarkConanObjectCollected(ctx context.Context, key string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE native_conan_object_intents SET collected_at=now() WHERE object_key=$1 AND collected_at IS NULL`, key)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) StageConanObject(ctx context.Context, object ConanObjectIntent) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO native_conan_object_intents (object_key,repository_id,digest,size) VALUES ($1,$2,$3,$4) ON CONFLICT (object_key) DO NOTHING`, object.ObjectKey, object.RepositoryID, object.Digest, object.Size)
	return err
}

func (s *PostgresStore) PutConanRecipeRevision(ctx context.Context, revision ConanRecipeRevision, assets []ConanAsset) (ConanRecipeRevision, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ConanRecipeRevision{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var existing ConanRecipeRevision
	err = scanConanRecipeRevision(tx.QueryRowContext(ctx, `SELECT repository_id::text,reference,revision,digest,state,created_at FROM native_conan_recipe_revisions WHERE repository_id::text=$1 AND reference=$2 AND revision=$3 FOR SHARE`, revision.RepositoryID, revision.Reference, revision.Revision), &existing)
	if err == nil {
		return existing, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ConanRecipeRevision{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_conan_recipe_revisions (repository_id,reference,revision,digest,state) VALUES ($1,$2,$3,$4,'visible')`, revision.RepositoryID, revision.Reference, revision.Revision, revision.Digest); err != nil {
		return ConanRecipeRevision{}, err
	}
	for _, asset := range assets {
		if err = claimConanObject(ctx, tx, asset.ObjectKey); err != nil {
			return ConanRecipeRevision{}, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO native_conan_assets (repository_id,reference,recipe_revision,package_id,package_revision,path,object_key,digest,size) VALUES ($1,$2,$3,'','',$4,$5,$6,$7) ON CONFLICT DO NOTHING`, asset.RepositoryID, asset.Reference, asset.RecipeRevision, asset.Path, asset.ObjectKey, asset.Digest, asset.Size); err != nil {
			return ConanRecipeRevision{}, err
		}
	}
	if err = scanConanRecipeRevision(tx.QueryRowContext(ctx, `SELECT repository_id::text,reference,revision,digest,state,created_at FROM native_conan_recipe_revisions WHERE repository_id::text=$1 AND reference=$2 AND revision=$3`, revision.RepositoryID, revision.Reference, revision.Revision), &revision); err != nil {
		return ConanRecipeRevision{}, err
	}
	return revision, tx.Commit()
}

func (s *PostgresStore) PutConanPackageRevision(ctx context.Context, revision ConanPackageRevision, assets []ConanAsset) (ConanPackageRevision, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ConanPackageRevision{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var recipeState string
	if err = tx.QueryRowContext(ctx, `SELECT state FROM native_conan_recipe_revisions WHERE repository_id::text=$1 AND reference=$2 AND revision=$3 FOR SHARE`, revision.RepositoryID, revision.Reference, revision.RecipeRevision).Scan(&recipeState); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ConanPackageRevision{}, ErrNotFound
		}
		return ConanPackageRevision{}, err
	}
	if recipeState != "visible" {
		return ConanPackageRevision{}, ErrDisabled
	}
	var existing ConanPackageRevision
	err = scanConanPackageRevision(tx.QueryRowContext(ctx, `SELECT repository_id::text,reference,recipe_revision,package_id,revision,digest,state,created_at FROM native_conan_package_revisions WHERE repository_id::text=$1 AND reference=$2 AND recipe_revision=$3 AND package_id=$4 AND revision=$5 FOR SHARE`, revision.RepositoryID, revision.Reference, revision.RecipeRevision, revision.PackageID, revision.Revision), &existing)
	if err == nil {
		return existing, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ConanPackageRevision{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_conan_package_revisions (repository_id,reference,recipe_revision,package_id,revision,digest,state) VALUES ($1,$2,$3,$4,$5,$6,'visible')`, revision.RepositoryID, revision.Reference, revision.RecipeRevision, revision.PackageID, revision.Revision, revision.Digest); err != nil {
		return ConanPackageRevision{}, err
	}
	for _, asset := range assets {
		if err = claimConanObject(ctx, tx, asset.ObjectKey); err != nil {
			return ConanPackageRevision{}, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO native_conan_assets (repository_id,reference,recipe_revision,package_id,package_revision,path,object_key,digest,size) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT DO NOTHING`, asset.RepositoryID, asset.Reference, asset.RecipeRevision, asset.PackageID, asset.PackageRevision, asset.Path, asset.ObjectKey, asset.Digest, asset.Size); err != nil {
			return ConanPackageRevision{}, err
		}
	}
	if err = scanConanPackageRevision(tx.QueryRowContext(ctx, `SELECT repository_id::text,reference,recipe_revision,package_id,revision,digest,state,created_at FROM native_conan_package_revisions WHERE repository_id::text=$1 AND reference=$2 AND recipe_revision=$3 AND package_id=$4 AND revision=$5`, revision.RepositoryID, revision.Reference, revision.RecipeRevision, revision.PackageID, revision.Revision), &revision); err != nil {
		return ConanPackageRevision{}, err
	}
	return revision, tx.Commit()
}

func (s *PostgresStore) PublishReplicatedConanRevision(ctx context.Context, publication ConanReplicationPublication) (ConanRecipeRevision, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ConanRecipeRevision{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var source ConanRecipeRevision
	err = scanConanRecipeRevision(tx.QueryRowContext(ctx, `SELECT repository_id::text,reference,revision,digest,state,created_at FROM native_conan_recipe_revisions WHERE repository_id::text=$1 AND reference=$2 AND revision=$3 AND digest=$4 AND state='visible' FOR SHARE`, publication.SourceRepositoryID, publication.Recipe.Reference, publication.Recipe.Revision, publication.Recipe.Digest), &source)
	if errors.Is(err, sql.ErrNoRows) {
		return ConanRecipeRevision{}, ErrNotFound
	}
	if err != nil {
		return ConanRecipeRevision{}, err
	}
	for _, pkg := range publication.Packages {
		var visible bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM native_conan_package_revisions WHERE repository_id::text=$1 AND reference=$2 AND recipe_revision=$3 AND package_id=$4 AND revision=$5 AND digest=$6 AND state='visible')`, publication.SourceRepositoryID, pkg.Reference, pkg.RecipeRevision, pkg.PackageID, pkg.Revision, pkg.Digest).Scan(&visible); err != nil {
			return ConanRecipeRevision{}, err
		}
		if !visible {
			return ConanRecipeRevision{}, ErrNotFound
		}
	}
	var sourcePackageCount int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM native_conan_package_revisions WHERE repository_id::text=$1 AND reference=$2 AND recipe_revision=$3 AND state='visible'`, publication.SourceRepositoryID, publication.Recipe.Reference, publication.Recipe.Revision).Scan(&sourcePackageCount); err != nil {
		return ConanRecipeRevision{}, err
	}
	if sourcePackageCount != len(publication.Packages) {
		return ConanRecipeRevision{}, ErrNotFound
	}
	for _, asset := range publication.SourceAssets {
		var matched bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM native_conan_assets WHERE repository_id::text=$1 AND reference=$2 AND recipe_revision=$3 AND package_id=$4 AND package_revision=$5 AND path=$6 AND object_key=$7 AND digest=$8 AND size=$9)`, publication.SourceRepositoryID, asset.Reference, asset.RecipeRevision, asset.PackageID, asset.PackageRevision, asset.Path, asset.ObjectKey, asset.Digest, asset.Size).Scan(&matched); err != nil {
			return ConanRecipeRevision{}, err
		}
		if !matched {
			return ConanRecipeRevision{}, ErrNotFound
		}
	}
	var existing ConanRecipeRevision
	err = scanConanRecipeRevision(tx.QueryRowContext(ctx, `SELECT repository_id::text,reference,revision,digest,state,created_at FROM native_conan_recipe_revisions WHERE repository_id::text=$1 AND reference=$2 AND revision=$3 FOR SHARE`, publication.Recipe.RepositoryID, publication.Recipe.Reference, publication.Recipe.Revision), &existing)
	if err == nil {
		if existing.Digest != publication.Recipe.Digest {
			return ConanRecipeRevision{}, ErrNameExists
		}
		return existing, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ConanRecipeRevision{}, err
	}
	for _, asset := range publication.TargetAssets {
		if _, err = tx.ExecContext(ctx, `INSERT INTO native_conan_object_intents (object_key,repository_id,digest,size) VALUES ($1,$2,$3,$4) ON CONFLICT (object_key) DO NOTHING`, asset.ObjectKey, asset.RepositoryID, asset.Digest, asset.Size); err != nil {
			return ConanRecipeRevision{}, err
		}
		if err = claimConanObject(ctx, tx, asset.ObjectKey); err != nil {
			return ConanRecipeRevision{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_conan_recipe_revisions (repository_id,reference,revision,digest,state) VALUES ($1,$2,$3,$4,'visible')`, publication.Recipe.RepositoryID, publication.Recipe.Reference, publication.Recipe.Revision, publication.Recipe.Digest); err != nil {
		return ConanRecipeRevision{}, err
	}
	for _, pkg := range publication.Packages {
		if _, err = tx.ExecContext(ctx, `INSERT INTO native_conan_package_revisions (repository_id,reference,recipe_revision,package_id,revision,digest,state) VALUES ($1,$2,$3,$4,$5,$6,'visible')`, publication.Recipe.RepositoryID, pkg.Reference, pkg.RecipeRevision, pkg.PackageID, pkg.Revision, pkg.Digest); err != nil {
			return ConanRecipeRevision{}, err
		}
	}
	for _, asset := range publication.TargetAssets {
		if _, err = tx.ExecContext(ctx, `INSERT INTO native_conan_assets (repository_id,reference,recipe_revision,package_id,package_revision,path,object_key,digest,size) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, asset.RepositoryID, asset.Reference, asset.RecipeRevision, asset.PackageID, asset.PackageRevision, asset.Path, asset.ObjectKey, asset.Digest, asset.Size); err != nil {
			return ConanRecipeRevision{}, err
		}
	}
	if err = scanConanRecipeRevision(tx.QueryRowContext(ctx, `SELECT repository_id::text,reference,revision,digest,state,created_at FROM native_conan_recipe_revisions WHERE repository_id::text=$1 AND reference=$2 AND revision=$3`, publication.Recipe.RepositoryID, publication.Recipe.Reference, publication.Recipe.Revision), &publication.Recipe); err != nil {
		return ConanRecipeRevision{}, err
	}
	return publication.Recipe, tx.Commit()
}

func claimConanObject(ctx context.Context, tx *sql.Tx, objectKey string) error {
	result, err := tx.ExecContext(ctx, `UPDATE native_conan_object_intents SET claimed_at=now() WHERE object_key=$1 AND claimed_at IS NULL`, objectKey)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrDisabled
	}
	return nil
}

func (s *PostgresStore) PromoteConanRecipeRevision(ctx context.Context, p ConanPromotion) (ConanRecipeRevision, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ConanRecipeRevision{}, err
	}
	defer tx.Rollback()
	var out ConanRecipeRevision
	err = scanConanRecipeRevision(tx.QueryRowContext(ctx, `SELECT repository_id::text,reference,revision,digest,state,created_at FROM native_conan_recipe_revisions WHERE repository_id::text=$1 AND reference=$2 AND revision=$3 AND digest=$4 AND state='visible' FOR SHARE`, p.SourceRepositoryID, p.Reference, p.Revision, p.Digest), &out)
	if errors.Is(err, sql.ErrNoRows) {
		return ConanRecipeRevision{}, ErrNotFound
	}
	if err != nil {
		return ConanRecipeRevision{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_conan_recipe_revisions(repository_id,reference,revision,digest,state) VALUES($1,$2,$3,$4,'visible')`, p.TargetRepositoryID, p.Reference, p.Revision, p.Digest); err != nil {
		return ConanRecipeRevision{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_conan_package_revisions(repository_id,reference,recipe_revision,package_id,revision,digest,state) SELECT $1,reference,recipe_revision,package_id,revision,digest,'visible' FROM native_conan_package_revisions WHERE repository_id::text=$2 AND reference=$3 AND recipe_revision=$4 AND state='visible'`, p.TargetRepositoryID, p.SourceRepositoryID, p.Reference, p.Revision); err != nil {
		return ConanRecipeRevision{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_conan_assets(repository_id,reference,recipe_revision,package_id,package_revision,path,object_key,digest,size) SELECT $1,reference,recipe_revision,package_id,package_revision,path,object_key,digest,size FROM native_conan_assets WHERE repository_id::text=$2 AND reference=$3 AND recipe_revision=$4`, p.TargetRepositoryID, p.SourceRepositoryID, p.Reference, p.Revision); err != nil {
		return ConanRecipeRevision{}, err
	}
	out.RepositoryID = p.TargetRepositoryID
	if err = tx.Commit(); err != nil {
		return ConanRecipeRevision{}, err
	}
	return out, nil
}

func (s *PostgresStore) GetConanRecipeRevision(ctx context.Context, repositoryID, reference, revision string) (ConanRecipeRevision, error) {
	var item ConanRecipeRevision
	err := scanConanRecipeRevision(s.db.QueryRowContext(ctx, `SELECT repository_id::text,reference,revision,digest,state,created_at FROM native_conan_recipe_revisions WHERE repository_id::text=$1 AND reference=$2 AND revision=$3`, repositoryID, reference, revision), &item)
	if errors.Is(err, sql.ErrNoRows) {
		return ConanRecipeRevision{}, ErrNotFound
	}
	return item, err
}

func (s *PostgresStore) GetConanPackageRevision(ctx context.Context, repositoryID, reference, recipeRevision, packageID, revision string) (ConanPackageRevision, error) {
	var item ConanPackageRevision
	err := scanConanPackageRevision(s.db.QueryRowContext(ctx, `SELECT repository_id::text,reference,recipe_revision,package_id,revision,digest,state,created_at FROM native_conan_package_revisions WHERE repository_id::text=$1 AND reference=$2 AND recipe_revision=$3 AND package_id=$4 AND revision=$5`, repositoryID, reference, recipeRevision, packageID, revision), &item)
	if errors.Is(err, sql.ErrNoRows) {
		return ConanPackageRevision{}, ErrNotFound
	}
	return item, err
}

func (s *PostgresStore) SearchConanReferences(ctx context.Context, repositoryID, prefix string, limit int, after string) ([]ConanReference, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.reference, COALESCE(p.publisher,'')
		FROM (SELECT DISTINCT reference FROM native_conan_recipe_revisions WHERE repository_id::text=$1 AND state='visible' AND reference LIKE $2 || '%' AND reference > $3 ORDER BY reference LIMIT $4) r
		LEFT JOIN LATERAL (
			SELECT s.publisher FROM native_conan_publish_sessions s
			WHERE s.repository_id::text=$1 AND s.reference=r.reference AND s.state='committed'
			ORDER BY s.expires_at DESC LIMIT 1
		) p ON true
		ORDER BY r.reference`, repositoryID, prefix, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := []ConanReference{}
	for rows.Next() {
		var ref ConanReference
		if err = rows.Scan(&ref.Reference, &ref.Publisher); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func (s *PostgresStore) ListConanRecipeRevisions(ctx context.Context, repositoryID, reference string) ([]ConanRecipeRevision, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT repository_id::text,reference,revision,digest,state,created_at FROM native_conan_recipe_revisions WHERE repository_id::text=$1 AND reference=$2 AND state='visible' ORDER BY created_at DESC`, repositoryID, reference)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConanRecipeRevision
	for rows.Next() {
		var item ConanRecipeRevision
		if err := scanConanRecipeRevision(rows, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) SearchConanRecipeRevisions(ctx context.Context, repositoryID, reference, query string, limit int, after string) ([]ConanRecipeRevision, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT repository_id::text,reference,revision,digest,state,created_at FROM native_conan_recipe_revisions WHERE repository_id::text=$1 AND reference=$2 AND state='visible' AND revision>$3 AND ($4='' OR revision ILIKE '%' || $4 || '%' OR digest ILIKE '%' || $4 || '%') ORDER BY revision LIMIT $5`, repositoryID, reference, after, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConanRecipeRevision
	for rows.Next() {
		var item ConanRecipeRevision
		if err := scanConanRecipeRevision(rows, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ListConanPackageRevisions(ctx context.Context, repositoryID, reference, recipeRevision, packageID string) ([]ConanPackageRevision, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT repository_id::text,reference,recipe_revision,package_id,revision,digest,state,created_at FROM native_conan_package_revisions WHERE repository_id::text=$1 AND reference=$2 AND recipe_revision=$3 AND package_id=$4 AND state='visible' ORDER BY created_at DESC`, repositoryID, reference, recipeRevision, packageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConanPackageRevision
	for rows.Next() {
		var item ConanPackageRevision
		if err := scanConanPackageRevision(rows, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func (s *PostgresStore) ListConanPackageIDs(ctx context.Context, repositoryID, reference, recipeRevision string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT package_id FROM native_conan_package_revisions WHERE repository_id::text=$1 AND reference=$2 AND recipe_revision=$3 AND state='visible' ORDER BY package_id`, repositoryID, reference, recipeRevision)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ListConanRecipeAssets(ctx context.Context, repositoryID, reference, revision string) ([]ConanAsset, error) {
	return s.listConanAssets(ctx, repositoryID, reference, revision, "", "")
}

func (s *PostgresStore) ListConanPackageAssets(ctx context.Context, repositoryID, reference, recipeRevision, packageID, packageRevision string) ([]ConanAsset, error) {
	return s.listConanAssets(ctx, repositoryID, reference, recipeRevision, packageID, packageRevision)
}

func (s *PostgresStore) listConanAssets(ctx context.Context, repositoryID, reference, recipeRevision, packageID, packageRevision string) ([]ConanAsset, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT repository_id::text,reference,recipe_revision,package_id,package_revision,path,object_key,digest,size FROM native_conan_assets WHERE repository_id::text=$1 AND reference=$2 AND recipe_revision=$3 AND package_id=$4 AND package_revision=$5 ORDER BY path`, repositoryID, reference, recipeRevision, packageID, packageRevision)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConanAsset
	for rows.Next() {
		var asset ConanAsset
		if err := scanConanAsset(rows, &asset); err != nil {
			return nil, err
		}
		out = append(out, asset)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetConanRecipeAsset(ctx context.Context, repositoryID, reference, revision, path string) (ConanAsset, error) {
	return s.getConanAsset(ctx, repositoryID, reference, revision, "", "", path)
}

func (s *PostgresStore) GetConanPackageAsset(ctx context.Context, repositoryID, reference, recipeRevision, packageID, packageRevision, path string) (ConanAsset, error) {
	return s.getConanAsset(ctx, repositoryID, reference, recipeRevision, packageID, packageRevision, path)
}

func (s *PostgresStore) getConanAsset(ctx context.Context, repositoryID, reference, recipeRevision, packageID, packageRevision, path string) (ConanAsset, error) {
	var asset ConanAsset
	err := scanConanAsset(s.db.QueryRowContext(ctx, `SELECT repository_id::text,reference,recipe_revision,package_id,package_revision,path,object_key,digest,size FROM native_conan_assets WHERE repository_id::text=$1 AND reference=$2 AND recipe_revision=$3 AND package_id=$4 AND package_revision=$5 AND path=$6`, repositoryID, reference, recipeRevision, packageID, packageRevision, path), &asset)
	if errors.Is(err, sql.ErrNoRows) {
		return ConanAsset{}, ErrNotFound
	}
	return asset, err
}

func (s *PostgresStore) TombstoneConanRecipeRevision(ctx context.Context, repositoryID, reference, revision string) (ConanRecipeRevision, error) {
	var item ConanRecipeRevision
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return item, err
	}
	defer func() { _ = tx.Rollback() }()
	err = scanConanRecipeRevision(tx.QueryRowContext(ctx, `UPDATE native_conan_recipe_revisions SET state='deleted' WHERE repository_id::text=$1 AND reference=$2 AND revision=$3 RETURNING repository_id::text,reference,revision,digest,state,created_at`, repositoryID, reference, revision), &item)
	if errors.Is(err, sql.ErrNoRows) {
		return ConanRecipeRevision{}, ErrNotFound
	}
	if err != nil {
		return item, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE native_conan_package_revisions SET state='deleted' WHERE repository_id::text=$1 AND reference=$2 AND recipe_revision=$3`, repositoryID, reference, revision); err != nil {
		return item, err
	}
	coordinate := reference + "#" + revision
	if _, err = tx.ExecContext(ctx, `INSERT INTO artifact_tombstones (repository_id,format,coordinate,digest) VALUES ($1,'conan',$2,$3) ON CONFLICT DO NOTHING`, repositoryID, coordinate, item.Digest); err != nil {
		return item, err
	}
	return item, tx.Commit()
}

func (s *PostgresStore) TombstoneConanPackageRevision(ctx context.Context, repositoryID, reference, recipeRevision, packageID, revision string) (ConanPackageRevision, error) {
	var item ConanPackageRevision
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return item, err
	}
	defer func() { _ = tx.Rollback() }()
	err = scanConanPackageRevision(tx.QueryRowContext(ctx, `UPDATE native_conan_package_revisions SET state='deleted' WHERE repository_id::text=$1 AND reference=$2 AND recipe_revision=$3 AND package_id=$4 AND revision=$5 RETURNING repository_id::text,reference,recipe_revision,package_id,revision,digest,state,created_at`, repositoryID, reference, recipeRevision, packageID, revision), &item)
	if errors.Is(err, sql.ErrNoRows) {
		return ConanPackageRevision{}, ErrNotFound
	}
	if err != nil {
		return item, err
	}
	coordinate := reference + "#" + recipeRevision + "/" + packageID + "#" + revision
	if _, err = tx.ExecContext(ctx, `INSERT INTO artifact_tombstones (repository_id,format,coordinate,digest) VALUES ($1,'conan',$2,$3) ON CONFLICT DO NOTHING`, repositoryID, coordinate, item.Digest); err != nil {
		return item, err
	}
	return item, tx.Commit()
}

func (s *PostgresStore) RestoreConanRecipeRevision(ctx context.Context, repositoryID, reference, revision string) (ConanRecipeRevision, error) {
	var item ConanRecipeRevision
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return item, err
	}
	defer func() { _ = tx.Rollback() }()
	err = scanConanRecipeRevision(tx.QueryRowContext(ctx, `UPDATE native_conan_recipe_revisions r SET state='visible' WHERE r.repository_id::text=$1 AND r.reference=$2 AND r.revision=$3 AND r.state='deleted' AND NOT EXISTS (SELECT 1 FROM native_conan_assets a JOIN native_conan_object_intents i ON i.object_key=a.object_key WHERE a.repository_id=r.repository_id AND a.reference=r.reference AND a.recipe_revision=r.revision AND a.package_id='' AND i.collected_at IS NOT NULL) RETURNING repository_id::text,reference,revision,digest,state,created_at`, repositoryID, reference, revision), &item)
	if errors.Is(err, sql.ErrNoRows) {
		return item, ErrNotFound
	}
	if err != nil {
		return item, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM artifact_tombstones WHERE repository_id::text=$1 AND format='conan' AND coordinate=$2`, repositoryID, reference+"#"+revision); err != nil {
		return item, err
	}
	return item, tx.Commit()
}
func (s *PostgresStore) RestoreConanPackageRevision(ctx context.Context, repositoryID, reference, recipeRevision, packageID, revision string) (ConanPackageRevision, error) {
	var item ConanPackageRevision
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return item, err
	}
	defer func() { _ = tx.Rollback() }()
	err = scanConanPackageRevision(tx.QueryRowContext(ctx, `UPDATE native_conan_package_revisions p SET state='visible' WHERE p.repository_id::text=$1 AND p.reference=$2 AND p.recipe_revision=$3 AND p.package_id=$4 AND p.revision=$5 AND p.state='deleted' AND EXISTS (SELECT 1 FROM native_conan_recipe_revisions r WHERE r.repository_id=p.repository_id AND r.reference=p.reference AND r.revision=p.recipe_revision AND r.state='visible') AND NOT EXISTS (SELECT 1 FROM native_conan_assets a JOIN native_conan_object_intents i ON i.object_key=a.object_key WHERE a.repository_id=p.repository_id AND a.reference=p.reference AND a.recipe_revision=p.recipe_revision AND a.package_id=p.package_id AND a.package_revision=p.revision AND i.collected_at IS NOT NULL) RETURNING repository_id::text,reference,recipe_revision,package_id,revision,digest,state,created_at`, repositoryID, reference, recipeRevision, packageID, revision), &item)
	if errors.Is(err, sql.ErrNoRows) {
		return item, ErrNotFound
	}
	if err != nil {
		return item, err
	}
	coordinate := reference + "#" + recipeRevision + "/" + packageID + "#" + revision
	if _, err = tx.ExecContext(ctx, `DELETE FROM artifact_tombstones WHERE repository_id::text=$1 AND format='conan' AND coordinate=$2`, repositoryID, coordinate); err != nil {
		return item, err
	}
	return item, tx.Commit()
}

func scanConanRecipeRevision(scanner interface{ Scan(...any) error }, item *ConanRecipeRevision) error {
	return scanner.Scan(&item.RepositoryID, &item.Reference, &item.Revision, &item.Digest, &item.State, &item.CreatedAt)
}

func scanConanPackageRevision(scanner interface{ Scan(...any) error }, item *ConanPackageRevision) error {
	return scanner.Scan(&item.RepositoryID, &item.Reference, &item.RecipeRevision, &item.PackageID, &item.Revision, &item.Digest, &item.State, &item.CreatedAt)
}

func scanConanAsset(scanner interface{ Scan(...any) error }, asset *ConanAsset) error {
	return scanner.Scan(&asset.RepositoryID, &asset.Reference, &asset.RecipeRevision, &asset.PackageID, &asset.PackageRevision, &asset.Path, &asset.ObjectKey, &asset.Digest, &asset.Size)
}
