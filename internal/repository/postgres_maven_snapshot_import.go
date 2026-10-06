package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const mavenImportColumns = `repository_id::text,coordinate,target_id,target_binding,source_id,manifest_digest,plan_digest,session_id::text,actor,state,metadata,aliases,created_at,takeover_key,takeover_actor,taken_over_at,current_build_number,current_aliases`

func scanMavenImport(row interface{ Scan(...any) error }) (v MavenSnapshotImport, err error) {
	var metadata, aliases, current []byte
	var takenOver sql.NullTime
	err = row.Scan(&v.RepositoryID, &v.Coordinate, &v.TargetID, &v.TargetBinding, &v.SourceID, &v.ManifestDigest, &v.PlanDigest, &v.SessionID, &v.Actor, &v.State, &metadata, &aliases, &v.CreatedAt, &v.TakeoverKey, &v.TakeoverActor, &takenOver, &v.CurrentBuildNumber, &current)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	if err != nil {
		return v, err
	}
	if len(metadata) > 0 {
		err = json.Unmarshal(metadata, &v.Metadata)
	}
	if err == nil {
		err = json.Unmarshal(aliases, &v.Aliases)
	}
	if err == nil {
		err = json.Unmarshal(current, &v.CurrentAliases)
	}
	if takenOver.Valid {
		v.TakenOverAt = takenOver.Time
	}
	return v, err
}
func (s *PostgresStore) GetMavenSnapshotImport(ctx context.Context, repo, coordinate string) (MavenSnapshotImport, error) {
	return scanMavenImport(s.db.QueryRowContext(ctx, `SELECT `+mavenImportColumns+` FROM native_maven_snapshot_imports WHERE repository_id=$1 AND coordinate=$2`, repo, coordinate))
}
func lockMavenCoordinate(ctx context.Context, tx *sql.Tx, repo, coordinate string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, repo+":"+coordinate)
	return err
}

// Capacity triggers lock the repository quota even when its value is zero.
// Acquire it before artifact or CAS intent locks in every Maven transaction
// that participates in publication visibility. The repository lock also
// fences insertion of a previously absent quota by the quota setter.
func lockMavenCapacityTx(ctx context.Context, tx *sql.Tx, repositories ...string) error {
	repos := append([]string(nil), repositories...)
	sort.Strings(repos)
	for i, repo := range repos {
		if i > 0 && repo == repos[i-1] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `SELECT id FROM hosted_repositories WHERE id=$1 FOR SHARE`, repo); err != nil {
			return err
		}
	}
	for i, repo := range repos {
		if i > 0 && repo == repos[i-1] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `SELECT repository_id FROM repository_capacity_quotas WHERE repository_id=$1 FOR UPDATE`, repo); err != nil {
			return err
		}
	}
	return nil
}

func lockMavenArtifactIntentsTx(ctx context.Context, tx *sql.Tx, repo, prefix string) error {
	_, err := tx.ExecContext(ctx, `SELECT i.object_key FROM native_maven_object_intents i WHERE i.object_key IN (SELECT a.object_key FROM native_maven_assets a WHERE a.repository_id=$1 AND left(a.path,length($2))=$2 AND (right($2,1)='/' OR substring(a.path,length($2)+1,1) IN ('.','-'))) ORDER BY i.object_key FOR UPDATE`, repo, prefix)
	return err
}

func lockMavenAssetIntentRowsTx(ctx context.Context, tx *sql.Tx, assets []MavenAsset) error {
	ordered := append([]MavenAsset(nil), assets...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ObjectKey < ordered[j].ObjectKey })
	for _, asset := range ordered {
		if _, err := tx.ExecContext(ctx, `SELECT object_key FROM native_maven_object_intents WHERE object_key=$1 FOR UPDATE`, asset.ObjectKey); err != nil {
			return err
		}
	}
	return nil
}
func rejectMavenImportPublication(ctx context.Context, tx *sql.Tx, repo, coordinate string) error {
	var reserved bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM native_maven_snapshot_imports WHERE repository_id=$1 AND coordinate=$2)`, repo, coordinate).Scan(&reserved)
	if err != nil {
		return err
	}
	if reserved {
		return ErrNameExists
	}
	return nil
}
func checkPostgresMavenImport(ctx context.Context, tx *sql.Tx, p MavenSnapshotImportPlan) (MavenSnapshotImport, error) {
	if !validMavenImportPlan(p) {
		return MavenSnapshotImport{}, ErrDisabled
	}
	if err := lockMavenCoordinate(ctx, tx, p.RepositoryID, p.Coordinate); err != nil {
		return MavenSnapshotImport{}, err
	}
	var repoID string
	err := tx.QueryRowContext(ctx, `SELECT id::text FROM hosted_repositories WHERE id=$1 AND format='maven' AND repo_type='hosted' AND state='active' FOR SHARE`, p.RepositoryID).Scan(&repoID)
	if errors.Is(err, sql.ErrNoRows) {
		return MavenSnapshotImport{}, ErrNotFound
	}
	if err != nil {
		return MavenSnapshotImport{}, err
	}
	// Retention updates lock the Repository first. Hold that same row through
	// commit, including when the policy row has not been created yet.
	var retention bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM repository_retention_policies WHERE repository_id=$1 AND enabled)`, p.RepositoryID).Scan(&retention)
	if err != nil {
		return MavenSnapshotImport{}, err
	}
	if retention {
		return MavenSnapshotImport{}, ErrMavenSnapshotImportRetention
	}

	v, err := scanMavenImport(tx.QueryRowContext(ctx, `SELECT `+mavenImportColumns+` FROM native_maven_snapshot_imports WHERE repository_id=$1 AND coordinate=$2 FOR UPDATE`, p.RepositoryID, p.Coordinate))
	if err == nil {
		if v.Writable() {
			return v, ErrMavenSnapshotTakenOver
		}
		if v.PlanDigest != mavenImportDigest(p) {
			return v, ErrIdempotencyConflict
		}
		return v, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return v, err
	}
	var conflict bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM native_maven_artifacts WHERE repository_id=$1 AND coordinate=$2) OR EXISTS(SELECT 1 FROM native_maven_publish_sessions WHERE repository_id=$1 AND coordinate=$2) OR EXISTS(SELECT 1 FROM native_maven_assets WHERE repository_id=$1 AND left(path,length($3))=$3)`, p.RepositoryID, p.Coordinate, mavenArtifactPathPrefix(p.Coordinate)).Scan(&conflict)
	if err != nil {
		return v, err
	}
	if conflict {
		return v, ErrNameExists
	}
	return MavenSnapshotImport{}, nil
}
func (s *PostgresStore) CheckMavenSnapshotImport(ctx context.Context, p MavenSnapshotImportPlan) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = checkPostgresMavenImport(ctx, tx, p)
	return err
}
func (s *PostgresStore) BeginMavenSnapshotImport(ctx context.Context, p MavenSnapshotImportPlan) (MavenSnapshotImport, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MavenSnapshotImport{}, err
	}
	defer func() { _ = tx.Rollback() }()
	v, err := checkPostgresMavenImport(ctx, tx, p)
	if err != nil {
		return v, err
	}
	if v.SessionID != "" {
		if v.State == "staged" {
			_, err = tx.ExecContext(ctx, `UPDATE native_maven_publish_sessions SET state='open',expires_at=now()+interval '24 hours' WHERE id=$1`, v.SessionID)
		}
	} else {
		v = MavenSnapshotImport{RepositoryID: p.RepositoryID, Coordinate: p.Coordinate, TargetID: p.TargetID, TargetBinding: p.TargetBinding, SourceID: p.SourceID, ManifestDigest: p.ManifestDigest, PlanDigest: mavenImportDigest(p), SessionID: uuid.NewString(), Actor: p.Actor, State: "staged", Metadata: p.Metadata, Aliases: p.Aliases, CreatedAt: time.Now().UTC()}
		objects := []MavenDeclaredObject{}
		for _, a := range p.Assets {
			objects = append(objects, MavenDeclaredObject{Name: strings.TrimPrefix(a.Path, mavenArtifactPathPrefix(p.Coordinate)), Digest: a.Digest, Size: a.Size})
		}
		raw, _ := json.Marshal(objects)
		_, err = tx.ExecContext(ctx, `INSERT INTO native_maven_publish_sessions(id,repository_id,coordinate,publisher,pom_object,state,expires_at,objects) VALUES($1,$2,$3,$4,'snapshot-import','open',now()+interval '24 hours',$5)`, v.SessionID, p.RepositoryID, p.Coordinate, p.Actor, raw)
		if err == nil {
			metadata, _ := json.Marshal(p.Metadata)
			aliases, _ := json.Marshal(p.Aliases)
			_, err = tx.ExecContext(ctx, `INSERT INTO native_maven_snapshot_imports(repository_id,coordinate,target_id,target_binding,source_id,manifest_digest,plan_digest,session_id,actor,state,metadata,aliases) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'staged',$10,$11)`, p.RepositoryID, p.Coordinate, p.TargetID, p.TargetBinding, p.SourceID, p.ManifestDigest, v.PlanDigest, v.SessionID, p.Actor, metadata, aliases)
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	return v, err
}
func (s *PostgresStore) CommitMavenSnapshotImport(ctx context.Context, p MavenSnapshotImportPlan) (MavenSnapshotImport, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MavenSnapshotImport{}, err
	}
	defer func() { _ = tx.Rollback() }()
	v, err := checkPostgresMavenImport(ctx, tx, p)
	if err != nil {
		return v, err
	}
	if v.SessionID == "" {
		return v, ErrNotFound
	}
	if v.State == "committed" {
		return v, nil
	}
	// Lock session before intents, the same order used by the collector. A
	// resumed session protects unclaimed objects; active claims reject commit.
	if _, err = tx.ExecContext(ctx, `SELECT id FROM native_maven_publish_sessions WHERE id=$1 FOR UPDATE`, v.SessionID); err != nil {
		return v, err
	}
	var uploaded int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM native_maven_publish_uploads WHERE session_id=$1`, v.SessionID).Scan(&uploaded); err != nil {
		return v, err
	}
	if uploaded != len(p.Assets) {
		return v, ErrDisabled
	}
	for _, a := range p.Assets {
		var key string
		err = tx.QueryRowContext(ctx, `SELECT object_key FROM native_maven_publish_uploads WHERE session_id=$1 AND object_name=$2`, v.SessionID, strings.TrimPrefix(a.Path, mavenArtifactPathPrefix(p.Coordinate))).Scan(&key)
		if err != nil {
			return v, err
		}
		if key != a.ObjectKey {
			return v, ErrDisabled
		}
	}
	if err = lockMavenCapacityTx(ctx, tx, p.RepositoryID); err != nil {
		return v, err
	}
	if err = lockMavenSnapshotAssetIntentsTx(ctx, tx, p.Assets); err != nil {
		return v, err
	}
	for i, a := range p.Artifacts {
		id := uuid.NewSHA1(uuid.MustParse(v.SessionID), []byte(a.SourceTimestamp+":"+strconv.Itoa(a.SourceBuildNumber)+":"+a.Digest)).String()
		created, _ := time.Parse("20060102.150405", a.SourceTimestamp)
		_, err = tx.ExecContext(ctx, `INSERT INTO native_maven_artifacts(id,repository_id,coordinate,digest,state,created_at,build_number,source_timestamp,source_build_number) VALUES($1,$2,$3,$4,'visible',$5,$6,$7,$8)`, id, p.RepositoryID, p.Coordinate, a.Digest, created, i+1, a.SourceTimestamp, a.SourceBuildNumber)
		if err != nil {
			return v, err
		}
	}
	for _, a := range p.Assets {
		var claimed, deleted bool
		if err = tx.QueryRowContext(ctx, `SELECT claimed_at IS NOT NULL,deleted_at IS NOT NULL FROM native_maven_object_intents WHERE object_key=$1 FOR UPDATE`, a.ObjectKey).Scan(&claimed, &deleted); err != nil {
			return v, err
		}
		if claimed || deleted {
			return v, ErrDisabled
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO native_maven_assets(repository_id,path,object_key,digest,size) VALUES($1,$2,$3,$4,$5)`, p.RepositoryID, a.Path, a.ObjectKey, a.Digest, a.Size)
		if IsQuotaExceeded(err) {
			return v, ErrQuotaExceeded
		}
		if err != nil {
			return v, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO native_maven_object_references(object_key,repository_id) VALUES($1,$2) ON CONFLICT(object_key) DO NOTHING`, a.ObjectKey, p.RepositoryID); err != nil {
			return v, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE native_maven_publish_sessions SET state='committed' WHERE id=$1`, v.SessionID); err != nil {
		return v, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE native_maven_snapshot_imports SET state='committed' WHERE repository_id=$1 AND coordinate=$2`, p.RepositoryID, p.Coordinate); err != nil {
		return v, err
	}
	var repoName string
	if err = tx.QueryRowContext(ctx, `SELECT name FROM hosted_repositories WHERE id=$1`, p.RepositoryID).Scan(&repoName); err != nil {
		return v, err
	}
	if err = insertAudit(ctx, tx, AuditRecord{Repository: repoName, GroupName: repoName, Actor: p.Actor, Operation: "maven.snapshot.import", Format: "maven", Resource: p.Coordinate, Outcome: AuditResolved, OccurredAt: time.Now().UTC(), Evidence: map[string]string{"manifestDigest": p.ManifestDigest, "sourceId": p.SourceID, "sessionId": v.SessionID, "repositoryId": p.RepositoryID, "targetBinding": p.TargetBinding}}); err != nil {
		return v, err
	}
	if err = tx.Commit(); err != nil {
		return v, err
	}
	v.State = "committed"
	return v, nil
}

// MavenImportDatabaseIdentity identifies the actual PG cluster/database,
// excluding connection credentials and transient network addresses.
func (s *PostgresStore) MavenImportDatabaseIdentity(ctx context.Context) (string, error) {
	var database, system string
	err := s.db.QueryRowContext(ctx, `SELECT current_database(),(pg_control_system()).system_identifier::text`).Scan(&database, &system)
	if err != nil {
		return "", err
	}
	return database + "\x00" + system, nil
}
