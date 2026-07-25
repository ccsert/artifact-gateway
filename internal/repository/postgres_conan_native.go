package repository

import (
	"context"
	"database/sql"
	"errors"
)

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

func scanConanRecipeRevision(scanner interface{ Scan(...any) error }, item *ConanRecipeRevision) error {
	return scanner.Scan(&item.RepositoryID, &item.Reference, &item.Revision, &item.Digest, &item.State, &item.CreatedAt)
}

func scanConanPackageRevision(scanner interface{ Scan(...any) error }, item *ConanPackageRevision) error {
	return scanner.Scan(&item.RepositoryID, &item.Reference, &item.RecipeRevision, &item.PackageID, &item.Revision, &item.Digest, &item.State, &item.CreatedAt)
}
