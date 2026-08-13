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
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/refaptsigner"
)

type configuration struct {
	ListenAddress   string
	Token           string
	KeyFile         string
	Name            string
	Comment         string
	Email           string
	RSABits         int
	MaxReleaseBytes int64
}

func main() {
	config, err := loadConfig(os.Getenv)
	if err != nil {
		slog.Error("invalid reference APT signer configuration", "error", err)
		os.Exit(1)
	}
	entity, err := refaptsigner.LoadOrCreateEntity(context.Background(), refaptsigner.KeyOptions{
		Path: config.KeyFile, Name: config.Name, Comment: config.Comment, Email: config.Email, RSABits: config.RSABits,
	})
	if err != nil {
		slog.Error("initialize reference APT signing key")
		os.Exit(1)
	}
	identity := config.Name + " <" + config.Email + ">"
	service, err := refaptsigner.NewService(entity, identity)
	if err != nil {
		slog.Error("initialize reference APT signer")
		os.Exit(1)
	}
	handler, err := refaptsigner.NewHandler(refaptsigner.Options{
		Token: config.Token, Service: service, MaxReleaseBytes: config.MaxReleaseBytes,
	})
	if err != nil {
		slog.Error("initialize reference APT signer HTTP handler")
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
	slog.Info("reference APT signer listening", "address", config.ListenAddress, "key_fingerprint", service.Fingerprint())
	select {
	case err = <-errorsChannel:
		if !errors.Is(err, http.ErrServerClosed) {
			slog.Error("reference APT signer stopped", "error", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err = server.Shutdown(shutdownContext); err != nil {
			slog.Error("reference APT signer graceful shutdown failed", "error", err)
			os.Exit(1)
		}
	}
}

func loadConfig(getenv func(string) string) (configuration, error) {
	config := configuration{
		ListenAddress:   envOrDefault(getenv, "REFERENCE_APT_SIGNER_LISTEN_ADDRESS", "127.0.0.1:18083"),
		Token:           getenv("REFERENCE_APT_SIGNER_TOKEN"),
		KeyFile:         envOrDefault(getenv, "REFERENCE_APT_SIGNER_KEY_FILE", "/var/lib/reference-apt-signer/apt-release-private.gpg"),
		Name:            envOrDefault(getenv, "REFERENCE_APT_SIGNER_NAME", "Artifact Gateway Local APT"),
		Comment:         envOrDefault(getenv, "REFERENCE_APT_SIGNER_COMMENT", "reference signer - local development only"),
		Email:           envOrDefault(getenv, "REFERENCE_APT_SIGNER_EMAIL", "apt-release@artifact-gateway.local"),
		RSABits:         4096,
		MaxReleaseBytes: 16 << 20,
	}
	if !loopbackAddress(config.ListenAddress) || len(config.Token) < 32 || len(config.Token) > 256 || strings.ContainsAny(config.Token, "\x00\r\n") ||
		!filepath.IsAbs(config.KeyFile) || strings.ContainsAny(config.Name+config.Comment+config.Email, "\x00\r\n") ||
		config.Name == "" || config.Email == "" {
		return configuration{}, errors.New("listener, token, key path, or identity is invalid")
	}
	var err error
	if config.RSABits, err = intValue(getenv("REFERENCE_APT_SIGNER_RSA_BITS"), config.RSABits, 2048, 4096); err != nil {
		return configuration{}, fmt.Errorf("REFERENCE_APT_SIGNER_RSA_BITS: %w", err)
	}
	if config.MaxReleaseBytes, err = byteValue(getenv("REFERENCE_APT_SIGNER_MAX_RELEASE_BYTES"), config.MaxReleaseBytes, 1024, 16<<20); err != nil {
		return configuration{}, fmt.Errorf("REFERENCE_APT_SIGNER_MAX_RELEASE_BYTES: %w", err)
	}
	return config, nil
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
