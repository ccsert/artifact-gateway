package app

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/operationalog"
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
		route := &runtimeLogRouteState{pattern: "unmatched"}
		ctx = context.WithValue(ctx, runtimeLogRouteContextKey{}, route)
		failure := &rawFailureState{}
		ctx = context.WithValue(ctx, rawFailureContextKey{}, failure)
		state, ok := ctx.Value(requestClassStateContextKey{}).(*requestClassState)
		if !ok {
			// Standalone handlers still share the resolved compatibility class,
			// without creating or updating any metrics counters.
			state = &requestClassState{class: classifyRequest(r.URL.Path)}
			ctx = context.WithValue(ctx, requestClassStateContextKey{}, state)
		}
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
		attrs := []slog.Attr{
			slog.String("component", "http"), slog.String("operation", "http.request"),
			slog.String("requestId", ids.RequestID), slog.String("traceId", ids.TraceID),
			slog.String("method", r.Method), slog.String("route", route.current()),
			slog.String("requestClass", requestClassNames[state.current()]),
			slog.Int("status", captured.Code), slog.Int64("durationMs", duration.Milliseconds()),
		}
		if code := failure.current(); code != "" && captured.Code >= http.StatusBadRequest {
			attrs = append(attrs, slog.String("errorCode", code), slog.String("phase", operationalog.FailurePhase(code)))
		}
		logger.LogAttrs(ctx, level, "gateway HTTP request", attrs...)
	})
}
