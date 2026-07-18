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

func TestNewDependenciesUsesTestAdapter(t *testing.T) {
	dependencies := NewDependencies(config.Config{AdapterMode: "test"})
	if dependencies.adapter.Mode() != "test" {
		t.Fatalf("adapter mode = %q, want test", dependencies.adapter.Mode())
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

func TestReadinessSucceedsWhenDependenciesAreAvailable(t *testing.T) {
	handler := NewHandler(Dependencies{checkers: []Checker{checkerFunc(func(context.Context) error { return nil })}})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
}
