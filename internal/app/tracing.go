package app

import (
	"context"
	"net/http"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/config"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/trace"
)

const tracerName = "github.com/artifact-gateway/artifact-gateway"

// NewTracing installs an OTLP/HTTP exporter when configured. Without an
// endpoint, the SDK remains local and unsampled, so development needs no
// collector while HTTP context propagation remains standards-compliant.
func NewTracing(ctx context.Context, cfg config.Config) (func(context.Context) error, error) {
	sampler := trace.ParentBased(trace.TraceIDRatioBased(cfg.OTELSamplingRatio))
	options := []trace.TracerProviderOption{trace.WithSampler(sampler)}
	if cfg.OTLPHTTPEndpoint != "" {
		exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpoint(cfg.OTLPHTTPEndpoint), otlptracehttp.WithInsecure())
		if err != nil {
			return nil, err
		}
		options = append(options, trace.WithBatcher(exporter))
	}
	provider := trace.NewTracerProvider(options...)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	return provider.Shutdown, nil
}

func tracedHTTPHandler(handler http.Handler) http.Handler {
	return otelhttp.NewHandler(handler, "gateway.http")
}

func tracedHTTPClient(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	copy := *client
	copy.Transport = otelhttp.NewTransport(client.Transport)
	return &copy
}
