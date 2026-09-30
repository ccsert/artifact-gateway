package operationalog

import (
	"context"
	"io"
	"log/slog"
	"strings"

	"github.com/artifact-gateway/artifact-gateway/internal/requestcontext"
)

// NewLogger emits one bounded-schema JSON object per line. Runtime output is
// always written to the supplied writer; deployment owns shipping and retention.
func NewLogger(output io.Writer, instanceID, sessionID string) *slog.Logger {
	inner := slog.NewJSONHandler(output, &slog.HandlerOptions{ReplaceAttr: redactAttribute}).WithAttrs([]slog.Attr{
		slog.String("instanceId", instanceID), slog.String("sessionId", sessionID),
	})
	return slog.New(handler{inner: inner})
}

type handler struct{ inner slog.Handler }

func (h handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h handler) Handle(ctx context.Context, record slog.Record) error {
	copy := record.Clone()
	found := make(map[string]bool)
	copy.Attrs(func(attr slog.Attr) bool { found[attr.Key] = true; return true })
	if !found["component"] {
		copy.AddAttrs(slog.String("component", "gateway"))
	}
	if !found["operation"] {
		copy.AddAttrs(slog.String("operation", "event"))
	}
	ids, _ := requestcontext.FromContext(ctx)
	if !found["requestId"] {
		copy.AddAttrs(slog.String("requestId", ids.RequestID))
	}
	if !found["traceId"] {
		copy.AddAttrs(slog.String("traceId", ids.TraceID))
	}
	return h.inner.Handle(ctx, copy)
}

func (h handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return handler{inner: h.inner.WithAttrs(attrs)}
}
func (h handler) WithGroup(name string) slog.Handler { return handler{inner: h.inner.WithGroup(name)} }

func redactAttribute(groups []string, attr slog.Attr) slog.Attr {
	if sensitiveAttributeName(attr.Key) {
		return slog.String(attr.Key, "[redacted]")
	}
	// slog passes group names separately and never calls ReplaceAttr on the
	// group itself. Apply the same policy to every ancestor of a leaf value.
	for _, group := range groups {
		if sensitiveAttributeName(group) {
			return slog.String(attr.Key, "[redacted]")
		}
	}
	return attr
}

func sensitiveAttributeName(name string) bool {
	key := strings.ToLower(name)
	for _, sensitive := range []string{"password", "secret", "token", "credential", "authorization", "cookie", "body", "query", "url", "error", "err"} {
		if strings.Contains(key, sensitive) {
			return true
		}
	}
	return false
}
