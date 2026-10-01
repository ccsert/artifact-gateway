package operationalog

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	"golang.org/x/net/http/httpguts"
)

// OTLPOptions configures a private Logs provider, separate from tracing. Headers
// are deployment credentials; Report receives only safe diagnostic counters.
type OTLPOptions struct {
	Endpoint       string
	Headers        map[string]string
	InstanceID     string
	SessionID      string
	QueueSize      int
	BatchSize      int
	ExportInterval time.Duration
	Timeout        time.Duration
	Report         func(OTLPStats)
}

type OTLPStats struct {
	ExportedRecords    uint64
	ExportFailures     uint64
	UnconfirmedRecords uint64
	RejectedEvents     uint64
}

// OTLPOutput accepts complete, already redacted NDJSON from NewLogger. Emit is
// asynchronous; a successful Write acknowledges SDK admission, not delivery.
// The official bounded batch queue drops oldest records when full and reports
// those drops through the SDK diagnostic logger. Shutdown prevents new emits.
type OTLPOutput struct {
	mu       sync.RWMutex
	provider *sdklog.LoggerProvider
	logger   otellog.Logger
	observed *observedExporter
	closed   bool
	done     chan struct{}
	closeErr error
}

func NewOTLPOutput(options OTLPOptions) (*OTLPOutput, error) {
	return newOTLPOutput(options, nil)
}

func newOTLPOutput(options OTLPOptions, client *http.Client) (*OTLPOutput, error) {
	endpoint, err := url.Parse(options.Endpoint)
	if err != nil || endpoint.Hostname() == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" || options.QueueSize < 1 || options.QueueSize > 1024 || options.BatchSize < 1 || options.BatchSize > 64 || options.BatchSize > options.QueueSize || options.ExportInterval <= 0 || options.Timeout <= 0 || options.Timeout > 30*time.Second {
		return nil, errors.New("invalid OTLP Logs options")
	}
	for name, value := range options.Headers {
		if !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(value) || len(value) > 4096 {
			return nil, errors.New("invalid OTLP Logs header")
		}
		switch strings.ToLower(name) {
		case "host", "content-type", "content-length", "content-encoding", "connection", "transfer-encoding":
			return nil, errors.New("reserved OTLP Logs header")
		}
	}
	if endpoint.Path == "" || endpoint.Path == "/" {
		endpoint.Path = "/v1/logs"
	}
	if client == nil {
		transport := http.DefaultTransport
		if base, ok := transport.(*http.Transport); ok {
			transport = base.Clone()
		}
		client = &http.Client{Transport: transport}
	} else {
		copy := *client
		client = &copy
	}
	client.Timeout = options.Timeout
	// A collector redirect must not move logs or credentials to another host.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	exporter, err := otlploghttp.New(context.Background(),
		otlploghttp.WithEndpointURL(endpoint.String()), otlploghttp.WithHeaders(options.Headers),
		otlploghttp.WithHTTPClient(client), otlploghttp.WithTimeout(options.Timeout),
		otlploghttp.WithCompression(otlploghttp.NoCompression), otlploghttp.WithMaxRequestSize(16<<20),
		otlploghttp.WithRetry(otlploghttp.RetryConfig{Enabled: true, InitialInterval: 100 * time.Millisecond, MaxInterval: time.Second, MaxElapsedTime: options.Timeout}),
	)
	if err != nil {
		return nil, err
	}
	observed := &observedExporter{Exporter: exporter, report: options.Report}
	processor := sdklog.NewBatchProcessor(observed, sdklog.WithMaxQueueSize(options.QueueSize),
		sdklog.WithExportMaxBatchSize(options.BatchSize), sdklog.WithExportBufferSize(1),
		sdklog.WithExportInterval(options.ExportInterval), sdklog.WithExportTimeout(options.Timeout))
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(processor),
		sdklog.WithAttributeCountLimit(-1), sdklog.WithAttributeValueLengthLimit(-1),
		sdklog.WithResource(resource.NewSchemaless(attribute.String("service.name", "artifact-gateway"),
			attribute.String("service.instance.id", options.InstanceID), attribute.String("gateway.runtime.session.id", options.SessionID))))
	return &OTLPOutput{provider: provider, logger: provider.Logger("artifact-gateway/runtime"), observed: observed, done: make(chan struct{})}, nil
}

func (o *OTLPOutput) Write(data []byte) (int, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if o.closed {
		return 0, io.ErrClosedPipe
	}
	if len(data) == 0 {
		return 0, nil
	}
	ctx, record, err := decodeOTLPRecord(data)
	if err != nil {
		o.observed.reject()
		return 0, err
	}
	o.logger.Emit(ctx, record)
	return len(data), nil
}

func (o *OTLPOutput) ForceFlush(ctx context.Context) error {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if o.closed {
		return io.ErrClosedPipe
	}
	return errors.Join(o.provider.ForceFlush(ctx), o.observed.lastError())
}

func (o *OTLPOutput) Shutdown(ctx context.Context) error {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		select {
		case <-o.done:
			return o.closeErr
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	o.closed = true
	o.mu.Unlock()
	err := errors.Join(o.provider.Shutdown(ctx), o.observed.lastError())
	o.mu.Lock()
	o.closeErr = err
	close(o.done)
	o.mu.Unlock()
	return err
}

func (o *OTLPOutput) Stats() OTLPStats { return o.observed.snapshot() }

// The SDK otherwise sends raw Export errors to its global ErrorHandler. Track
// failures privately and report counters instead; Flush/Shutdown return the
// latest error to our caller's existing redacted cleanup boundary. The official
// exporter handles bounded retries; failed batches are not requeued here.
type observedExporter struct {
	sdklog.Exporter
	mu     sync.Mutex
	stats  OTLPStats
	err    error
	report func(OTLPStats)
}

func (e *observedExporter) Export(ctx context.Context, records []sdklog.Record) error {
	err := e.Exporter.Export(ctx, records)
	e.mu.Lock()
	if err != nil {
		e.err = err
		e.stats.ExportFailures++
		e.stats.UnconfirmedRecords += uint64(len(records))
	} else {
		e.stats.ExportedRecords += uint64(len(records))
	}
	stats := e.stats
	e.mu.Unlock()
	if err != nil && e.report != nil {
		e.report(stats)
	}
	return nil
}

func (e *observedExporter) reject() {
	e.mu.Lock()
	e.stats.RejectedEvents++
	stats := e.stats
	e.mu.Unlock()
	if e.report != nil {
		e.report(stats)
	}
}

func (e *observedExporter) snapshot() OTLPStats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stats
}

func (e *observedExporter) lastError() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.err
}
