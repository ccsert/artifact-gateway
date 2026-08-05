#!/bin/sh
set -eu

# Read-only smoke check for local PostgreSQL capabilities introduced by the
# observability/search migrations.
docker compose exec -T postgres psql -X -U gateway -d gateway -v ON_ERROR_STOP=1 <<'SQL'
SELECT current_setting('server_version') AS server_version,
       current_setting('shared_preload_libraries') AS shared_preload_libraries;

SELECT extname
FROM pg_extension
WHERE extname IN ('pg_stat_statements', 'pg_trgm')
ORDER BY extname;

SELECT viewname
FROM pg_views
WHERE viewname = 'artifact_search_projection';

SELECT tgname
FROM pg_trigger
WHERE tgname LIKE '%notify%'
  AND NOT tgisinternal
ORDER BY tgname;

SELECT indexrelname
FROM pg_stat_user_indexes
WHERE indexrelname LIKE '%trgm%' OR indexrelname LIKE '%brin%'
ORDER BY indexrelname;
SQL
