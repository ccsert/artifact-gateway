-- Extend the existing finite mail outbox; legacy test descriptors retain v1.
ALTER TABLE email_test_deliveries
 ADD COLUMN kind text NOT NULL DEFAULT 'test' CHECK (kind IN ('test','repository_quota')),
 ADD COLUMN quota_rule_id uuid REFERENCES repository_quota_alert_rules(id),
 ADD COLUMN episode_id uuid,
 ADD COLUMN event_sequence bigint,
 ADD COLUMN quota_descriptor jsonb,
 ADD CONSTRAINT email_quota_descriptor_shape CHECK (
  (kind='test' AND quota_rule_id IS NULL AND episode_id IS NULL AND event_sequence IS NULL AND quota_descriptor IS NULL)
  OR (kind='repository_quota' AND quota_rule_id IS NOT NULL AND episode_id IS NOT NULL AND event_sequence>0 AND quota_descriptor IS NOT NULL AND octet_length(quota_descriptor::text)<=8192)
 );
CREATE INDEX email_quota_order ON email_test_deliveries(quota_rule_id,event_sequence) WHERE kind='repository_quota';
CREATE TABLE repository_quota_alert_events (
 id uuid PRIMARY KEY,
 rule_id uuid NOT NULL REFERENCES repository_quota_alert_rules(id),
 rule_version bigint NOT NULL,
 episode_id uuid NOT NULL,
 sequence bigint NOT NULL CHECK(sequence>0),
 scenario text NOT NULL CHECK(scenario IN ('warning','critical','resolved')),
 snapshot jsonb NOT NULL CHECK(octet_length(snapshot::text)<=8192),
 target_id uuid NOT NULL REFERENCES email_targets(id),
 target_version bigint NOT NULL,
 delivery_id uuid REFERENCES email_test_deliveries(id),
 notification_code text NOT NULL CHECK(notification_code IN ('queued','queue_full','email_disabled','target_disabled','target_changed','target_unavailable')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(rule_id,sequence), UNIQUE(episode_id,scenario)
);
