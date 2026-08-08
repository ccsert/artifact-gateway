package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

const runtimeNodeSessionColumns = `session_id,instance_id,roles,worker_formats,worker_kinds,started_at,last_seen_at,stopped_at`

func encodeRuntimeNodeValues(node RuntimeNode) ([]byte, []byte, []byte, error) {
	roles, err := json.Marshal(node.Roles)
	if err != nil {
		return nil, nil, nil, err
	}
	formats, err := json.Marshal(node.WorkerFormats)
	if err != nil {
		return nil, nil, nil, err
	}
	kinds, err := json.Marshal(node.WorkerKinds)
	if err != nil {
		return nil, nil, nil, err
	}
	return roles, formats, kinds, nil
}

func scanRuntimeNode(scanner interface{ Scan(...any) error }) (RuntimeNode, error) {
	var node RuntimeNode
	var roles, formats, kinds []byte
	var stoppedAt sql.NullTime
	if err := scanner.Scan(&node.SessionID, &node.InstanceID, &roles, &formats, &kinds, &node.StartedAt, &node.LastSeenAt, &stoppedAt); err != nil {
		return RuntimeNode{}, err
	}
	if err := json.Unmarshal(roles, &node.Roles); err != nil {
		return RuntimeNode{}, err
	}
	if err := json.Unmarshal(formats, &node.WorkerFormats); err != nil {
		return RuntimeNode{}, err
	}
	if err := json.Unmarshal(kinds, &node.WorkerKinds); err != nil {
		return RuntimeNode{}, err
	}
	if node.Roles == nil {
		node.Roles = []string{}
	}
	if node.WorkerFormats == nil {
		node.WorkerFormats = []string{}
	}
	if node.WorkerKinds == nil {
		node.WorkerKinds = []string{}
	}
	if stoppedAt.Valid {
		node.StoppedAt = stoppedAt.Time
	}
	return node, nil
}

func (s *PostgresStore) UpsertRuntimeNodeHeartbeat(ctx context.Context, node RuntimeNode) error {
	if err := node.Validate(); err != nil {
		return err
	}
	roles, formats, kinds, err := encodeRuntimeNodeValues(node)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `INSERT INTO runtime_node_sessions (`+runtimeNodeSessionColumns+`)
VALUES ($1,$2,$3::jsonb,$4::jsonb,$5::jsonb,$6,$7,NULL)
ON CONFLICT (session_id) DO UPDATE SET roles=EXCLUDED.roles,worker_formats=EXCLUDED.worker_formats,worker_kinds=EXCLUDED.worker_kinds,last_seen_at=EXCLUDED.last_seen_at,stopped_at=NULL
WHERE runtime_node_sessions.instance_id=EXCLUDED.instance_id AND runtime_node_sessions.started_at=EXCLUDED.started_at`, node.SessionID, node.InstanceID, roles, formats, kinds, node.StartedAt, node.LastSeenAt)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return ErrInvalidRuntimeNode
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO runtime_nodes (instance_id,roles,worker_formats,worker_kinds,started_at,last_seen_at)
VALUES ($1,$2::jsonb,$3::jsonb,$4::jsonb,$5,$6)
ON CONFLICT (instance_id) DO UPDATE SET roles=EXCLUDED.roles,worker_formats=EXCLUDED.worker_formats,worker_kinds=EXCLUDED.worker_kinds,started_at=EXCLUDED.started_at,last_seen_at=EXCLUDED.last_seen_at`, node.InstanceID, roles, formats, kinds, node.StartedAt, node.LastSeenAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) MarkRuntimeNodeStopped(ctx context.Context, instanceID, sessionID string, stoppedAt time.Time) error {
	if instanceID == "" || sessionID == "" || stoppedAt.IsZero() {
		return ErrInvalidRuntimeNode
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `UPDATE runtime_node_sessions
SET stopped_at=$3,last_seen_at=GREATEST(last_seen_at,$3)
WHERE instance_id=$1 AND session_id=$2`, instanceID, sessionID, stoppedAt); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE runtime_nodes AS legacy
SET last_seen_at=GREATEST(legacy.last_seen_at,$3)
FROM runtime_node_sessions AS session
WHERE session.instance_id=$1 AND session.session_id=$2
AND legacy.instance_id=session.instance_id AND legacy.started_at=session.started_at`, instanceID, sessionID, stoppedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) ListRuntimeNodes(ctx context.Context) ([]RuntimeNode, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+runtimeNodeSessionColumns+` FROM runtime_node_sessions
UNION ALL
SELECT 'legacy:'||legacy.instance_id,legacy.instance_id,legacy.roles,legacy.worker_formats,legacy.worker_kinds,legacy.started_at,legacy.last_seen_at,NULL
FROM runtime_nodes AS legacy
WHERE NOT EXISTS (
    SELECT 1 FROM runtime_node_sessions AS session
    WHERE session.instance_id=legacy.instance_id AND session.started_at=legacy.started_at
)
ORDER BY last_seen_at DESC,instance_id,session_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	nodes := make([]RuntimeNode, 0)
	for rows.Next() {
		node, scanErr := scanRuntimeNode(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

func (s *PostgresStore) PruneRuntimeNodes(ctx context.Context, before time.Time) (int64, error) {
	if before.IsZero() {
		return 0, ErrInvalidRuntimeNode
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	sessions, err := tx.ExecContext(ctx, `DELETE FROM runtime_node_sessions WHERE last_seen_at < $1`, before)
	if err != nil {
		return 0, err
	}
	legacy, err := tx.ExecContext(ctx, `DELETE FROM runtime_nodes WHERE last_seen_at < $1`, before)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	sessionCount, _ := sessions.RowsAffected()
	legacyCount, _ := legacy.RowsAffected()
	return sessionCount + legacyCount, nil
}

var _ RuntimeNodeStore = (*PostgresStore)(nil)
var _ RuntimeNodeStore = (*MemoryStore)(nil)
