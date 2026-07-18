package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/config"
)

type Checker interface {
	Check(context.Context) error
}

type Dependencies struct {
	checkers []Checker
}

func NewDependencies(cfg config.Config) Dependencies {
	return Dependencies{
		checkers: []Checker{
			tcpChecker{address: databaseAddress(cfg.DatabaseURL)},
			tcpChecker{address: cfg.RedisAddress},
			httpChecker{url: s3EndpointURL(cfg.S3Endpoint)},
		},
	}
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

func s3EndpointURL(endpoint string) string {
	return strings.TrimRight(endpoint, "/") + "/"
}
