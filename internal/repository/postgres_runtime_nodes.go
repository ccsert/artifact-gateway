package repository

import (
	"context"
	"encoding/json"
)

const runtimeNodeColumns = `instance_id,roles,worker_formats,worker_kinds,started_at,last_seen_at`

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
	if err := scanner.Scan(&node.InstanceID, &roles, &formats, &kinds, &node.StartedAt, &node.LastSeenAt); err != nil {
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
	_, err = s.db.ExecContext(ctx, `INSERT INTO runtime_nodes (`+runtimeNodeColumns+`)
VALUES ($1,$2::jsonb,$3::jsonb,$4::jsonb,$5,$6)
ON CONFLICT (instance_id) DO UPDATE SET roles=EXCLUDED.roles,worker_formats=EXCLUDED.worker_formats,worker_kinds=EXCLUDED.worker_kinds,started_at=EXCLUDED.started_at,last_seen_at=EXCLUDED.last_seen_at`, node.InstanceID, roles, formats, kinds, node.StartedAt, node.LastSeenAt)
	return err
}

func (s *PostgresStore) ListRuntimeNodes(ctx context.Context) ([]RuntimeNode, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+runtimeNodeColumns+` FROM runtime_nodes ORDER BY last_seen_at DESC,instance_id`)
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

var _ RuntimeNodeStore = (*PostgresStore)(nil)
var _ RuntimeNodeStore = (*MemoryStore)(nil)
