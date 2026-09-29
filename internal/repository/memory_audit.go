package repository

import (
	"context"
	"sort"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/requestcontext"
)

func (s *MemoryStore) RecordAudit(ctx context.Context, record AuditRecord) error {
	if ids, ok := requestcontext.FromContext(ctx); ok {
		if record.RequestID == "" {
			record.RequestID = ids.RequestID
		}
		if record.TraceID == "" {
			record.TraceID = ids.TraceID
		}
	}
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
	record.Evidence = cloneAuditEvidence(record.Evidence)
	s.Audits = append(s.Audits, record)
	if record.IsArtifactDownload() {
		s.recordArtifactUsageLocked(record.UsageIncrement())
	}
}

func cloneAuditEvidence(evidence map[string]string) map[string]string {
	if len(evidence) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(evidence))
	for key, value := range evidence {
		cloned[key] = value
	}
	return cloned
}

func (s *MemoryStore) ListAudits(_ context.Context, query AuditQuery) ([]AuditRecord, error) {
	page, err := s.ListAuditPage(context.Background(), query)
	return page.Items, err
}

func (s *MemoryStore) ListRepositoryRequestStatistics(_ context.Context, now time.Time) ([]RepositoryRequestStatistics, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now = now.UTC()
	cutoff1, cutoff7, cutoff30 := now.Add(-24*time.Hour), now.Add(-7*24*time.Hour), now.Add(-30*24*time.Hour)
	byRepository := make(map[string]*RepositoryRequestStatistics)
	for _, audit := range s.Audits {
		if audit.Repository == "" || audit.Format == "management" || audit.OccurredAt.Before(cutoff30) || audit.OccurredAt.After(now) {
			continue
		}
		item := byRepository[audit.Repository]
		if item == nil {
			item = &RepositoryRequestStatistics{Repository: audit.Repository}
			byRepository[audit.Repository] = item
		}
		denied := audit.Outcome == AuditAccessDenied || audit.Outcome == AuditProxyDenied || audit.Outcome == "denied"
		item.Requests.ThirtyDays++
		if denied {
			item.Denied.ThirtyDays++
		}
		if !audit.OccurredAt.Before(cutoff7) {
			item.Requests.SevenDays++
			if denied {
				item.Denied.SevenDays++
			}
		}
		if !audit.OccurredAt.Before(cutoff1) {
			item.Requests.OneDay++
			if denied {
				item.Denied.OneDay++
			}
		}
	}
	result := make([]RepositoryRequestStatistics, 0, len(byRepository))
	for _, item := range byRepository {
		result = append(result, *item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Repository < result[j].Repository })
	return result, nil
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
		record.Evidence = cloneAuditEvidence(record.Evidence)
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
