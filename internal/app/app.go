package app

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/authorization"
	"github.com/artifact-gateway/artifact-gateway/internal/config"
	"github.com/jackc/pgx/v5"
)

type Checker interface {
	Check(context.Context) error
}

type Dependencies struct {
	checkers []Checker
	// NativeMavenObjectStore is supplied by the runtime after S3 is initialized.
	// Tests omit it and receive an isolated in-memory store.
	NativeMavenObjectStore OCIObjectStore
	NativeOCIObjectStore   OCIObjectStore
	NativeConanObjectStore OCIObjectStore
	NativeNPMObjectStore   OCIObjectStore
	OIDCClient             *authorization.OIDCClient
	OIDCLoginValidator     *authorization.OIDCValidator
	OIDCRuntime            *OIDCRuntime
}

func NewDependencies(cfg config.Config) Dependencies {
	return Dependencies{
		checkers: []Checker{
			postgresChecker{databaseURL: cfg.DatabaseURL},
			httpChecker{url: s3EndpointURL(cfg.S3Endpoint)},
		},
	}
}

func (d Dependencies) WithDatabasePool(db *sql.DB) Dependencies {
	if db == nil {
		return d
	}
	checkers := append([]Checker(nil), d.checkers...)
	for index, checker := range checkers {
		if _, ok := checker.(postgresChecker); ok {
			checkers[index] = postgresPoolChecker{db: db}
			break
		}
	}
	d.checkers = checkers
	return d
}

func NewHandler(dependencies Dependencies) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /readyz", dependencies.ready)
	return mux
}

// NewOperationalHandler exposes process health and metrics for scheduler or
// worker-only nodes without exposing the artifact and management protocols.
func NewOperationalHandler(dependencies Dependencies, metrics *Metrics) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /readyz", dependencies.ready)
	if metrics != nil {
		mux.Handle("GET /metrics", http.HandlerFunc(metrics.Handler))
	}
	return mux
}

func (d Dependencies) ready(w http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
	defer cancel()
	for _, checker := range d.checkers {
		if checker.Check(ctx) != nil {
			http.Error(w, "dependency unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

type postgresChecker struct{ databaseURL string }

func (p postgresChecker) Check(ctx context.Context) error {
	connection, err := pgx.Connect(ctx, p.databaseURL)
	if err != nil {
		return err
	}
	defer func() { _ = connection.Close(ctx) }()
	return connection.Ping(ctx)
}

type postgresPoolChecker struct{ db *sql.DB }

func (p postgresPoolChecker) Check(ctx context.Context) error {
	return p.db.PingContext(ctx)
}

type httpChecker struct{ url string }

func (h httpChecker) Check(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.url, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode >= http.StatusInternalServerError {
		return fmt.Errorf("health endpoint returned HTTP %d", response.StatusCode)
	}
	return nil
}

func s3EndpointURL(endpoint string) string {
	return strings.TrimRight(endpoint, "/") + "/"
}
