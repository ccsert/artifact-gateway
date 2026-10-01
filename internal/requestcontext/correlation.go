package requestcontext

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
)

type Correlation struct {
	RequestID string
	TraceID   string
}

type correlationKey struct{}

func SafeID(value string) string {
	// Long opaque IDs and JWT-like values are easily confused with credentials.
	// Accept UUIDs or short operator-supplied labels; generate our own otherwise.
	if len(value) == 0 || len(value) > 64 {
		return ""
	}
	if _, err := uuid.Parse(value); err != nil && (len(value) > 24 || strings.Contains(value, ".")) {
		return ""
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '.' && r != '_' && r != '-' {
			return ""
		}
	}
	return value
}

func FromContext(ctx context.Context) (Correlation, bool) {
	value, ok := ctx.Value(correlationKey{}).(Correlation)
	return value, ok && value.RequestID != "" && value.TraceID != ""
}

func WithRequest(ctx context.Context, headerRequestID string) (context.Context, Correlation) {
	if existing, ok := FromContext(ctx); ok {
		return ctx, existing
	}
	requestID := SafeID(headerRequestID)
	if requestID == "" {
		requestID = uuid.NewString()
	}
	traceID := trace.SpanContextFromContext(ctx).TraceID().String()
	if traceID == "00000000000000000000000000000000" || traceID == "" {
		traceID = strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	correlation := Correlation{RequestID: requestID, TraceID: traceID}
	return context.WithValue(ctx, correlationKey{}, correlation), correlation
}
