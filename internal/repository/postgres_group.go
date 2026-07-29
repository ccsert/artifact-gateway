package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func (s *PostgresStore) CreateGroup(ctx context.Context, group Group) (Group, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Group{}, err
	}
	defer func() { _ = tx.Rollback() }()
	normalizeGroup(&group)
	err = tx.QueryRowContext(ctx, `INSERT INTO oci_groups (name, enabled, anonymous) VALUES ($1, true, $2) RETURNING created_at`, group.Name, group.Anonymous).Scan(&group.CreatedAt)
	if err != nil {
		if isUnique(err) {
			return Group{}, ErrNameExists
		}
		return Group{}, err
	}
	for _, member := range group.Members {
		if _, err := tx.ExecContext(ctx, `INSERT INTO oci_group_members (group_name, name, member_type, endpoint, position, anonymous, allowed_hosts, repository_id) VALUES ($1,$2,$3,$4,$5,$6,COALESCE($7::text[], '{}'::text[]),NULLIF($8,'')::uuid)`, group.Name, member.Name, member.Type, member.Endpoint, member.Position, member.Anonymous, member.AllowedHosts, member.RepositoryID); err != nil {
			return Group{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Group{}, err
	}
	return group, nil
}

func (s *PostgresStore) GetGroup(ctx context.Context, name string) (Group, error) {
	var group Group
	if err := s.db.QueryRowContext(ctx, `SELECT name, enabled, anonymous, created_at FROM oci_groups WHERE name=$1`, name).Scan(&group.Name, &group.Enabled, &group.Anonymous, &group.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Group{}, ErrNotFound
		}
		return Group{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT name, member_type, endpoint, position, anonymous, array_to_json(allowed_hosts), COALESCE(repository_id::text, '') FROM oci_group_members WHERE group_name=$1 ORDER BY position`, name)
	if err != nil {
		return Group{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var member Member
		var allowedHosts []byte
		if err := rows.Scan(&member.Name, &member.Type, &member.Endpoint, &member.Position, &member.Anonymous, &allowedHosts, &member.RepositoryID); err != nil {
			return Group{}, err
		}
		if err := json.Unmarshal(allowedHosts, &member.AllowedHosts); err != nil {
			return Group{}, err
		}
		group.Members = append(group.Members, member)
	}
	return group, rows.Err()
}

// listGroups reads every group from the given tables. Table and column names
// are compile-time constants, never request input. withAllowedHosts must be
// false for tables without an allowed_hosts column (maven_group_members).
func (s *PostgresStore) listGroups(ctx context.Context, groupsTable, membersTable string, withQuota, withAllowedHosts bool) ([]Group, error) {
	groupQuery := `SELECT name, enabled, anonymous, created_at FROM ` + groupsTable + ` ORDER BY name`
	if withQuota {
		groupQuery = `SELECT name, enabled, anonymous, cache_quota_bytes, created_at FROM ` + groupsTable + ` ORDER BY name`
	}
	groupRows, err := s.db.QueryContext(ctx, groupQuery)
	if err != nil {
		return nil, err
	}
	defer func() { _ = groupRows.Close() }()
	groups := make([]Group, 0)
	for groupRows.Next() {
		var group Group
		if withQuota {
			if err := groupRows.Scan(&group.Name, &group.Enabled, &group.Anonymous, &group.CacheQuotaBytes, &group.CreatedAt); err != nil {
				return nil, err
			}
		} else if err := groupRows.Scan(&group.Name, &group.Enabled, &group.Anonymous, &group.CreatedAt); err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	if err := groupRows.Err(); err != nil {
		return nil, err
	}
	memberQuery := `SELECT name, member_type, endpoint, position, anonymous, array_to_json(allowed_hosts), COALESCE(repository_id::text, '') FROM ` + membersTable + ` WHERE group_name=$1 ORDER BY position`
	if !withAllowedHosts {
		memberQuery = `SELECT name, member_type, endpoint, position, anonymous, COALESCE(repository_id::text, '') FROM ` + membersTable + ` WHERE group_name=$1 ORDER BY position`
	}
	for i := range groups {
		rows, err := s.db.QueryContext(ctx, memberQuery, groups[i].Name)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var member Member
			if withAllowedHosts {
				var allowedHosts []byte
				if err := rows.Scan(&member.Name, &member.Type, &member.Endpoint, &member.Position, &member.Anonymous, &allowedHosts, &member.RepositoryID); err != nil {
					_ = rows.Close()
					return nil, err
				}
				if err := json.Unmarshal(allowedHosts, &member.AllowedHosts); err != nil {
					_ = rows.Close()
					return nil, err
				}
			} else if err := rows.Scan(&member.Name, &member.Type, &member.Endpoint, &member.Position, &member.Anonymous, &member.RepositoryID); err != nil {
				_ = rows.Close()
				return nil, err
			}
			groups[i].Members = append(groups[i].Members, member)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return groups, nil
}

func (s *PostgresStore) ListGroups(ctx context.Context) ([]Group, error) {
	return s.listGroups(ctx, "oci_groups", "oci_group_members", false, true)
}

func (s *PostgresStore) DisableGroup(ctx context.Context, name string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE oci_groups SET enabled=false WHERE name=$1`, name)
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

func (s *PostgresStore) CreateRawGroup(ctx context.Context, group Group) (Group, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Group{}, err
	}
	defer func() { _ = tx.Rollback() }()
	normalizeGroup(&group)
	err = tx.QueryRowContext(ctx, `INSERT INTO raw_groups (name,enabled,anonymous,cache_quota_bytes) VALUES ($1,true,$2,$3) RETURNING created_at`, group.Name, group.Anonymous, group.CacheQuotaBytes).Scan(&group.CreatedAt)
	if err != nil {
		if isUnique(err) {
			return Group{}, ErrNameExists
		}
		return Group{}, err
	}
	for _, m := range group.Members {
		if _, err := tx.ExecContext(ctx, `INSERT INTO raw_group_members (group_name,name,member_type,endpoint,position,anonymous,allowed_hosts,repository_id) VALUES ($1,$2,$3,$4,$5,$6,COALESCE($7::text[], '{}'::text[]),NULLIF($8,'')::uuid)`, group.Name, m.Name, m.Type, m.Endpoint, m.Position, m.Anonymous, m.AllowedHosts, m.RepositoryID); err != nil {
			return Group{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Group{}, err
	}
	return group, nil
}
func (s *PostgresStore) GetRawGroup(ctx context.Context, name string) (Group, error) {
	var g Group
	if err := s.db.QueryRowContext(ctx, `SELECT name,enabled,anonymous,cache_quota_bytes,created_at FROM raw_groups WHERE name=$1`, name).Scan(&g.Name, &g.Enabled, &g.Anonymous, &g.CacheQuotaBytes, &g.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Group{}, ErrNotFound
		}
		return Group{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT name,member_type,endpoint,position,anonymous,array_to_json(allowed_hosts),COALESCE(repository_id::text, '') FROM raw_group_members WHERE group_name=$1 ORDER BY position`, name)
	if err != nil {
		return Group{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var m Member
		var allowedHosts []byte
		if err := rows.Scan(&m.Name, &m.Type, &m.Endpoint, &m.Position, &m.Anonymous, &allowedHosts, &m.RepositoryID); err != nil {
			return Group{}, err
		}
		if err := json.Unmarshal(allowedHosts, &m.AllowedHosts); err != nil {
			return Group{}, err
		}
		g.Members = append(g.Members, m)
	}
	return g, rows.Err()
}
func (s *PostgresStore) ListRawGroups(ctx context.Context) ([]Group, error) {
	return s.listGroups(ctx, "raw_groups", "raw_group_members", true, true)
}
func (s *PostgresStore) DisableRawGroup(ctx context.Context, name string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE raw_groups SET enabled=false WHERE name=$1`, name)
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

func (s *PostgresStore) CreateMavenGroup(ctx context.Context, group Group) (Group, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Group{}, err
	}
	defer func() { _ = tx.Rollback() }()
	normalizeGroup(&group)
	err = tx.QueryRowContext(ctx, `INSERT INTO maven_groups (name, enabled, anonymous) VALUES ($1, true, $2) RETURNING created_at`, group.Name, group.Anonymous).Scan(&group.CreatedAt)
	if err != nil {
		if isUnique(err) {
			return Group{}, ErrNameExists
		}
		return Group{}, err
	}
	for _, member := range group.Members {
		if _, err := tx.ExecContext(ctx, `INSERT INTO maven_group_members (group_name, name, member_type, endpoint, position, anonymous, repository_id) VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,'')::uuid)`, group.Name, member.Name, member.Type, member.Endpoint, member.Position, member.Anonymous, member.RepositoryID); err != nil {
			return Group{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Group{}, err
	}
	return group, nil
}

func (s *PostgresStore) GetMavenGroup(ctx context.Context, name string) (Group, error) {
	var group Group
	if err := s.db.QueryRowContext(ctx, `SELECT name, enabled, anonymous, created_at FROM maven_groups WHERE name=$1`, name).Scan(&group.Name, &group.Enabled, &group.Anonymous, &group.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Group{}, ErrNotFound
		}
		return Group{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT name, member_type, endpoint, position, anonymous, COALESCE(repository_id::text, '') FROM maven_group_members WHERE group_name=$1 ORDER BY position`, name)
	if err != nil {
		return Group{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var member Member
		if err := rows.Scan(&member.Name, &member.Type, &member.Endpoint, &member.Position, &member.Anonymous, &member.RepositoryID); err != nil {
			return Group{}, err
		}
		group.Members = append(group.Members, member)
	}
	return group, rows.Err()
}

func (s *PostgresStore) ListMavenGroups(ctx context.Context) ([]Group, error) {
	return s.listGroups(ctx, "maven_groups", "maven_group_members", false, false)
}

func (s *PostgresStore) DisableMavenGroup(ctx context.Context, name string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE maven_groups SET enabled=false WHERE name=$1`, name)
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

func (s *PostgresStore) CreateConanGroup(ctx context.Context, group Group) (Group, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Group{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if group.CacheQuotaBytes == 0 {
		group.CacheQuotaBytes = 1 << 30
	}
	normalizeGroup(&group)
	err = tx.QueryRowContext(ctx, `INSERT INTO conan_groups (name, enabled, anonymous, cache_quota_bytes) VALUES ($1, true, $2, $3) RETURNING created_at`, group.Name, group.Anonymous, group.CacheQuotaBytes).Scan(&group.CreatedAt)
	if err != nil {
		if isUnique(err) {
			return Group{}, ErrNameExists
		}
		return Group{}, err
	}
	for _, member := range group.Members {
		if _, err := tx.ExecContext(ctx, `INSERT INTO conan_group_members (group_name, name, member_type, endpoint, position, anonymous, allowed_hosts, repository_id) VALUES ($1,$2,$3,$4,$5,$6,COALESCE($7::text[], '{}'::text[]),NULLIF($8,'')::uuid)`, group.Name, member.Name, member.Type, member.Endpoint, member.Position, member.Anonymous, member.AllowedHosts, member.RepositoryID); err != nil {
			return Group{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Group{}, err
	}
	return group, nil
}
func (s *PostgresStore) GetConanGroup(ctx context.Context, name string) (Group, error) {
	var group Group
	if err := s.db.QueryRowContext(ctx, `SELECT name, enabled, anonymous, cache_quota_bytes, created_at FROM conan_groups WHERE name=$1`, name).Scan(&group.Name, &group.Enabled, &group.Anonymous, &group.CacheQuotaBytes, &group.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Group{}, ErrNotFound
		}
		return Group{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT name, member_type, endpoint, position, anonymous, array_to_json(allowed_hosts), COALESCE(repository_id::text, '') FROM conan_group_members WHERE group_name=$1 ORDER BY position`, name)
	if err != nil {
		return Group{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var member Member
		var allowedHosts []byte
		if err := rows.Scan(&member.Name, &member.Type, &member.Endpoint, &member.Position, &member.Anonymous, &allowedHosts, &member.RepositoryID); err != nil {
			return Group{}, err
		}
		if err := json.Unmarshal(allowedHosts, &member.AllowedHosts); err != nil {
			return Group{}, err
		}
		group.Members = append(group.Members, member)
	}
	return group, rows.Err()
}
func (s *PostgresStore) ListConanGroups(ctx context.Context) ([]Group, error) {
	return s.listGroups(ctx, "conan_groups", "conan_group_members", true, true)
}
func (s *PostgresStore) DisableConanGroup(ctx context.Context, name string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE conan_groups SET enabled=false WHERE name=$1`, name)
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
