package backupops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Postgres is explicit operational connection data. Credentials stay in process
// memory/environment; subprocess arguments and public errors do not include DSNs.
type Postgres struct {
	DSN            string `json:"dsn"`
	ToolsContainer string `json:"toolsContainer,omitempty"`
	DockerContext  string `json:"dockerContext,omitempty"`
}

func (p Postgres) Ledger(ctx context.Context) ([]byte, error) {
	conn, err := p.connect(ctx)
	if err != nil {
		return nil, errors.New("database unavailable")
	}
	defer func() { _ = conn.Close(ctx) }()
	rows, err := conn.Query(ctx, "SELECT filename,checksum FROM artifact_gateway_schema_migrations ORDER BY filename")
	if err != nil {
		return nil, errors.New("schema unavailable")
	}
	defer rows.Close()
	ledger := map[string]string{}
	for rows.Next() {
		var name, sum string
		if rows.Scan(&name, &sum) != nil {
			return nil, errors.New("schema unavailable")
		}
		ledger[name] = sum
	}
	if rows.Err() != nil {
		return nil, errors.New("schema unavailable")
	}
	return MigrationLedger(ledger)
}

// Metadata fingerprints every public table before startup mutates runtime
// records. It includes grants, ownership, audits and durable work, but never
// writes row contents or secrets into the backup report.
func (p Postgres) Metadata(ctx context.Context) ([]byte, error) {
	conn, err := p.connect(ctx)
	if err != nil {
		return nil, errors.New("database unavailable")
	}
	defer func() { _ = conn.Close(ctx) }()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SET LOCAL TIME ZONE 'UTC'"); err != nil {
		return nil, errors.New("metadata timezone unavailable")
	}
	rows, err := tx.Query(ctx, "SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename")
	if err != nil {
		return nil, err
	}
	var tables []string
	for rows.Next() {
		var table string
		if rows.Scan(&table) != nil {
			rows.Close()
			return nil, errors.New("metadata unavailable")
		}
		tables = append(tables, table)
	}
	rows.Close()
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	type fingerprint struct {
		Table  string `json:"table"`
		Rows   int64  `json:"rows"`
		SHA256 string `json:"sha256"`
	}
	result := make([]fingerprint, 0, len(tables))
	sort.Strings(tables)
	for _, table := range tables {
		query := "SELECT to_jsonb(t)::text FROM " + pgx.Identifier{"public", table}.Sanitize() + " t ORDER BY to_jsonb(t)::text COLLATE \"C\""
		data, err := tx.Query(ctx, query)
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		var count int64
		for data.Next() {
			var row string
			if data.Scan(&row) != nil {
				data.Close()
				return nil, errors.New("metadata unavailable")
			}
			_, _ = fmt.Fprintf(h, "%d:%s\n", len(row), row)
			count++
		}
		data.Close()
		if data.Err() != nil {
			return nil, data.Err()
		}
		result = append(result, fingerprint{Table: table, Rows: count, SHA256: "sha256:" + hex.EncodeToString(h.Sum(nil))})
	}
	if tx.Commit(ctx) != nil {
		return nil, errors.New("metadata snapshot failed")
	}
	return json.Marshal(result)
}

func (p Postgres) command(ctx context.Context, tool string, args ...string) (*exec.Cmd, error) {
	cfg, err := p.config()
	if err != nil {
		return nil, errors.New("invalid database settings")
	}
	// Support the PostgreSQL connection modes already used by the local runbooks.
	// Advanced TLS/client certificate configurations remain an explicit limitation.
	if cfg.TLSConfig != nil {
		return nil, errors.New("backup PG tools require an explicit local sslmode=disable connection")
	}
	environment := append([]string{}, "PGHOST="+cfg.Host, "PGPORT="+fmt.Sprint(cfg.Port), "PGDATABASE="+cfg.Database, "PGUSER="+cfg.User, "PGPASSWORD="+cfg.Password, "PGSSLMODE=disable")
	var cmd *exec.Cmd
	if p.ToolsContainer != "" {
		argv := []string{"--context", p.DockerContext, "exec", "-i"}
		for _, name := range []string{"PGHOST", "PGPORT", "PGDATABASE", "PGUSER", "PGPASSWORD", "PGSSLMODE"} {
			argv = append(argv, "--env", name)
		}
		argv = append(argv, p.ToolsContainer, tool)
		argv = append(argv, args...)
		cmd = exec.CommandContext(ctx, "docker", argv...)
	} else {
		cmd = exec.CommandContext(ctx, tool, args...)
	}
	if p.ToolsContainer != "" {
		environment = append(environment, "PGHOST=127.0.0.1", "PGPORT=5432")
	}
	cmd.Env = sanitizedEnvironment(environment)
	cmd.Stderr = io.Discard
	return cmd, nil
}
func (p Postgres) Dump(ctx context.Context, w io.Writer) error {
	cmd, err := p.command(ctx, "pg_dump", "--format=custom", "--no-owner", "--no-privileges")
	if err != nil {
		return err
	}
	cmd.Stdout = w
	if cmd.Run() != nil {
		return errors.New("database export failed")
	}
	return nil
}
func (p Postgres) RestoreDatabase(ctx context.Context, r io.Reader) error {
	cmd, err := p.command(ctx, "pg_restore", "--exit-on-error", "--no-owner", "--no-privileges", "--dbname=gateway")
	if err != nil {
		return err
	}
	cmd.Stdin = r
	if cmd.Run() != nil {
		return errors.New("database restore failed")
	}
	return nil
}
func (p Postgres) Empty(ctx context.Context) error {
	conn, err := p.connect(ctx)
	if err != nil {
		return errors.New("target database unavailable")
	}
	defer func() { _ = conn.Close(ctx) }()
	var count int
	if conn.QueryRow(ctx, "SELECT count(*) FROM pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema')").Scan(&count) != nil || count != 0 {
		return errors.New("target database not empty")
	}
	return nil
}

func sanitizedEnvironment(extra []string) []string {
	var result []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "DOCKER_") || strings.HasPrefix(name, "COMPOSE_") || strings.HasPrefix(name, "PG") {
			continue
		}
		result = append(result, entry)
	}
	return append(result, extra...)
}
