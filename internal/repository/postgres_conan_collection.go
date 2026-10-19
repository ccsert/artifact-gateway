package repository

import (
	"context"
	"database/sql"
	"errors"
	"sort"
)

const conanObjectVisibleReferenceSQL = `EXISTS (
 SELECT 1 FROM native_conan_assets a
 JOIN native_conan_recipe_revisions r ON r.repository_id=a.repository_id AND r.reference=a.reference AND r.revision=a.recipe_revision
 LEFT JOIN native_conan_package_revisions p ON p.repository_id=a.repository_id AND p.reference=a.reference AND p.recipe_revision=a.recipe_revision AND p.package_id=a.package_id AND p.revision=a.package_revision
 WHERE a.object_key=$1 AND r.state='visible' AND (a.package_id='' OR p.state='visible'))`

const conanObjectOpenSessionReferenceSQL = `EXISTS (
 SELECT 1 FROM native_conan_publish_uploads u
 JOIN native_conan_publish_sessions s ON s.id=u.session_id
 WHERE u.object_key=$1 AND s.state='open' AND s.expires_at>now())`

// Every new object reference uses this same object-key boundary, including
// promotions whose shared key belongs to another repository. Take repository
// and session locks first, then all object keys in order, then recipe rows.
// Advisory locks also fence keys whose intent has not yet been inserted.
func lockConanObjectsTx(ctx context.Context, tx *sql.Tx, keys ...string) error {
	ordered := append([]string(nil), keys...)
	sort.Strings(ordered)
	for index, key := range ordered {
		if key == "" {
			return ErrDisabled
		}
		if index > 0 && ordered[index-1] == key {
			continue
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "conan-object:"+key); err != nil {
			return err
		}
		var existing string
		if err := tx.QueryRowContext(ctx, `SELECT object_key FROM native_conan_object_intents WHERE object_key=$1 FOR UPDATE`, key).Scan(&existing); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return nil
}

func lockAvailableConanAssetsTx(ctx context.Context, tx *sql.Tx, assets []ConanAsset) error {
	keys := make([]string, 0, len(assets))
	for _, asset := range assets {
		keys = append(keys, asset.ObjectKey)
	}
	if err := lockConanObjectsTx(ctx, tx, keys...); err != nil {
		return err
	}
	for _, asset := range assets {
		var valid bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM native_conan_object_intents WHERE object_key=$1 AND digest=$2 AND size=$3 AND collecting_at IS NULL AND collected_at IS NULL)`, asset.ObjectKey, asset.Digest, asset.Size).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return ErrDisabled
		}
	}
	return nil
}

func lockConanRevisionObjectsTx(ctx context.Context, tx *sql.Tx, repositoryID, reference, revision string) ([]ConanAsset, error) {
	rows, err := tx.QueryContext(ctx, `SELECT repository_id::text,reference,recipe_revision,package_id,package_revision,path,object_key,digest,size FROM native_conan_assets WHERE repository_id=$1 AND reference=$2 AND recipe_revision=$3`, repositoryID, reference, revision)
	if err != nil {
		return nil, err
	}
	var assets []ConanAsset
	var keys []string
	for rows.Next() {
		var asset ConanAsset
		if err := scanConanAsset(rows, &asset); err != nil {
			_ = rows.Close()
			return nil, err
		}
		assets = append(assets, asset)
		keys = append(keys, asset.ObjectKey)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	if err = lockConanObjectsTx(ctx, tx, keys...); err != nil {
		return nil, err
	}
	return assets, nil
}

func (s *PostgresStore) BeginConanObjectCollection(ctx context.Context, key string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockConanObjectsTx(ctx, tx, key); err != nil {
		return false, err
	}
	var collected bool
	if err = tx.QueryRowContext(ctx, `SELECT collected_at IS NOT NULL FROM native_conan_object_intents WHERE object_key=$1`, key).Scan(&collected); errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	} else if err != nil {
		return false, err
	}
	var referenced bool
	if err = tx.QueryRowContext(ctx, `SELECT (`+conanObjectVisibleReferenceSQL+`) OR (`+conanObjectOpenSessionReferenceSQL+`)`, key).Scan(&referenced); err != nil {
		return false, err
	}
	if collected || referenced {
		return false, tx.Commit()
	}
	if _, err = tx.ExecContext(ctx, `UPDATE native_conan_object_intents SET collecting_at=COALESCE(collecting_at,now()) WHERE object_key=$1`, key); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (s *PostgresStore) CompleteConanObjectCollection(ctx context.Context, key string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockConanObjectsTx(ctx, tx, key); err != nil {
		return err
	}
	var collecting, collected bool
	if err = tx.QueryRowContext(ctx, `SELECT collecting_at IS NOT NULL,collected_at IS NOT NULL FROM native_conan_object_intents WHERE object_key=$1`, key).Scan(&collecting, &collected); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if collected {
		return tx.Commit()
	}
	var referenced bool
	if err = tx.QueryRowContext(ctx, `SELECT (`+conanObjectVisibleReferenceSQL+`) OR (`+conanObjectOpenSessionReferenceSQL+`)`, key).Scan(&referenced); err != nil {
		return err
	}
	if !collecting || referenced {
		return ErrDisabled
	}
	if _, err = tx.ExecContext(ctx, `UPDATE native_conan_object_intents SET collected_at=now() WHERE object_key=$1`, key); err != nil {
		return err
	}
	return tx.Commit()
}
