-- +goose Up
-- Default-disabled Helm preview; these are package-level durable jobs, not
-- Nexus migration tasks and not the legacy aggregate replication plan.
CREATE TABLE native_helm_replication_jobs (
 id UUID PRIMARY KEY,
 plan_digest TEXT NOT NULL CHECK(plan_digest ~ '^sha256:[0-9a-f]{64}$'),
 source_repository_id UUID NOT NULL REFERENCES hosted_repositories(id),
 target_repository_id UUID NOT NULL REFERENCES hosted_repositories(id),
 actor TEXT NOT NULL CHECK(length(actor) BETWEEN 1 AND 512),
 actor_kind TEXT NOT NULL CHECK(actor_kind IN ('static_admin','static_resolver','local_session','oidc','api_key','service_account_credential')),
 credential_id TEXT NOT NULL DEFAULT '',
 version BIGINT NOT NULL DEFAULT 1 CHECK(version>0),
 state TEXT NOT NULL CHECK(state IN ('queued','running','succeeded','failed')),
 lease_owner TEXT NOT NULL DEFAULT '',
 lease_token TEXT NOT NULL DEFAULT '',
 lease_fence BIGINT NOT NULL DEFAULT 0 CHECK(lease_fence>=0),
 lease_until TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CHECK(source_repository_id<>target_repository_id),
 CHECK((state='running')=(lease_owner<>'' AND lease_token<>'' AND lease_until IS NOT NULL))
);
CREATE INDEX native_helm_replication_claim ON native_helm_replication_jobs(created_at,id) WHERE state IN ('queued','running');
CREATE TABLE native_helm_replication_items (
 job_id UUID NOT NULL REFERENCES native_helm_replication_jobs(id),
 item_index INTEGER NOT NULL CHECK(item_index BETWEEN 0 AND 99),
 name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 63 AND name ~ '^[a-z0-9][a-z0-9-]*$'),
 version TEXT NOT NULL,
 digest TEXT NOT NULL CHECK(digest ~ '^sha256:[0-9a-f]{64}$'),
 size BIGINT NOT NULL CHECK(size BETWEEN 1 AND 67108864),
 provenance_digest TEXT NOT NULL DEFAULT '',
 provenance_size BIGINT NOT NULL DEFAULT 0 CHECK(provenance_size BETWEEN 0 AND 1048576),
 CHECK((provenance_size=0 AND provenance_digest='') OR (provenance_size>0 AND provenance_digest ~ '^sha256:[0-9a-f]{64}$')),
 source_governance_version BIGINT NOT NULL CHECK(source_governance_version>0),
 state TEXT NOT NULL CHECK(state IN ('pending','running','succeeded','failed')),
 attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts>=0),
 code TEXT NOT NULL DEFAULT '',
 target_session_id UUID REFERENCES native_helm_publication_sessions(id),
 result TEXT NOT NULL DEFAULT '' CHECK(result IN ('','published','already_present')),
 completed_at TIMESTAMPTZ,
 PRIMARY KEY(job_id,item_index),
 UNIQUE(job_id,name,version),
 CHECK((state IN ('succeeded','failed'))=(completed_at IS NOT NULL)),
 CHECK(state<>'succeeded' OR (target_session_id IS NOT NULL AND result<>''))
);
CREATE TABLE native_helm_replication_retries (
 job_id UUID NOT NULL REFERENCES native_helm_replication_jobs(id),
 request_key TEXT NOT NULL CHECK(length(request_key) BETWEEN 1 AND 200),
 payload_digest TEXT NOT NULL CHECK(payload_digest ~ '^[0-9a-f]{64}$'),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(job_id,request_key)
);
-- +goose Down
-- Forward-only: old workers do not understand durable publication fences.
