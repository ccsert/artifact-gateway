package backupops

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Operational PG connections are explicit local URLs, never libpq service
// files or environment defaults. Native tool environments discard PG settings.
func (p Postgres) config() (*pgx.ConnConfig, error) {
	u, err := url.Parse(p.DSN)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.User == nil || u.User.Username() == "" || u.Fragment != "" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") || len(u.Path) < 2 || strings.Contains(u.Path[1:], "/") {
		return nil, errors.New("explicit local database URL required")
	}
	password, ok := u.User.Password()
	if !ok || password == "" {
		return nil, errors.New("explicit database credentials required")
	}
	port, e := strconv.Atoi(u.Port())
	if e != nil || port < 1 || port > 65535 {
		return nil, errors.New("explicit database port required")
	}
	q := u.Query()
	if len(q) != 1 || q.Get("sslmode") != "disable" || len(q["sslmode"]) != 1 {
		return nil, errors.New("unsupported database options")
	}
	// pgx reads service files before applying URL overrides. Refuse this mode
	// without touching the file; the normal application configuration is unaffected.
	if os.Getenv("PGSERVICE") != "" || os.Getenv("PGSERVICEFILE") != "" {
		return nil, errors.New("inherited PG service configuration unsupported")
	}
	u.Host = "127.0.0.1:" + strconv.Itoa(port)
	q.Set("hostaddr", "127.0.0.1")
	q.Set("passfile", "/dev/null")
	u.RawQuery = q.Encode()
	cfg, e := pgx.ParseConfig(u.String())
	if e != nil {
		return nil, errors.New("invalid database settings")
	}
	cfg.Host = "127.0.0.1"
	cfg.Port = uint16(port)
	cfg.Fallbacks = nil
	cfg.TLSConfig = nil
	cfg.ConnectTimeout = 5 * time.Second
	cfg.RuntimeParams = map[string]string{"timezone": "UTC", "application_name": "artifact-gateway-backup"}
	return cfg, nil
}
func (p Postgres) connect(ctx context.Context) (*pgx.Conn, error) {
	cfg, err := p.config()
	if err != nil {
		return nil, err
	}
	return pgx.ConnectConfig(ctx, cfg)
}
