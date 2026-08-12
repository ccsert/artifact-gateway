package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/refscanner"
)

type configuration struct {
	ListenAddress    string
	Token            string
	TrivyBinary      string
	ScanTimeout      time.Duration
	HealthTimeout    time.Duration
	MaxArtifactBytes int64
	MaxOutputBytes   int64
	MaxConcurrent    int
	SBOMDir          string
	SBOMBaseURL      string
	MaxSBOMBytes     int64
}

func main() {
	config, err := loadConfig(os.Getenv)
	if err != nil {
		slog.Error("invalid reference scanner configuration", "error", err)
		os.Exit(1)
	}
	engine, err := refscanner.NewTrivyEngine(refscanner.TrivyOptions{
		Binary: config.TrivyBinary, MaxOutputBytes: config.MaxOutputBytes,
	})
	if err != nil {
		slog.Error("initialize Trivy engine")
		os.Exit(1)
	}
	handler, err := refscanner.NewHandler(refscanner.Options{
		Token: config.Token, Engine: engine, MaxArtifactBytes: config.MaxArtifactBytes,
		ScanTimeout: config.ScanTimeout, HealthTimeout: config.HealthTimeout,
		MaxConcurrent: config.MaxConcurrent, SBOMDir: config.SBOMDir,
		SBOMBaseURL: config.SBOMBaseURL, MaxSBOMBytes: config.MaxSBOMBytes,
	})
	if err != nil {
		slog.Error("initialize reference scanner HTTP adapter")
		os.Exit(1)
	}
	server := &http.Server{
		Addr: config.ListenAddress, Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errorsChannel := make(chan error, 1)
	go func() { errorsChannel <- server.ListenAndServe() }()
	slog.Info("reference scanner listening", "address", config.ListenAddress, "token_configured", config.Token != "")
	select {
	case err = <-errorsChannel:
		if !errors.Is(err, http.ErrServerClosed) {
			slog.Error("reference scanner stopped", "error", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err = server.Shutdown(shutdownContext); err != nil {
			slog.Error("reference scanner graceful shutdown failed", "error", err)
			os.Exit(1)
		}
	}
}

func loadConfig(getenv func(string) string) (configuration, error) {
	value := configuration{
		ListenAddress:    envOrDefault(getenv, "REFERENCE_SCANNER_LISTEN_ADDRESS", "127.0.0.1:18082"),
		Token:            getenv("REFERENCE_SCANNER_TOKEN"),
		TrivyBinary:      envOrDefault(getenv, "REFERENCE_SCANNER_TRIVY_BINARY", "trivy"),
		ScanTimeout:      10 * time.Minute,
		HealthTimeout:    10 * time.Second,
		MaxArtifactBytes: 20 << 30,
		MaxOutputBytes:   64 << 20,
		MaxConcurrent:    1,
		SBOMDir:          envOrDefault(getenv, "REFERENCE_SCANNER_SBOM_DIR", "/var/lib/reference-scanner/sboms"),
		SBOMBaseURL:      envOrDefault(getenv, "REFERENCE_SCANNER_BASE_URL", "http://127.0.0.1:18082"),
		MaxSBOMBytes:     2 << 30,
	}
	if !loopbackAddress(value.ListenAddress) || strings.ContainsAny(value.Token, "\r\n") || len(value.Token) > 4096 || strings.ContainsAny(value.TrivyBinary, "\x00\r\n") {
		return configuration{}, errors.New("listener, token, or Trivy binary is invalid")
	}
	var err error
	if value.ScanTimeout, err = durationValue(getenv("REFERENCE_SCANNER_SCAN_TIMEOUT"), value.ScanTimeout, time.Second, 30*time.Minute); err != nil {
		return configuration{}, fmt.Errorf("REFERENCE_SCANNER_SCAN_TIMEOUT: %w", err)
	}
	if value.HealthTimeout, err = durationValue(getenv("REFERENCE_SCANNER_HEALTH_TIMEOUT"), value.HealthTimeout, time.Second, 30*time.Second); err != nil {
		return configuration{}, fmt.Errorf("REFERENCE_SCANNER_HEALTH_TIMEOUT: %w", err)
	}
	if value.MaxArtifactBytes, err = byteValue(getenv("REFERENCE_SCANNER_MAX_ARTIFACT_BYTES"), value.MaxArtifactBytes, 1, 1<<40); err != nil {
		return configuration{}, fmt.Errorf("REFERENCE_SCANNER_MAX_ARTIFACT_BYTES: %w", err)
	}
	if value.MaxOutputBytes, err = byteValue(getenv("REFERENCE_SCANNER_MAX_OUTPUT_BYTES"), value.MaxOutputBytes, 1024, 512<<20); err != nil {
		return configuration{}, fmt.Errorf("REFERENCE_SCANNER_MAX_OUTPUT_BYTES: %w", err)
	}
	if value.MaxSBOMBytes, err = byteValue(getenv("REFERENCE_SCANNER_MAX_SBOM_BYTES"), value.MaxSBOMBytes, 1024, 1<<40); err != nil {
		return configuration{}, fmt.Errorf("REFERENCE_SCANNER_MAX_SBOM_BYTES: %w", err)
	}
	if value.MaxConcurrent, err = intValue(getenv("REFERENCE_SCANNER_MAX_CONCURRENT"), value.MaxConcurrent, 1, 32); err != nil {
		return configuration{}, fmt.Errorf("REFERENCE_SCANNER_MAX_CONCURRENT: %w", err)
	}
	return value, nil
}

func intValue(raw string, fallback, minimum, maximum int) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < minimum || value > maximum {
		return 0, errors.New("integer value is outside the allowed range")
	}
	return value, nil
}

func envOrDefault(getenv func(string) string, name, fallback string) string {
	if value := strings.TrimSpace(getenv(name)); value != "" {
		return value
	}
	return fallback
}

func loopbackAddress(value string) bool {
	host, port, err := net.SplitHostPort(value)
	if err != nil || host == "" {
		return false
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func durationValue(raw string, fallback, minimum, maximum time.Duration) (time.Duration, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || value < minimum || value > maximum {
		return 0, errors.New("duration is outside the allowed range")
	}
	return value, nil
}

func byteValue(raw string, fallback, minimum, maximum int64) (int64, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || value < minimum || value > maximum {
		return 0, errors.New("byte value is outside the allowed range")
	}
	return value, nil
}
