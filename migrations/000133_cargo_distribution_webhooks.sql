-- +goose Up
-- Cargo distribution events are written in the same transaction as the
-- terminal operation state. A retrying attempt has not reached a final result.
ALTER TABLE webhook_subscriptions DROP CONSTRAINT webhook_subscriptions_event_types_check;
ALTER TABLE webhook_subscriptions ADD CONSTRAINT webhook_subscriptions_event_types_check CHECK (
    jsonb_typeof(event_types) = 'array'
    AND jsonb_array_length(event_types) > 0
    AND event_types <@ '["artifact.quarantined", "artifact.released", "cargo.promotion.completed", "cargo.promotion.failed", "cargo.replication.completed", "cargo.replication.failed"]'::jsonb
);
ALTER TABLE webhook_events DROP CONSTRAINT webhook_events_event_type_check;
ALTER TABLE webhook_events ADD CONSTRAINT webhook_events_event_type_check CHECK (
    event_type IN ('artifact.quarantined', 'artifact.released', 'cargo.promotion.completed', 'cargo.promotion.failed', 'cargo.replication.completed', 'cargo.replication.failed')
);

CREATE FUNCTION enqueue_cargo_distribution_webhook(kind TEXT, outcome TEXT, data JSONB, occurred TIMESTAMPTZ)
RETURNS VOID AS $$
DECLARE
    event_id UUID := gen_random_uuid();
    event_type TEXT := 'cargo.' || kind || '.' || outcome;
BEGIN
    INSERT INTO webhook_events (id,event_type,occurred_at,payload)
    VALUES (event_id,event_type,occurred,data);
    INSERT INTO webhook_deliveries (id,event_id,subscription_id,state,next_attempt_at,created_at,updated_at)
    SELECT gen_random_uuid(),event_id,id,'pending',clock.now,clock.now,clock.now
    FROM webhook_subscriptions CROSS JOIN (SELECT clock_timestamp() AS now) AS clock
    WHERE enabled=true AND event_types ? event_type;
END;
$$ LANGUAGE plpgsql;

CREATE FUNCTION cargo_promotion_state_webhook() RETURNS TRIGGER AS $$
BEGIN
    IF OLD.state='running' AND NEW.state IN ('completed','failed')
       AND NEW.kind='promotion' AND NEW.payload->>'format'='cargo' THEN
        PERFORM enqueue_cargo_distribution_webhook('promotion',NEW.state,jsonb_build_object(
            'operationId',NEW.id,'kind','promotion','format','cargo',
            'sourceRepositoryId',NEW.payload->>'sourceRepositoryId',
            'targetRepositoryId',NEW.repository_id,
            'coordinate',(NEW.payload->>'name') || '@' || (NEW.payload->>'version'),
            'digest',NEW.payload->>'digest','state',NEW.state,'attempts',NEW.attempts
        ),COALESCE(NEW.completed_at,clock_timestamp()));
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER cargo_promotion_state_webhook_after_update
    AFTER UPDATE ON lifecycle_jobs FOR EACH ROW
    EXECUTE FUNCTION cargo_promotion_state_webhook();

CREATE FUNCTION cargo_replication_state_webhook() RETURNS TRIGGER AS $$
BEGIN
    IF OLD.state='running' AND NEW.format='cargo'
       AND (NEW.state='completed' OR (NEW.state='failed' AND NEW.next_attempt_at IS NULL)) THEN
        PERFORM enqueue_cargo_distribution_webhook('replication',NEW.state,jsonb_build_object(
            'operationId',NEW.id,'kind','replication','format','cargo',
            'sourceRepositoryId',NEW.source_repository_id,
            'targetRepositoryId',NEW.target_repository_id,
            'coordinate',NEW.coordinate,'digest',NEW.digest,
            'state',NEW.state,'attempts',NEW.attempts
        ),COALESCE(NEW.completed_at,clock_timestamp()));
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER cargo_replication_state_webhook_after_update
    AFTER UPDATE ON replication_plans FOR EACH ROW
    EXECUTE FUNCTION cargo_replication_state_webhook();

-- +goose Down
-- Forward-only: deleting event types or triggers would orphan persisted outbox records.
