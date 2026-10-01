package app

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/requestcontext"
	"github.com/felixge/httpsnoop"
)

type AccessLogOptions struct {
	Mode          string
	SlowThreshold time.Duration
	Logger        *slog.Logger
}

func (d Dependencies) requestObservability(next http.Handler) http.Handler {
	options := d.AccessLog
	if options.SlowThreshold <= 0 {
		options.SlowThreshold = time.Second
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, ids := requestcontext.WithRequest(r.Context(), r.Header.Get("X-Request-ID"))
		r = r.WithContext(ctx)
		w.Header().Set("X-Request-ID", ids.RequestID)
		w.Header().Set("X-Trace-ID", ids.TraceID)
		started := time.Now()
		captured := httpsnoop.CaptureMetrics(next, w, r)
		duration := time.Since(started)
		level := slog.LevelInfo
		switch {
		case captured.Code >= http.StatusInternalServerError:
			level = slog.LevelError
		case duration >= options.SlowThreshold:
			level = slog.LevelWarn
		case options.Mode != "full":
			return
		}
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		logger.LogAttrs(ctx, level, "gateway HTTP request",
			slog.String("component", "http"), slog.String("operation", "http.request"),
			slog.String("requestId", ids.RequestID), slog.String("traceId", ids.TraceID),
			slog.String("method", r.Method), slog.String("route", route),
			slog.String("requestClass", requestClassNames[classifyRequest(r.URL.Path)]),
			slog.Int("status", captured.Code), slog.Int64("durationMs", duration.Milliseconds()),
		)
	})
}
