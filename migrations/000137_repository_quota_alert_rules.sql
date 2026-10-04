-- Explicit finite repository logical quota policies. Deletion retains state;
-- repository_id deliberately has no cascading FK, so removal cannot fake recovery.
CREATE TABLE repository_quota_alert_rules (
 id uuid PRIMARY KEY,
 repository_id uuid NOT NULL,
 target_id uuid NOT NULL REFERENCES email_targets(id),
 target_version bigint NOT NULL,
 enabled boolean NOT NULL DEFAULT false,
 deleted boolean NOT NULL DEFAULT false,
 version bigint NOT NULL DEFAULT 1,
 state_version bigint NOT NULL DEFAULT 1,
 policy jsonb NOT NULL CHECK (jsonb_typeof(policy)='object' AND octet_length(policy::text)<=2048),
 state jsonb NOT NULL CHECK (jsonb_typeof(state)='object' AND octet_length(state::text)<=4096),
 active_episode_id uuid,
 last_event_id uuid,
 sequence bigint NOT NULL DEFAULT 0 CHECK (sequence>=0),
 evaluated_at timestamptz,
 next_evaluate_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK (policy ?& ARRAY['warningBasisPoints','criticalBasisPoints','recoveryBelowBasisPoints','warningForSeconds','criticalForSeconds','recoveryForSeconds','maxSampleAgeSeconds']),
 CHECK ((policy->>'recoveryBelowBasisPoints')::integer>0 AND (policy->>'recoveryBelowBasisPoints')::integer<(policy->>'warningBasisPoints')::integer AND (policy->>'warningBasisPoints')::integer<(policy->>'criticalBasisPoints')::integer AND (policy->>'criticalBasisPoints')::integer<=10000),
 CHECK ((policy->>'warningForSeconds')::integer BETWEEN 1 AND 86400 AND (policy->>'criticalForSeconds')::integer BETWEEN 1 AND 86400 AND (policy->>'recoveryForSeconds')::integer BETWEEN 1 AND 86400),
 CHECK ((policy->>'maxSampleAgeSeconds')::integer BETWEEN 30 AND 3600),
 CHECK (NOT deleted OR NOT enabled)
);
CREATE UNIQUE INDEX repository_quota_rule_scope ON repository_quota_alert_rules(repository_id) WHERE NOT deleted;
CREATE INDEX repository_quota_rule_due ON repository_quota_alert_rules(next_evaluate_at,id) WHERE enabled AND NOT deleted;
