package repository

import (
	"context"
	"time"
)

func (s *PostgresStore) RecordAudit(ctx context.Context, audit AuditRecord) error {
	if audit.OccurredAt.IsZero() {
		audit.OccurredAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO resolver_audit_log (group_name, repository, member_name, outcome, actor, occurred_at, format, resource, representation, member_type, upstream_host, operation, http_status, cache_disposition, bytes, authorization_source, authorization_reason, request_id, trace_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`, audit.GroupName, audit.Repository, audit.MemberName, audit.Outcome, audit.Actor, audit.OccurredAt, audit.Format, audit.Resource, audit.Representation, audit.MemberType, audit.UpstreamHost, audit.Operation, audit.Status, audit.CacheDisposition, audit.Bytes, audit.AuthorizationSource, audit.AuthorizationReason, audit.RequestID, audit.TraceID)
	return err
}

func (s *PostgresStore) ListAudits(ctx context.Context, query AuditQuery) ([]AuditRecord, error) {
	limit := query.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT group_name, repository, member_name, outcome, actor, occurred_at,
		COALESCE(format, ''), COALESCE(resource, ''), COALESCE(representation, ''), COALESCE(member_type, ''), COALESCE(upstream_host, ''), COALESCE(operation, ''),
		COALESCE(http_status, 0), COALESCE(cache_disposition, ''), COALESCE(bytes, 0), COALESCE(authorization_source, ''), COALESCE(authorization_reason, ''), COALESCE(request_id, ''), COALESCE(trace_id, '')
		FROM resolver_audit_log
		WHERE ($1 = '' OR group_name = $1) AND ($2 = '' OR repository = $2)
		ORDER BY occurred_at DESC, id DESC
		LIMIT $3`, query.GroupName, query.Repository, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var audits []AuditRecord
	for rows.Next() {
		var audit AuditRecord
		if err := rows.Scan(&audit.GroupName, &audit.Repository, &audit.MemberName, &audit.Outcome, &audit.Actor, &audit.OccurredAt, &audit.Format, &audit.Resource, &audit.Representation, &audit.MemberType, &audit.UpstreamHost, &audit.Operation, &audit.Status, &audit.CacheDisposition, &audit.Bytes, &audit.AuthorizationSource, &audit.AuthorizationReason, &audit.RequestID, &audit.TraceID); err != nil {
			return nil, err
		}
		audits = append(audits, audit)
	}
	return audits, rows.Err()
}
