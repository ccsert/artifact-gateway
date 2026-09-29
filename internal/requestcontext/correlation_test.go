package requestcontext

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestWithRequestRejectsUnsafeInputAndPreservesTrace(t *testing.T) {
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1, 2, 3}, SpanID: trace.SpanID{4, 5, 6}, TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)
	correlated, ids := WithRequest(ctx, "private?token=secret")
	if ids.RequestID == "" || ids.RequestID == "private?token=secret" || ids.TraceID != spanContext.TraceID().String() {
		t.Fatalf("correlation=%#v", ids)
	}
	again, preserved := WithRequest(correlated, "other")
	if again != correlated || preserved != ids {
		t.Fatalf("nested correlation=%#v", preserved)
	}
	if SafeID("valid-123_abc") == "" || SafeID("contains/slash") != "" || SafeID("eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.signature") != "" {
		t.Fatal("safe ID validation failed")
	}
}
