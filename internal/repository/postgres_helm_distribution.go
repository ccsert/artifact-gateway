package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	dist "github.com/ccsert/artifact-gateway/internal/helmdistribution"
	"github.com/google/uuid"
)

const helmReplicationColumns = `id::text,plan_digest,source_repository_id::text,target_repository_id::text,actor,actor_kind,credential_id,version::text,state,lease_owner,lease_token,lease_fence,lease_until,created_at,updated_at`

type helmReplicationQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadHelmReplication(ctx context.Context, q helmReplicationQuery, id string, lock bool) (dist.Job, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	var j dist.Job
	var until sql.NullTime
	err := q.QueryRowContext(ctx, `SELECT `+helmReplicationColumns+` FROM native_helm_replication_jobs WHERE id::text=$1`+suffix, id).Scan(&j.ID, &j.PlanDigest, &j.SourceRepositoryID, &j.TargetRepositoryID, &j.Actor, &j.ActorKind, &j.CredentialID, &j.Version, &j.State, &j.Lease.Owner, &j.Lease.Token, &j.Lease.Fence, &until, &j.CreatedAt, &j.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return j, dist.ErrNotFound
	}
	if err != nil {
		return j, err
	}
	j.Lease.JobID = j.ID
	if until.Valid {
		j.Lease.Until = until.Time
	}
	rows, err := q.QueryContext(ctx, `SELECT item_index,name,version,digest,size,provenance_digest,provenance_size,source_governance_version::text,state,attempts,code,COALESCE(target_session_id::text,''),result,completed_at FROM native_helm_replication_items WHERE job_id::text=$1 ORDER BY item_index`, id)
	if err != nil {
		return j, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var i dist.Item
		var done sql.NullTime
		if err = rows.Scan(&i.Index, &i.Name, &i.Version, &i.Digest, &i.Size, &i.ProvenanceDigest, &i.ProvenanceSize, &i.SourceGovernanceVersion, &i.State, &i.Attempts, &i.Code, &i.TargetSessionID, &i.Result, &done); err != nil {
			return j, err
		}
		if done.Valid {
			i.CompletedAt = &done.Time
		}
		j.Items = append(j.Items, i)
	}
	return j, rows.Err()
}
func (s *PostgresStore) helmReplicationTransaction(ctx context.Context, lease dist.Lease) (*sql.Tx, dist.Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, dist.Job{}, err
	}
	j, err := loadHelmReplication(ctx, tx, lease.JobID, true)
	if err == nil && !helmReplicationLease(j, lease) {
		err = dist.ErrLeaseLost
	}
	if err != nil {
		_ = tx.Rollback()
		return nil, j, err
	}
	return tx, j, nil
}
func helmReplicationActorTx(ctx context.Context, tx *sql.Tx, j dist.Job, a *dist.Authority) error {
	if !helmReplicationActorValid(j) {
		return dist.ErrAccessDenied
	}
	a.Actor = j.Actor
	a.Kind = j.ActorKind
	switch j.ActorKind {
	case "static_admin", "static_resolver":
		a.Active = true
	case "local_session", "oidc":
		var state string
		err := tx.QueryRowContext(ctx, `SELECT role,state,must_change_password FROM users WHERE lower(name)=lower($1) FOR SHARE`, strings.TrimPrefix(j.Actor, "user:")).Scan(&a.Role, &state, &a.MustChangePassword)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		a.Active = state == string(UserActive)
	case "api_key", "service_account_credential":
		credential := strings.TrimPrefix(j.Actor, "api-key:")
		account := ""
		if j.ActorKind == "service_account_credential" {
			account = strings.TrimPrefix(j.Actor, "service-account:")
			credential = j.CredentialID
			var state string
			err := tx.QueryRowContext(ctx, `SELECT state FROM service_accounts WHERE id::text=$1 FOR SHARE`, account).Scan(&state)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			if state != string(ServiceAccountActive) {
				return nil
			}
		}
		var roles []byte
		var revoked, expires sql.NullTime
		err := tx.QueryRowContext(ctx, `SELECT array_to_json(roles),revoked_at,expires_at FROM api_keys WHERE id::text=$1 AND COALESCE(service_account_id::text,'')=$2 FOR SHARE`, credential, account).Scan(&roles, &revoked, &expires)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var values []string
		if err = json.Unmarshal(roles, &values); err != nil {
			return err
		}
		a.Role = helmReplicationKeyRole(values)
		a.Active = !revoked.Valid && (!expires.Valid || expires.Time.After(time.Now()))
		if expires.Valid {
			a.ValidUntil = expires.Time
		}
	}
	return nil
}
func helmReplicationAuthorityTx(ctx context.Context, tx *sql.Tx, j dist.Job) (dist.Authority, error) {
	a := dist.Authority{}
	ids := []string{j.SourceRepositoryID, j.TargetRepositoryID}
	sort.Strings(ids)
	for _, id := range ids {
		var repo HostedRepository
		err := tx.QueryRowContext(ctx, `SELECT id::text,name,format,repo_type,state FROM hosted_repositories WHERE id::text=$1 FOR UPDATE`, id).Scan(&repo.ID, &repo.Name, &repo.Format, &repo.Type, &repo.State)
		if errors.Is(err, sql.ErrNoRows) {
			return a, dist.ErrNotFound
		}
		if err != nil {
			return a, err
		}
		if repo.Format != FormatHelm || repo.Type != RepositoryTypeHosted || repo.State != RepositoryActive {
			return a, dist.ErrNotFound
		}
		// Replacements and individual grant writes lock this same row. Creating
		// the default row also closes the otherwise-unlocked absent-set case.
		if _, err = tx.ExecContext(ctx, `INSERT INTO repository_grant_sets(repository_id,version) VALUES($1,1) ON CONFLICT DO NOTHING`, id); err != nil {
			return a, err
		}
		var version string
		if err = tx.QueryRowContext(ctx, `SELECT version::text FROM repository_grant_sets WHERE repository_id::text=$1 FOR SHARE`, id).Scan(&version); err != nil {
			return a, err
		}
		set, err := loadRepositoryGrants(ctx, tx, id, version)
		if err != nil {
			return a, err
		}
		authority := helmReplicationGrantSet(repo, set)
		if id == j.SourceRepositoryID {
			a.Source = authority
		} else {
			a.Target = authority
		}
	}
	if err := helmReplicationActorTx(ctx, tx, j, &a); err != nil {
		return a, err
	}
	return a, nil
}
func helmReplicationSourceTx(ctx context.Context, tx *sql.Tx, j dist.Job, index int) (HelmPublication, error) {
	if index < 0 || index >= len(j.Items) {
		return HelmPublication{}, dist.ErrInvalid
	}
	i := j.Items[index]
	p, err := scanHelmPublication(tx.QueryRowContext(ctx, `SELECT `+helmPublicationColumns+` FROM native_helm_publications WHERE repository_id::text=$1 AND name=$2 AND version=$3 FOR SHARE`, j.SourceRepositoryID, i.Name, i.Version))
	if errors.Is(err, ErrNotFound) {
		return p, dist.ErrSourceChanged
	}
	if err != nil {
		return p, err
	}
	return p, helmReplicationSource(p, i)
}
func saveHelmReplicationItem(ctx context.Context, tx *sql.Tx, j dist.Job, index int) error {
	i := j.Items[index]
	_, err := tx.ExecContext(ctx, `UPDATE native_helm_replication_items SET state=$3,attempts=$4,code=$5,target_session_id=NULLIF($6,'')::uuid,result=$7,completed_at=$8 WHERE job_id::text=$1 AND item_index=$2`, j.ID, index, i.State, i.Attempts, i.Code, i.TargetSessionID, i.Result, i.CompletedAt)
	return err
}
func saveHelmReplicationProgress(ctx context.Context, tx *sql.Tx, j dist.Job, lease dist.Lease, validUntil time.Time) error {
	var expiry any
	if !validUntil.IsZero() {
		expiry = validUntil
	}
	result, err := tx.ExecContext(ctx, `UPDATE native_helm_replication_jobs SET version=version+1,updated_at=clock_timestamp() WHERE id::text=$1 AND state='running' AND lease_owner=$2 AND lease_token=$3 AND lease_fence=$4 AND lease_until>clock_timestamp() AND ($5::timestamptz IS NULL OR $5>clock_timestamp())`, j.ID, lease.Owner, lease.Token, lease.Fence, expiry)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		if !validUntil.IsZero() && !validUntil.After(time.Now()) {
			return dist.ErrAccessDenied
		}
		return dist.ErrLeaseLost
	}
	return nil
}
func (s *PostgresStore) CreateHelmReplication(ctx context.Context, j dist.Job, check dist.Check) (dist.Job, bool, error) {
	if !dist.ValidNewJob(j) {
		return dist.Job{}, false, dist.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return dist.Job{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "helm-replication-create:"+j.ID); err != nil {
		return dist.Job{}, false, err
	}
	a, err := helmReplicationAuthorityTx(ctx, tx, j)
	if err != nil {
		return dist.Job{}, false, err
	}
	if err = helmReplicationCheck(ctx, check, a); err != nil {
		return dist.Job{}, false, err
	}
	prior, err := loadHelmReplication(ctx, tx, j.ID, false)
	if err == nil {
		if prior.PlanDigest != j.PlanDigest || prior.Actor != j.Actor || prior.ActorKind != j.ActorKind || prior.CredentialID != j.CredentialID {
			return dist.Job{}, false, dist.ErrConflict
		}
		return prior, true, tx.Commit()
	}
	if !errors.Is(err, dist.ErrNotFound) {
		return dist.Job{}, false, err
	}
	extra := int64(0)
	for i, item := range j.Items {
		if _, err = helmReplicationSourceTx(ctx, tx, j, i); err != nil {
			return dist.Job{}, false, err
		}
		var exists bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM native_helm_publications WHERE repository_id::text=$1 AND name=$2 AND version=$3)`, j.TargetRepositoryID, item.Name, item.Version).Scan(&exists); err != nil {
			return dist.Job{}, false, err
		}
		if !exists {
			extra += item.Size + item.ProvenanceSize
		}
	}
	if err = helmQuota(ctx, tx, j.TargetRepositoryID, extra); err != nil {
		return dist.Job{}, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO native_helm_replication_jobs(id,plan_digest,source_repository_id,target_repository_id,actor,actor_kind,credential_id,state) VALUES($1,$2,$3,$4,$5,$6,$7,'queued')`, j.ID, j.PlanDigest, j.SourceRepositoryID, j.TargetRepositoryID, j.Actor, j.ActorKind, j.CredentialID)
	if err != nil {
		return dist.Job{}, false, err
	}
	for _, i := range j.Items {
		if _, err = tx.ExecContext(ctx, `INSERT INTO native_helm_replication_items(job_id,item_index,name,version,digest,size,provenance_digest,provenance_size,source_governance_version,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'pending')`, j.ID, i.Index, i.Name, i.Version, i.Digest, i.Size, i.ProvenanceDigest, i.ProvenanceSize, i.SourceGovernanceVersion); err != nil {
			return dist.Job{}, false, err
		}
	}
	j, err = loadHelmReplication(ctx, tx, j.ID, false)
	if err != nil {
		return j, false, err
	}
	if err = insertAudit(ctx, tx, helmReplicationAudit(j, a.Target.Name, "create", 202, nil)); err != nil {
		return dist.Job{}, false, err
	}
	return j, false, tx.Commit()
}
func (s *PostgresStore) GetHelmReplication(ctx context.Context, id string) (dist.Job, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return dist.Job{}, err
	}
	defer func() { _ = tx.Rollback() }()
	j, err := loadHelmReplication(ctx, tx, id, false)
	if err != nil {
		return j, err
	}
	return j, tx.Commit()
}
func (s *PostgresStore) RetryHelmReplication(ctx context.Context, id, expected string, indexes []int, key string, check dist.Check) (dist.Job, bool, error) {
	digest, err := dist.RetryDigest(expected, indexes)
	if err != nil || key == "" || len(key) > 200 || strings.ContainsAny(key, "\x00\r\n") {
		return dist.Job{}, false, dist.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return dist.Job{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	j, err := loadHelmReplication(ctx, tx, id, true)
	if err != nil {
		return j, false, err
	}
	a, err := helmReplicationAuthorityTx(ctx, tx, j)
	if err != nil {
		return dist.Job{}, false, err
	}
	if err = helmReplicationCheck(ctx, check, a); err != nil {
		return dist.Job{}, false, err
	}
	var prior string
	err = tx.QueryRowContext(ctx, `SELECT payload_digest FROM native_helm_replication_retries WHERE job_id::text=$1 AND request_key=$2`, id, key).Scan(&prior)
	if err == nil {
		if prior != digest {
			return dist.Job{}, false, dist.ErrConflict
		}
		return j, true, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return dist.Job{}, false, err
	}
	j, err = helmReplicationRetry(j, expected, indexes)
	if err != nil {
		return dist.Job{}, false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE native_helm_replication_jobs SET state='queued',version=version+1,lease_owner='',lease_token='',lease_until=NULL,updated_at=clock_timestamp() WHERE id::text=$1`, id); err != nil {
		return dist.Job{}, false, err
	}
	for _, index := range indexes {
		if err = saveHelmReplicationItem(ctx, tx, j, index); err != nil {
			return dist.Job{}, false, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_helm_replication_retries(job_id,request_key,payload_digest) VALUES($1,$2,$3)`, id, key, digest); err != nil {
		return dist.Job{}, false, err
	}
	if err = insertAudit(ctx, tx, helmReplicationAudit(j, a.Target.Name, "retry", 202, nil)); err != nil {
		return dist.Job{}, false, err
	}
	j, err = loadHelmReplication(ctx, tx, id, false)
	if err != nil {
		return j, false, err
	}
	return j, false, tx.Commit()
}
func (s *PostgresStore) ClaimHelmReplication(ctx context.Context, owner string, d time.Duration) (dist.Job, error) {
	if owner == "" || len(owner) > 512 || !dist.ValidDuration(d) {
		return dist.Job{}, dist.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return dist.Job{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var id string
	err = tx.QueryRowContext(ctx, `SELECT id::text FROM native_helm_replication_jobs WHERE state='queued' OR state='running' AND lease_until<=clock_timestamp() ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return dist.Job{}, dist.ErrNotFound
	}
	if err != nil {
		return dist.Job{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE native_helm_replication_jobs SET state='running',version=version+1,lease_owner=$2,lease_token=$3,lease_fence=lease_fence+1,lease_until=clock_timestamp()+$4::bigint*interval '1 millisecond',updated_at=clock_timestamp() WHERE id::text=$1`, id, owner, uuid.NewString(), d.Milliseconds()); err != nil {
		return dist.Job{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE native_helm_replication_items SET state='pending' WHERE job_id::text=$1 AND state='running'`, id); err != nil {
		return dist.Job{}, err
	}
	j, err := loadHelmReplication(ctx, tx, id, false)
	if err != nil {
		return j, err
	}
	return j, tx.Commit()
}
func (s *PostgresStore) RenewHelmReplication(ctx context.Context, lease dist.Lease, d time.Duration) (dist.Lease, error) {
	if !dist.ValidDuration(d) {
		return dist.Lease{}, dist.ErrInvalid
	}
	var until time.Time
	err := s.db.QueryRowContext(ctx, `UPDATE native_helm_replication_jobs SET lease_until=clock_timestamp()+$5::bigint*interval '1 millisecond' WHERE id::text=$1 AND state='running' AND lease_owner=$2 AND lease_token=$3 AND lease_fence=$4 AND lease_until>clock_timestamp() RETURNING lease_until`, lease.JobID, lease.Owner, lease.Token, lease.Fence, d.Milliseconds()).Scan(&until)
	if errors.Is(err, sql.ErrNoRows) {
		return dist.Lease{}, dist.ErrLeaseLost
	}
	lease.Until = until
	return lease, err
}
func (s *PostgresStore) BindHelmReplicationSession(ctx context.Context, lease dist.Lease, index int, sessionID string, check dist.Check) (dist.Job, error) {
	tx, j, err := s.helmReplicationTransaction(ctx, lease)
	if err != nil {
		return dist.Job{}, err
	}
	defer func() { _ = tx.Rollback() }()
	a, err := helmReplicationAuthorityTx(ctx, tx, j)
	if err != nil {
		return dist.Job{}, err
	}
	if err = helmReplicationCheck(ctx, check, a); err != nil {
		return dist.Job{}, err
	}
	source, err := helmReplicationSourceTx(ctx, tx, j, index)
	if err != nil {
		return dist.Job{}, err
	}
	session, err := scanHelmSession(tx.QueryRowContext(ctx, `SELECT `+helmSessionColumns+` FROM native_helm_publication_sessions WHERE id::text=$1 FOR SHARE`, sessionID))
	if err != nil {
		return dist.Job{}, err
	}
	if session.RepositoryID != j.TargetRepositoryID || session.Name != source.Name || session.Version != source.Version || session.Digest != source.Digest || session.Size != source.Size || session.ProvenanceDigest != source.ProvenanceDigest || session.ProvenanceSize != source.ProvenanceSize || session.SourceVersion != "" {
		return dist.Job{}, dist.ErrTargetBlocked
	}
	item := j.Items[index]
	if item.State != "pending" && item.State != "running" {
		return dist.Job{}, dist.ErrConflict
	}
	if item.State != "running" {
		item.Attempts++
	}
	item.State = "running"
	item.TargetSessionID = session.ID
	j.Items[index] = item
	if err = saveHelmReplicationItem(ctx, tx, j, index); err != nil {
		return dist.Job{}, err
	}
	if err = saveHelmReplicationProgress(ctx, tx, j, lease, a.ValidUntil); err != nil {
		return dist.Job{}, err
	}
	j, err = loadHelmReplication(ctx, tx, j.ID, false)
	if err != nil {
		return j, err
	}
	return j, tx.Commit()
}
func (s *PostgresStore) CompleteHelmReplicationItem(ctx context.Context, lease dist.Lease, index int, proof dist.Commit, check dist.Check) (dist.Job, error) {
	tx, j, err := s.helmReplicationTransaction(ctx, lease)
	if err != nil {
		return dist.Job{}, err
	}
	defer func() { _ = tx.Rollback() }()
	a, err := helmReplicationAuthorityTx(ctx, tx, j)
	if err != nil {
		return dist.Job{}, err
	}
	if err = helmReplicationCheck(ctx, check, a); err != nil {
		return dist.Job{}, err
	}
	source, err := helmReplicationSourceTx(ctx, tx, j, index)
	if err != nil {
		return dist.Job{}, err
	}
	item := j.Items[index]
	if item.State == "succeeded" {
		return j, tx.Commit()
	}
	if item.State != "running" || item.TargetSessionID != proof.SessionID {
		return dist.Job{}, dist.ErrConflict
	}
	session, err := scanHelmSession(tx.QueryRowContext(ctx, `SELECT `+helmSessionColumns+` FROM native_helm_publication_sessions WHERE id::text=$1 FOR UPDATE`, proof.SessionID))
	if err != nil {
		return dist.Job{}, err
	}
	if session.RepositoryID != j.TargetRepositoryID {
		return dist.Job{}, dist.ErrTargetBlocked
	}
	target, err := scanHelmPublication(tx.QueryRowContext(ctx, `SELECT `+helmPublicationColumns+` FROM native_helm_publications WHERE repository_id::text=$1 AND name=$2 AND version=$3 FOR UPDATE`, j.TargetRepositoryID, item.Name, item.Version))
	if err == nil {
		if err = helmReplicationExisting(source, target); err != nil {
			return dist.Job{}, err
		}
		if target.SessionID != session.ID {
			return dist.Job{}, dist.ErrTargetBlocked
		}
		item.Result = "already_present"
	} else if errors.Is(err, ErrNotFound) {
		if err = helmReplicationSession(source, session, j.TargetRepositoryID); err != nil {
			return dist.Job{}, err
		}
		if session.Actor != j.Actor || session.State != "pending" || !session.ExpiresAt.After(time.Now()) || !validHelmSession(session) {
			return dist.Job{}, dist.ErrTargetBlocked
		}
		if err = helmQuota(ctx, tx, j.TargetRepositoryID, 0); err != nil {
			return dist.Job{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE native_helm_publication_sessions SET state='committed',committed_at=clock_timestamp() WHERE id::text=$1`, session.ID); err != nil {
			return dist.Job{}, err
		}
		target, err = scanHelmPublication(tx.QueryRowContext(ctx, `INSERT INTO native_helm_publications(repository_id,name,version,session_id,digest,object_key,size,provenance_digest,provenance_key,provenance_size,metadata_json,chart_yaml,publisher,published_at) SELECT repository_id,name,version,id,digest,object_key,size,provenance_digest,provenance_key,provenance_size,metadata_json,chart_yaml,actor,committed_at FROM native_helm_publication_sessions WHERE id::text=$1 RETURNING `+helmPublicationColumns, session.ID))
		if err != nil {
			return dist.Job{}, err
		}
		if err = insertAudit(ctx, tx, helmPublicationAudit(target, a.Target.Name)); err != nil {
			return dist.Job{}, err
		}
		item.Result = "published"
	} else {
		return dist.Job{}, err
	}
	now := time.Now().UTC()
	item.State = "succeeded"
	item.Code = ""
	item.CompletedAt = &now
	j.Items[index] = item
	j.Version = dist.NextVersion(j.Version)
	if err = saveHelmReplicationItem(ctx, tx, j, index); err != nil {
		return dist.Job{}, err
	}
	if err = saveHelmReplicationProgress(ctx, tx, j, lease, a.ValidUntil); err != nil {
		return dist.Job{}, err
	}
	if err = insertAudit(ctx, tx, helmReplicationAudit(j, a.Target.Name, "item", 200, &item)); err != nil {
		return dist.Job{}, err
	}
	j, err = loadHelmReplication(ctx, tx, j.ID, false)
	if err != nil {
		return j, err
	}
	return j, tx.Commit()
}
func (s *PostgresStore) FailHelmReplicationItem(ctx context.Context, lease dist.Lease, index int, code string) error {
	if !dist.ValidFailureCode(code) {
		return dist.ErrInvalid
	}
	tx, j, err := s.helmReplicationTransaction(ctx, lease)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if index < 0 || index >= len(j.Items) || j.Items[index].State == "succeeded" {
		return dist.ErrConflict
	}
	item := j.Items[index]
	if item.State == "pending" {
		item.Attempts++
	}
	item.State = "failed"
	item.Code = code
	now := time.Now().UTC()
	item.CompletedAt = &now
	j.Items[index] = item
	j.Version = dist.NextVersion(j.Version)
	if err = saveHelmReplicationItem(ctx, tx, j, index); err != nil {
		return err
	}
	if err = saveHelmReplicationProgress(ctx, tx, j, lease, time.Time{}); err != nil {
		return err
	}
	var name string
	if err = tx.QueryRowContext(ctx, `SELECT name FROM hosted_repositories WHERE id::text=$1`, j.TargetRepositoryID).Scan(&name); err != nil {
		return err
	}
	if err = insertAudit(ctx, tx, helmReplicationAudit(j, name, "item", dist.FailureStatus(code), &item)); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *PostgresStore) FinishHelmReplication(ctx context.Context, lease dist.Lease) (dist.Job, error) {
	tx, j, err := s.helmReplicationTransaction(ctx, lease)
	if err != nil {
		return j, err
	}
	defer func() { _ = tx.Rollback() }()
	j.State = "succeeded"
	for _, item := range j.Items {
		if item.State == "pending" || item.State == "running" {
			return dist.Job{}, dist.ErrConflict
		}
		if item.State == "failed" {
			j.State = "failed"
		}
	}
	if err = saveHelmReplicationProgress(ctx, tx, j, lease, time.Time{}); err != nil {
		return dist.Job{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE native_helm_replication_jobs SET state=$2,lease_owner='',lease_token='',lease_until=NULL WHERE id::text=$1`, j.ID, j.State); err != nil {
		return dist.Job{}, err
	}
	var name string
	if err = tx.QueryRowContext(ctx, `SELECT name FROM hosted_repositories WHERE id::text=$1`, j.TargetRepositoryID).Scan(&name); err != nil {
		return dist.Job{}, err
	}
	j.Version = dist.NextVersion(j.Version)
	status := 200
	if j.State == "failed" {
		status = 409
	}
	if err = insertAudit(ctx, tx, helmReplicationAudit(j, name, "complete", status, nil)); err != nil {
		return dist.Job{}, err
	}
	j, err = loadHelmReplication(ctx, tx, j.ID, false)
	if err != nil {
		return j, err
	}
	return j, tx.Commit()
}

func (s *PostgresStore) AuthorizeHelmReplicationItem(ctx context.Context, lease dist.Lease, index int, check dist.Check) error {
	tx, j, err := s.helmReplicationTransaction(ctx, lease)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	a, err := helmReplicationAuthorityTx(ctx, tx, j)
	if err != nil {
		return err
	}
	if err = helmReplicationCheck(ctx, check, a); err != nil {
		return err
	}
	if _, err = helmReplicationSourceTx(ctx, tx, j, index); err != nil {
		return err
	}
	return tx.Commit()
}
