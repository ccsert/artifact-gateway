package repository

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"
)

func artifactQuarantineWebhookEvent(value ArtifactQuarantine) WebhookEvent {
	eventType := WebhookEventArtifactQuarantined
	if value.State == ArtifactQuarantineStateReleased {
		eventType = WebhookEventArtifactReleased
	}
	data, _ := json.Marshal(ArtifactQuarantineWebhookData{
		RepositoryID: value.RepositoryID, Format: value.Format, Coordinate: value.Coordinate,
		Digest: value.Digest, State: value.State, Reason: value.Reason, Actor: value.UpdatedBy, Version: value.Version,
	})
	return WebhookEvent{ID: uuid.NewString(), Type: eventType, OccurredAt: value.UpdatedAt, Data: data}
}

func cloneWebhookSubscription(value WebhookSubscription) WebhookSubscription {
	value.EventTypes = append([]WebhookEventType(nil), value.EventTypes...)
	return value
}

func cloneWebhookEvent(value WebhookEvent) WebhookEvent {
	value.Data = append([]byte(nil), value.Data...)
	return value
}

func (s *MemoryStore) CreateWebhookSubscription(_ context.Context, value WebhookSubscription) (WebhookSubscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.webhookSubscriptions {
		if existing.Name == value.Name {
			return WebhookSubscription{}, ErrWebhookSubscriptionNameExists
		}
	}
	now := time.Now().UTC()
	value.Version, value.CreatedAt, value.UpdatedAt = "1", now, now
	s.webhookSubscriptions[value.ID] = cloneWebhookSubscription(value)
	return cloneWebhookSubscription(value), nil
}

func (s *MemoryStore) ListWebhookSubscriptions(_ context.Context) ([]WebhookSubscription, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	values := make([]WebhookSubscription, 0, len(s.webhookSubscriptions))
	for _, value := range s.webhookSubscriptions {
		values = append(values, cloneWebhookSubscription(value))
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Name < values[j].Name })
	return values, nil
}

func (s *MemoryStore) GetWebhookSubscription(_ context.Context, id string) (WebhookSubscription, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.webhookSubscriptions[id]
	if !ok {
		return WebhookSubscription{}, ErrNotFound
	}
	return cloneWebhookSubscription(value), nil
}

func (s *MemoryStore) UpdateWebhookSubscription(_ context.Context, value WebhookSubscription, expectedVersion string) (WebhookSubscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.webhookSubscriptions[value.ID]
	if !ok {
		return WebhookSubscription{}, ErrNotFound
	}
	if current.Version != expectedVersion {
		return WebhookSubscription{}, ErrVersionConflict
	}
	for id, existing := range s.webhookSubscriptions {
		if id != value.ID && existing.Name == value.Name {
			return WebhookSubscription{}, ErrWebhookSubscriptionNameExists
		}
	}
	value.Version = nextHostedGroupVersion(current.Version)
	value.CreatedAt, value.UpdatedAt = current.CreatedAt, time.Now().UTC()
	s.webhookSubscriptions[value.ID] = cloneWebhookSubscription(value)
	return cloneWebhookSubscription(value), nil
}

func webhookSubscriptionAccepts(value WebhookSubscription, eventType WebhookEventType) bool {
	if !value.Enabled {
		return false
	}
	for _, candidate := range value.EventTypes {
		if candidate == eventType {
			return true
		}
	}
	return false
}

func (s *MemoryStore) EnqueueWebhookEvent(_ context.Context, event WebhookEvent) (WebhookEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enqueueWebhookEventLocked(event), nil
}

func (s *MemoryStore) enqueueWebhookEventLocked(event WebhookEvent) WebhookEvent {
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	s.webhookEvents[event.ID] = cloneWebhookEvent(event)
	for _, subscription := range s.webhookSubscriptions {
		if !webhookSubscriptionAccepts(subscription, event.Type) {
			continue
		}
		now := event.OccurredAt
		delivery := WebhookDelivery{ID: uuid.NewString(), EventID: event.ID, EventType: event.Type, SubscriptionID: subscription.ID, State: WebhookDeliveryPending, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now}
		s.webhookDeliveries[delivery.ID] = delivery
	}
	return cloneWebhookEvent(event)
}

func (s *MemoryStore) ListWebhookDeliveries(_ context.Context, query WebhookDeliveryQuery) ([]WebhookDelivery, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	limit := query.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	values := make([]WebhookDelivery, 0, len(s.webhookDeliveries))
	for _, value := range s.webhookDeliveries {
		if query.SubscriptionID != "" && value.SubscriptionID != query.SubscriptionID || query.State != "" && value.State != query.State {
			continue
		}
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].CreatedAt.Equal(values[j].CreatedAt) {
			return values[i].ID > values[j].ID
		}
		return values[i].CreatedAt.After(values[j].CreatedAt)
	})
	if len(values) > limit {
		values = values[:limit]
	}
	return values, nil
}

func (s *MemoryStore) GetWebhookDelivery(_ context.Context, id string) (WebhookDelivery, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.webhookDeliveries[id]
	if !ok {
		return WebhookDelivery{}, ErrNotFound
	}
	return value, nil
}

func (s *MemoryStore) ClaimWebhookDeliveries(_ context.Context, owner string, now time.Time, lease time.Duration, limit int) ([]WebhookDeliveryClaim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	candidates := make([]WebhookDelivery, 0)
	for _, value := range s.webhookDeliveries {
		due := (value.State == WebhookDeliveryPending || value.State == WebhookDeliveryRetrying) && !value.NextAttemptAt.After(now)
		expired := value.State == WebhookDeliveryDelivering && !value.LeaseExpiresAt.After(now)
		if expired && value.Attempts >= 8 {
			value.State, value.LeaseOwner, value.LeaseToken, value.LeaseExpiresAt, value.UpdatedAt = WebhookDeliveryDead, "", "", time.Time{}, now
			value.NextAttemptAt = time.Time{}
			s.webhookDeliveries[value.ID] = value
			continue
		}
		if due || expired {
			candidates = append(candidates, value)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].NextAttemptAt.Equal(candidates[j].NextAttemptAt) {
			return candidates[i].CreatedAt.Before(candidates[j].CreatedAt)
		}
		return candidates[i].NextAttemptAt.Before(candidates[j].NextAttemptAt)
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	claims := make([]WebhookDeliveryClaim, 0, len(candidates))
	for _, delivery := range candidates {
		delivery.State, delivery.Attempts, delivery.LeaseOwner, delivery.LeaseToken, delivery.LeaseExpiresAt, delivery.UpdatedAt = WebhookDeliveryDelivering, delivery.Attempts+1, owner, uuid.NewString(), now.Add(lease), now
		delivery.NextAttemptAt = time.Time{}
		s.webhookDeliveries[delivery.ID] = delivery
		event, eventOK := s.webhookEvents[delivery.EventID]
		subscription, subscriptionOK := s.webhookSubscriptions[delivery.SubscriptionID]
		if eventOK && subscriptionOK {
			claims = append(claims, WebhookDeliveryClaim{Delivery: delivery, Event: cloneWebhookEvent(event), Subscription: cloneWebhookSubscription(subscription)})
		}
	}
	return claims, nil
}

func (s *MemoryStore) CompleteWebhookDelivery(_ context.Context, id, leaseToken string, status int, completedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.webhookDeliveries[id]
	if !ok {
		return ErrNotFound
	}
	if value.State != WebhookDeliveryDelivering || value.LeaseToken != leaseToken || !value.LeaseExpiresAt.After(completedAt) {
		return ErrVersionConflict
	}
	value.State, value.LastStatus, value.DeliveredAt, value.UpdatedAt = WebhookDeliverySucceeded, status, completedAt, completedAt
	value.NextAttemptAt, value.LeaseExpiresAt, value.LeaseOwner, value.LeaseToken, value.LastError = time.Time{}, time.Time{}, "", "", ""
	s.webhookDeliveries[id] = value
	return nil
}

func (s *MemoryStore) FailWebhookDelivery(_ context.Context, id, leaseToken string, failedAt, retryAt time.Time, status int, message string, dead bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.webhookDeliveries[id]
	if !ok {
		return ErrNotFound
	}
	if value.State != WebhookDeliveryDelivering || value.LeaseToken != leaseToken || !value.LeaseExpiresAt.After(failedAt) {
		return ErrVersionConflict
	}
	value.State, value.NextAttemptAt = WebhookDeliveryRetrying, retryAt
	if dead || value.Attempts >= 8 {
		value.State, value.NextAttemptAt = WebhookDeliveryDead, time.Time{}
	}
	value.LastStatus, value.LastError, value.LeaseOwner, value.LeaseToken, value.LeaseExpiresAt, value.UpdatedAt = status, message, "", "", time.Time{}, failedAt
	s.webhookDeliveries[id] = value
	return nil
}

func (s *MemoryStore) ReplayWebhookDelivery(_ context.Context, id string, now time.Time) (WebhookDelivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.webhookDeliveries[id]
	if !ok {
		return WebhookDelivery{}, ErrNotFound
	}
	if value.State != WebhookDeliveryDead {
		return WebhookDelivery{}, ErrInvalidWebhookDeliveryState
	}
	value.State, value.Attempts, value.NextAttemptAt, value.LastStatus, value.LastError, value.UpdatedAt = WebhookDeliveryPending, 0, now, 0, "", now
	value.LeaseOwner, value.LeaseToken, value.LeaseExpiresAt = "", "", time.Time{}
	s.webhookDeliveries[id] = value
	return value, nil
}
