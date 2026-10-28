package pgstore

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	protocol "github.com/ccsert/artifact-gateway/internal/protocol/rubygems"
	"github.com/ccsert/artifact-gateway/internal/repository"
	dist "github.com/ccsert/artifact-gateway/internal/rubygemsdistribution"
	domain "github.com/ccsert/artifact-gateway/internal/rubygemspublication"
	"github.com/google/uuid"
)

const rubyGemsReplicationColumns = `id::text,plan_digest,source_repository_id::text,target_repository_id::text,actor,actor_kind,credential_id,user_id,session_id,session_version,credential_until,version::text,state,lease_owner,lease_token,lease_fence,lease_until,created_at,updated_at`

type rubyGemsReplicationQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadRubyGemsReplication(ctx context.Context, q rubyGemsReplicationQuery, id string, lock bool) (dist.Job, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	var j dist.Job
	var until, credentialUntil sql.NullTime
	err := q.QueryRowContext(ctx, `SELECT `+rubyGemsReplicationColumns+` FROM native_rubygems_replication_jobs WHERE id::text=$1`+suffix, id).Scan(&j.ID, &j.PlanDigest, &j.SourceRepositoryID, &j.TargetRepositoryID, &j.Actor, &j.ActorKind, &j.CredentialID, &j.UserID, &j.SessionID, &j.SessionVersion, &credentialUntil, &j.Version, &j.State, &j.Lease.Owner, &j.Lease.Token, &j.Lease.Fence, &until, &j.CreatedAt, &j.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return j, dist.ErrNotFound
	}
	if err != nil {
		return j, err
	}
	if credentialUntil.Valid {
		j.ValidUntil = credentialUntil.Time
	}
	j.Lease.JobID = j.ID
	if until.Valid {
		j.Lease.Until = until.Time
	}
	rows, err := q.QueryContext(ctx, `SELECT item_index,name,version,digest,size,platform,source_governance_version::text,state,attempts,code,COALESCE(target_session_id::text,''),result,completed_at FROM native_rubygems_replication_items WHERE job_id::text=$1 ORDER BY item_index`, id)
	if err != nil {
		return j, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var i dist.Item
		var done sql.NullTime
		if err = rows.Scan(&i.Index, &i.Name, &i.Version, &i.Digest, &i.Size, &i.Platform, &i.SourceGovernanceVersion, &i.State, &i.Attempts, &i.Code, &i.TargetSessionID, &i.Result, &done); err != nil {
			return j, err
		}
		if done.Valid {
			i.CompletedAt = &done.Time
		}
		j.Items = append(j.Items, i)
	}
	return j, rows.Err()
}
func (s *Store) rubyGemsReplicationTransaction(ctx context.Context, lease dist.Lease) (*sql.Tx, dist.Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, dist.Job{}, err
	}
	j, err := loadRubyGemsReplication(ctx, tx, lease.JobID, true)
	if err == nil && !dist.LeaseMatches(j, lease) {
		err = dist.ErrLeaseLost
	}
	if err != nil {
		_ = tx.Rollback()
		return nil, j, err
	}
	return tx, j, nil
}
func rubyGemsReplicationAuthorityTx(ctx context.Context, tx *sql.Tx, j dist.Job) (dist.Authority, error) {
	ids := []string{j.SourceRepositoryID, j.TargetRepositoryID}
	sort.Strings(ids)
	for _, id := range ids {
		var r repository.HostedRepository
		err := tx.QueryRowContext(ctx, `SELECT id::text,name,format,repo_type,state FROM hosted_repositories WHERE id::text=$1 FOR UPDATE`, id).Scan(&r.ID, &r.Name, &r.Format, &r.Type, &r.State)
		if errors.Is(err, sql.ErrNoRows) {
			return dist.Authority{}, dist.ErrNotFound
		}
		if err != nil {
			return dist.Authority{}, err
		}
		if !domain.Active(r) {
			return dist.Authority{}, dist.ErrNotFound
		}
	}
	read := func(id string) (repository.RubyGemsGovernanceAuthority, error) {
		return governanceAuthorityTx(ctx, tx, domain.GovernanceChange{Identity: domain.ReplicationIdentity(j), Actor: j.Actor, RepositoryID: id})
	}
	a, err := read(j.SourceRepositoryID)
	if err != nil {
		return dist.Authority{}, err
	}
	b, err := read(j.TargetRepositoryID)
	if err != nil {
		return dist.Authority{}, err
	}
	return domain.ReplicationAuthority(j, a, b), nil
}
func replicationQuota(ctx context.Context, tx *sql.Tx, repo string, extra int64) error {
	c, err := capacity(ctx, tx, repo)
	if err != nil {
		return err
	}
	if c.QuotaBytes > 0 && (c.UsedBytes+c.PendingBytes > c.QuotaBytes || extra > c.QuotaBytes-c.UsedBytes-c.PendingBytes) {
		return dist.ErrCapacity
	}
	return nil
}
func replicationPublication(ctx context.Context, q rubyGemsReplicationQuery, repo, name, version, platform, lock string) (domain.Publication, error) {
	s, err := scan(q.QueryRowContext(ctx, `SELECT `+columns+` FROM native_rubygems_sessions WHERE repository_id::text=$1 AND name=$2 AND canonical_version=$3 AND platform=$4 AND state='committed'`+lock, repo, name, protocol.CanonicalVersion(version), platform))
	return domain.Visible(s), err
}
func rubyGemsReplicationSourceTx(ctx context.Context, tx *sql.Tx, j dist.Job, index int) (domain.Publication, error) {
	if index < 0 || index >= len(j.Items) {
		return domain.Publication{}, dist.ErrInvalid
	}
	i := j.Items[index]
	p, err := replicationPublication(ctx, tx, j.SourceRepositoryID, i.Name, i.Version, i.Platform, " FOR SHARE")
	if errors.Is(err, repository.ErrNotFound) {
		return p, dist.ErrSourceChanged
	}
	if err != nil {
		return p, err
	}
	return p, domain.ReplicationSource(p, i)
}
func saveRubyGemsReplicationItem(ctx context.Context, tx *sql.Tx, j dist.Job, index int) error {
	i := j.Items[index]
	_, err := tx.ExecContext(ctx, `UPDATE native_rubygems_replication_items SET state=$3,attempts=$4,code=$5,target_session_id=NULLIF($6,'')::uuid,result=$7,completed_at=$8 WHERE job_id::text=$1 AND item_index=$2`, j.ID, index, i.State, i.Attempts, i.Code, i.TargetSessionID, i.Result, i.CompletedAt)
	return err
}
func saveRubyGemsReplicationProgress(ctx context.Context, tx *sql.Tx, j dist.Job, lease dist.Lease, validUntil time.Time, advance bool) error {
	progress := "version=version+1,updated_at=clock_timestamp()"
	if !advance {
		progress = "version=version"
	}
	var expiry any
	if !validUntil.IsZero() {
		expiry = validUntil
	}
	result, err := tx.ExecContext(ctx, `UPDATE native_rubygems_replication_jobs SET `+progress+` WHERE id::text=$1 AND state='running' AND lease_owner=$2 AND lease_token=$3 AND lease_fence=$4 AND lease_until>clock_timestamp() AND ($5::timestamptz IS NULL OR $5>clock_timestamp())`, j.ID, lease.Owner, lease.Token, lease.Fence, expiry)
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
func (s *Store) CreateRubyGemsReplication(ctx context.Context, j dist.Job, check dist.Check) (dist.Job, bool, error) {
	if !dist.ValidNewJob(j) {
		return dist.Job{}, false, dist.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return dist.Job{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "rubygems-replication-create:"+j.ID); err != nil {
		return dist.Job{}, false, err
	}
	a, err := rubyGemsReplicationAuthorityTx(ctx, tx, j)
	if err != nil {
		return dist.Job{}, false, err
	}
	if err = dist.CheckAuthority(ctx, check, a); err != nil {
		return dist.Job{}, false, err
	}
	prior, err := loadRubyGemsReplication(ctx, tx, j.ID, false)
	if err == nil {
		if prior.PlanDigest != j.PlanDigest || !dist.SameSubject(prior.Subject(), j.Subject()) {
			return dist.Job{}, false, dist.ErrConflict
		}
		if err = dist.CheckAuthority(ctx, check, a); err != nil {
			return dist.Job{}, false, err
		}
		return prior, true, tx.Commit()
	}
	if !errors.Is(err, dist.ErrNotFound) {
		return dist.Job{}, false, err
	}
	extra := int64(0)
	for index, item := range j.Items {
		source, err := rubyGemsReplicationSourceTx(ctx, tx, j, index)
		if err != nil {
			return dist.Job{}, false, err
		}
		prior, err := scan(tx.QueryRowContext(ctx, `SELECT `+columns+` FROM native_rubygems_sessions WHERE repository_id::text=$1 AND ((name=$2 AND canonical_version=$3 AND platform=$4) OR full_name=$5) AND (state='committed' OR state='pending' AND expires_at>clock_timestamp()) FOR SHARE`, j.TargetRepositoryID, item.Name, protocol.CanonicalVersion(item.Version), item.Platform, source.FullName()))
		if err == nil {
			if err = domain.ReplicationSession(source, prior, j.TargetRepositoryID); err != nil {
				return dist.Job{}, false, err
			}
			if prior.State == "pending" && prior.Actor != j.Actor {
				return dist.Job{}, false, dist.ErrTargetBlocked
			}
		} else if errors.Is(err, repository.ErrNotFound) {
			extra += item.Size
		} else {
			return dist.Job{}, false, err
		}
	}
	if err = replicationQuota(ctx, tx, j.TargetRepositoryID, extra); err != nil {
		return dist.Job{}, false, err
	}
	var credentialUntil any
	if !j.ValidUntil.IsZero() {
		credentialUntil = j.ValidUntil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO native_rubygems_replication_jobs(id,plan_digest,source_repository_id,target_repository_id,actor,actor_kind,credential_id,user_id,session_id,session_version,credential_until,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'queued')`, j.ID, j.PlanDigest, j.SourceRepositoryID, j.TargetRepositoryID, j.Actor, j.ActorKind, j.CredentialID, j.UserID, j.SessionID, j.SessionVersion, credentialUntil)
	if err != nil {
		return dist.Job{}, false, err
	}
	for _, i := range j.Items {
		if _, err = tx.ExecContext(ctx, `INSERT INTO native_rubygems_replication_items(job_id,item_index,name,version,canonical_version,platform,digest,size,source_governance_version,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'pending')`, j.ID, i.Index, i.Name, i.Version, protocol.CanonicalVersion(i.Version), i.Platform, i.Digest, i.Size, i.SourceGovernanceVersion); err != nil {
			return dist.Job{}, false, err
		}
	}
	j, err = loadRubyGemsReplication(ctx, tx, j.ID, false)
	if err != nil {
		return j, false, err
	}
	if err = insertAudit(ctx, tx, domain.ReplicationAudit(j, a.Target.Name, "create", 202, nil)); err != nil {
		return dist.Job{}, false, err
	}
	if err = dist.CheckAuthority(ctx, check, a); err != nil {
		return dist.Job{}, false, err
	}
	return j, false, tx.Commit()
}
func (s *Store) GetRubyGemsReplication(ctx context.Context, id string) (dist.Job, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return dist.Job{}, err
	}
	defer func() { _ = tx.Rollback() }()
	j, err := loadRubyGemsReplication(ctx, tx, id, false)
	if err != nil {
		return j, err
	}
	return j, tx.Commit()
}
func (s *Store) RetryRubyGemsReplication(ctx context.Context, id, expected string, indexes []int, key string, check dist.Check) (dist.Job, bool, error) {
	digest, err := dist.RetryDigest(expected, indexes)
	if err != nil || key == "" || len(key) > 200 || strings.ContainsAny(key, "\x00\r\n") {
		return dist.Job{}, false, dist.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return dist.Job{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	j, err := loadRubyGemsReplication(ctx, tx, id, true)
	if err != nil {
		return j, false, err
	}
	a, err := rubyGemsReplicationAuthorityTx(ctx, tx, j)
	if err != nil {
		return dist.Job{}, false, err
	}
	if err = dist.CheckAuthority(ctx, check, a); err != nil {
		return dist.Job{}, false, err
	}
	var prior string
	err = tx.QueryRowContext(ctx, `SELECT payload_digest FROM native_rubygems_replication_retries WHERE job_id::text=$1 AND request_key=$2`, id, key).Scan(&prior)
	if err == nil {
		if prior != digest {
			return dist.Job{}, false, dist.ErrConflict
		}
		if err = dist.CheckAuthority(ctx, check, a); err != nil {
			return dist.Job{}, false, err
		}
		return j, true, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return dist.Job{}, false, err
	}
	j, err = dist.RetryJob(j, expected, indexes)
	if err != nil {
		return dist.Job{}, false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE native_rubygems_replication_jobs SET state='queued',version=version+1,lease_owner='',lease_token='',lease_until=NULL,updated_at=clock_timestamp() WHERE id::text=$1`, id); err != nil {
		return dist.Job{}, false, err
	}
	for _, index := range indexes {
		if err = saveRubyGemsReplicationItem(ctx, tx, j, index); err != nil {
			return dist.Job{}, false, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_rubygems_replication_retries(job_id,request_key,payload_digest) VALUES($1,$2,$3)`, id, key, digest); err != nil {
		return dist.Job{}, false, err
	}
	if err = insertAudit(ctx, tx, domain.ReplicationAudit(j, a.Target.Name, "retry", 202, nil)); err != nil {
		return dist.Job{}, false, err
	}
	j, err = loadRubyGemsReplication(ctx, tx, id, false)
	if err != nil {
		return j, false, err
	}
	if err = dist.CheckAuthority(ctx, check, a); err != nil {
		return dist.Job{}, false, err
	}
	return j, false, tx.Commit()
}
func (s *Store) ClaimRubyGemsReplication(ctx context.Context, owner string, d time.Duration) (dist.Job, error) {
	if owner == "" || len(owner) > 512 || !dist.ValidDuration(d) {
		return dist.Job{}, dist.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return dist.Job{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var id string
	err = tx.QueryRowContext(ctx, `SELECT id::text FROM native_rubygems_replication_jobs WHERE state='queued' OR state='running' AND lease_until<=clock_timestamp() ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return dist.Job{}, dist.ErrNotFound
	}
	if err != nil {
		return dist.Job{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE native_rubygems_replication_jobs SET state='running',version=version+1,lease_owner=$2,lease_token=$3,lease_fence=lease_fence+1,lease_until=clock_timestamp()+$4::bigint*interval '1 millisecond',updated_at=clock_timestamp() WHERE id::text=$1`, id, owner, uuid.NewString(), d.Milliseconds()); err != nil {
		return dist.Job{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE native_rubygems_replication_items SET state='pending' WHERE job_id::text=$1 AND state='running'`, id); err != nil {
		return dist.Job{}, err
	}
	j, err := loadRubyGemsReplication(ctx, tx, id, false)
	if err != nil {
		return j, err
	}
	return j, tx.Commit()
}
func (s *Store) RenewRubyGemsReplication(ctx context.Context, lease dist.Lease, d time.Duration) (dist.Lease, error) {
	if !dist.ValidDuration(d) {
		return dist.Lease{}, dist.ErrInvalid
	}
	var until time.Time
	err := s.db.QueryRowContext(ctx, `UPDATE native_rubygems_replication_jobs SET lease_until=clock_timestamp()+$5::bigint*interval '1 millisecond' WHERE id::text=$1 AND state='running' AND lease_owner=$2 AND lease_token=$3 AND lease_fence=$4 AND lease_until>clock_timestamp() RETURNING lease_until`, lease.JobID, lease.Owner, lease.Token, lease.Fence, d.Milliseconds()).Scan(&until)
	if errors.Is(err, sql.ErrNoRows) {
		return dist.Lease{}, dist.ErrLeaseLost
	}
	lease.Until = until
	return lease, err
}
func (s *Store) BindRubyGemsReplicationSession(ctx context.Context, lease dist.Lease, index int, sessionID string, check dist.Check) (dist.Job, error) {
	tx, j, err := s.rubyGemsReplicationTransaction(ctx, lease)
	if err != nil {
		return dist.Job{}, err
	}
	defer func() { _ = tx.Rollback() }()
	a, err := rubyGemsReplicationAuthorityTx(ctx, tx, j)
	if err != nil {
		return dist.Job{}, err
	}
	if err = dist.CheckAuthority(ctx, check, a); err != nil {
		return dist.Job{}, err
	}
	source, err := rubyGemsReplicationSourceTx(ctx, tx, j, index)
	if err != nil {
		return dist.Job{}, err
	}
	session, err := scan(tx.QueryRowContext(ctx, `SELECT `+columns+` FROM native_rubygems_sessions WHERE id::text=$1 FOR SHARE`, sessionID))
	if err != nil {
		return dist.Job{}, err
	}
	if err = domain.ReplicationSession(source, session, j.TargetRepositoryID); err != nil {
		return dist.Job{}, err
	}
	if session.State != "committed" && (session.State != "pending" || !session.ExpiresAt.After(time.Now()) || session.Actor != j.Actor) {
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
	if err = saveRubyGemsReplicationItem(ctx, tx, j, index); err != nil {
		return dist.Job{}, err
	}
	if err = saveRubyGemsReplicationProgress(ctx, tx, j, lease, a.ValidUntil, true); err != nil {
		return dist.Job{}, err
	}
	j, err = loadRubyGemsReplication(ctx, tx, j.ID, false)
	if err != nil {
		return j, err
	}
	return j, tx.Commit()
}
func (s *Store) CompleteRubyGemsReplicationItem(ctx context.Context, lease dist.Lease, index int, proof dist.Commit, check dist.Check) (dist.Job, error) {
	tx, j, err := s.rubyGemsReplicationTransaction(ctx, lease)
	if err != nil {
		return dist.Job{}, err
	}
	defer func() { _ = tx.Rollback() }()
	a, err := rubyGemsReplicationAuthorityTx(ctx, tx, j)
	if err != nil {
		return dist.Job{}, err
	}
	if err = dist.CheckAuthority(ctx, check, a); err != nil {
		return dist.Job{}, err
	}
	source, err := rubyGemsReplicationSourceTx(ctx, tx, j, index)
	if err != nil {
		return dist.Job{}, err
	}
	item := j.Items[index]
	if item.TargetSessionID != proof.SessionID {
		return dist.Job{}, dist.ErrConflict
	}
	session, err := scan(tx.QueryRowContext(ctx, `SELECT `+columns+` FROM native_rubygems_sessions WHERE id::text=$1 FOR UPDATE`, proof.SessionID))
	if err != nil {
		return dist.Job{}, err
	}
	if err = domain.ReplicationSession(source, session, j.TargetRepositoryID); err != nil {
		return dist.Job{}, err
	}
	if item.State == "succeeded" {
		if err = saveRubyGemsReplicationProgress(ctx, tx, j, lease, a.ValidUntil, false); err != nil {
			return dist.Job{}, err
		}
		return j, tx.Commit()
	}
	if item.State != "running" {
		return dist.Job{}, dist.ErrConflict
	}
	if session.State == "committed" {
		item.Result = "already_present"
	} else {
		if session.State != "pending" || session.Actor != j.Actor || !session.ExpiresAt.After(time.Now()) || !domain.ValidInput(session) {
			return dist.Job{}, dist.ErrTargetBlocked
		}
		if err = replicationQuota(ctx, tx, j.TargetRepositoryID, 0); err != nil {
			return dist.Job{}, err
		}
		session, err = scan(tx.QueryRowContext(ctx, `UPDATE native_rubygems_sessions SET state='committed',committed_at=clock_timestamp() WHERE id::text=$1 RETURNING `+columns, session.ID))
		if err != nil {
			return dist.Job{}, err
		}
		if err = insertAudit(ctx, tx, domain.Audit(domain.Visible(session), a.Target.Name)); err != nil {
			return dist.Job{}, err
		}
		item.Result = "published"
	}
	now := time.Now().UTC()
	item.State = "succeeded"
	item.Code = ""
	item.CompletedAt = &now
	j.Items[index] = item
	j.Version = dist.NextVersion(j.Version)
	if err = saveRubyGemsReplicationItem(ctx, tx, j, index); err != nil {
		return dist.Job{}, err
	}
	if err = insertAudit(ctx, tx, domain.ReplicationAudit(j, a.Target.Name, "item", 200, &item)); err != nil {
		return dist.Job{}, err
	}
	if item.Result == "published" && !session.ExpiresAt.After(time.Now()) {
		return dist.Job{}, dist.ErrTargetBlocked
	}
	// Last write fences publication, both audit records and item checkpoint after
	// lock waits and injected audit latency, using the database wall clock.
	if err = saveRubyGemsReplicationProgress(ctx, tx, j, lease, a.ValidUntil, true); err != nil {
		return dist.Job{}, err
	}
	j, err = loadRubyGemsReplication(ctx, tx, j.ID, false)
	if err != nil {
		return j, err
	}
	return j, tx.Commit()
}
func (s *Store) FailRubyGemsReplicationItem(ctx context.Context, lease dist.Lease, index int, code string) error {
	if !dist.ValidFailureCode(code) {
		return dist.ErrInvalid
	}
	tx, j, err := s.rubyGemsReplicationTransaction(ctx, lease)
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
	if err = saveRubyGemsReplicationItem(ctx, tx, j, index); err != nil {
		return err
	}
	var name string
	if err = tx.QueryRowContext(ctx, `SELECT name FROM hosted_repositories WHERE id::text=$1`, j.TargetRepositoryID).Scan(&name); err != nil {
		return err
	}
	if err = insertAudit(ctx, tx, domain.ReplicationAudit(j, name, "item", dist.FailureStatus(code), &item)); err != nil {
		return err
	}
	if err = saveRubyGemsReplicationProgress(ctx, tx, j, lease, time.Time{}, true); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) FinishRubyGemsReplication(ctx context.Context, lease dist.Lease) (dist.Job, error) {
	tx, j, err := s.rubyGemsReplicationTransaction(ctx, lease)
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
	var name string
	if err = tx.QueryRowContext(ctx, `SELECT name FROM hosted_repositories WHERE id::text=$1`, j.TargetRepositoryID).Scan(&name); err != nil {
		return dist.Job{}, err
	}
	j.Version = dist.NextVersion(j.Version)
	status := 200
	if j.State == "failed" {
		status = 409
	}
	if err = insertAudit(ctx, tx, domain.ReplicationAudit(j, name, "complete", status, nil)); err != nil {
		return dist.Job{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE native_rubygems_replication_jobs SET state=$2,version=version+1,updated_at=clock_timestamp(),lease_owner='',lease_token='',lease_until=NULL WHERE id::text=$1 AND state='running' AND lease_owner=$3 AND lease_token=$4 AND lease_fence=$5 AND lease_until>clock_timestamp()`, j.ID, j.State, lease.Owner, lease.Token, lease.Fence)
	if err != nil {
		return dist.Job{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return dist.Job{}, err
	}
	if n != 1 {
		return dist.Job{}, dist.ErrLeaseLost
	}
	j, err = loadRubyGemsReplication(ctx, tx, j.ID, false)
	if err != nil {
		return j, err
	}
	return j, tx.Commit()
}

func (s *Store) AuthorizeRubyGemsReplicationItem(ctx context.Context, lease dist.Lease, index int, check dist.Check) error {
	tx, j, err := s.rubyGemsReplicationTransaction(ctx, lease)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	a, err := rubyGemsReplicationAuthorityTx(ctx, tx, j)
	if err != nil {
		return err
	}
	if err = dist.CheckAuthority(ctx, check, a); err != nil {
		return err
	}
	if _, err = rubyGemsReplicationSourceTx(ctx, tx, j, index); err != nil {
		return err
	}
	if err = dist.CheckAuthority(ctx, check, a); err != nil {
		return err
	}
	if err = saveRubyGemsReplicationProgress(ctx, tx, j, lease, a.ValidUntil, false); err != nil {
		return err
	}
	return tx.Commit()
}

var _ dist.Store = (*Store)(nil)
