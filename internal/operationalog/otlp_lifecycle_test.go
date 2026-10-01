package operationalog

import (
	"bytes"
	"context"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	logglobal "go.opentelemetry.io/otel/log/global"
)

type otlpSyntheticTransport func(*http.Request) (*http.Response, error)

func (f otlpSyntheticTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func otlpSyntheticResponse(r *http.Request, status int, body []byte) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/x-protobuf"}}, Body: io.NopCloser(bytes.NewReader(body)), Request: r}
}

func TestOTLPOutputDoesNotInheritTraceTLSOrChangeGlobalProviders(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("initialization contacted receiver") }))
	defer server.Close()
	certificatePath := filepath.Join(t.TempDir(), "synthetic-trace-ca.pem")
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(certificatePath, certificate, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OTEL_EXPORTER_OTLP_CERTIFICATE", certificatePath)
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_CERTIFICATE", "")
	traceProvider, logsProvider := otel.GetTracerProvider(), logglobal.GetLoggerProvider()
	output, err := NewOTLPOutput(otlpTestOptions("http://synthetic-logs.invalid"))
	if err != nil {
		t.Fatalf("shared trace TLS changed explicit Logs initialization: %v", err)
	}
	defer shutdownOTLP(t, output)
	if otel.GetTracerProvider() != traceProvider || logglobal.GetLoggerProvider() != logsProvider || os.Getenv("OTEL_EXPORTER_OTLP_CERTIFICATE") != certificatePath {
		t.Fatal("Logs changed tracing/global configuration")
	}
}

func TestOTLPOutputShutdownObservesDeadlineDuringConcurrentFlush(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	client := &http.Client{Transport: otlpSyntheticTransport(func(r *http.Request) (*http.Response, error) {
		once.Do(func() { close(started) })
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	options := otlpTestOptions("https://synthetic-blocked.invalid")
	options.Timeout = 800 * time.Millisecond
	output, err := newOTLPOutput(options, client)
	if err != nil {
		t.Fatal(err)
	}
	NewLogger(output, "synthetic", "synthetic").Info("synthetic blocked event")
	flushed := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		flushed <- output.ForceFlush(ctx)
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("flush did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	begin := time.Now()
	err = output.Shutdown(ctx)
	elapsed := time.Since(begin)
	if !errors.Is(err, context.DeadlineExceeded) || elapsed > 300*time.Millisecond {
		t.Errorf("20ms shutdown budget: elapsed=%s, error=%v", elapsed, err)
	}
	if err := <-flushed; err == nil {
		t.Error("interrupted flush reported success")
	}
	if _, err := output.Write([]byte("{}\n")); !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("shutdown did not stop admission: %v", err)
	}
	if err := output.Shutdown(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("repeated shutdown lost its result: %v", err)
	}
}

func TestOTLPOutputRejectsCapacityOverflowUntilExportCompletes(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var first, released sync.Once
	unblock := func() { released.Do(func() { close(release) }) }
	client := &http.Client{Transport: otlpSyntheticTransport(func(r *http.Request) (*http.Response, error) {
		first.Do(func() { close(started) })
		select {
		case <-release:
			return otlpSyntheticResponse(r, http.StatusOK, nil), nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})}
	options := otlpTestOptions("https://synthetic-blocked.invalid")
	options.QueueSize, options.BatchSize = 1, 1
	var reports atomic.Int64
	options.Report = func(OTLPStats) { reports.Add(1) }
	output, err := newOTLPOutput(options, client)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unblock(); shutdownOTLP(t, output) })
	data := []byte("{\"time\":\"2026-01-01T00:00:00Z\",\"level\":\"INFO\",\"msg\":\"synthetic capacity marker\"}\n")
	if n, err := output.Write(data); n != len(data) || err != nil {
		t.Fatal(n, err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("export did not start")
	}
	if n, err := output.Write(data); n != 0 || err == nil {
		t.Errorf("capacity one admitted a second unconfirmed event: n=%d, error=%v", n, err)
	}
	if output.Stats().RejectedEvents != 1 || reports.Load() != 1 {
		t.Errorf("overflow not reported: stats=%+v, reports=%d", output.Stats(), reports.Load())
	}
	unblock()
	flushOTLP(t, output)
	if n, err := output.Write(data); n != len(data) || err != nil {
		t.Fatalf("capacity not released after export: n=%d, error=%v", n, err)
	}
	flushOTLP(t, output)
	if output.Stats().ExportedRecords != 2 || output.Stats().RejectedEvents != 1 {
		t.Fatalf("accepted/rejected delivery counts=%+v", output.Stats())
	}
}

func TestOTLPOutputShutdownReleasesPrivateIdleConnection(t *testing.T) {
	idle, closed := make(chan struct{}), make(chan struct{})
	var idleOnce, closeOnce sync.Once
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateIdle {
			idleOnce.Do(func() { close(idle) })
		}
		if state == http.StateClosed {
			closeOnce.Do(func() { close(closed) })
		}
	}
	server.Start()
	defer server.Close()
	output, err := NewOTLPOutput(otlpTestOptions(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { shutdownOTLP(t, output) })
	NewLogger(output, "synthetic", "synthetic").Info("synthetic idle connection")
	flushOTLP(t, output)
	select {
	case <-idle:
	case <-time.After(3 * time.Second):
		t.Fatal("connection did not become idle")
	}
	shutdownOTLP(t, output)
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown left the private idle connection open")
	}
}

func TestOTLPOutputConcurrentOverflowHasExactAdmissionAndDeliveryCounts(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once, released sync.Once
	unblock := func() { released.Do(func() { close(release) }) }
	client := &http.Client{Transport: otlpSyntheticTransport(func(r *http.Request) (*http.Response, error) {
		once.Do(func() { close(started) })
		select {
		case <-release:
			return otlpSyntheticResponse(r, http.StatusOK, nil), nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})}
	options := otlpTestOptions("https://synthetic-slow.invalid")
	options.QueueSize, options.BatchSize = 4, 1
	options.Timeout = 3 * time.Second
	var reports atomic.Int64
	options.Report = func(OTLPStats) { reports.Add(1) }
	output, err := newOTLPOutput(options, client)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unblock(); shutdownOTLP(t, output) })
	data := []byte("{\"time\":\"2026-01-01T00:00:00Z\",\"level\":\"INFO\",\"msg\":\"synthetic concurrent capacity event\"}\n")
	if _, err := output.Write(data); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("export did not start")
	}
	var accepted, rejected atomic.Int64
	var workers sync.WaitGroup
	for i := 0; i < 64; i++ {
		workers.Go(func() {
			if n, err := output.Write(data); n == len(data) && err == nil {
				accepted.Add(1)
			} else if n == 0 && err != nil {
				rejected.Add(1)
			} else {
				t.Errorf("unexpected admission result: n=%d err=%v", n, err)
			}
		})
	}
	workers.Wait()
	stats := output.Stats()
	if accepted.Load() != 3 || rejected.Load() != 61 || reports.Load() != 61 || stats.AcceptedRecords != 4 || stats.PendingRecords != 4 || stats.QueueRejectedEvents != 61 || stats.ExportedRecords != 0 {
		t.Fatalf("bounded concurrent admission: accepted=%d rejected=%d reports=%d stats=%+v", accepted.Load(), rejected.Load(), reports.Load(), stats)
	}
	unblock()
	flushOTLP(t, output)
	stats = output.Stats()
	if stats.ExportedRecords != 4 || stats.PendingRecords != 0 || stats.UnconfirmedRecords != 0 {
		t.Fatalf("admitted events were lost: %+v", stats)
	}
}

func TestOTLPOutputReleasesCapacityAfterFailureAndRetainsFailureEvidence(t *testing.T) {
	var requests atomic.Int64
	client := &http.Client{Transport: otlpSyntheticTransport(func(r *http.Request) (*http.Response, error) {
		if requests.Add(1) == 1 {
			return otlpSyntheticResponse(r, http.StatusBadRequest, nil), nil
		}
		return otlpSyntheticResponse(r, http.StatusOK, nil), nil
	})}
	options := otlpTestOptions("https://synthetic-recovered.invalid")
	options.QueueSize, options.BatchSize = 1, 1
	output, err := newOTLPOutput(options, client)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("{\"time\":\"2026-01-01T00:00:00Z\",\"level\":\"INFO\",\"msg\":\"synthetic failure recovery event\"}\n")
	for i := 0; i < 2; i++ {
		if n, err := output.Write(data); n != len(data) || err != nil {
			t.Fatalf("failed export did not release capacity: n=%d err=%v", n, err)
		}
		if err := output.ForceFlush(context.Background()); err == nil {
			t.Error("historical failed export was erased")
		}
	}
	if err := output.Shutdown(context.Background()); err == nil {
		t.Error("shutdown lost failure evidence")
	}
	stats := output.Stats()
	if requests.Load() != 2 || stats.AcceptedRecords != 2 || stats.ExportedRecords != 1 || stats.UnconfirmedRecords != 1 || stats.PendingRecords != 0 || stats.QueueRejectedEvents != 0 || stats.ExportFailures != 1 {
		t.Fatalf("recovery counts = %+v, requests = %d", stats, requests.Load())
	}
}

func TestOTLPOutputShutdownBudgetAndAccountingWithBufferedExports(t *testing.T) {
	for _, concurrentFlush := range []bool{false, true} {
		t.Run(map[bool]string{false: "buffered", true: "concurrent flush"}[concurrentFlush], func(t *testing.T) {
			started := make(chan struct{})
			var calls atomic.Int64
			client := &http.Client{Transport: otlpSyntheticTransport(func(r *http.Request) (*http.Response, error) {
				if calls.Add(1) == 1 {
					close(started)
				}
				<-r.Context().Done()
				return nil, r.Context().Err()
			})}
			options := otlpTestOptions("https://synthetic-buffered.invalid")
			options.QueueSize, options.BatchSize, options.Timeout = 4, 1, time.Second
			output, err := newOTLPOutput(options, client)
			if err != nil {
				t.Fatal(err)
			}
			data := []byte("{\"time\":\"2026-01-01T00:00:00Z\",\"level\":\"INFO\",\"msg\":\"synthetic buffered event\"}\n")
			if _, err := output.Write(data); err != nil {
				t.Fatal(err)
			}
			<-started
			for i := 0; i < 2; i++ {
				if _, err := output.Write(data); err != nil {
					t.Fatal(err)
				}
			}
			// Allow the SDK poller to fill its export buffer behind the blocked
			// request; the assertions concern public deadline/delivery behavior.
			time.Sleep(40 * time.Millisecond)
			flushed := make(chan error, 1)
			if concurrentFlush {
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
					defer cancel()
					flushed <- output.ForceFlush(ctx)
				}()
				time.Sleep(40 * time.Millisecond)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			begin := time.Now()
			err = output.Shutdown(ctx)
			if !errors.Is(err, context.DeadlineExceeded) || time.Since(begin) > 300*time.Millisecond {
				t.Errorf("shutdown budget ignored: elapsed=%s error=%v", time.Since(begin), err)
			}
			if concurrentFlush {
				if err := <-flushed; err == nil {
					t.Error("interrupted flush reported success")
				}
			}
			stats := output.Stats()
			if stats.AcceptedRecords != 3 || stats.ExportedRecords != 0 || stats.PendingRecords != 0 || stats.UnconfirmedRecords != 3 {
				t.Errorf("expired shutdown did not settle admitted events: %+v", stats)
			}
		})
	}
}

func TestOTLPOutputRejectsDuringCleanupWithoutBlockingShutdown(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	client := &http.Client{Transport: otlpSyntheticTransport(func(r *http.Request) (*http.Response, error) {
		once.Do(func() { close(started) })
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	options := otlpTestOptions("https://synthetic-concurrent-cleanup.invalid")
	output, err := newOTLPOutput(options, client)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("{\"time\":\"2026-01-01T00:00:00Z\",\"level\":\"INFO\",\"msg\":\"synthetic cleanup admission\"}\n")
	if _, err := output.Write(data); err != nil {
		t.Fatal(err)
	}
	flushed := make(chan error, 2)
	flush := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
		defer cancel()
		flushed <- output.ForceFlush(ctx)
	}
	go flush()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("cleanup did not enter export")
	}
	go flush()
	begin := time.Now()
	if n, err := output.Write(data); n != 0 || err == nil || time.Since(begin) > 300*time.Millisecond {
		t.Errorf("cleanup admission was not rejected promptly: n=%d error=%v elapsed=%s", n, err, time.Since(begin))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	begin = time.Now()
	if err := output.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) || time.Since(begin) > 300*time.Millisecond {
		t.Errorf("shutdown with concurrent cleanup exceeded budget: elapsed=%s error=%v", time.Since(begin), err)
	}
	for i := 0; i < 2; i++ {
		select {
		case err := <-flushed:
			if err == nil {
				t.Error("interrupted cleanup reported success")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("cleanup worker did not finish")
		}
	}
	stats := output.Stats()
	if stats.AcceptedRecords != 1 || stats.RejectedEvents != 1 || stats.PendingRecords != 0 || stats.UnconfirmedRecords != 1 || stats.ExportedRecords != 0 {
		t.Errorf("concurrent cleanup counts = %+v", stats)
	}
}
