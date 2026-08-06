package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

func (s *PostgresStore) CreateHostedRepository(ctx context.Context, repo HostedRepository) (HostedRepository, error) {
	repo = normalizeHostedRepository(repo)
	egressProxy, err := marshalEgressProxy(repo.EgressProxy)
	if err != nil {
		return HostedRepository{}, err
	}
	err = s.db.QueryRowContext(ctx, `INSERT INTO hosted_repositories (id, name, format, repo_type, endpoint, allowed_hosts, anonymous_read, state, version, egress_proxy) VALUES ($1,$2,$3,$4,$5,COALESCE($6::text[], '{}'::text[]),$7,'active',1,$8) RETURNING state, version, created_at`, repo.ID, repo.Name, repo.Format, repo.Type, repo.Endpoint, repo.AllowedHosts, repo.AnonymousRead, egressProxy).Scan(&repo.State, &repo.Version, &repo.CreatedAt)
	if isUnique(err) {
		return HostedRepository{}, ErrNameExists
	}
	if err != nil {
		return HostedRepository{}, err
	}
	return repo, nil
}

func (s *PostgresStore) CreateHostedRepositoryIdempotently(ctx context.Context, repo HostedRepository, actor, key, payload string) (HostedRepository, bool, error) {
	repo = normalizeHostedRepository(repo)
	egressProxy, err := marshalEgressProxy(repo.EgressProxy)
	if err != nil {
		return HostedRepository{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return HostedRepository{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	// Row locks cannot serialize the first use of a key because no row exists
	// yet. A transaction-scoped advisory lock covers that gap without holding a
	// process-local mutex across gateway instances.
	// PostgreSQL text parameters reject NUL bytes. Prefix the variable parts
	// with their lengths so distinct actor/key pairs cannot share a lock key.
	lockKey := fmt.Sprintf("%d:%s%d:%s", len(actor), actor, len(key), key)
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return HostedRepository{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM hosted_repository_idempotency WHERE actor=$1 AND target=$2 AND key=$3 AND expires_at <= now()`, actor, "/repositories", key); err != nil {
		return HostedRepository{}, false, err
	}
	var storedPayload, repositoryID string
	err = tx.QueryRowContext(ctx, `SELECT payload_hash, repository_id::text FROM hosted_repository_idempotency WHERE actor=$1 AND target=$2 AND key=$3 AND expires_at > now() FOR UPDATE`, actor, "/repositories", key).Scan(&storedPayload, &repositoryID)
	if err == nil {
		if storedPayload != payload {
			return HostedRepository{}, false, ErrIdempotencyConflict
		}
		err = scanHostedRepository(tx.QueryRowContext(ctx, `SELECT `+hostedRepositoryColumns+` FROM hosted_repositories WHERE id::text=$1`, repositoryID), &repo)
		if err != nil {
			return HostedRepository{}, false, err
		}
		if err = tx.Commit(); err != nil {
			return HostedRepository{}, false, err
		}
		return repo, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return HostedRepository{}, false, err
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO hosted_repositories (id, name, format, repo_type, endpoint, allowed_hosts, anonymous_read, state, version, egress_proxy) VALUES ($1,$2,$3,$4,$5,COALESCE($6::text[], '{}'::text[]),$7,'active',1,$8) RETURNING state, version, created_at`, repo.ID, repo.Name, repo.Format, repo.Type, repo.Endpoint, repo.AllowedHosts, repo.AnonymousRead, egressProxy).Scan(&repo.State, &repo.Version, &repo.CreatedAt)
	if isUnique(err) {
		return HostedRepository{}, false, ErrNameExists
	}
	if err != nil {
		return HostedRepository{}, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO hosted_repository_idempotency (actor, target, key, payload_hash, repository_id, expires_at) VALUES ($1,$2,$3,$4,$5,now() + interval '24 hours')`, actor, "/repositories", key, payload, repo.ID)
	if err != nil {
		return HostedRepository{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return HostedRepository{}, false, err
	}
	return repo, false, nil
}

// hostedRepositoryColumns is the canonical projection for hosted_repositories
// reads. allowed_hosts is projected through array_to_json so it scans into
// []byte and decodes into []string without a pq dependency.
const hostedRepositoryColumns = `id::text, name, format, repo_type, endpoint, array_to_json(allowed_hosts), anonymous_read, state, version::text, created_at, egress_proxy`

// marshalEgressProxy encodes the egress proxy configuration for the JSONB
// column. The response-only CredentialsConfigured marker is never persisted.
func marshalEgressProxy(proxy *EgressProxy) (any, error) {
	if proxy == nil {
		return nil, nil
	}
	stored := *proxy
	stored.CredentialsConfigured = false
	encoded, err := json.Marshal(stored)
	if err != nil {
		return nil, err
	}
	return string(encoded), nil
}

func scanHostedRepository(row interface{ Scan(...any) error }, repo *HostedRepository) error {
	var allowedHosts, egressProxy []byte
	if err := row.Scan(&repo.ID, &repo.Name, &repo.Format, &repo.Type, &repo.Endpoint, &allowedHosts, &repo.AnonymousRead, &repo.State, &repo.Version, &repo.CreatedAt, &egressProxy); err != nil {
		return err
	}
	if err := json.Unmarshal(allowedHosts, &repo.AllowedHosts); err != nil {
		return err
	}
	if len(egressProxy) > 0 {
		var proxy EgressProxy
		if err := json.Unmarshal(egressProxy, &proxy); err != nil {
			return err
		}
		repo.EgressProxy = &proxy
	}
	return nil
}

func (s *PostgresStore) ListHostedRepositories(ctx context.Context, limit int, after string) ([]HostedRepository, string, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if after != "" {
		var exists bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM hosted_repositories WHERE id::text=$1)`, after).Scan(&exists); err != nil {
			return nil, "", err
		}
		if !exists {
			return nil, "", ErrNotFound
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+hostedRepositoryColumns+` FROM hosted_repositories WHERE ($1 = '' OR id::text > $1) ORDER BY id LIMIT $2`, after, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rows.Close() }()
	items := make([]HostedRepository, 0, limit)
	for rows.Next() {
		var repo HostedRepository
		if err := scanHostedRepository(rows, &repo); err != nil {
			return nil, "", err
		}
		items = append(items, repo)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		next = items[limit-1].ID
		items = items[:limit]
	}
	return items, next, nil
}

func (s *PostgresStore) GetHostedRepositoryByName(ctx context.Context, name string) (HostedRepository, error) {
	var repo HostedRepository
	err := scanHostedRepository(s.db.QueryRowContext(ctx, `SELECT `+hostedRepositoryColumns+` FROM hosted_repositories WHERE name=$1`, name), &repo)
	if errors.Is(err, sql.ErrNoRows) {
		return HostedRepository{}, ErrNotFound
	}
	if err != nil {
		return HostedRepository{}, err
	}
	return repo, nil
}

func (s *PostgresStore) GetHostedRepository(ctx context.Context, id string) (HostedRepository, error) {
	var repo HostedRepository
	err := scanHostedRepository(s.db.QueryRowContext(ctx, `SELECT `+hostedRepositoryColumns+` FROM hosted_repositories WHERE id::text=$1`, id), &repo)
	if errors.Is(err, sql.ErrNoRows) {
		return HostedRepository{}, ErrNotFound
	}
	if err != nil {
		return HostedRepository{}, err
	}
	return repo, nil
}

func (s *PostgresStore) DisableHostedRepository(ctx context.Context, id string) (HostedRepository, error) {
	var repo HostedRepository
	err := scanHostedRepository(s.db.QueryRowContext(ctx, `UPDATE hosted_repositories SET state='deleting', version=version+1 WHERE id::text=$1 AND state='active' RETURNING `+hostedRepositoryColumns, id), &repo)
	if errors.Is(err, sql.ErrNoRows) {
		// Deletion is an asynchronous lifecycle transition. Treat a retry for an
		// already deleting repository as successful so clients can safely retry
		// after a timeout or page refresh.
		stateErr := scanHostedRepository(s.db.QueryRowContext(ctx, `SELECT `+hostedRepositoryColumns+` FROM hosted_repositories WHERE id::text=$1 AND state='deleting'`, id), &repo)
		if stateErr == nil {
			return repo, nil
		}
		if !errors.Is(stateErr, sql.ErrNoRows) {
			return HostedRepository{}, stateErr
		}
		return HostedRepository{}, ErrNotFound
	}
	if err != nil {
		return HostedRepository{}, err
	}
	return repo, nil
}

func (s *PostgresStore) FinalizeHostedRepositoryDeletion(ctx context.Context, id string) (HostedRepository, error) {
	var repo HostedRepository
	err := scanHostedRepository(s.db.QueryRowContext(ctx, `UPDATE hosted_repositories SET state='deleted', version=version+1 WHERE id::text=$1 AND state='deleting' RETURNING `+hostedRepositoryColumns, id), &repo)
	if errors.Is(err, sql.ErrNoRows) {
		err = scanHostedRepository(s.db.QueryRowContext(ctx, `SELECT `+hostedRepositoryColumns+` FROM hosted_repositories WHERE id::text=$1`, id), &repo)
		if errors.Is(err, sql.ErrNoRows) {
			return HostedRepository{}, ErrNotFound
		}
		if err != nil {
			return HostedRepository{}, err
		}
		if repo.State == RepositoryDeleted {
			return repo, nil
		}
		return HostedRepository{}, ErrVersionConflict
	}
	if err != nil {
		return HostedRepository{}, err
	}
	return repo, nil
}

func (s *PostgresStore) UpdateHostedRepository(ctx context.Context, repo HostedRepository, expectedVersion string) (HostedRepository, error) {
	egressProxy, err := marshalEgressProxy(repo.EgressProxy)
	if err != nil {
		return HostedRepository{}, err
	}
	var updated HostedRepository
	err = scanHostedRepository(s.db.QueryRowContext(ctx, `UPDATE hosted_repositories SET endpoint=$2, allowed_hosts=COALESCE($3::text[], '{}'::text[]), anonymous_read=$4, version=version+1, egress_proxy=$6 WHERE id::text=$1 AND state='active' AND version::text=$5 RETURNING `+hostedRepositoryColumns, repo.ID, repo.Endpoint, repo.AllowedHosts, repo.AnonymousRead, expectedVersion, egressProxy), &updated)
	if errors.Is(err, sql.ErrNoRows) {
		if _, getErr := s.GetHostedRepository(ctx, repo.ID); errors.Is(getErr, ErrNotFound) {
			return HostedRepository{}, ErrNotFound
		}
		return HostedRepository{}, ErrVersionConflict
	}
	if err != nil {
		return HostedRepository{}, err
	}
	return updated, nil
}

func scanHostedGroupMembers(ctx context.Context, query interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, group *HostedGroup) error {
	rows, err := query.QueryContext(ctx, `SELECT repository_id::text, position FROM hosted_group_members WHERE group_id::text=$1 ORDER BY position`, group.ID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	group.Members = nil
	for rows.Next() {
		var member GroupMember
		if err := rows.Scan(&member.RepositoryID, &member.Position); err != nil {
			return err
		}
		group.Members = append(group.Members, member)
	}
	return rows.Err()
}

func (s *PostgresStore) getHostedGroup(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, id string) (HostedGroup, error) {
	var group HostedGroup
	err := query.QueryRowContext(ctx, `SELECT id::text, name, format, anonymous_read, version::text FROM hosted_groups WHERE id::text=$1`, id).Scan(&group.ID, &group.Name, &group.Format, &group.AnonymousRead, &group.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return HostedGroup{}, ErrNotFound
	}
	if err != nil {
		return HostedGroup{}, err
	}
	if err := scanHostedGroupMembers(ctx, query, &group); err != nil {
		return HostedGroup{}, err
	}
	return group, nil
}

func (s *PostgresStore) CreateHostedGroupIdempotently(ctx context.Context, group HostedGroup, actor, key, payload string) (HostedGroup, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return HostedGroup{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	lockKey := fmt.Sprintf("hosted-group:%d:%s%d:%s", len(actor), actor, len(key), key)
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return HostedGroup{}, false, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM hosted_group_idempotency WHERE actor=$1 AND key=$2 AND expires_at <= now()`, actor, key); err != nil {
		return HostedGroup{}, false, err
	}
	var storedPayload, groupID string
	err = tx.QueryRowContext(ctx, `SELECT payload_hash, group_id::text FROM hosted_group_idempotency WHERE actor=$1 AND key=$2 FOR UPDATE`, actor, key).Scan(&storedPayload, &groupID)
	if err == nil {
		if storedPayload != payload {
			return HostedGroup{}, false, ErrIdempotencyConflict
		}
		group, err = s.getHostedGroup(ctx, tx, groupID)
		if err != nil {
			return HostedGroup{}, false, err
		}
		if err = tx.Commit(); err != nil {
			return HostedGroup{}, false, err
		}
		return group, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return HostedGroup{}, false, err
	}
	if err = tx.QueryRowContext(ctx, `INSERT INTO hosted_groups (id,name,format,anonymous_read,version) VALUES ($1,$2,$3,$4,1) RETURNING version::text`, group.ID, group.Name, group.Format, group.AnonymousRead).Scan(&group.Version); err != nil {
		if isUnique(err) {
			return HostedGroup{}, false, ErrNameExists
		}
		return HostedGroup{}, false, err
	}
	for _, member := range group.Members {
		if _, err = tx.ExecContext(ctx, `INSERT INTO hosted_group_members (group_id,repository_id,position) VALUES ($1,$2,$3)`, group.ID, member.RepositoryID, member.Position); err != nil {
			if isUnique(err) {
				return HostedGroup{}, false, ErrNameExists
			}
			return HostedGroup{}, false, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO hosted_group_idempotency (actor,key,payload_hash,group_id,expires_at) VALUES ($1,$2,$3,$4,now() + interval '24 hours')`, actor, key, payload, group.ID); err != nil {
		return HostedGroup{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return HostedGroup{}, false, err
	}
	return group, false, nil
}

func (s *PostgresStore) ListHostedGroups(ctx context.Context, limit int, after string) ([]HostedGroup, string, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id::text, name, format, anonymous_read, version::text FROM hosted_groups WHERE ($1='' OR id::text>$1) ORDER BY id LIMIT $2`, after, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rows.Close() }()
	groups := make([]HostedGroup, 0, limit)
	for rows.Next() {
		var group HostedGroup
		if err := rows.Scan(&group.ID, &group.Name, &group.Format, &group.AnonymousRead, &group.Version); err != nil {
			return nil, "", err
		}
		if err := scanHostedGroupMembers(ctx, s.db, &group); err != nil {
			return nil, "", err
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(groups) > limit {
		next = groups[limit-1].ID
		groups = groups[:limit]
	}
	return groups, next, nil
}

func (s *PostgresStore) GetHostedGroup(ctx context.Context, id string) (HostedGroup, error) {
	return s.getHostedGroup(ctx, s.db, id)
}

func (s *PostgresStore) replaceHostedGroup(ctx context.Context, id, name string, format Format, anonymousRead bool, members []GroupMember, expectedVersion string, replaceMetadata bool) (HostedGroup, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return HostedGroup{}, err
	}
	defer func() { _ = tx.Rollback() }()
	query := `UPDATE hosted_groups SET version=version+1`
	args := []any{id, expectedVersion}
	if replaceMetadata {
		query += `, name=$3, format=$4, anonymous_read=$5`
		args = []any{id, expectedVersion, name, format, anonymousRead}
	}
	query += ` WHERE id::text=$1 AND version::text=$2 RETURNING id::text,name,format,anonymous_read,version::text`
	var group HostedGroup
	err = tx.QueryRowContext(ctx, query, args...).Scan(&group.ID, &group.Name, &group.Format, &group.AnonymousRead, &group.Version)
	if errors.Is(err, sql.ErrNoRows) {
		if _, getErr := s.getHostedGroup(ctx, tx, id); errors.Is(getErr, ErrNotFound) {
			return HostedGroup{}, ErrNotFound
		}
		return HostedGroup{}, ErrVersionConflict
	}
	if err != nil {
		return HostedGroup{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM hosted_group_members WHERE group_id::text=$1`, id); err != nil {
		return HostedGroup{}, err
	}
	for _, member := range members {
		if _, err = tx.ExecContext(ctx, `INSERT INTO hosted_group_members (group_id,repository_id,position) VALUES ($1,$2,$3)`, id, member.RepositoryID, member.Position); err != nil {
			return HostedGroup{}, err
		}
	}
	group.Members = append([]GroupMember(nil), members...)
	if err = tx.Commit(); err != nil {
		return HostedGroup{}, err
	}
	return group, nil
}

func (s *PostgresStore) ReplaceHostedGroup(ctx context.Context, group HostedGroup, expectedVersion string) (HostedGroup, error) {
	return s.replaceHostedGroup(ctx, group.ID, group.Name, group.Format, group.AnonymousRead, group.Members, expectedVersion, true)
}
func (s *PostgresStore) ReplaceHostedGroupMembers(ctx context.Context, id string, members []GroupMember, expectedVersion string) (HostedGroup, error) {
	current, err := s.GetHostedGroup(ctx, id)
	if err != nil {
		return HostedGroup{}, err
	}
	return s.replaceHostedGroup(ctx, id, current.Name, current.Format, current.AnonymousRead, members, expectedVersion, false)
}
func (s *PostgresStore) DeleteHostedGroup(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM hosted_groups WHERE id::text=$1`, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func loadRepositoryGrants(ctx context.Context, query interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, repositoryID, version string) (RepositoryGrantSet, error) {
	rows, err := query.QueryContext(ctx, `SELECT principal, array_to_json(scopes), resource_prefix FROM repository_grants WHERE repository_id::text=$1 ORDER BY principal, resource_prefix`, repositoryID)
	if err != nil {
		return RepositoryGrantSet{}, err
	}
	defer func() { _ = rows.Close() }()
	set := RepositoryGrantSet{Version: version, Grants: []RepositoryGrant{}}
	for rows.Next() {
		var grant RepositoryGrant
		var scopes []byte
		if err := rows.Scan(&grant.Principal, &scopes, &grant.ResourcePrefix); err != nil {
			return RepositoryGrantSet{}, err
		}
		if err := json.Unmarshal(scopes, &grant.Scopes); err != nil {
			return RepositoryGrantSet{}, err
		}
		set.Grants = append(set.Grants, grant)
	}
	return set, rows.Err()
}

func (s *PostgresStore) GetRepositoryGrants(ctx context.Context, repositoryID string) (RepositoryGrantSet, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM hosted_repositories WHERE id::text=$1)`, repositoryID).Scan(&exists); err != nil {
		return RepositoryGrantSet{}, err
	}
	if !exists {
		return RepositoryGrantSet{}, ErrNotFound
	}
	var version string
	err := s.db.QueryRowContext(ctx, `SELECT version::text FROM repository_grant_sets WHERE repository_id::text=$1`, repositoryID).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return RepositoryGrantSet{Version: "1", Grants: []RepositoryGrant{}}, nil
	}
	if err != nil {
		return RepositoryGrantSet{}, err
	}
	return loadRepositoryGrants(ctx, s.db, repositoryID, version)
}

func (s *PostgresStore) ReplaceRepositoryGrants(ctx context.Context, repositoryID string, grants []RepositoryGrant, expectedVersion string) (RepositoryGrantSet, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RepositoryGrantSet{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM hosted_repositories WHERE id::text=$1)`, repositoryID).Scan(&exists); err != nil {
		return RepositoryGrantSet{}, err
	}
	if !exists {
		return RepositoryGrantSet{}, ErrNotFound
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO repository_grant_sets (repository_id,version) VALUES ($1,1) ON CONFLICT DO NOTHING`, repositoryID); err != nil {
		return RepositoryGrantSet{}, err
	}
	var version string
	err = tx.QueryRowContext(ctx, `UPDATE repository_grant_sets SET version=version+1 WHERE repository_id::text=$1 AND version::text=$2 RETURNING version::text`, repositoryID, expectedVersion).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return RepositoryGrantSet{}, ErrVersionConflict
	}
	if err != nil {
		return RepositoryGrantSet{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM repository_grants WHERE repository_id::text=$1`, repositoryID); err != nil {
		return RepositoryGrantSet{}, err
	}
	for _, grant := range grants {
		if _, err = tx.ExecContext(ctx, `INSERT INTO repository_grants (repository_id,principal,scopes,resource_prefix) VALUES ($1,$2,$3,$4)`, repositoryID, grant.Principal, grant.Scopes, grant.ResourcePrefix); err != nil {
			return RepositoryGrantSet{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return RepositoryGrantSet{}, err
	}
	return RepositoryGrantSet{Version: version, Grants: append([]RepositoryGrant{}, grants...)}, nil
}

func (s *PostgresStore) GetRepositoryRetentionPolicy(ctx context.Context, repositoryID string) (RepositoryRetentionPolicy, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM hosted_repositories WHERE id::text=$1)`, repositoryID).Scan(&exists); err != nil {
		return RepositoryRetentionPolicy{}, err
	}
	if !exists {
		return RepositoryRetentionPolicy{}, ErrNotFound
	}
	policy := defaultRepositoryRetentionPolicy()
	var coordinatePatterns, protectedPatterns []byte
	err := s.db.QueryRowContext(ctx, `SELECT version::text,enabled,keep_days,snapshot_keep_days,minimum_versions,maximum_versions,array_to_json(coordinate_patterns),array_to_json(protected_patterns) FROM repository_retention_policies WHERE repository_id::text=$1`, repositoryID).Scan(&policy.Version, &policy.Enabled, &policy.KeepDays, &policy.SnapshotKeepDays, &policy.MinimumVersions, &policy.MaximumVersions, &coordinatePatterns, &protectedPatterns)
	if errors.Is(err, sql.ErrNoRows) {
		return policy, nil
	}
	if err != nil {
		return policy, err
	}
	if err = json.Unmarshal(coordinatePatterns, &policy.CoordinatePatterns); err != nil {
		return RepositoryRetentionPolicy{}, err
	}
	if err = json.Unmarshal(protectedPatterns, &policy.ProtectedPatterns); err != nil {
		return RepositoryRetentionPolicy{}, err
	}
	return policy, nil
}

func (s *PostgresStore) ReplaceRepositoryRetentionPolicy(ctx context.Context, repositoryID string, policy RepositoryRetentionPolicy, expectedVersion string) (RepositoryRetentionPolicy, error) {
	if policy.SnapshotKeepDays == 0 {
		policy.SnapshotKeepDays = policy.KeepDays
	}
	if policy.CoordinatePatterns == nil {
		policy.CoordinatePatterns = []string{}
	}
	if policy.ProtectedPatterns == nil {
		policy.ProtectedPatterns = []string{}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RepositoryRetentionPolicy{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM hosted_repositories WHERE id::text=$1)`, repositoryID).Scan(&exists); err != nil {
		return RepositoryRetentionPolicy{}, err
	}
	if !exists {
		return RepositoryRetentionPolicy{}, ErrNotFound
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO repository_retention_policies (repository_id,version,enabled,keep_days,snapshot_keep_days,minimum_versions,maximum_versions,coordinate_patterns,protected_patterns) VALUES ($1,1,false,30,30,1,0,'{}','{}') ON CONFLICT DO NOTHING`, repositoryID); err != nil {
		return RepositoryRetentionPolicy{}, err
	}
	err = tx.QueryRowContext(ctx, `UPDATE repository_retention_policies SET version=version+1,enabled=$3,keep_days=$4,snapshot_keep_days=$5,minimum_versions=$6,maximum_versions=$7,coordinate_patterns=COALESCE($8::text[],'{}'),protected_patterns=COALESCE($9::text[],'{}') WHERE repository_id::text=$1 AND version::text=$2 RETURNING version::text`, repositoryID, expectedVersion, policy.Enabled, policy.KeepDays, policy.SnapshotKeepDays, policy.MinimumVersions, policy.MaximumVersions, policy.CoordinatePatterns, policy.ProtectedPatterns).Scan(&policy.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return RepositoryRetentionPolicy{}, ErrVersionConflict
	}
	if err != nil {
		return RepositoryRetentionPolicy{}, err
	}
	if err = tx.Commit(); err != nil {
		return RepositoryRetentionPolicy{}, err
	}
	return policy, nil
}
