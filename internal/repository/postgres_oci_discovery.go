package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	discovery "github.com/ccsert/artifact-gateway/internal/ocidiscovery"
)

var _ discovery.Store = (*PostgresStore)(nil)

func checkPostgresOCIGuards(ctx context.Context, tx *sql.Tx, guards []discovery.Guard) error {
	if err := discovery.ValidateGuards(guards); err != nil {
		return err
	}
	for _, guard := range sortedOCIGuards(guards) {
		query := ""
		switch guard.Kind {
		case "group":
			query = `SELECT version::text FROM hosted_groups WHERE id=$1 AND format='oci' FOR SHARE`
		case "repository":
			query = `SELECT version::text FROM hosted_repositories WHERE id=$1 AND format='oci' AND state='active' FOR SHARE`
		case "grants":
			// Lock a physical default row, too: an unlocked missing row cannot
			// fence a concurrent first authorization change.
			if guard.Version == "1" {
				if _, err := tx.ExecContext(ctx, `INSERT INTO repository_grant_sets(repository_id,version) VALUES($1,1) ON CONFLICT DO NOTHING`, guard.ID); err != nil {
					return err
				}
			}
			query = `SELECT version::text FROM repository_grant_sets WHERE repository_id=$1 FOR SHARE`
		}
		var version string
		err := tx.QueryRowContext(ctx, query, guard.ID).Scan(&version)
		if errors.Is(err, sql.ErrNoRows) || err == nil && version != guard.Version {
			return discovery.ErrChanged
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *PostgresStore) SaveOCIDiscoverySnapshot(ctx context.Context, snapshot discovery.Snapshot) error {
	if err := discovery.ValidateSnapshot(snapshot, time.Now().UTC()); err != nil {
		return err
	}
	data, err := discovery.Payload(snapshot)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := checkPostgresOCIGuards(ctx, tx, snapshot.Guards); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "oci-discovery-snapshots:"+snapshot.Owner); err != nil {
		return err
	}
	var prior []byte
	err = tx.QueryRowContext(ctx, `SELECT payload FROM oci_discovery_snapshots WHERE id=$1`, snapshot.ID).Scan(&prior)
	if err == nil {
		var existing discovery.Snapshot
		if json.Unmarshal(prior, &existing) != nil || !sameOCISnapshot(existing, snapshot) {
			return discovery.ErrChanged
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM oci_discovery_snapshots WHERE id IN (SELECT id FROM oci_discovery_snapshots WHERE expires_at<=clock_timestamp() ORDER BY expires_at LIMIT 100)`); err != nil {
		return err
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM oci_discovery_snapshots WHERE owner=$1 AND expires_at>clock_timestamp()`, snapshot.Owner).Scan(&active); err != nil {
		return err
	}
	if active >= 32 {
		return discovery.ErrLimit
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO oci_discovery_snapshots(id,owner,kind,payload,expires_at) VALUES ($1,$2,$3,$4::jsonb,$5)`, snapshot.ID, snapshot.Owner, snapshot.Kind, string(data), snapshot.ExpiresAt); err != nil {
		return err
	}
	if err := insertAudit(ctx, tx, ociSnapshotAudit(snapshot)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) GetOCIDiscoverySnapshot(ctx context.Context, id, owner string) (discovery.Snapshot, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return discovery.Snapshot{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var data []byte
	err = tx.QueryRowContext(ctx, `SELECT payload FROM oci_discovery_snapshots WHERE id::text=$1 AND owner=$2 AND expires_at>clock_timestamp()`, id, owner).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return discovery.Snapshot{}, discovery.ErrNotFound
	}
	if err != nil {
		return discovery.Snapshot{}, err
	}
	var snapshot discovery.Snapshot
	if json.Unmarshal(data, &snapshot) != nil {
		return discovery.Snapshot{}, discovery.ErrInvalid
	}
	if err := checkPostgresOCIGuards(ctx, tx, snapshot.Guards); err != nil {
		return discovery.Snapshot{}, err
	}
	if err := tx.Commit(); err != nil {
		return discovery.Snapshot{}, err
	}
	return snapshot, nil
}

func (s *PostgresStore) PublishOCISubjectBinding(ctx context.Context, binding discovery.Binding) error {
	if err := discovery.ValidateBinding(binding); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := checkPostgresOCIGuards(ctx, tx, binding.Guards); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "oci-discovery-binding:"+binding.Scope+":"+binding.Owner+":"+binding.Image); err != nil {
		return err
	}
	var prior []byte
	err = tx.QueryRowContext(ctx, `SELECT payload FROM oci_subject_bindings WHERE scope=$1 AND owner=$2 AND image=$3 AND subject=$4 FOR UPDATE`, binding.Scope, binding.Owner, binding.Image, binding.Subject).Scan(&prior)
	if err == nil {
		var old discovery.Binding
		if json.Unmarshal(prior, &old) != nil {
			return discovery.ErrInvalid
		}
		binding, err = mergeOCIBinding(old, binding)
		if err != nil {
			return err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	data, err := discovery.Payload(binding)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO oci_subject_bindings(scope,owner,image,subject,source_id,source_version,payload) VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb) ON CONFLICT(scope,owner,image,subject) DO UPDATE SET payload=EXCLUDED.payload`, binding.Scope, binding.Owner, binding.Image, binding.Subject, binding.SourceID, binding.SourceVersion, string(data)); err != nil {
		return err
	}
	for _, object := range binding.Objects {
		encoded, err := discovery.Payload(object)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO oci_bound_objects(scope,owner,image,digest,subject,source_id,source_version,payload) VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb) ON CONFLICT DO NOTHING`, binding.Scope, binding.Owner, binding.Image, object.Digest, binding.Subject, binding.SourceID, binding.SourceVersion, string(encoded)); err != nil {
			return err
		}
		var sourceID, sourceVersion string
		var stored []byte
		if err := tx.QueryRowContext(ctx, `SELECT source_id::text,source_version::text,payload FROM oci_bound_objects WHERE scope=$1 AND owner=$2 AND image=$3 AND digest=$4`, binding.Scope, binding.Owner, binding.Image, object.Digest).Scan(&sourceID, &sourceVersion, &stored); err != nil {
			return err
		}
		var old discovery.Object
		if json.Unmarshal(stored, &old) != nil || sourceID != binding.SourceID || sourceVersion != binding.SourceVersion || old != object {
			return discovery.ErrChanged
		}
	}
	if err := insertAudit(ctx, tx, ociDiscoveryAudit(binding.Actor, binding.Scope, binding.Image, binding.Subject, "oci.referrers.bind")); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) GetOCISubjectBinding(ctx context.Context, scope, owner, image, subject string) (discovery.Binding, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM oci_subject_bindings WHERE scope=$1 AND owner=$2 AND image=$3 AND subject=$4`, scope, owner, image, subject).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return discovery.Binding{}, discovery.ErrNotFound
	}
	if err != nil {
		return discovery.Binding{}, err
	}
	var binding discovery.Binding
	if json.Unmarshal(data, &binding) != nil {
		return discovery.Binding{}, discovery.ErrInvalid
	}
	return binding, nil
}

func (s *PostgresStore) GetOCIBoundObject(ctx context.Context, scope, owner, image, digest string) (discovery.Binding, discovery.Object, error) {
	var bindingData, objectData []byte
	err := s.db.QueryRowContext(ctx, `SELECT b.payload,o.payload FROM oci_bound_objects o JOIN oci_subject_bindings b USING(scope,owner,image,subject) WHERE o.scope=$1 AND o.owner=$2 AND o.image=$3 AND o.digest=$4`, scope, owner, image, digest).Scan(&bindingData, &objectData)
	if errors.Is(err, sql.ErrNoRows) {
		return discovery.Binding{}, discovery.Object{}, discovery.ErrNotFound
	}
	if err != nil {
		return discovery.Binding{}, discovery.Object{}, err
	}
	var binding discovery.Binding
	var object discovery.Object
	if json.Unmarshal(bindingData, &binding) != nil || json.Unmarshal(objectData, &object) != nil {
		return discovery.Binding{}, discovery.Object{}, discovery.ErrInvalid
	}
	return binding, object, nil
}
