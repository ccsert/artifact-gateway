#!/bin/sh
set -eu

migration_dir=${MIGRATION_DIR:-/migrations}
pg_host=${PGHOST:-postgres}
pg_user=${PGUSER:-gateway}
pg_database=${PGDATABASE:-gateway}

run_psql() {
  psql -X -h "$pg_host" -U "$pg_user" -d "$pg_database" -v ON_ERROR_STOP=1 "$@"
}

file_sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
    return
  fi
  printf '%s\n' 'A SHA-256 utility (sha256sum or shasum) is required.' >&2
  exit 1
}

run_psql <<'SQL'
CREATE TABLE IF NOT EXISTS artifact_gateway_schema_migrations (
    filename TEXT PRIMARY KEY,
    checksum TEXT NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
SQL

for migration in "$migration_dir"/*.sql; do
  filename=${migration##*/}
  case "$filename" in
    *[!A-Za-z0-9._-]*)
      printf 'Unsupported migration filename: %s\n' "$filename" >&2
      exit 1
      ;;
  esac

  checksum=$(file_sha256 "$migration")
  recorded_checksum=$(run_psql -At -v migration_name="$filename" <<'SQL'
SELECT checksum
FROM artifact_gateway_schema_migrations
WHERE filename = :'migration_name';
SQL
  )

  if [ -n "$recorded_checksum" ]; then
    if [ "$recorded_checksum" != "$checksum" ]; then
      printf 'Applied migration %s has changed (recorded %s, current %s).\n' "$filename" "$recorded_checksum" "$checksum" >&2
      exit 1
    fi
    printf 'Skipping applied migration %s.\n' "$filename"
    continue
  fi

  printf 'Applying migration %s.\n' "$filename"
  {
    sed '/-- +goose Down/,$d' "$migration"
    printf "\nINSERT INTO artifact_gateway_schema_migrations (filename, checksum) VALUES ('%s', '%s');\n" "$filename" "$checksum"
  } | run_psql --single-transaction
done
