package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/http/httpguts"
)

func configureOTLPLogs(cfg *Config) error {
	cfg.OTLPLogsEndpoint = strings.TrimSpace(os.Getenv("GATEWAY_OTLP_LOGS_ENDPOINT"))
	if cfg.OTLPLogsEndpoint != "" {
		endpoint, err := url.Parse(cfg.OTLPLogsEndpoint)
		if err != nil || endpoint.Hostname() == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" {
			return fmt.Errorf("GATEWAY_OTLP_LOGS_ENDPOINT must be an HTTP(S) URL without credentials, query or fragment")
		}
	}
	cfg.OTLPLogsHeaders = make(map[string]string)
	if raw := strings.TrimSpace(os.Getenv("GATEWAY_OTLP_LOGS_HEADERS")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg.OTLPLogsHeaders); err != nil || cfg.OTLPLogsHeaders == nil {
			return fmt.Errorf("GATEWAY_OTLP_LOGS_HEADERS must be a JSON object of string values")
		}
	}
	for name, value := range cfg.OTLPLogsHeaders {
		if !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(value) || len(value) > 4096 {
			return fmt.Errorf("GATEWAY_OTLP_LOGS_HEADERS contains an invalid header")
		}
		switch strings.ToLower(name) {
		case "host", "content-type", "content-length", "content-encoding", "connection", "transfer-encoding":
			return fmt.Errorf("GATEWAY_OTLP_LOGS_HEADERS contains a reserved header")
		}
	}
	var err error
	if cfg.OTLPLogsQueueSize, err = positiveIntEnv("GATEWAY_OTLP_LOGS_QUEUE_SIZE", 256, false); err != nil {
		return err
	}
	if cfg.OTLPLogsQueueSize > 1024 {
		return fmt.Errorf("GATEWAY_OTLP_LOGS_QUEUE_SIZE must be at most 1024")
	}
	if cfg.OTLPLogsTimeout, err = boundedLogDuration("GATEWAY_OTLP_LOGS_TIMEOUT", 3*time.Second, 30*time.Second); err != nil {
		return err
	}
	cfg.OTLPLogsShutdownTimeout, err = boundedLogDuration("GATEWAY_OTLP_LOGS_SHUTDOWN_TIMEOUT", 5*time.Second, 10*time.Second)
	return err
}

func boundedLogDuration(name string, fallback, maximum time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	var duration time.Duration
	var err error
	if seconds, parseErr := strconv.ParseInt(raw, 10, 64); parseErr == nil {
		if seconds < 1 || seconds > int64(maximum/time.Second) {
			return 0, fmt.Errorf("%s is outside its duration bounds", name)
		}
		duration = time.Duration(seconds) * time.Second
	} else {
		duration, err = time.ParseDuration(raw)
	}
	if err != nil || duration < time.Millisecond || duration > maximum {
		return 0, fmt.Errorf("%s must be between 1ms and %s", name, maximum)
	}
	return duration, nil
}
