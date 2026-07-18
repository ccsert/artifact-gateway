package app

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/config"
)

type Checker interface {
	Check(context.Context) error
}

type Dependencies struct {
	checkers []Checker
	adapter  Adapter
}

type Adapter interface {
	Mode() string
}

type testAdapter struct{}

func (testAdapter) Mode() string { return "test" }

type giteaAdapter struct{}

func (giteaAdapter) Mode() string { return "gitea" }

func NewDependencies(cfg config.Config) Dependencies {
	return Dependencies{
		checkers: []Checker{
			tcpChecker{address: databaseAddress(cfg.DatabaseURL)},
			tcpChecker{address: cfg.RedisAddress},
		},
		adapter: newAdapter(cfg.AdapterMode),
	}
}

func newAdapter(mode string) Adapter {
	if mode == "gitea" {
		return giteaAdapter{}
	}
	return testAdapter{}
}

func NewHandler(dependencies Dependencies) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /readyz", dependencies.ready)
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

type tcpChecker struct{ address string }

func (t tcpChecker) Check(ctx context.Context) error {
	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", t.address)
	if err != nil {
		return err
	}
	return connection.Close()
}

func databaseAddress(databaseURL string) string {
	parsed, err := url.Parse(databaseURL)
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	port := parsed.Port()
	if port == "" {
		port = "5432"
	}
	return net.JoinHostPort(parsed.Hostname(), port)
}
