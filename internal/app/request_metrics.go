package app

import (
	"database/sql"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/felixge/httpsnoop"
)

type requestClass uint8

const (
	requestClassManagement requestClass = iota
	requestClassOCI
	requestClassMaven
	requestClassRaw
	requestClassConan
	requestClassNPM
	requestClassPyPI
	requestClassGo
	requestClassHealth
	requestClassMetrics
	requestClassOther
	requestClassCount
)

var requestClassNames = [...]string{"management", "oci", "maven", "raw", "conan", "npm", "pypi", "go", "health", "metrics", "other"}
var requestStatusNames = [...]string{"1xx", "2xx", "3xx", "4xx", "5xx", "other"}
var requestDurationBuckets = [...]time.Duration{5 * time.Millisecond, 10 * time.Millisecond, 25 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2500 * time.Millisecond, 5 * time.Second, 10 * time.Second}

type requestMetrics struct {
	requests [requestClassCount][len(requestStatusNames)]atomic.Uint64
	inFlight [requestClassCount]atomic.Int64
	buckets  [requestClassCount][len(requestStatusNames)][len(requestDurationBuckets)]atomic.Uint64
	sumNanos [requestClassCount][len(requestStatusNames)]atomic.Uint64
}

type DatabaseStatsFunc func() sql.DBStats

type namedDatabaseStats struct {
	pool  string
	stats DatabaseStatsFunc
}

func (m *Metrics) WithDatabaseStats(stats DatabaseStatsFunc) *Metrics {
	return m.WithDatabasePoolStats("primary", stats)
}

func (m *Metrics) WithDatabasePoolStats(pool string, stats DatabaseStatsFunc) *Metrics {
	if stats == nil {
		return m
	}
	switch pool {
	case "primary", "artifact-locks", "coordinator", "notifications":
		m.databaseStats = append(m.databaseStats, namedDatabaseStats{pool: pool, stats: stats})
	}
	return m
}

func (m *Metrics) Instrument(next http.Handler) http.Handler {
	if m == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		class := classifyRequest(r.URL.Path)
		m.httpRequests.inFlight[class].Add(1)
		started := time.Now()
		captured := httpsnoop.Metrics{Code: http.StatusOK}
		defer func() {
			m.httpRequests.inFlight[class].Add(-1)
			m.httpRequests.observe(class, captured.Code, time.Since(started))
		}()
		captured.CaptureMetrics(w, func(wrapped http.ResponseWriter) {
			next.ServeHTTP(wrapped, r)
		})
	})
}

func (m *requestMetrics) observe(class requestClass, status int, duration time.Duration) {
	statusIndex := requestStatusIndex(status)
	m.requests[class][statusIndex].Add(1)
	m.sumNanos[class][statusIndex].Add(uint64(max(duration, 0)))
	for index, upperBound := range requestDurationBuckets {
		if duration <= upperBound {
			m.buckets[class][statusIndex][index].Add(1)
		}
	}
}

func classifyRequest(path string) requestClass {
	switch {
	case path == "/metrics":
		return requestClassMetrics
	case path == "/livez" || path == "/readyz":
		return requestClassHealth
	case strings.HasPrefix(path, "/api/"):
		return requestClassManagement
	case strings.HasPrefix(path, "/v2/"):
		return requestClassOCI
	case strings.HasPrefix(path, "/repository/maven/") || strings.HasPrefix(path, "/maven/"):
		return requestClassMaven
	case strings.HasPrefix(path, "/raw/"):
		return requestClassRaw
	case strings.HasPrefix(path, "/conan/"):
		return requestClassConan
	case strings.HasPrefix(path, "/npm/"):
		return requestClassNPM
	case strings.HasPrefix(path, "/pypi/"):
		return requestClassPyPI
	case strings.HasPrefix(path, "/go/"):
		return requestClassGo
	default:
		return requestClassOther
	}
}

func requestStatusIndex(status int) int {
	if status >= 100 && status < 600 {
		return status/100 - 1
	}
	return len(requestStatusNames) - 1
}

func (m *Metrics) writeHTTPMetrics(w io.Writer) {
	_, _ = io.WriteString(w, "# TYPE artifact_gateway_http_requests_total counter\n# TYPE artifact_gateway_http_requests_in_flight gauge\n# TYPE artifact_gateway_http_request_duration_seconds histogram\n")
	for classIndex, class := range requestClassNames {
		_, _ = io.WriteString(w, "artifact_gateway_http_requests_in_flight{class=\""+class+"\"} "+itoa(m.httpRequests.inFlight[classIndex].Load())+"\n")
		for statusIndex, status := range requestStatusNames {
			count := m.httpRequests.requests[classIndex][statusIndex].Load()
			labels := "class=\"" + class + "\",status=\"" + status + "\""
			_, _ = io.WriteString(w, "artifact_gateway_http_requests_total{"+labels+"} "+utoa(count)+"\n")
			for bucketIndex, upperBound := range requestDurationBuckets {
				seconds := strconv.FormatFloat(upperBound.Seconds(), 'f', -1, 64)
				_, _ = io.WriteString(w, "artifact_gateway_http_request_duration_seconds_bucket{"+labels+",le=\""+seconds+"\"} "+utoa(m.httpRequests.buckets[classIndex][statusIndex][bucketIndex].Load())+"\n")
			}
			_, _ = io.WriteString(w, "artifact_gateway_http_request_duration_seconds_bucket{"+labels+",le=\"+Inf\"} "+utoa(count)+"\n")
			sum := float64(m.httpRequests.sumNanos[classIndex][statusIndex].Load()) / float64(time.Second)
			_, _ = io.WriteString(w, "artifact_gateway_http_request_duration_seconds_sum{"+labels+"} "+strconv.FormatFloat(sum, 'f', 9, 64)+"\n")
			_, _ = io.WriteString(w, "artifact_gateway_http_request_duration_seconds_count{"+labels+"} "+utoa(count)+"\n")
		}
	}
}

func (m *Metrics) writeRuntimeMetrics(w io.Writer) {
	if m.instanceID != "" {
		roles := make([]string, 0, len(m.nodeRoles))
		for _, role := range m.nodeRoles {
			roles = append(roles, escapeMetricLabel(role))
		}
		_, _ = io.WriteString(w, "# TYPE artifact_gateway_node_info gauge\nartifact_gateway_node_info{instance_id=\""+escapeMetricLabel(m.instanceID)+"\",roles=\""+strings.Join(roles, ",")+"\"} 1\n")
	}
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	_, _ = io.WriteString(w, "# TYPE artifact_gateway_runtime_goroutines gauge\nartifact_gateway_runtime_goroutines "+utoa(uint64(runtime.NumGoroutine()))+"\n")
	_, _ = io.WriteString(w, "# TYPE artifact_gateway_runtime_heap_alloc_bytes gauge\nartifact_gateway_runtime_heap_alloc_bytes "+utoa(stats.HeapAlloc)+"\n")
	_, _ = io.WriteString(w, "# TYPE artifact_gateway_runtime_heap_inuse_bytes gauge\nartifact_gateway_runtime_heap_inuse_bytes "+utoa(stats.HeapInuse)+"\n")
	_, _ = io.WriteString(w, "# TYPE artifact_gateway_runtime_heap_sys_bytes gauge\nartifact_gateway_runtime_heap_sys_bytes "+utoa(stats.HeapSys)+"\n")
	_, _ = io.WriteString(w, "# TYPE artifact_gateway_runtime_gc_cycles_total counter\nartifact_gateway_runtime_gc_cycles_total "+utoa(uint64(stats.NumGC))+"\n")
	_, _ = io.WriteString(w, "# TYPE artifact_gateway_runtime_gc_pause_seconds_total counter\nartifact_gateway_runtime_gc_pause_seconds_total "+strconv.FormatFloat(float64(stats.PauseTotalNs)/float64(time.Second), 'f', 9, 64)+"\n")
}

func escapeMetricLabel(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return strings.ReplaceAll(value, "\n", `\n`)
}

func (m *Metrics) writeDatabaseMetrics(w io.Writer) {
	if len(m.databaseStats) == 0 {
		return
	}
	_, _ = io.WriteString(w, "# TYPE artifact_gateway_database_connections gauge\n")
	_, _ = io.WriteString(w, "# TYPE artifact_gateway_database_connection_wait_total counter\n")
	_, _ = io.WriteString(w, "# TYPE artifact_gateway_database_connection_wait_seconds_total counter\n")
	_, _ = io.WriteString(w, "# TYPE artifact_gateway_database_connections_closed_total counter\n")
	for _, source := range m.databaseStats {
		stats := source.stats()
		pool := `pool="` + source.pool + `"`
		_, _ = io.WriteString(w, "artifact_gateway_database_connections{"+pool+",state=\"max_open\"} "+itoa(int64(stats.MaxOpenConnections))+"\n")
		_, _ = io.WriteString(w, "artifact_gateway_database_connections{"+pool+",state=\"open\"} "+itoa(int64(stats.OpenConnections))+"\n")
		_, _ = io.WriteString(w, "artifact_gateway_database_connections{"+pool+",state=\"in_use\"} "+itoa(int64(stats.InUse))+"\n")
		_, _ = io.WriteString(w, "artifact_gateway_database_connections{"+pool+",state=\"idle\"} "+itoa(int64(stats.Idle))+"\n")
		_, _ = io.WriteString(w, "artifact_gateway_database_connection_wait_total{"+pool+"} "+utoa(uint64(stats.WaitCount))+"\n")
		_, _ = io.WriteString(w, "artifact_gateway_database_connection_wait_seconds_total{"+pool+"} "+strconv.FormatFloat(stats.WaitDuration.Seconds(), 'f', 9, 64)+"\n")
		_, _ = io.WriteString(w, "artifact_gateway_database_connections_closed_total{"+pool+",reason=\"idle_limit\"} "+utoa(uint64(stats.MaxIdleClosed))+"\n")
		_, _ = io.WriteString(w, "artifact_gateway_database_connections_closed_total{"+pool+",reason=\"idle_time\"} "+utoa(uint64(stats.MaxIdleTimeClosed))+"\n")
		_, _ = io.WriteString(w, "artifact_gateway_database_connections_closed_total{"+pool+",reason=\"lifetime\"} "+utoa(uint64(stats.MaxLifetimeClosed))+"\n")
	}
}
