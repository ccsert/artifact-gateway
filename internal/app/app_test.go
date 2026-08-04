package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/config"
)

type checkerFunc func(context.Context) error

func (f checkerFunc) Check(ctx context.Context) error { return f(ctx) }

func TestLivenessDoesNotRequireDependencies(t *testing.T) {
	handler := NewHandler(Dependencies{checkers: []Checker{checkerFunc(func(context.Context) error { return errors.New("down") })}})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
}

func TestReadinessReportsDependencyFailure(t *testing.T) {
	handler := NewHandler(Dependencies{checkers: []Checker{checkerFunc(func(context.Context) error { return errors.New("down") })}})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestNewDependenciesChecksS3Endpoint(t *testing.T) {
	dependencies := NewDependencies(config.Config{
		DatabaseURL: "postgres://gateway:password@db:5432/gateway",
		S3Endpoint:  "https://objects.example.test/prefix",
	})
	if len(dependencies.checkers) != 2 {
		t.Fatalf("checker count = %d, want 2", len(dependencies.checkers))
	}
	databaseChecker, ok := dependencies.checkers[0].(postgresChecker)
	if !ok {
		t.Fatalf("database checker type = %T, want postgresChecker", dependencies.checkers[0])
	}
	if databaseChecker.databaseURL != "postgres://gateway:password@db:5432/gateway" {
		t.Fatalf("database checker URL = %q", databaseChecker.databaseURL)
	}
	s3Checker, ok := dependencies.checkers[1].(httpChecker)
	if !ok {
		t.Fatalf("S3 checker type = %T, want httpChecker", dependencies.checkers[1])
	}
	if s3Checker.url != "https://objects.example.test/prefix/" {
		t.Fatalf("S3 checker URL = %q", s3Checker.url)
	}
}

func TestReadinessReportsS3Failure(t *testing.T) {
	handler := NewHandler(Dependencies{checkers: []Checker{
		checkerFunc(func(context.Context) error { return nil }),
		checkerFunc(func(context.Context) error { return nil }),
		checkerFunc(func(context.Context) error { return errors.New("object storage down") }),
	}})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestHTTPCheckerRejectsUnavailableS3HealthEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	if err := (httpChecker{url: server.URL}).Check(context.Background()); err == nil {
		t.Fatal("Check() error = nil, want error")
	}
}

func TestReadinessSucceedsWhenDependenciesAreAvailable(t *testing.T) {
	handler := NewHandler(Dependencies{checkers: []Checker{checkerFunc(func(context.Context) error { return nil })}})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
}
