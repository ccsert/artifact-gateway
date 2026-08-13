package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

func (s *PostgresStore) RecordAudit(ctx context.Context, audit AuditRecord) error {
	return insertAudit(ctx, s.db, audit)
}

type auditExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertAudit(ctx context.Context, execer auditExecer, audit AuditRecord) error {
	if audit.OccurredAt.IsZero() {
		audit.OccurredAt = time.Now().UTC()
	}
	evidence := []byte("{}")
	if len(audit.Evidence) > 0 {
		encoded, err := json.Marshal(audit.Evidence)
		if err != nil {
			return err
		}
		evidence = encoded
	}
	_, err := execer.ExecContext(ctx, `INSERT INTO resolver_audit_log (group_name, repository, member_name, outcome, actor, occurred_at, format, resource, representation, member_type, upstream_host, operation, http_status, cache_disposition, bytes, authorization_source, authorization_reason, request_id, trace_id, evidence) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20::jsonb)`, audit.GroupName, audit.Repository, audit.MemberName, audit.Outcome, audit.Actor, audit.OccurredAt, audit.Format, audit.Resource, audit.Representation, audit.MemberType, audit.UpstreamHost, audit.Operation, audit.Status, audit.CacheDisposition, audit.Bytes, audit.AuthorizationSource, audit.AuthorizationReason, audit.RequestID, audit.TraceID, evidence)
	return err
}

func (s *PostgresStore) ListAudits(ctx context.Context, query AuditQuery) ([]AuditRecord, error) {
	page, err := s.ListAuditPage(ctx, query)
	return page.Items, err
}

func (s *PostgresStore) ListAuditPage(ctx context.Context, query AuditQuery) (AuditPage, error) {
	limit := query.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var from any
	if !query.From.IsZero() {
		from = query.From
	}
	var to any
	if !query.To.IsZero() {
		to = query.To
	}
	var before any
	if !query.Before.OccurredAt.IsZero() {
		before = query.Before.OccurredAt
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, group_name, repository, member_name, outcome, actor, occurred_at,
		COALESCE(format, ''), COALESCE(resource, ''), COALESCE(representation, ''), COALESCE(member_type, ''), COALESCE(upstream_host, ''), COALESCE(operation, ''),
		COALESCE(http_status, 0), COALESCE(cache_disposition, ''), COALESCE(bytes, 0), COALESCE(authorization_source, ''), COALESCE(authorization_reason, ''), COALESCE(request_id, ''), COALESCE(trace_id, ''), COALESCE(evidence, '{}'::jsonb)
		FROM resolver_audit_log
		WHERE ($1 = '' OR group_name = $1) AND ($2 = '' OR repository = $2)
		  AND ($3 = '' OR outcome = $3) AND ($4 = '' OR format = $4)
		  AND ($5 = '' OR operation = $5) AND ($6 = '' OR actor = $6)
		  AND ($7::timestamptz IS NULL OR occurred_at >= $7)
		  AND ($8::timestamptz IS NULL OR occurred_at <= $8)
		  AND ($9::timestamptz IS NULL OR occurred_at < $9 OR (occurred_at = $9 AND id < $10))
		ORDER BY occurred_at DESC, id DESC
		LIMIT $11`, query.GroupName, query.Repository, query.Outcome, query.Format, query.Operation, query.Actor, from, to, before, query.Before.ID, limit+1)
	if err != nil {
		return AuditPage{}, err
	}
	defer func() { _ = rows.Close() }()
	page := AuditPage{Items: make([]AuditRecord, 0, limit)}
	for rows.Next() {
		var audit AuditRecord
		var evidence []byte
		if err := rows.Scan(&audit.ID, &audit.GroupName, &audit.Repository, &audit.MemberName, &audit.Outcome, &audit.Actor, &audit.OccurredAt, &audit.Format, &audit.Resource, &audit.Representation, &audit.MemberType, &audit.UpstreamHost, &audit.Operation, &audit.Status, &audit.CacheDisposition, &audit.Bytes, &audit.AuthorizationSource, &audit.AuthorizationReason, &audit.RequestID, &audit.TraceID, &evidence); err != nil {
			return AuditPage{}, err
		}
		if err := json.Unmarshal(evidence, &audit.Evidence); err != nil {
			return AuditPage{}, err
		}
		page.Items = append(page.Items, audit)
	}
	if err := rows.Err(); err != nil {
		return AuditPage{}, err
	}
	if len(page.Items) > limit {
		last := page.Items[limit-1]
		page.Items = page.Items[:limit]
		page.Next = &AuditCursor{OccurredAt: last.OccurredAt, ID: last.ID}
	}
	return page, nil
}
