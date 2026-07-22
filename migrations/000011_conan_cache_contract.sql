-- +goose Up
ALTER TABLE conan_groups ADD COLUMN IF NOT EXISTS cache_quota_bytes BIGINT NOT NULL DEFAULT 1073741824 CHECK (cache_quota_bytes > 0);
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'conan_group_proxy_allowlist_required') THEN
    ALTER TABLE conan_group_members ADD CONSTRAINT conan_group_proxy_allowlist_required CHECK (member_type <> 'proxy' OR cardinality(allowed_hosts) > 0) NOT VALID;
  END IF;
END
$$;

-- +goose Down
-- Conan V2 cache quota is additive; compensate forward rather than removing it.
