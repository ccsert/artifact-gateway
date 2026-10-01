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

type handler struct {
	inner slog.Handler
	bound schemaFields
}

// schemaFields tracks attributes in the handler's current group only. A value
// copy keeps parents and sibling handlers independent without shared mutation.
type schemaFields struct {
	component, operation, requestID, traceID bool
}

func (fields *schemaFields) include(attr slog.Attr) {
	if attr.Value.Kind() == slog.KindGroup {
		if attr.Key == "" {
			for _, child := range attr.Value.Group() {
				fields.include(child)
			}
		}
		return
	}
	switch attr.Key {
	case "component":
		fields.component = true
	case "operation":
		fields.operation = true
	case "requestId":
		fields.requestID = true
	case "traceId":
		fields.traceID = true
	}
}

// Resolve values once before inspecting them, then pass the resolved attributes
// to the JSON handler so LogValuers, including inline groups, are not evaluated
// again with potentially different results.
func resolveAttribute(attr slog.Attr) slog.Attr {
	attr.Value = attr.Value.Resolve()
	if attr.Value.Kind() == slog.KindGroup {
		group := attr.Value.Group()
		resolved := make([]slog.Attr, len(group))
		for i, child := range group {
			resolved[i] = resolveAttribute(child)
		}
		attr.Value = slog.GroupValue(resolved...)
	}
	return attr
}

func (h handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h handler) Handle(ctx context.Context, record slog.Record) error {
	copy := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	found := h.bound
	record.Attrs(func(attr slog.Attr) bool {
		attr = resolveAttribute(attr)
		found.include(attr)
		copy.AddAttrs(attr)
		return true
	})
	if !found.component {
		copy.AddAttrs(slog.String("component", "gateway"))
	}
	if !found.operation {
		copy.AddAttrs(slog.String("operation", "event"))
	}
	ids, _ := requestcontext.FromContext(ctx)
	if !found.requestID {
		copy.AddAttrs(slog.String("requestId", ids.RequestID))
	}
	if !found.traceID {
		copy.AddAttrs(slog.String("traceId", ids.TraceID))
	}
	return h.inner.Handle(ctx, copy)
}

func (h handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	resolved := make([]slog.Attr, len(attrs))
	for i, attr := range attrs {
		resolved[i] = resolveAttribute(attr)
		h.bound.include(resolved[i])
	}
	h.inner = h.inner.WithAttrs(resolved)
	return h
}
func (h handler) WithGroup(name string) slog.Handler {
	if name != "" {
		h.inner = h.inner.WithGroup(name)
		h.bound = schemaFields{}
	}
	return h
}

func redactAttribute(_ []string, attr slog.Attr) slog.Attr {
	key := strings.ToLower(attr.Key)
	for _, sensitive := range []string{"password", "secret", "token", "credential", "authorization", "cookie", "body", "query", "url", "error", "err"} {
		if strings.Contains(key, sensitive) {
			return slog.String(attr.Key, "[redacted]")
		}
	}
	return attr
}
