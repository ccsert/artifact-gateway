package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

const webhookSubscriptionColumns = `id::text,name,endpoint_url,secret_ciphertext,event_types,enabled,version::text,created_at,updated_at`
const webhookDeliveryColumns = `d.id::text,d.event_id::text,e.event_type,d.subscription_id::text,d.state,d.attempts,d.next_attempt_at,d.lease_owner,d.lease_token::text,d.lease_expires_at,d.last_status,d.last_error,d.created_at,d.updated_at,d.delivered_at`

func encodeWebhookEventTypes(values []WebhookEventType) ([]byte, error) {
	if values == nil {
		values = []WebhookEventType{}
	}
	return json.Marshal(values)
}

func scanWebhookSubscription(scanner interface{ Scan(...any) error }) (WebhookSubscription, error) {
	var value WebhookSubscription
	var eventTypes []byte
	if err := scanner.Scan(&value.ID, &value.Name, &value.EndpointURL, &value.SecretCiphertext, &eventTypes, &value.Enabled, &value.Version, &value.CreatedAt, &value.UpdatedAt); err != nil {
		return WebhookSubscription{}, err
	}
	if err := json.Unmarshal(eventTypes, &value.EventTypes); err != nil {
		return WebhookSubscription{}, err
	}
	return value, nil
}

func (s *PostgresStore) CreateWebhookSubscription(ctx context.Context, value WebhookSubscription) (WebhookSubscription, error) {
	eventTypes, err := encodeWebhookEventTypes(value.EventTypes)
	if err != nil {
		return WebhookSubscription{}, err
	}
	created, err := scanWebhookSubscription(s.db.QueryRowContext(ctx, `INSERT INTO webhook_subscriptions (id,name,endpoint_url,secret_ciphertext,event_types,enabled)
VALUES ($1,$2,$3,$4,$5::jsonb,$6) RETURNING `+webhookSubscriptionColumns, value.ID, value.Name, value.EndpointURL, value.SecretCiphertext, eventTypes, value.Enabled))
	if isUnique(err) {
		return WebhookSubscription{}, ErrWebhookSubscriptionNameExists
	}
	return created, err
}

func (s *PostgresStore) ListWebhookSubscriptions(ctx context.Context) ([]WebhookSubscription, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+webhookSubscriptionColumns+` FROM webhook_subscriptions ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	values := make([]WebhookSubscription, 0)
	for rows.Next() {
		value, scanErr := scanWebhookSubscription(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *PostgresStore) GetWebhookSubscription(ctx context.Context, id string) (WebhookSubscription, error) {
	value, err := scanWebhookSubscription(s.db.QueryRowContext(ctx, `SELECT `+webhookSubscriptionColumns+` FROM webhook_subscriptions WHERE id::text=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return WebhookSubscription{}, ErrNotFound
	}
	return value, err
}

func (s *PostgresStore) UpdateWebhookSubscription(ctx context.Context, value WebhookSubscription, expectedVersion string) (WebhookSubscription, error) {
	eventTypes, err := encodeWebhookEventTypes(value.EventTypes)
	if err != nil {
		return WebhookSubscription{}, err
	}
	updated, err := scanWebhookSubscription(s.db.QueryRowContext(ctx, `UPDATE webhook_subscriptions
SET name=$2,endpoint_url=$3,secret_ciphertext=$4,event_types=$5::jsonb,enabled=$6,version=version+1,updated_at=now()
WHERE id::text=$1 AND version::text=$7 RETURNING `+webhookSubscriptionColumns,
		value.ID, value.Name, value.EndpointURL, value.SecretCiphertext, eventTypes, value.Enabled, expectedVersion))
	if isUnique(err) {
		return WebhookSubscription{}, ErrWebhookSubscriptionNameExists
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return updated, err
	}
	var exists bool
	if queryErr := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM webhook_subscriptions WHERE id::text=$1)`, value.ID).Scan(&exists); queryErr != nil {
		return WebhookSubscription{}, queryErr
	}
	if exists {
		return WebhookSubscription{}, ErrVersionConflict
	}
	return WebhookSubscription{}, ErrNotFound
}

func (s *PostgresStore) EnqueueWebhookEvent(ctx context.Context, event WebhookEvent) (WebhookEvent, error) {
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WebhookEvent{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = enqueueWebhookEventPostgres(ctx, tx, event); err != nil {
		return WebhookEvent{}, err
	}
	if err = tx.Commit(); err != nil {
		return WebhookEvent{}, err
	}
	return event, nil
}

func enqueueWebhookEventPostgres(ctx context.Context, tx *sql.Tx, event WebhookEvent) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO webhook_events (id,event_type,occurred_at,payload) VALUES ($1,$2,$3,$4::jsonb)`, event.ID, event.Type, event.OccurredAt, event.Data); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO webhook_deliveries (id,event_id,subscription_id,state,next_attempt_at,created_at,updated_at)
SELECT gen_random_uuid(),$1,id,'pending',clock.now,clock.now,clock.now FROM webhook_subscriptions
CROSS JOIN (SELECT clock_timestamp() AS now) AS clock
WHERE enabled=true AND EXISTS (SELECT 1 FROM jsonb_array_elements_text(event_types) AS configured(value) WHERE configured.value=$2)`, event.ID, event.Type)
	return err
}

func scanWebhookDelivery(scanner interface{ Scan(...any) error }) (WebhookDelivery, error) {
	var value WebhookDelivery
	var nextAttemptAt, leaseExpiresAt, deliveredAt sql.NullTime
	var leaseOwner, leaseToken sql.NullString
	var lastStatus sql.NullInt64
	if err := scanner.Scan(&value.ID, &value.EventID, &value.EventType, &value.SubscriptionID, &value.State, &value.Attempts, &nextAttemptAt, &leaseOwner, &leaseToken, &leaseExpiresAt, &lastStatus, &value.LastError, &value.CreatedAt, &value.UpdatedAt, &deliveredAt); err != nil {
		return WebhookDelivery{}, err
	}
	if nextAttemptAt.Valid {
		value.NextAttemptAt = nextAttemptAt.Time
	}
	if leaseOwner.Valid {
		value.LeaseOwner = leaseOwner.String
	}
	if leaseToken.Valid {
		value.LeaseToken = leaseToken.String
	}
	if leaseExpiresAt.Valid {
		value.LeaseExpiresAt = leaseExpiresAt.Time
	}
	if lastStatus.Valid {
		value.LastStatus = int(lastStatus.Int64)
	}
	if deliveredAt.Valid {
		value.DeliveredAt = deliveredAt.Time
	}
	return value, nil
}

func (s *PostgresStore) ListWebhookDeliveries(ctx context.Context, query WebhookDeliveryQuery) ([]WebhookDelivery, error) {
	limit := query.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+webhookDeliveryColumns+` FROM webhook_deliveries d JOIN webhook_events e ON e.id=d.event_id
WHERE ($1='' OR d.subscription_id::text=$1) AND ($2='' OR d.state=$2) ORDER BY d.created_at DESC,d.id DESC LIMIT $3`, query.SubscriptionID, query.State, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	values := make([]WebhookDelivery, 0, limit)
	for rows.Next() {
		value, scanErr := scanWebhookDelivery(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *PostgresStore) GetWebhookDelivery(ctx context.Context, id string) (WebhookDelivery, error) {
	value, err := scanWebhookDelivery(s.db.QueryRowContext(ctx, `SELECT `+webhookDeliveryColumns+` FROM webhook_deliveries d JOIN webhook_events e ON e.id=d.event_id WHERE d.id::text=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return WebhookDelivery{}, ErrNotFound
	}
	return value, err
}

func (s *PostgresStore) ClaimWebhookDeliveries(ctx context.Context, owner string, _ time.Time, lease time.Duration, limit int) ([]WebhookDeliveryClaim, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `WITH clock AS MATERIALIZED (SELECT clock_timestamp() AS now), exhausted AS (
    UPDATE webhook_deliveries SET state='dead',next_attempt_at=NULL,lease_owner=NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=clock.now
    FROM clock WHERE state='delivering' AND lease_expires_at<=clock.now AND attempts>=8
), candidates AS (
    SELECT delivery.id FROM webhook_deliveries AS delivery CROSS JOIN clock
    WHERE ((state IN ('pending','retrying') AND next_attempt_at<=clock.now) OR (state='delivering' AND lease_expires_at<=clock.now))
      AND attempts<8 ORDER BY COALESCE(next_attempt_at,lease_expires_at),created_at,id FOR UPDATE OF delivery SKIP LOCKED LIMIT $3
), claimed AS (
    UPDATE webhook_deliveries AS delivery SET state='delivering',attempts=delivery.attempts+1,next_attempt_at=NULL,
        lease_owner=$1,lease_token=gen_random_uuid(),lease_expires_at=clock.now+$2::interval,updated_at=clock.now
    FROM candidates,clock WHERE delivery.id=candidates.id RETURNING delivery.*
)
SELECT d.id::text,d.event_id::text,e.event_type,d.subscription_id::text,d.state,d.attempts,d.next_attempt_at,d.lease_owner,d.lease_token::text,d.lease_expires_at,d.last_status,d.last_error,d.created_at,d.updated_at,d.delivered_at,
       e.id::text,e.event_type,e.occurred_at,e.payload,
       s.id::text,s.name,s.endpoint_url,s.secret_ciphertext,s.event_types,s.enabled,s.version::text,s.created_at,s.updated_at
FROM claimed d JOIN webhook_events e ON e.id=d.event_id JOIN webhook_subscriptions s ON s.id=d.subscription_id
ORDER BY d.created_at,d.id`, owner, lease.String(), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	claims := make([]WebhookDeliveryClaim, 0, limit)
	for rows.Next() {
		var delivery WebhookDelivery
		var event WebhookEvent
		var subscription WebhookSubscription
		var eventData, eventTypes []byte
		var nextAttemptAt, leaseExpiresAt, deliveredAt sql.NullTime
		var leaseOwner sql.NullString
		var lastStatus sql.NullInt64
		if err = rows.Scan(
			&delivery.ID, &delivery.EventID, &delivery.EventType, &delivery.SubscriptionID, &delivery.State, &delivery.Attempts, &nextAttemptAt, &leaseOwner, &delivery.LeaseToken, &leaseExpiresAt, &lastStatus, &delivery.LastError, &delivery.CreatedAt, &delivery.UpdatedAt, &deliveredAt,
			&event.ID, &event.Type, &event.OccurredAt, &eventData,
			&subscription.ID, &subscription.Name, &subscription.EndpointURL, &subscription.SecretCiphertext, &eventTypes, &subscription.Enabled, &subscription.Version, &subscription.CreatedAt, &subscription.UpdatedAt,
		); err != nil {
			return nil, err
		}
		delivery.LeaseOwner, delivery.LeaseExpiresAt = leaseOwner.String, leaseExpiresAt.Time
		if lastStatus.Valid {
			delivery.LastStatus = int(lastStatus.Int64)
		}
		if deliveredAt.Valid {
			delivery.DeliveredAt = deliveredAt.Time
		}
		event.Data = append([]byte(nil), eventData...)
		if err = json.Unmarshal(eventTypes, &subscription.EventTypes); err != nil {
			return nil, err
		}
		claims = append(claims, WebhookDeliveryClaim{Delivery: delivery, Event: event, Subscription: subscription})
	}
	return claims, rows.Err()
}

func (s *PostgresStore) CompleteWebhookDelivery(ctx context.Context, id, leaseToken string, status int, _ time.Time) error {
	result, err := s.db.ExecContext(ctx, `WITH clock AS MATERIALIZED (SELECT clock_timestamp() AS now)
UPDATE webhook_deliveries SET state='succeeded',next_attempt_at=NULL,lease_owner=NULL,lease_token=NULL,lease_expires_at=NULL,last_status=$3,last_error='',delivered_at=clock.now,updated_at=clock.now
FROM clock WHERE id::text=$1 AND state='delivering' AND lease_token::text=$2 AND lease_expires_at>clock.now`, id, leaseToken, status)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return ErrVersionConflict
	}
	return nil
}

func (s *PostgresStore) FailWebhookDelivery(ctx context.Context, id, leaseToken string, failedAt, retryAt time.Time, status int, message string, dead bool) error {
	retryDelay := retryAt.Sub(failedAt)
	if retryAt.IsZero() || retryDelay < 0 {
		retryDelay = 0
	}
	result, err := s.db.ExecContext(ctx, `WITH clock AS MATERIALIZED (SELECT clock_timestamp() AS now)
UPDATE webhook_deliveries SET state=CASE WHEN $3 OR attempts>=8 THEN 'dead' ELSE 'retrying' END,
    next_attempt_at=CASE WHEN $3 OR attempts>=8 THEN NULL ELSE clock.now+$4::interval END,
    lease_owner=NULL,lease_token=NULL,lease_expires_at=NULL,last_status=NULLIF($5,0),last_error=$6,updated_at=clock.now
FROM clock WHERE id::text=$1 AND state='delivering' AND lease_token::text=$2 AND lease_expires_at>clock.now`, id, leaseToken, dead, retryDelay.String(), status, message)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return ErrVersionConflict
	}
	return nil
}

func (s *PostgresStore) ReplayWebhookDelivery(ctx context.Context, id string, _ time.Time) (WebhookDelivery, error) {
	value, err := scanWebhookDelivery(s.db.QueryRowContext(ctx, `WITH clock AS MATERIALIZED (SELECT clock_timestamp() AS now), replayed AS (
UPDATE webhook_deliveries SET state='pending',attempts=0,next_attempt_at=clock.now,lease_owner=NULL,lease_token=NULL,lease_expires_at=NULL,last_status=NULL,last_error='',delivered_at=NULL,updated_at=clock.now
FROM clock WHERE id::text=$1 AND state='dead' RETURNING webhook_deliveries.*)
SELECT d.id::text,d.event_id::text,e.event_type,d.subscription_id::text,d.state,d.attempts,d.next_attempt_at,d.lease_owner,d.lease_token::text,d.lease_expires_at,d.last_status,d.last_error,d.created_at,d.updated_at,d.delivered_at
FROM replayed d JOIN webhook_events e ON e.id=d.event_id`, id))
	if !errors.Is(err, sql.ErrNoRows) {
		return value, err
	}
	var exists bool
	if queryErr := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM webhook_deliveries WHERE id::text=$1)`, id).Scan(&exists); queryErr != nil {
		return WebhookDelivery{}, queryErr
	}
	if exists {
		return WebhookDelivery{}, ErrInvalidWebhookDeliveryState
	}
	return WebhookDelivery{}, ErrNotFound
}

var _ WebhookStore = (*PostgresStore)(nil)
var _ WebhookStore = (*MemoryStore)(nil)
