-- Keep an in-flight token's one attempt, while durably revoking later automatic attempts.
ALTER TABLE email_test_deliveries
 ADD COLUMN automatic_cancellation_code text NOT NULL DEFAULT ''
 CHECK(automatic_cancellation_code IN ('','rule_changed','rule_disabled','rule_deleted')),
 ADD CONSTRAINT email_quota_sequence_required CHECK(kind<>'repository_quota' OR event_sequence IS NOT NULL);
ALTER TABLE repository_quota_alert_events
 DROP CONSTRAINT repository_quota_alert_events_notification_code_check,
 ADD CONSTRAINT repository_quota_alert_events_notification_code_check
 CHECK(notification_code IN ('queued','queue_full','email_disabled','encryption_key_unavailable','target_disabled','target_changed','target_unavailable'));
