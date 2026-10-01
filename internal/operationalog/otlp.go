package operationalog

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
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
	AcceptedRecords     uint64
	ExportedRecords     uint64
	PendingRecords      uint64
	ExportFailures      uint64
	UnconfirmedRecords  uint64
	RejectedEvents      uint64
	QueueRejectedEvents uint64
	CleanupFailures     uint64
}

// OTLPOutput accepts complete, already redacted NDJSON from NewLogger. Emit is
// asynchronous; a successful Write acknowledges SDK admission, not delivery.
// QueueSize bounds all queued and in-flight records. A full capacity rejects
// the new event without waiting, preserving admitted records and reporting safe
// counters. The SDK queue cannot overflow this admission bound.
type OTLPOutput struct {
	mu        sync.RWMutex
	sdkMu     sync.RWMutex
	provider  *sdklog.LoggerProvider
	logger    otellog.Logger
	observed  *observedExporter
	closed    bool
	done      chan struct{}
	closeErr  error
	closeIdle func()
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
	logResource, err := privateLogResource(options.InstanceID, options.SessionID)
	if err != nil {
		return nil, err
	}
	if endpoint.Path == "" || endpoint.Path == "/" {
		endpoint.Path = "/v1/logs"
	}
	var closeIdle func()
	if client == nil {
		transport := http.DefaultTransport
		if base, ok := transport.(*http.Transport); ok {
			owned := base.Clone()
			transport, closeIdle = owned, owned.CloseIdleConnections
		}
		client = &http.Client{Transport: transport}
	} else {
		copy := *client
		client = &copy
	}
	client.Timeout = options.Timeout
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = otlpHTTPTransport{base: transport, maximum: options.Timeout}
	// A collector redirect must not move logs or credentials to another host.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	exporter, err := otlploghttp.New(context.Background(),
		otlploghttp.WithEndpointURL(endpoint.String()), otlploghttp.WithHeaders(options.Headers),
		otlploghttp.WithHTTPClient(client), otlploghttp.WithTimeout(options.Timeout),
		// The private client owns TLS trust. Explicit nil also prevents the SDK
		// from parsing unrelated shared Trace certificate environment settings.
		otlploghttp.WithTLSClientConfig(nil),
		otlploghttp.WithCompression(otlploghttp.NoCompression), otlploghttp.WithMaxRequestSize(16<<20),
		otlploghttp.WithRetry(otlploghttp.RetryConfig{Enabled: true, InitialInterval: 100 * time.Millisecond, MaxInterval: time.Second, MaxElapsedTime: options.Timeout}),
	)
	if err != nil {
		if closeIdle != nil {
			closeIdle()
		}
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	observed := &observedExporter{Exporter: exporter, report: options.Report, capacity: uint64(options.QueueSize), lifetime: ctx, cancel: cancel}
	processor := sdklog.NewBatchProcessor(observed, sdklog.WithMaxQueueSize(options.QueueSize),
		sdklog.WithExportMaxBatchSize(options.BatchSize), sdklog.WithExportBufferSize(1),
		sdklog.WithExportInterval(options.ExportInterval), sdklog.WithExportTimeout(options.Timeout))
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(processor),
		sdklog.WithAttributeCountLimit(-1), sdklog.WithAttributeValueLengthLimit(-1),
		sdklog.WithResource(logResource))
	return &OTLPOutput{provider: provider, logger: provider.Logger("artifact-gateway/runtime"), observed: observed, done: make(chan struct{}), closeIdle: closeIdle}, nil
}

func (o *OTLPOutput) Write(data []byte) (int, error) {
	o.mu.RLock()
	if o.closed {
		o.mu.RUnlock()
		return 0, io.ErrClosedPipe
	}
	if len(data) == 0 {
		o.mu.RUnlock()
		return 0, nil
	}
	ctx, record, err := decodeOTLPRecord(data)
	if err != nil {
		stats := o.observed.reject()
		o.mu.RUnlock()
		o.observed.notify(stats)
		return 0, err
	}
	// SDK cleanup may hold its queue lock while waiting on an export barrier.
	// Do not let Emit wait behind that lock with our close-state lock held.
	// Runtime Output already serializes flush/write; direct callers receive an
	// explicit admission rejection while private SDK cleanup is in progress.
	if !o.sdkMu.TryRLock() {
		stats := o.observed.reject()
		o.mu.RUnlock()
		o.observed.notify(stats)
		return 0, errors.New("OTLP Logs cleanup in progress")
	}
	admitted, stats := o.observed.reserve()
	if !admitted {
		o.sdkMu.RUnlock()
		o.mu.RUnlock()
		o.observed.notify(stats)
		return 0, errors.New("OTLP Logs capacity full")
	}
	o.logger.Emit(ctx, record)
	o.sdkMu.RUnlock()
	o.mu.RUnlock()
	return len(data), nil
}

func (o *OTLPOutput) ForceFlush(ctx context.Context) error {
	o.mu.RLock()
	closed := o.closed
	o.mu.RUnlock()
	if closed {
		return io.ErrClosedPipe
	}
	err := o.cleanup(ctx, func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return o.provider.ForceFlush(ctx)
	})
	if err != nil {
		o.observed.cleanupFailure()
	}
	return errors.Join(err, o.observed.lastError())
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
	providerErr := o.cleanup(ctx, o.provider.Shutdown)
	// SDK cleanup can wait on an internal mutex beyond ctx. Cancel active
	// requests and settle every remaining reservation when our budget expires;
	// a late SDK no-op is not a receiver acknowledgement.
	if providerErr != nil {
		o.observed.stop(providerErr)
	}
	if o.closeIdle != nil {
		o.closeIdle()
	}
	if providerErr != nil {
		o.observed.cleanupFailure()
	}
	err := errors.Join(providerErr, o.observed.lastError())
	o.mu.Lock()
	o.closeErr = err
	close(o.done)
	o.mu.Unlock()
	return err
}

func (o *OTLPOutput) Stats() OTLPStats { return o.observed.snapshot() }

func (o *OTLPOutput) cleanup(ctx context.Context, cleanup func(context.Context) error) error {
	return awaitOTLPCleanup(ctx, func(ctx context.Context) error {
		o.sdkMu.Lock()
		defer o.sdkMu.Unlock()
		// Shutdown must still stop SDK workers if the budget expired while
		// waiting on the gate. Its canceled context prevents further delivery.
		return cleanup(ctx)
	})
}

// The SDK otherwise sends raw Export errors to its global ErrorHandler. Track
// failures privately and report counters instead; Flush/Shutdown return the
// latest error to our caller's existing redacted cleanup boundary. The official
// exporter handles bounded retries; failed batches are not requeued here.
type observedExporter struct {
	sdklog.Exporter
	mu       sync.Mutex
	stats    OTLPStats
	err      error
	report   func(OTLPStats)
	capacity uint64
	lifetime context.Context
	cancel   context.CancelFunc
	stopped  bool
}

func (e *observedExporter) Export(ctx context.Context, records []sdklog.Record) error {
	e.mu.Lock()
	stopped := e.stopped
	e.mu.Unlock()
	if stopped {
		return nil // Stop already counted these unconfirmed reservations.
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(e.lifetime, cancel)
	defer func() { stop(); cancel() }()
	err := e.Exporter.Export(ctx, records)
	e.mu.Lock()
	if e.stopped {
		e.mu.Unlock()
		return nil
	}
	e.stats.PendingRecords -= uint64(len(records))
	if err != nil {
		e.err = err
		e.stats.ExportFailures++
		e.stats.UnconfirmedRecords += uint64(len(records))
	} else {
		e.stats.ExportedRecords += uint64(len(records))
	}
	stats := e.stats
	e.mu.Unlock()
	if err != nil {
		e.notify(stats)
	}
	return nil
}

func (e *observedExporter) Shutdown(ctx context.Context) error {
	e.stop(ctx.Err())
	return e.Exporter.Shutdown(ctx)
}

func (e *observedExporter) stop(err error) {
	e.mu.Lock()
	if e.stopped {
		e.mu.Unlock()
		return
	}
	e.stopped = true
	pending := e.stats.PendingRecords
	e.stats.UnconfirmedRecords += pending
	e.stats.PendingRecords = 0
	if pending != 0 && err == nil {
		err = io.ErrClosedPipe
	}
	if err != nil {
		e.err = err
	}
	stats := e.stats
	e.mu.Unlock()
	e.cancel()
	if pending != 0 {
		e.notify(stats)
	}
}

// SDK v0.20.0 cleanup has context-insensitive internal mutex waits. Keep the
// public budget explicit; the SDK receives the same cancellation and its
// buffered result lets the worker finish after a caller's deadline.
func awaitOTLPCleanup(ctx context.Context, cleanup func(context.Context) error) error {
	result := make(chan error, 1)
	go func() { result <- cleanup(ctx) }()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// WithResource merges OTEL_RESOURCE_ATTRIBUTES before applying our values.
// Validate before the SDK can emit raw parse errors via its global handler,
// and override sensitive values with the existing shared name policy. Safe
// deployment metadata remains useful; fixed Gateway identity takes priority.
func privateLogResource(instanceID, sessionID string) (*resource.Resource, error) {
	var attrs []attribute.KeyValue
	if raw := strings.TrimSpace(os.Getenv("OTEL_RESOURCE_ATTRIBUTES")); raw != "" {
		for _, pair := range strings.Split(raw, ",") {
			name, value, found := strings.Cut(pair, "=")
			name = strings.TrimSpace(name)
			decoded, err := url.PathUnescape(strings.TrimSpace(value))
			if !found || name == "" || err != nil {
				return nil, errors.New("invalid OTLP Logs resource configuration")
			}
			if sensitiveAttributeName(name) {
				decoded = "[redacted]"
			}
			attrs = append(attrs, attribute.String(name, decoded))
		}
	}
	attrs = append(attrs, attribute.String("service.name", "artifact-gateway"), attribute.String("service.instance.id", instanceID), attribute.String("gateway.runtime.session.id", sessionID))
	return resource.NewSchemaless(attrs...), nil
}

func (e *observedExporter) reject() OTLPStats {
	e.mu.Lock()
	e.stats.RejectedEvents++
	stats := e.stats
	e.mu.Unlock()
	return stats
}

func (e *observedExporter) reserve() (bool, OTLPStats) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stats.PendingRecords >= e.capacity {
		e.stats.RejectedEvents++
		e.stats.QueueRejectedEvents++
		return false, e.stats
	}
	e.stats.AcceptedRecords++
	e.stats.PendingRecords++
	return true, e.stats
}

func (e *observedExporter) cleanupFailure() {
	e.mu.Lock()
	e.stats.CleanupFailures++
	stats := e.stats
	e.mu.Unlock()
	e.notify(stats)
}

func (e *observedExporter) notify(stats OTLPStats) {
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
