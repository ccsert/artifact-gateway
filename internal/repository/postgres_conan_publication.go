package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
)

const conanSessionColumns = `id::text,repository_id::text,publisher,kind,reference,recipe_revision,package_id,package_revision,state,expires_at,objects,native,version`

// Keep native, management, lifecycle and distribution writers in the same
// repository/quota/recipe lock order. No object I/O occurs under these locks.
func lockConanRepositoriesTx(ctx context.Context, tx *sql.Tx, ids ...string) error {
	ordered := append([]string(nil), ids...)
	sort.Strings(ordered)
	for i, id := range ordered {
		if i > 0 && id == ordered[i-1] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "conan-publication:"+id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT id FROM hosted_repositories WHERE id=$1 FOR SHARE`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT repository_id FROM repository_capacity_quotas WHERE repository_id=$1 FOR UPDATE`, id); err != nil {
			return err
		}
	}
	return nil
}

func scanConanSession(row interface{ Scan(...any) error }) (session ConanPublishSession, err error) {
	var data []byte
	err = row.Scan(&session.ID, &session.RepositoryID, &session.Publisher, &session.Kind, &session.Reference, &session.RecipeRevision, &session.PackageID, &session.PackageRevision, &session.State, &session.ExpiresAt, &data, &session.Native, &session.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return session, ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal(data, &session.Objects)
	}
	return session, err
}

func (s *PostgresStore) BeginConanNativeSession(ctx context.Context, incoming ConanPublishSession) (ConanPublishSession, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ConanPublishSession{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockConanRepositoriesTx(ctx, tx, incoming.RepositoryID); err != nil {
		return ConanPublishSession{}, err
	}
	var valid bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM hosted_repositories WHERE id=$1 AND format='conan' AND repo_type='hosted' AND state='active')`, incoming.RepositoryID).Scan(&valid)
	if err != nil {
		return ConanPublishSession{}, err
	}
	if !valid {
		return ConanPublishSession{}, ErrNotFound
	}
	_, err = tx.ExecContext(ctx, `UPDATE native_conan_publish_sessions SET state='expired' WHERE repository_id=$1 AND native AND state='open' AND expires_at <= now()`, incoming.RepositoryID)
	if err != nil {
		return ConanPublishSession{}, err
	}
	session, err := scanConanSession(tx.QueryRowContext(ctx, `SELECT `+conanSessionColumns+` FROM native_conan_publish_sessions WHERE repository_id=$1 AND publisher=$2 AND kind=$3 AND reference=$4 AND recipe_revision=$5 AND package_id=$6 AND package_revision=$7 AND native AND state='open' FOR UPDATE`, incoming.RepositoryID, incoming.Publisher, incoming.Kind, incoming.Reference, incoming.RecipeRevision, incoming.PackageID, incoming.PackageRevision))
	if err == nil {
		return session, tx.Commit()
	}
	if !errors.Is(err, ErrNotFound) {
		return ConanPublishSession{}, err
	}
	incoming.ID, incoming.Native, incoming.Version = uuid.NewString(), true, 1
	incoming.State, incoming.ExpiresAt, incoming.Objects = "open", time.Now().UTC().Add(time.Hour), make([]MavenDeclaredObject, 0)
	// Seed only a newly created session, under the publication locks. An open
	// session's uploads must never be overwritten by retry snapshot repair.
	rows, err := tx.QueryContext(ctx, `SELECT a.path,a.object_key,a.digest,a.size FROM native_conan_assets a
		JOIN native_conan_recipe_revisions r ON r.repository_id=a.repository_id AND r.reference=a.reference AND r.revision=a.recipe_revision
		LEFT JOIN native_conan_package_revisions p ON p.repository_id=a.repository_id AND p.reference=a.reference AND p.recipe_revision=a.recipe_revision AND p.package_id=a.package_id AND p.revision=a.package_revision
		WHERE a.repository_id=$1 AND a.reference=$2 AND a.recipe_revision=$3 AND a.package_id=$4 AND a.package_revision=$5
		AND r.state='visible' AND (a.package_id='' OR p.state='visible') ORDER BY a.path`, incoming.RepositoryID, incoming.Reference, incoming.RecipeRevision, incoming.PackageID, incoming.PackageRevision)
	if err != nil {
		return ConanPublishSession{}, err
	}
	uploads := make(map[string]string)
	for rows.Next() {
		var object MavenDeclaredObject
		var key string
		if err = rows.Scan(&object.Name, &key, &object.Digest, &object.Size); err != nil {
			_ = rows.Close()
			return ConanPublishSession{}, err
		}
		incoming.Objects = append(incoming.Objects, object)
		uploads[object.Name] = key
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return ConanPublishSession{}, err
	}
	keys := make([]string, 0, len(uploads))
	for _, key := range uploads {
		keys = append(keys, key)
	}
	if err = lockConanObjectsTx(ctx, tx, keys...); err != nil {
		return ConanPublishSession{}, err
	}
	for _, object := range incoming.Objects {
		var available bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM native_conan_object_intents WHERE object_key=$1 AND digest=$2 AND size=$3 AND collecting_at IS NULL AND collected_at IS NULL)`, uploads[object.Name], object.Digest, object.Size).Scan(&available)
		if err != nil {
			return ConanPublishSession{}, err
		}
		if !available {
			return ConanPublishSession{}, ErrDisabled
		}
	}
	objects, err := json.Marshal(incoming.Objects)
	if err != nil {
		return ConanPublishSession{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO native_conan_publish_sessions(id,repository_id,publisher,kind,reference,recipe_revision,package_id,package_revision,state,expires_at,objects,native,version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'open',$9,$10,true,1)`, incoming.ID, incoming.RepositoryID, incoming.Publisher, incoming.Kind, incoming.Reference, incoming.RecipeRevision, incoming.PackageID, incoming.PackageRevision, incoming.ExpiresAt, objects)
	if err != nil {
		return ConanPublishSession{}, err
	}
	for name, key := range uploads {
		if _, err = tx.ExecContext(ctx, `INSERT INTO native_conan_publish_uploads(session_id,object_name,object_key) VALUES($1,$2,$3)`, incoming.ID, name, key); err != nil {
			return ConanPublishSession{}, err
		}
	}
	return incoming, tx.Commit()
}

func (s *PostgresStore) StageConanPublicationObject(ctx context.Context, id string, object MavenDeclaredObject, key string) (ConanPublishSession, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ConanPublishSession{}, err
	}
	defer func() { _ = tx.Rollback() }()
	session, err := scanConanSession(tx.QueryRowContext(ctx, `SELECT `+conanSessionColumns+` FROM native_conan_publish_sessions WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return ConanPublishSession{}, err
	}
	if session.State != "open" || !session.ExpiresAt.After(time.Now()) {
		return ConanPublishSession{}, ErrDisabled
	}
	if err = lockConanObjectsTx(ctx, tx, key); err != nil {
		return ConanPublishSession{}, err
	}
	var verified bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM native_conan_object_intents WHERE object_key=$1 AND repository_id=$2 AND digest=$3 AND size=$4 AND claimed_at IS NULL AND collecting_at IS NULL AND collected_at IS NULL)`, key, session.RepositoryID, object.Digest, object.Size).Scan(&verified)
	if err != nil {
		return ConanPublishSession{}, err
	}
	if !verified {
		return ConanPublishSession{}, ErrDisabled
	}
	session, err = stageConanPublication(session, object)
	if err != nil {
		return ConanPublishSession{}, err
	}
	data, err := json.Marshal(session.Objects)
	if err != nil {
		return ConanPublishSession{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE native_conan_publish_sessions SET objects=$2,version=$3 WHERE id=$1`, id, data, session.Version); err != nil {
		return ConanPublishSession{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_conan_publish_uploads(session_id,object_name,object_key) VALUES($1,$2,$3) ON CONFLICT(session_id,object_name) DO UPDATE SET object_key=EXCLUDED.object_key`, id, object.Name, key); err != nil {
		return ConanPublishSession{}, err
	}
	return session, tx.Commit()
}

func (s *PostgresStore) CommitConanPublicationSession(ctx context.Context, id string, version int64, digest, actor string) (ConanPublishSession, bool, error) {
	identity, err := s.GetConanPublishSession(ctx, id)
	if err != nil {
		return ConanPublishSession{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ConanPublishSession{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = checkNativePublicationGuard(ctx, tx); err != nil {
		return ConanPublishSession{}, false, err
	}
	if err = lockConanRepositoriesTx(ctx, tx, identity.RepositoryID); err != nil {
		return ConanPublishSession{}, false, err
	}
	session, err := scanConanSession(tx.QueryRowContext(ctx, `SELECT `+conanSessionColumns+` FROM native_conan_publish_sessions WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return ConanPublishSession{}, false, err
	}
	if actor == "" || session.Version != version || session.State != "open" && session.State != "committed" || !session.ExpiresAt.After(time.Now()) {
		return ConanPublishSession{}, false, ErrVersionConflict
	}
	var repo HostedRepository
	repo.ID = session.RepositoryID
	err = tx.QueryRowContext(ctx, `SELECT name FROM hosted_repositories WHERE id=$1 AND repo_type='hosted' AND state='active'`, repo.ID).Scan(&repo.Name)
	if err != nil {
		return ConanPublishSession{}, false, ErrDisabled
	}
	uploads := map[string]string{}
	rows, err := tx.QueryContext(ctx, `SELECT object_name,object_key FROM native_conan_publish_uploads WHERE session_id=$1`, id)
	if err != nil {
		return ConanPublishSession{}, false, err
	}
	for rows.Next() {
		var name, key string
		if err = rows.Scan(&name, &key); err != nil {
			_ = rows.Close()
			return ConanPublishSession{}, false, err
		}
		uploads[name] = key
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return ConanPublishSession{}, false, err
	}
	assets, err := conanSessionAssets(session, uploads)
	if err != nil {
		return ConanPublishSession{}, false, err
	}
	keys := make([]string, 0, len(assets))
	for _, asset := range assets {
		keys = append(keys, asset.ObjectKey)
	}
	if err = lockConanObjectsTx(ctx, tx, keys...); err != nil {
		return ConanPublishSession{}, false, err
	}
	for _, asset := range assets {
		var valid bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM native_conan_object_intents i WHERE i.object_key=$1 AND i.digest=$3 AND i.size=$4 AND i.collecting_at IS NULL AND i.collected_at IS NULL AND (i.repository_id=$2 OR EXISTS(
			SELECT 1 FROM native_conan_assets a WHERE a.repository_id=$2 AND a.reference=$5 AND a.recipe_revision=$6 AND a.package_id=$7 AND a.package_revision=$8 AND a.path=$9 AND a.object_key=i.object_key AND a.digest=i.digest AND a.size=i.size
		)))`, asset.ObjectKey, repo.ID, asset.Digest, asset.Size, session.Reference, session.RecipeRevision, session.PackageID, session.PackageRevision, asset.Path).Scan(&valid)
		if err != nil {
			return ConanPublishSession{}, false, err
		}
		if !valid {
			return ConanPublishSession{}, false, ErrDisabled
		}
	}
	var tombstoned, quarantined bool
	coordinate := session.Reference + "#" + session.RecipeRevision
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM artifact_tombstones WHERE repository_id=$1 AND format='conan' AND coordinate=$2), EXISTS(SELECT 1 FROM artifact_quarantines q JOIN native_conan_recipe_revisions r ON r.repository_id=q.repository_id AND r.reference=$3 AND r.revision=$4 AND r.digest=q.digest WHERE q.repository_id=$1 AND q.format='conan' AND q.coordinate=$2 AND q.state='quarantined')`, repo.ID, coordinate, session.Reference, session.RecipeRevision).Scan(&tombstoned, &quarantined)
	if err != nil {
		return ConanPublishSession{}, false, err
	}
	if tombstoned {
		return ConanPublishSession{}, false, ErrArtifactTombstoned
	}
	if quarantined {
		return ConanPublishSession{}, false, ErrDisabled
	}
	var replay bool
	if session.Kind == "recipe" {
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM native_conan_recipe_revisions WHERE repository_id=$1 AND reference=$2 AND revision=$3)`, repo.ID, session.Reference, session.RecipeRevision).Scan(&replay)
		if err == nil {
			_, err = putConanRecipeRevisionTx(ctx, tx, ConanRecipeRevision{RepositoryID: repo.ID, Reference: session.Reference, Revision: session.RecipeRevision, Digest: digest}, assets)
		}
	} else {
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM native_conan_package_revisions WHERE repository_id=$1 AND reference=$2 AND recipe_revision=$3 AND package_id=$4 AND revision=$5)`, repo.ID, session.Reference, session.RecipeRevision, session.PackageID, session.PackageRevision).Scan(&replay)
		if err == nil {
			_, err = putConanPackageRevisionTx(ctx, tx, ConanPackageRevision{RepositoryID: repo.ID, Reference: session.Reference, RecipeRevision: session.RecipeRevision, PackageID: session.PackageID, Revision: session.PackageRevision, Digest: digest}, assets)
		}
	}
	if err != nil {
		return ConanPublishSession{}, false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE native_conan_publish_sessions SET state='committed' WHERE id=$1`, id); err != nil {
		return ConanPublishSession{}, false, err
	}
	session.State = "committed"
	if err = insertAudit(ctx, tx, conanPublicationAudit(repo, session, digest, actor, replay)); err != nil {
		return ConanPublishSession{}, false, err
	}
	return session, replay, tx.Commit()
}
