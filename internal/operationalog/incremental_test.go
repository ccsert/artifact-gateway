package operationalog

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestForwardPagingConsumesUnreadPositionAndReportsRealRetentionGap(t *testing.T) {
	b := NewBuffer(250)
	logger := NewLogger(b, "node", "session")
	for i := 1; i <= 230; i++ {
		logger.Info(fmt.Sprintf("event-%d", i), "component", "http")
	}
	f := Filter{To: time.Now().Add(time.Minute), InstanceID: "node", SessionID: "session", Limit: 100, Forward: true}
	for _, want := range []struct {
		first, last, count int
		more               bool
	}{{1, 100, 100, true}, {101, 200, 100, true}, {201, 230, 30, false}} {
		p := b.Query(context.Background(), f)
		if len(p.Items) != want.count || p.Items[0].Sequence != uint64(want.first) || p.Items[len(p.Items)-1].Sequence != uint64(want.last) || p.HasMore != want.more || p.Gap {
			t.Fatalf("page %#v", p)
		}
		f.After = p.Position
	}
	// Filtering sequence numbers is not loss; a page still consumes scanned nonmatches.
	f.After = 0
	f.Keyword = "event-230"
	p := b.Query(context.Background(), f)
	if len(p.Items) != 1 || p.Position != 230 || p.Gap {
		t.Fatalf("filtered page %#v", p)
	}
	f.Keyword = ""
	f.After = 1
	for i := 0; i < 30; i++ {
		logger.Info("new event")
	}
	p = b.Query(context.Background(), f)
	if !p.Gap || p.Earliest != 11 || p.Latest != 260 || p.Items[0].Sequence != 11 {
		t.Fatalf("overwrite page %#v", p)
	}
}

func TestSafeDiagnosticProjectionUsesRedactedTopLevelAllowlist(t *testing.T) {
	b := NewBuffer(10)
	l := NewLogger(b, "node", "session")
	l.With("method", "GET", "status", 503).Info("request", "durationMs", int64(25), "requestClass", "maven", "jobId", "job-123", "attempt", 2, "AUTHORIZATION", "synthetic-secret")
	l.WithGroup("password").With("status", 500).Info("sensitive group", "jobId", "synthetic-secret")
	l.Info("nested", slog.Group("details", "status", 501, "attempt", 3), "method", "https://synthetic-secret", "status", 999, "durationMs", -1, "requestClass", "arbitrary", "jobId", "https://synthetic-secret", "attempt", -1)
	p := b.Query(context.Background(), Filter{To: time.Now().Add(time.Minute), Limit: 10})
	if len(p.Items) != 3 {
		t.Fatalf("page %#v", p)
	}
	good := p.Items[2]
	if good.Status == nil || *good.Status != 503 || good.DurationMS == nil || *good.DurationMS != 25 || good.Method != "GET" || good.RequestClass != "maven" || good.JobID != "job-123" || good.Attempt == nil || *good.Attempt != 2 {
		t.Fatalf("projection %#v", good)
	}
	for _, entry := range p.Items[:2] {
		if entry.Status != nil || entry.DurationMS != nil || entry.Method != "" || entry.RequestClass != "" || entry.JobID != "" || entry.Attempt != nil {
			t.Fatalf("unsafe projection %#v", entry)
		}
	}
	encoded, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "synthetic-secret") {
		t.Fatalf("secret in projection %s", encoded)
	}
}

func TestRollingQueryBoundsAreEvaluatedAtSnapshot(t *testing.T) {
	b := NewBuffer(4)
	old := time.Now().Add(-time.Minute)
	NewLogger(b, "node", "session").Info("written after parameter parsing")
	f := Filter{From: old.Add(-time.Hour), To: old, RollingFrom: true, RollingTo: true, Forward: true, Limit: 4}
	p := b.Query(context.Background(), f)
	if len(p.Items) != 1 || p.Position != 1 {
		t.Fatalf("rolling query skipped recent event: %#v", p)
	}
	f.RollingFrom, f.RollingTo = false, false
	if p = b.Query(context.Background(), f); len(p.Items) != 0 {
		t.Fatalf("explicit time bound changed: %#v", p)
	}
}

func TestRollingQueryEnforcesWindowAtSnapshot(t *testing.T) {
	b := NewBuffer(4)
	NewLogger(b, "node", "session").Info("recent event")
	// The bounds were valid at parameter parsing time. The locked snapshot is
	// later, as it would be after waiting behind a writer.
	parsedTo := time.Now().Add(-time.Second)
	f := Filter{From: parsedTo.Add(-24*time.Hour + 500*time.Millisecond), To: parsedTo, RollingTo: true, MaxWindow: 24 * time.Hour, Limit: 4}
	p := b.Query(context.Background(), f)
	if !p.InvalidWindow || len(p.Items) != 0 || p.Position != 0 {
		t.Fatalf("snapshot expanded validated window: %#v", p)
	}
	f.From = time.Now().Add(-time.Hour)
	if p = b.Query(context.Background(), f); p.InvalidWindow || len(p.Items) != 1 {
		t.Fatalf("ordinary rolling window rejected: %#v", p)
	}
	// With only an explicit end, the rolling start can pass it while waiting.
	f = Filter{From: parsedTo.Add(-time.Hour), To: time.Now().Add(-time.Hour - time.Second), RollingFrom: true, MaxWindow: 24 * time.Hour, Limit: 4}
	if p = b.Query(context.Background(), f); !p.InvalidWindow || len(p.Items) != 0 {
		t.Fatalf("snapshot inverted validated window: %#v", p)
	}
}

func TestBufferQueryHonorsCanceledAndContendedDeadlines(t *testing.T) {
	b := NewBuffer(2)
	NewLogger(b, "node", "session").Info("retained")
	b.mu.Lock()
	defer b.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	started := time.Now()
	page := b.Query(ctx, Filter{To: time.Now().Add(time.Minute), Limit: 2})
	if ctx.Err() == nil || len(page.Items) != 0 || time.Since(started) > time.Second {
		t.Fatalf("contended query ignored deadline: %v %#v", ctx.Err(), page)
	}
}

func TestReturnedDiagnosticFieldsDoNotMutateRetainedEntries(t *testing.T) {
	b := NewBuffer(1)
	NewLogger(b, "node", "session").Info("request", "status", 503, "durationMs", 25, "attempt", 2)
	f := Filter{To: time.Now().Add(time.Minute), Limit: 1}
	p := b.Query(context.Background(), f)
	*p.Items[0].Status = 999
	*p.Items[0].DurationMS = -1
	*p.Items[0].Attempt = -1
	p = b.Query(context.Background(), f)
	if *p.Items[0].Status != 503 || *p.Items[0].DurationMS != 25 || *p.Items[0].Attempt != 2 {
		t.Fatalf("caller modified retained diagnostics %#v", p)
	}
}
