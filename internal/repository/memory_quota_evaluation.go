package repository

import (
	"context"
	"github.com/artifact-gateway/artifact-gateway/internal/quotaalert"
	"sort"
	"time"
)

func (s *MemoryStore) EvaluateNextRepositoryQuotaAlert(ctx context.Context, cfg QuotaAlertMailConfig, interval time.Duration) (bool, error) {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	ids := make([]string, 0, len(s.quotaAlertRules))
	for id := range s.quotaAlertRules {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		rule := s.quotaAlertRules[id]
		if !rule.Enabled || rule.Deleted || (!rule.EvaluatedAt.IsZero() && now.Before(rule.EvaluatedAt.Add(interval))) {
			continue
		}
		repo, exists := s.hostedRepositories[rule.RepositoryID]
		o := quotaalert.Observation{DataState: "repository_deleted", SampleAt: now}
		if exists {
			o.DataState = "repository_inactive"
			if repo.State == RepositoryDeleted {
				o.DataState = "repository_deleted"
			}
		}
		if exists && repo.State == RepositoryActive {
			capacity, err := s.repositoryCapacityLocked(rule.RepositoryID)
			if err != nil {
				o.DataState = "unknown"
			} else {
				o.DataState = "available"
				o.UsedBytes, o.QuotaBytes = capacity.UsedBytes, capacity.QuotaBytes
			}
		}
		e := quotaTransition(&rule, o, repo.Name, now)
		if e != nil {
			target := s.emailTargets[rule.TargetID]
			record := RepositoryQuotaAlertEvent{Snapshot: *e, TargetID: rule.TargetID, TargetVersion: rule.TargetVersion, NotificationCode: quotaRouteCode(cfg, target, rule)}
			if record.NotificationCode == "queued" {
				active := 0
				for _, d := range s.emailDeliveries {
					if d.State != "accepted" && d.State != "dead" {
						active++
					}
				}
				d := quotaDelivery(*e, target, cfg, active >= 1000)
				s.emailDeliveries[d.ID] = d
				record.DeliveryID = d.ID
				if active >= 1000 {
					record.NotificationCode = "queue_full"
				}
			}
			if s.quotaAlertEvents == nil {
				s.quotaAlertEvents = make(map[string]RepositoryQuotaAlertEvent)
			}
			s.quotaAlertEvents[e.ID] = record
		}
		s.quotaAlertRules[id] = rule
		return true, nil
	}
	return false, nil
}
