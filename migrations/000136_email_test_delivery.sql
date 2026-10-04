-- A finite email channel. Recipient snapshots are encrypted settings, never headers.
CREATE TABLE email_targets (
 id uuid PRIMARY KEY,
 name text NOT NULL CHECK (length(name) BETWEEN 1 AND 128),
 locale text NOT NULL CHECK (locale IN ('en','zh-CN')),
 recipient_ciphertext text NOT NULL,
 enabled boolean NOT NULL DEFAULT false,
 version bigint NOT NULL DEFAULT 1,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE email_test_deliveries (
 id uuid PRIMARY KEY,
 event_id uuid NOT NULL UNIQUE,
 request_key uuid NOT NULL UNIQUE,
 target_id uuid NOT NULL REFERENCES email_targets(id),
 target_version bigint NOT NULL,
 scenario text NOT NULL CHECK (scenario IN ('warning','critical','resolved')),
 locale text NOT NULL CHECK (locale IN ('en','zh-CN')),
 template_version text NOT NULL,
 recipient_ciphertext text NOT NULL,
 from_address text NOT NULL,
 console_origin text NOT NULL,
 state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','delivering','retrying','accepted','dead')),
 attempts integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 8),
 possible_duplicate boolean NOT NULL DEFAULT false,
 version bigint NOT NULL DEFAULT 1,
 error_code text NOT NULL DEFAULT '',
 lease_owner text NOT NULL DEFAULT '',
 lease_token uuid,
 lease_expires_at timestamptz,
 next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 accepted_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX email_test_delivery_due ON email_test_deliveries(next_attempt_at) WHERE state IN ('pending','retrying','delivering');
CREATE TABLE email_test_requests (id bigserial PRIMARY KEY, created_at timestamptz NOT NULL DEFAULT clock_timestamp());
CREATE INDEX email_test_requests_time ON email_test_requests(created_at);
