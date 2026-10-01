package repository

import (
	"context"
	"database/sql"
	"errors"
	"sort"

	"github.com/artifact-gateway/artifact-gateway/internal/protocol/cargo"
)

func (s *PostgresStore) ReconcileCargoGroupIndex(ctx context.Context, groupID, name string, candidates []CargoGroupVersion) ([]CargoGroupVersion, error) {
	identity, err := cargo.NormalizeIdentity(name, "0.0.0")
	if err != nil || groupID == "" {
		return nil, ErrInvalidCargoIdentity
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var format Format
	err = tx.QueryRowContext(ctx, `SELECT format FROM hosted_groups WHERE id::text=$1 FOR UPDATE`, groupID).Scan(&format)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if format != FormatCargo {
		return nil, ErrNotFound
	}
	memberRows, err := tx.QueryContext(ctx, `SELECT r.id::text FROM hosted_group_members m
		JOIN hosted_repositories r ON r.id=m.repository_id
		WHERE m.group_id::text=$1 AND r.format='cargo' AND r.state='active'`, groupID)
	if err != nil {
		return nil, err
	}
	members := make(map[string]bool)
	for memberRows.Next() {
		var id string
		if err := memberRows.Scan(&id); err != nil {
			_ = memberRows.Close()
			return nil, err
		}
		members[id] = true
	}
	err = memberRows.Err()
	_ = memberRows.Close()
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT group_id::text,source_repository_id::text,name,version,checksum,index_row
		FROM native_cargo_group_versions WHERE group_id::text=$1 AND collision_key=$2 FOR UPDATE`, groupID, identity.CollisionKey)
	if err != nil {
		return nil, err
	}
	previous := make([]CargoGroupVersion, 0)
	for rows.Next() {
		var value CargoGroupVersion
		if err := rows.Scan(&value.GroupID, &value.SourceRepositoryID, &value.Name, &value.Version, &value.Checksum, &value.IndexRow); err != nil {
			_ = rows.Close()
			return nil, err
		}
		previous = append(previous, value)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	selected, err := reconcileCargoGroupVersions(groupID, name, candidates, previous, members)
	if err != nil {
		return nil, err
	}
	for _, value := range selected {
		version, err := cargo.NormalizeIdentity(value.Name, value.Version)
		if err != nil {
			return nil, ErrInvalidCargoIdentity
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO native_cargo_group_versions
			(group_id,collision_key,version_key,source_repository_id,name,version,checksum,index_row)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (group_id,collision_key,version_key) DO UPDATE SET index_row=EXCLUDED.index_row`,
			groupID, identity.CollisionKey, version.VersionKey, value.SourceRepositoryID,
			value.Name, value.Version, value.Checksum, value.IndexRow)
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return selected, nil
}

func (s *PostgresStore) GetCargoGroupVersion(ctx context.Context, groupID, name, version string) (CargoGroupVersion, error) {
	identity, err := cargo.NormalizeIdentity(name, version)
	if err != nil || groupID == "" {
		return CargoGroupVersion{}, ErrInvalidCargoIdentity
	}
	var value CargoGroupVersion
	err = s.db.QueryRowContext(ctx, `SELECT group_id::text,source_repository_id::text,name,version,checksum,index_row
		FROM native_cargo_group_versions WHERE group_id::text=$1 AND collision_key=$2 AND version_key=$3`,
		groupID, identity.CollisionKey, identity.VersionKey).
		Scan(&value.GroupID, &value.SourceRepositoryID, &value.Name, &value.Version, &value.Checksum, &value.IndexRow)
	if errors.Is(err, sql.ErrNoRows) {
		return CargoGroupVersion{}, ErrNotFound
	}
	return value, err
}

func (s *PostgresStore) ListCargoGroupVersions(ctx context.Context, groupID, name string) ([]CargoGroupVersion, error) {
	identity, err := cargo.NormalizeIdentity(name, "0.0.0")
	if err != nil || groupID == "" {
		return nil, ErrInvalidCargoIdentity
	}
	rows, err := s.db.QueryContext(ctx, `SELECT group_id::text,source_repository_id::text,name,version,checksum,index_row
		FROM native_cargo_group_versions WHERE group_id::text=$1 AND collision_key=$2 ORDER BY version_key`, groupID, identity.CollisionKey)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]CargoGroupVersion, 0)
	for rows.Next() {
		var value CargoGroupVersion
		if err := rows.Scan(&value.GroupID, &value.SourceRepositoryID, &value.Name, &value.Version, &value.Checksum, &value.IndexRow); err != nil {
			return nil, err
		}
		items = append(items, value)
	}
	return items, rows.Err()
}

// Group mutations take member repository locks before the group row lock. A
// concurrent Hosted publication or Proxy index write takes the repository
// lock first, so the preflight sees a stable set of known coordinates.
func lockCargoGroupRepositories(ctx context.Context, tx *sql.Tx, memberSets ...[]GroupMember) error {
	set := make(map[string]bool)
	for _, members := range memberSets {
		for _, member := range members {
			set[member.RepositoryID] = true
		}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		var found string
		if err := tx.QueryRowContext(ctx, `SELECT id::text FROM hosted_repositories WHERE id::text=$1 FOR UPDATE`, id).Scan(&found); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
	}
	return nil
}

func preflightPostgresCargoGroupMembers(ctx context.Context, tx *sql.Tx, groupID string, members []GroupMember, additional ...cargoGroupKnownIndex) error {
	preflight := newCargoGroupPreflight(groupID)
	ownerRows, err := tx.QueryContext(ctx, `SELECT group_id::text,source_repository_id::text,name,version,checksum,index_row
		FROM native_cargo_group_versions WHERE group_id::text=$1`, groupID)
	if err != nil {
		return err
	}
	for ownerRows.Next() {
		var owner CargoGroupVersion
		if err := ownerRows.Scan(&owner.GroupID, &owner.SourceRepositoryID, &owner.Name, &owner.Version, &owner.Checksum, &owner.IndexRow); err != nil {
			_ = ownerRows.Close()
			return err
		}
		if err := preflight.addVersion(owner); err != nil {
			_ = ownerRows.Close()
			return err
		}
	}
	err = ownerRows.Err()
	_ = ownerRows.Close()
	if err != nil {
		return err
	}
	for _, member := range members {
		for _, query := range []string{
			`SELECT index_row FROM native_cargo_publications WHERE repository_id::text=$1`,
			`SELECT body FROM native_cargo_proxy_indexes WHERE repository_id::text=$1 AND status=200`,
		} {
			rows, err := tx.QueryContext(ctx, query, member.RepositoryID)
			if err != nil {
				return err
			}
			for rows.Next() {
				var body []byte
				if err := rows.Scan(&body); err != nil {
					_ = rows.Close()
					return err
				}
				if err := preflight.addIndex(cargoGroupKnownIndex{RepositoryID: member.RepositoryID, Body: body}); err != nil {
					_ = rows.Close()
					return err
				}
			}
			err = rows.Err()
			_ = rows.Close()
			if err != nil {
				return err
			}
		}
	}
	for _, index := range additional {
		if err := preflight.addIndex(index); err != nil {
			return err
		}
	}
	return nil
}
