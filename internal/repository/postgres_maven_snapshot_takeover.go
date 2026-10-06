package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

const mavenDeploymentColumns = `id::text,repository_id::text,coordinate,publisher,pom_object,state,expires_at,objects,client_timestamp,client_build_number`

func scanMavenDeployment(row interface{ Scan(...any) error }) (v MavenPublishSession, err error) {
	var objects []byte
	err = row.Scan(&v.ID, &v.RepositoryID, &v.Coordinate, &v.Publisher, &v.PomObject, &v.State, &v.ExpiresAt, &objects, &v.ClientTimestamp, &v.ClientBuildNumber)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal(objects, &v.Objects)
	}
	return v, err
}

// Coordinate, repository, checkpoint, session, then sorted intents is the
// transaction lock order shared with import and protocol publication.
func lockMavenTakeover(ctx context.Context, tx *sql.Tx, repo, coordinate string) (MavenSnapshotImport, error) {
	if err := lockMavenCoordinate(ctx, tx, repo, coordinate); err != nil {
		return MavenSnapshotImport{}, err
	}
	var strict, retention bool
	err := tx.QueryRowContext(ctx, `SELECT maven_strict_publication,EXISTS(SELECT 1 FROM repository_retention_policies WHERE repository_id=$1 AND enabled) FROM hosted_repositories WHERE id=$1 AND format='maven' AND repo_type='hosted' AND state='active' FOR SHARE`, repo).Scan(&strict, &retention)
	if errors.Is(err, sql.ErrNoRows) {
		return MavenSnapshotImport{}, ErrNotFound
	}
	if err != nil {
		return MavenSnapshotImport{}, err
	}
	v, err := scanMavenImport(tx.QueryRowContext(ctx, `SELECT `+mavenImportColumns+` FROM native_maven_snapshot_imports WHERE repository_id=$1 AND coordinate=$2 FOR UPDATE`, repo, coordinate))
	if err == nil && (strict || retention || v.State != "committed") {
		err = ErrMavenSnapshotTakeoverNotReady
	}
	if err == nil {
		err = checkMavenSnapshotReadPolicyTx(ctx, tx, repo, coordinate)
	}
	return v, err
}

func checkPostgresMavenTakeover(ctx context.Context, tx *sql.Tx, p MavenSnapshotImportPlan, key string) (MavenSnapshotImport, error) {
	v, err := lockMavenTakeover(ctx, tx, p.RepositoryID, p.Coordinate)
	if err != nil {
		return v, err
	}
	if !validMavenImportPlan(p) || key == "" || len(key) > 128 || v.PlanDigest != mavenImportDigest(p) || v.Writable() && (v.TakeoverKey != key || v.TakeoverActor != p.Actor) {
		return v, ErrIdempotencyConflict
	}
	var maximum int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(GREATEST(build_number,source_build_number)),0) FROM native_maven_artifacts WHERE repository_id=$1 AND coordinate=$2`, p.RepositoryID, p.Coordinate).Scan(&maximum); err != nil {
		return v, err
	}
	if maximum >= MaxMavenSnapshotBuildNumber {
		return v, ErrMavenSnapshotBuildExhausted
	}
	artifacts, err := lockMavenSnapshotArtifactsTx(ctx, tx, p.RepositoryID, p.Coordinate)
	if err != nil {
		return v, err
	}
	for _, expected := range p.Artifacts {
		found := false
		for _, a := range artifacts {
			if a.SourceTimestamp == expected.SourceTimestamp && a.SourceBuildNumber == expected.SourceBuildNumber && a.Digest == expected.Digest && a.State == "visible" {
				found = true
			}
		}
		if !found {
			return v, ErrMavenSnapshotTakeoverNotReady
		}
	}
	if err = lockMavenSnapshotAssetIntentsTx(ctx, tx, p.Assets); err != nil {
		return v, err
	}

	for _, expected := range p.Assets {
		var a MavenAsset
		var live bool
		err = tx.QueryRowContext(ctx, `SELECT a.repository_id::text,a.path,a.object_key,a.digest,a.size,EXISTS(SELECT 1 FROM native_maven_object_references r WHERE r.object_key=a.object_key) AND EXISTS(SELECT 1 FROM native_maven_object_intents i WHERE i.object_key=a.object_key AND claimed_at IS NULL AND deleted_at IS NULL) FROM native_maven_assets a WHERE repository_id=$1 AND path=$2`, p.RepositoryID, expected.Path).Scan(&a.RepositoryID, &a.Path, &a.ObjectKey, &a.Digest, &a.Size, &live)
		if errors.Is(err, sql.ErrNoRows) || err == nil && (a != expected || !live) {
			return v, ErrMavenSnapshotTakeoverNotReady
		}
		if err != nil {
			return v, err
		}
	}
	return v, nil
}
func (s *PostgresStore) CheckMavenSnapshotTakeover(ctx context.Context, p MavenSnapshotImportPlan, key string) (MavenSnapshotImport, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MavenSnapshotImport{}, err
	}
	defer func() { _ = tx.Rollback() }()
	return checkPostgresMavenTakeover(ctx, tx, p, key)
}
func (s *PostgresStore) TakeoverMavenSnapshotImport(ctx context.Context, p MavenSnapshotImportPlan, key string) (MavenSnapshotImport, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MavenSnapshotImport{}, err
	}
	defer func() { _ = tx.Rollback() }()
	v, err := checkPostgresMavenTakeover(ctx, tx, p, key)
	if err != nil {
		return v, err
	}
	if !v.Writable() {
		v.TakeoverKey, v.TakeoverActor, v.TakenOverAt = key, p.Actor, time.Now().UTC()
		if _, err = tx.ExecContext(ctx, `UPDATE native_maven_snapshot_imports SET takeover_key=$3,takeover_actor=$4,taken_over_at=$5 WHERE repository_id=$1 AND coordinate=$2`, p.RepositoryID, p.Coordinate, key, p.Actor, v.TakenOverAt); err != nil {
			return v, err
		}
		var name string
		if err = tx.QueryRowContext(ctx, `SELECT name FROM hosted_repositories WHERE id=$1`, p.RepositoryID).Scan(&name); err != nil {
			return v, err
		}
		if err = insertAudit(ctx, tx, AuditRecord{Repository: name, GroupName: name, Actor: p.Actor, Operation: "maven.snapshot.takeover", Format: "maven", Resource: p.Coordinate, Outcome: AuditResolved, OccurredAt: v.TakenOverAt, Evidence: map[string]string{"manifestDigest": p.ManifestDigest, "sourceId": p.SourceID, "repositoryId": p.RepositoryID, "targetBinding": p.TargetBinding, "idempotencyKey": key, "importSessionId": v.SessionID}}); err != nil {
			return v, err
		}
	}
	return v, tx.Commit()
}
func (s *PostgresStore) MavenSnapshotImportPathReserved(ctx context.Context, repo, coordinate, path string) (bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.source_timestamp,a.source_build_number,a.created_at,a.build_number FROM native_maven_artifacts a LEFT JOIN native_maven_publish_sessions s ON s.id=a.id WHERE a.repository_id=$1 AND a.coordinate=$2 AND (a.source_timestamp<>'' OR s.state='committed')`, repo, coordinate)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		a := MavenArtifact{Coordinate: coordinate}
		if err = rows.Scan(&a.SourceTimestamp, &a.SourceBuildNumber, &a.CreatedAt, &a.BuildNumber); err != nil {
			return false, err
		}
		if mavenArchivedNamespaceMatches(a, path) {
			return true, nil
		}
	}
	return false, rows.Err()
}
func (s *PostgresStore) FindMavenSnapshotDeployment(ctx context.Context, repo, coordinate, publisher, stamp string, build int) (MavenPublishSession, error) {
	return scanMavenDeployment(s.db.QueryRowContext(ctx, `SELECT `+mavenDeploymentColumns+` FROM native_maven_publish_sessions WHERE repository_id=$1 AND coordinate=$2 AND publisher=$3 AND client_timestamp=$4 AND client_build_number=$5`, repo, coordinate, publisher, stamp, build))
}
func (s *PostgresStore) CreateMavenSnapshotDeployment(ctx context.Context, v MavenPublishSession) (MavenPublishSession, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return v, err
	}
	defer func() { _ = tx.Rollback() }()
	i, err := lockMavenTakeover(ctx, tx, v.RepositoryID, v.Coordinate)
	if err != nil {
		return v, err
	}
	if !i.Writable() || v.ID == i.SessionID || !validMavenSnapshotReceipt(v) || v.State != "open" || !v.ExpiresAt.After(time.Now()) {
		return v, ErrDisabled
	}
	objects, _ := json.Marshal(v.Objects)
	_, err = tx.ExecContext(ctx, `INSERT INTO native_maven_publish_sessions(id,repository_id,coordinate,publisher,pom_object,state,expires_at,objects,client_timestamp,client_build_number) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, v.ID, v.RepositoryID, v.Coordinate, v.Publisher, v.PomObject, v.State, v.ExpiresAt, objects, v.ClientTimestamp, v.ClientBuildNumber)
	if isUnique(err) {
		return v, ErrNameExists
	}
	if err != nil {
		return v, err
	}
	return v, tx.Commit()
}
func (s *PostgresStore) CompleteMavenSnapshotDeployment(ctx context.Context, id, fingerprint, mainExtension string) (MavenSnapshotImport, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MavenSnapshotImport{}, err
	}
	defer func() { _ = tx.Rollback() }()
	initial, err := scanMavenDeployment(tx.QueryRowContext(ctx, `SELECT `+mavenDeploymentColumns+` FROM native_maven_publish_sessions WHERE id=$1`, id))
	if err != nil {
		return MavenSnapshotImport{}, err
	}
	v, err := lockMavenTakeover(ctx, tx, initial.RepositoryID, initial.Coordinate)
	if err != nil {
		return v, err
	}
	session, err := scanMavenDeployment(tx.QueryRowContext(ctx, `SELECT `+mavenDeploymentColumns+` FROM native_maven_publish_sessions WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return v, err
	}
	if session.State != "open" && session.State != "committed" || session.State == "open" && !session.ExpiresAt.After(time.Now()) {
		return v, ErrMavenSnapshotTakeoverNotReady
	}
	uploads := map[string]string{}
	rows, err := tx.QueryContext(ctx, `SELECT object_name,object_key FROM native_maven_publish_uploads WHERE session_id=$1`, id)
	if err != nil {
		return v, err
	}
	for rows.Next() {
		var name, key string
		if err = rows.Scan(&name, &key); err != nil {
			_ = rows.Close()
			return v, err
		}
		uploads[name] = key
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return v, err
	}
	if MavenSnapshotDeploymentFingerprint(session, uploads) != fingerprint {
		return v, ErrMavenSnapshotTakeoverNotReady
	}
	var a MavenArtifact
	err = tx.QueryRowContext(ctx, `SELECT id::text,repository_id::text,coordinate,digest,state,created_at,build_number,source_timestamp,source_build_number FROM native_maven_artifacts WHERE id=$1 FOR SHARE`, id).Scan(&a.ID, &a.RepositoryID, &a.Coordinate, &a.Digest, &a.State, &a.CreatedAt, &a.BuildNumber, &a.SourceTimestamp, &a.SourceBuildNumber)
	if err != nil {
		return v, err
	}
	aliases, err := mavenSnapshotDeploymentAliases(v, session, a, mainExtension)
	if err != nil {
		return v, err
	}
	artifacts, err := lockMavenSnapshotArtifactsTx(ctx, tx, v.RepositoryID, v.Coordinate)
	if err != nil {
		return v, err
	}
	aliasAssets := make([]MavenAsset, 0, len(aliases))
	for _, target := range aliases {
		var asset MavenAsset
		err = tx.QueryRowContext(ctx, `SELECT repository_id::text,path,object_key,digest,size FROM native_maven_assets WHERE repository_id=$1 AND path=$2`, v.RepositoryID, target).Scan(&asset.RepositoryID, &asset.Path, &asset.ObjectKey, &asset.Digest, &asset.Size)
		if errors.Is(err, sql.ErrNoRows) {
			return v, ErrMavenSnapshotTakeoverNotReady
		}
		if err != nil {
			return v, err
		}
		visible := false
		for _, owner := range artifacts {
			if owner.State == "visible" && mavenAssetBelongsToArtifactBuild(asset, owner) {
				visible = true
			}
		}
		if !visible {
			return v, ErrMavenSnapshotTakeoverNotReady
		}
		aliasAssets = append(aliasAssets, asset)
	}
	if err = lockMavenSnapshotAssetIntentsTx(ctx, tx, aliasAssets); err != nil {
		return v, err
	}

	keys := make([]string, 0, len(uploads))
	for _, key := range uploads {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		var live bool
		if err = tx.QueryRowContext(ctx, `SELECT claimed_at IS NULL AND deleted_at IS NULL FROM native_maven_object_intents WHERE object_key=$1 FOR UPDATE`, key).Scan(&live); err != nil {
			return v, err
		}
		if !live {
			return v, ErrMavenSnapshotTakeoverNotReady
		}
	}
	for _, object := range session.Objects {
		path := mavenSnapshotTimestampedPath(mavenArtifactPathPrefix(session.Coordinate)+object.Name, session.Coordinate, a.CreatedAt, a.BuildNumber)
		var matches bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM native_maven_assets a WHERE repository_id=$1 AND path=$2 AND object_key=$3 AND digest=$4 AND size=$5 AND EXISTS(SELECT 1 FROM native_maven_object_references r WHERE r.object_key=a.object_key))`, session.RepositoryID, path, uploads[object.Name], object.Digest, object.Size).Scan(&matches)
		if err != nil {
			return v, err
		}
		if !matches {
			return v, ErrMavenSnapshotTakeoverNotReady
		}
	}
	if session.State != "committed" {
		if a.BuildNumber > v.CurrentBuildNumber {
			v.CurrentBuildNumber, v.CurrentAliases = a.BuildNumber, aliases
			raw, _ := json.Marshal(aliases)
			if _, err = tx.ExecContext(ctx, `UPDATE native_maven_snapshot_imports SET current_build_number=$3,current_aliases=$4 WHERE repository_id=$1 AND coordinate=$2`, v.RepositoryID, v.Coordinate, a.BuildNumber, raw); err != nil {
				return v, err
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE native_maven_publish_sessions SET state='committed' WHERE id=$1`, id); err != nil {
			return v, err
		}
		var name string
		if err = tx.QueryRowContext(ctx, `SELECT name FROM hosted_repositories WHERE id=$1`, v.RepositoryID).Scan(&name); err != nil {
			return v, err
		}
		if err = insertAudit(ctx, tx, AuditRecord{Repository: name, Actor: session.Publisher, Operation: "maven.snapshot.deploy.complete", Format: "maven", Resource: v.Coordinate, Outcome: AuditResolved, OccurredAt: time.Now().UTC(), Evidence: map[string]string{"sessionId": id, "receipt": session.ClientTimestamp, "fingerprint": fingerprint}}); err != nil {
			return v, err
		}
	}
	return v, tx.Commit()
}

func lockMavenSnapshotArtifactsTx(ctx context.Context, tx *sql.Tx, repo, coordinate string) ([]MavenArtifact, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id::text,repository_id::text,coordinate,digest,state,created_at,build_number,source_timestamp,source_build_number FROM native_maven_artifacts WHERE repository_id=$1 AND coordinate=$2 ORDER BY id FOR SHARE`, repo, coordinate)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []MavenArtifact
	for rows.Next() {
		var a MavenArtifact
		if err = rows.Scan(&a.ID, &a.RepositoryID, &a.Coordinate, &a.Digest, &a.State, &a.CreatedAt, &a.BuildNumber, &a.SourceTimestamp, &a.SourceBuildNumber); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func lockMavenSnapshotAssetIntentsTx(ctx context.Context, tx *sql.Tx, assets []MavenAsset) error {
	keys := map[string]bool{}
	for _, a := range assets {
		keys[a.ObjectKey] = true
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		var live bool
		if err := tx.QueryRowContext(ctx, `SELECT claimed_at IS NULL AND deleted_at IS NULL FROM native_maven_object_intents WHERE object_key=$1 FOR UPDATE`, key).Scan(&live); err != nil {
			return err
		}
		if !live {
			return ErrMavenSnapshotTakeoverNotReady
		}
	}
	return nil
}

func checkMavenSnapshotReadPolicyTx(ctx context.Context, tx *sql.Tx, repo, coordinate string) error {
	var blocked bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM repository_quarantine_read_policies p WHERE repository_id=$1 AND enabled) AND EXISTS(SELECT 1 FROM artifact_quarantines q JOIN native_maven_artifacts a ON a.repository_id=q.repository_id AND a.coordinate=q.coordinate AND a.digest=q.digest WHERE q.repository_id=$1 AND q.format='maven' AND q.coordinate=$2 AND q.state='quarantined')`, repo, coordinate).Scan(&blocked)
	if err == nil && blocked {
		return ErrArtifactQuarantined
	}
	return err
}
