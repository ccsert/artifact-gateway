package app

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/aptpublication"
	"github.com/artifact-gateway/artifact-gateway/internal/authorization"
	"github.com/artifact-gateway/artifact-gateway/internal/buildinfo"
	"github.com/artifact-gateway/artifact-gateway/internal/config"
	"github.com/artifact-gateway/artifact-gateway/internal/consoletheme"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/scanning"
	"github.com/jackc/pgx/v5"
)

type Checker interface {
	Check(context.Context) error
}

type Dependencies struct {
	checkers       []Checker
	BuildVersion   string
	BuildRevision  string
	BuildModified  bool
	BuildGoVersion string
	Runtime        DiagnosticRuntime
	// NativeMavenObjectStore is supplied by the runtime after RustFS is initialized.
	// Tests omit it and receive an isolated in-memory store.
	NativeMavenObjectStore        OCIObjectStore
	NativeOCIObjectStore          OCIObjectStore
	NativeConanObjectStore        OCIObjectStore
	NativeNPMObjectStore          OCIObjectStore
	NativePyPIObjectStore         OCIObjectStore
	NativeGoObjectStore           OCIObjectStore
	NativeAPTObjectStore          OCIObjectStore
	APTSigner                     aptpublication.Signer
	APTSigning                    APTSigningRuntime
	NPMMetadataTTL                time.Duration
	NPMNegativeTTL                time.Duration
	NPMBreakerTTL                 time.Duration
	NPMProxyCoordinator           OCICacheCoordinator
	ArtifactScanner               scanning.Scanner
	ArtifactScannerName           string
	ArtifactScannerHealthTimeout  time.Duration
	ArtifactScannerDatabaseMaxAge time.Duration
	ArtifactScannerFormats        []repository.Format
	ArtifactScanResolver          ArtifactScanResolver
	OIDCClient                    *authorization.OIDCClient
	OIDCLoginValidator            *authorization.OIDCValidator
	OIDCRuntime                   *OIDCRuntime
	ConsoleThemes                 *consoletheme.Registry
}

type APTSigningMode string

const (
	APTSigningModeDisabled  APTSigningMode = "disabled"
	APTSigningModeReference APTSigningMode = "reference"
	APTSigningModeRemote    APTSigningMode = "remote"
)

type APTSigningRuntime struct {
	Mode                APTSigningMode
	TrustedFingerprints []string
}

type DiagnosticRuntime struct {
	InstanceID    string
	Roles         []string
	WorkerFormats []repository.Format
	WorkerKinds   []string
}

func NewDependencies(cfg config.Config) Dependencies {
	build := buildinfo.Read()
	roles := make([]string, 0, len(cfg.NodeRoles))
	for _, role := range cfg.NodeRoles {
		roles = append(roles, string(role))
	}
	formats := make([]repository.Format, 0, len(cfg.WorkerFormats))
	for _, format := range cfg.WorkerFormats {
		formats = append(formats, repository.Format(format))
	}
	return Dependencies{
		checkers: []Checker{
			postgresChecker{databaseURL: cfg.DatabaseURL},
			httpChecker{url: rustFSEndpointURL(cfg.RustFSEndpoint)},
		},
		BuildVersion:                  build.Version,
		BuildRevision:                 build.Revision,
		BuildModified:                 build.Modified,
		BuildGoVersion:                build.GoVersion,
		ArtifactScannerHealthTimeout:  2 * time.Second,
		ArtifactScannerDatabaseMaxAge: 24 * time.Hour,
		ConsoleThemes:                 consoletheme.NewRegistry(cfg.ConsoleThemeDir),
		APTSigning:                    aptSigningRuntime(cfg),
		Runtime: DiagnosticRuntime{
			InstanceID: cfg.InstanceID, Roles: roles,
			WorkerFormats: formats,
			WorkerKinds:   append([]string(nil), cfg.WorkerKinds...),
		},
	}
}

func aptSigningRuntime(cfg config.Config) APTSigningRuntime {
	if cfg.APTSignerEndpoint == "" {
		return APTSigningRuntime{Mode: APTSigningModeDisabled}
	}
	mode := APTSigningModeRemote
	if parsed, err := url.Parse(cfg.APTSignerEndpoint); err == nil && aptpublication.IsLoopbackSignerHost(parsed.Hostname()) {
		mode = APTSigningModeReference
	}
	return APTSigningRuntime{Mode: mode, TrustedFingerprints: append([]string(nil), cfg.APTSignerTrustedFingerprints...)}
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

func rustFSEndpointURL(endpoint string) string {
	return strings.TrimRight(endpoint, "/") + "/"
}
