package repository

import "context"

func (s *MemoryStore) RecordAudit(_ context.Context, record AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Audits = append(s.Audits, record)
	return nil
}

func (s *MemoryStore) ListAudits(_ context.Context, query AuditQuery) ([]AuditRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}
	records := make([]AuditRecord, 0, limit)
	for i := len(s.Audits) - 1; i >= 0 && len(records) < limit; i-- {
		record := s.Audits[i]
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
	return records, nil
}
