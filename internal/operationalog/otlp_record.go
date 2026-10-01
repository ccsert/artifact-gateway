package operationalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"time"

	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

// Bound each encoded input and nested mapping before it enters the SDK queue.
const maxOTLPEventBytes = 64 << 10

func decodeOTLPRecord(data []byte) (context.Context, otellog.Record, error) {
	var record otellog.Record
	if len(data) > maxOTLPEventBytes || len(data) < 3 || data[len(data)-1] != '\n' || bytes.ContainsAny(data[:len(data)-1], "\r\n") || !json.Valid(data) {
		return nil, record, errors.New("invalid or oversized OTLP Logs event")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var fields map[string]any
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		return nil, record, errors.New("invalid OTLP Logs JSON object")
	}
	timestampText, _ := fields["time"].(string)
	timestamp, err := time.Parse(time.RFC3339Nano, timestampText)
	// OTLP timestamps are unsigned nanoseconds since the epoch. Reject values
	// outside that wire range rather than allowing UnixNano to wrap.
	if err != nil || timestamp.Before(time.Unix(0, 0)) || timestamp.After(time.Unix(18446744073, 709551615)) {
		return nil, record, errors.New("invalid OTLP Logs timestamp")
	}
	levelText, _ := fields["level"].(string)
	var level slog.Level
	if err := level.UnmarshalText([]byte(levelText)); err != nil {
		return nil, record, errors.New("invalid OTLP Logs severity")
	}
	message, ok := fields["msg"].(string)
	if !ok {
		return nil, record, errors.New("invalid OTLP Logs message")
	}
	record.SetTimestamp(timestamp)
	record.SetObservedTimestamp(time.Now())
	record.SetSeverity(otellog.Severity(max(-8, min(15, int(level))) + 9))
	record.SetSeverityText(levelText)
	record.SetBody(otellog.StringValue(message))
	ctx := context.Background()
	if traceText, ok := fields["traceId"].(string); ok {
		if id, err := trace.TraceIDFromHex(traceText); err == nil {
			// Native trace correlation needs a valid TraceId, not a fabricated
			// SpanId or inherited tracing sampling decision.
			ctx = trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{TraceID: id}))
		}
	}
	for _, name := range sortedJSONKeys(fields) {
		if name == "time" || name == "level" || name == "msg" {
			continue
		}
		value, err := otlpJSONValue(fields[name], 0)
		if err != nil {
			return nil, record, err
		}
		record.AddAttributes(otellog.KeyValue{Key: name, Value: value})
	}
	return ctx, record, nil
}

func otlpJSONValue(value any, depth int) (otellog.Value, error) {
	if depth > 32 {
		return otellog.Value{}, errors.New("OTLP Logs event nesting exceeds 32 levels")
	}
	switch value := value.(type) {
	case nil:
		return otellog.Value{}, nil
	case string:
		return otellog.StringValue(value), nil
	case bool:
		return otellog.BoolValue(value), nil
	case json.Number:
		if !strings.ContainsAny(string(value), ".eE") {
			if integer, err := value.Int64(); err == nil {
				return otellog.Int64Value(integer), nil
			}
		} else if number, err := value.Float64(); err == nil {
			return otellog.Float64Value(number), nil
		}
		// OTLP integers are signed int64. Preserve wider integers and numbers
		// outside float64 as text rather than silently losing their precision.
		return otellog.StringValue(string(value)), nil
	case []any:
		values := make([]otellog.Value, 0, len(value))
		for _, item := range value {
			mapped, err := otlpJSONValue(item, depth+1)
			if err != nil {
				return otellog.Value{}, err
			}
			values = append(values, mapped)
		}
		return otellog.SliceValue(values...), nil
	case map[string]any:
		values := make([]otellog.KeyValue, 0, len(value))
		for _, name := range sortedJSONKeys(value) {
			mapped, err := otlpJSONValue(value[name], depth+1)
			if err != nil {
				return otellog.Value{}, err
			}
			values = append(values, otellog.KeyValue{Key: name, Value: mapped})
		}
		return otellog.MapValue(values...), nil
	default:
		return otellog.Value{}, errors.New("unsupported OTLP Logs attribute")
	}
}

func sortedJSONKeys(fields map[string]any) []string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
