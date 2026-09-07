-- Cache directory queries use the same authoritative control records as
-- protocol reads. No second projection or cache publication transaction exists.
-- Bound every index key so adding directory browsing cannot reject existing
-- long Raw paths or upstream URLs. Queries retain exact scope/path predicates.
CREATE INDEX cache_control_entries_proxy_browse_idx ON cache_control_entries (
    (md5(COALESCE(value->>'repository', value->>'Repository'))),
    (md5(COALESCE(value->>'endpoint', value->>'Endpoint'))),
    (left(COALESCE(value->>'path', value->>'Path'), 256)) text_pattern_ops
) WHERE key LIKE 'maven/index/%' OR key LIKE 'raw/index/%';
