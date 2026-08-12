package repository

import (
	"context"
	"sort"
	"time"
)

func (s *MemoryStore) RecordAudit(_ context.Context, record AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendAuditLocked(record)
	return nil
}

func (s *MemoryStore) appendAuditLocked(record AuditRecord) {
	if record.OccurredAt.IsZero() {
		record.OccurredAt = time.Now().UTC()
	}
	if record.ID == 0 {
		record.ID = int64(len(s.Audits) + 1)
	}
	s.Audits = append(s.Audits, record)
}

func (s *MemoryStore) ListAudits(_ context.Context, query AuditQuery) ([]AuditRecord, error) {
	page, err := s.ListAuditPage(context.Background(), query)
	return page.Items, err
}

func (s *MemoryStore) ListAuditPage(_ context.Context, query AuditQuery) (AuditPage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	limit := query.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	records := make([]AuditRecord, 0, limit+1)
	for i := range s.Audits {
		record := s.Audits[i]
		if record.ID == 0 {
			record.ID = int64(i + 1)
		}
		if record.OccurredAt.IsZero() {
			record.OccurredAt = time.Unix(0, int64(i)).UTC()
		}
		if !query.From.IsZero() && record.OccurredAt.Before(query.From) {
			continue
		}
		if !query.To.IsZero() && record.OccurredAt.After(query.To) {
			continue
		}
		if !query.Before.OccurredAt.IsZero() && (record.OccurredAt.After(query.Before.OccurredAt) || (record.OccurredAt.Equal(query.Before.OccurredAt) && record.ID >= query.Before.ID)) {
			continue
		}
		if query.GroupName != "" && record.GroupName != query.GroupName {
			continue
		}
		if query.Repository != "" && record.Repository != query.Repository {
			continue
		}
		if query.Outcome != "" && string(record.Outcome) != query.Outcome {
			continue
		}
		if query.Format != "" && record.Format != query.Format {
			continue
		}
		if query.Operation != "" && record.Operation != query.Operation {
			continue
		}
		if query.Actor != "" && record.Actor != query.Actor {
			continue
		}
		records = append(records, record)
	}
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].OccurredAt.Equal(records[j].OccurredAt) {
			return records[i].ID > records[j].ID
		}
		return records[i].OccurredAt.After(records[j].OccurredAt)
	})
	page := AuditPage{Items: records}
	if len(records) > limit {
		last := records[limit-1]
		page.Items = records[:limit]
		page.Next = &AuditCursor{OccurredAt: last.OccurredAt, ID: last.ID}
	}
	return page, nil
}
