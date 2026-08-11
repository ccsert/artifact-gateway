package app

import (
	"bufio"
	"database/sql"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/database"
)

func TestHTTPMetricsRecordBoundedClassStatusAndLatency(t *testing.T) {
	metrics := &Metrics{}
	handler := metrics.Instrument(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v2/repositories", nil))

	response := httptest.NewRecorder()
	metrics.Handler(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := response.Body.String()
	for _, expected := range []string{
		`artifact_gateway_http_requests_total{class="management",status="2xx"} 1`,
		`artifact_gateway_http_requests_in_flight{class="management"} 0`,
		`artifact_gateway_http_request_duration_seconds_bucket{class="management",status="2xx",le="+Inf"} 1`,
		`artifact_gateway_http_request_duration_seconds_count{class="management",status="2xx"} 1`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("metrics missing %q\n%s", expected, body)
		}
	}
}

func TestHTTPMetricsExposeInFlightRequest(t *testing.T) {
	metrics := &Metrics{}
	started := make(chan struct{})
	release := make(chan struct{})
	handler := metrics.Instrument(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v2/example/manifests/latest", nil))
		close(done)
	}()
	<-started
	response := httptest.NewRecorder()
	metrics.Handler(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(response.Body.String(), `artifact_gateway_http_requests_in_flight{class="oci"} 1`) {
		t.Fatalf("in-flight metric missing\n%s", response.Body.String())
	}
	close(release)
	<-done
}

func TestClassifyNPMRequest(t *testing.T) {
	if class := classifyRequest("/npm/releases/@scope/widget"); class != requestClassNPM {
		t.Fatalf("class=%v", class)
	}
}

func TestRuntimeAndDatabasePoolMetrics(t *testing.T) {
	pool, err := database.OpenPostgres("postgres://gateway:secret@localhost:5432/gateway?sslmode=disable", database.PoolConfig{MaxOpenConns: 7, MaxIdleConns: 2, ConnMaxLifetime: time.Minute, ConnMaxIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pool.Close() }()
	notifications, err := database.OpenPostgres("postgres://gateway:secret@localhost:5432/gateway?sslmode=disable", database.NotificationPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = notifications.Close() }()
	metrics := (&Metrics{}).
		WithDatabaseStats(pool.Stats).
		WithDatabasePoolStats("artifact-locks", func() sql.DBStats { return sql.DBStats{MaxOpenConnections: 4} }).
		WithDatabasePoolStats("notifications", notifications.Stats)
	response := httptest.NewRecorder()
	metrics.Handler(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := response.Body.String()
	for _, expected := range []string{
		"artifact_gateway_runtime_heap_alloc_bytes ",
		"artifact_gateway_runtime_gc_cycles_total ",
		"artifact_gateway_runtime_goroutines ",
		`artifact_gateway_database_connections{pool="primary",state="max_open"} 7`,
		`artifact_gateway_database_connections{pool="artifact-locks",state="max_open"} 4`,
		`artifact_gateway_database_connections{pool="notifications",state="max_open"} 2`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("metrics missing %q\n%s", expected, body)
		}
	}
}

func TestNodeInfoMetricEscapesConfiguredIdentity(t *testing.T) {
	metrics := (&Metrics{}).WithNodeIdentity("worker\"01\n", []string{"worker"})
	response := httptest.NewRecorder()
	metrics.Handler(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(response.Body.String(), `artifact_gateway_node_info{instance_id="worker\"01\n",roles="worker"} 1`) {
		t.Fatalf("node metric is missing or malformed:\n%s", response.Body.String())
	}
}

type basicResponseWriter struct{ header http.Header }

func (w *basicResponseWriter) Header() http.Header          { return w.header }
func (*basicResponseWriter) Write(body []byte) (int, error) { return len(body), nil }
func (*basicResponseWriter) WriteHeader(int)                {}

func TestMetricsResponseWriterDoesNotInventOptionalInterfaces(t *testing.T) {
	handler := (&Metrics{}).Instrument(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, ok := w.(http.Flusher); ok {
			t.Fatal("metrics response writer unexpectedly implements http.Flusher")
		}
		if _, ok := w.(http.Hijacker); ok {
			t.Fatal("metrics response writer unexpectedly implements http.Hijacker")
		}
		if _, ok := w.(http.Pusher); ok {
			t.Fatal("metrics response writer unexpectedly implements http.Pusher")
		}
		if _, ok := w.(io.ReaderFrom); ok {
			t.Fatal("metrics response writer unexpectedly implements io.ReaderFrom")
		}
	}))
	handler.ServeHTTP(&basicResponseWriter{header: make(http.Header)}, httptest.NewRequest(http.MethodGet, "/", nil))
}

type optionalResponseWriter struct{ *basicResponseWriter }

func (*optionalResponseWriter) Flush() {}
func (*optionalResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, http.ErrNotSupported
}
func (*optionalResponseWriter) Push(string, *http.PushOptions) error { return http.ErrNotSupported }
func (w *optionalResponseWriter) ReadFrom(reader io.Reader) (int64, error) {
	return io.Copy(io.Discard, reader)
}

func TestMetricsResponseWriterPreservesOptionalInterfaces(t *testing.T) {
	handler := (&Metrics{}).Instrument(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for name, supported := range map[string]bool{
			"flusher":     implements[http.Flusher](w),
			"hijacker":    implements[http.Hijacker](w),
			"pusher":      implements[http.Pusher](w),
			"reader_from": implements[io.ReaderFrom](w),
		} {
			if !supported {
				t.Fatalf("metrics response writer hid %s", name)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	handler.ServeHTTP(&optionalResponseWriter{basicResponseWriter: &basicResponseWriter{header: make(http.Header)}}, httptest.NewRequest(http.MethodGet, "/", nil))
}

func implements[T any](value any) bool {
	_, ok := value.(T)
	return ok
}

type statusSequenceWriter struct {
	*basicResponseWriter
	statuses []int
}

func (w *statusSequenceWriter) WriteHeader(status int) {
	w.statuses = append(w.statuses, status)
}

func TestHTTPMetricsPassThroughInformationalStatusAndRecordFinalStatus(t *testing.T) {
	metrics := &Metrics{}
	underlying := &statusSequenceWriter{basicResponseWriter: &basicResponseWriter{header: make(http.Header)}}
	handler := metrics.Instrument(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusEarlyHints)
		w.WriteHeader(http.StatusNoContent)
	}))
	handler.ServeHTTP(underlying, httptest.NewRequest(http.MethodGet, "/api/v2/repositories", nil))
	if len(underlying.statuses) != 2 || underlying.statuses[0] != http.StatusEarlyHints || underlying.statuses[1] != http.StatusNoContent {
		t.Fatalf("forwarded statuses=%v", underlying.statuses)
	}
	response := httptest.NewRecorder()
	metrics.Handler(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(response.Body.String(), `artifact_gateway_http_requests_total{class="management",status="2xx"} 1`) {
		t.Fatal("HTTP metrics did not record the final status family")
	}
}

func TestDatabaseMetricsIgnoreUnboundedPoolLabels(t *testing.T) {
	metrics := (&Metrics{}).WithDatabasePoolStats("repository-name", func() sql.DBStats {
		return sql.DBStats{MaxOpenConnections: 99}
	})
	response := httptest.NewRecorder()
	metrics.Handler(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if strings.Contains(response.Body.String(), "repository-name") {
		t.Fatal("database metrics exposed an unbounded pool label")
	}
}
