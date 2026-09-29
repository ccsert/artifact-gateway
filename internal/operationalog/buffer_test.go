package operationalog

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestBufferBoundsFiltersAndPagesRedactedLogs(t *testing.T) {
	buffer := NewBuffer(3)
	logger := NewLogger(buffer, "node-a", "session-a")
	for i := 0; i < 4; i++ {
		logger.Info("background operation", "component", "worker", "operation", "job.run", "requestId", "job-1", "secretToken", "private-value", "iteration", i)
	}
	filter := Filter{From: time.Now().Add(-time.Minute), To: time.Now().Add(time.Minute), InstanceID: "node-a", Component: "worker", RequestID: "job-1", Limit: 2}
	first := buffer.Query(context.Background(), filter)
	if len(first.Items) != 2 || first.Next == 0 || first.Items[0].Sequence != 4 || first.Items[1].Sequence != 3 {
		t.Fatalf("first page: %#v", first)
	}
	filter.Before = first.Next
	second := buffer.Query(context.Background(), filter)
	if len(second.Items) != 1 || second.Items[0].Sequence != 2 || second.Next != 0 {
		t.Fatalf("second page: %#v", second)
	}
	filter.Before = 0
	filter.Keyword = "private-value"
	if got := buffer.Query(context.Background(), filter); len(got.Items) != 0 {
		t.Fatalf("secret matched search: %#v", got)
	}
	filter.Keyword = "redacted"
	if got := buffer.Query(context.Background(), filter); len(got.Items) != 2 {
		t.Fatalf("redacted value not searchable: %#v", got)
	}
	var output bytes.Buffer
	if err := json.NewEncoder(&output).Encode(first); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(output.Bytes(), []byte("private-value")) {
		t.Fatalf("secret in API result: %s", output.String())
	}
}

func TestBufferAcceptsSplitLinesAndIgnoresInvalidEntries(t *testing.T) {
	buffer := NewBuffer(2)
	line := []byte(`{"time":"2026-09-29T00:00:00Z","level":"ERROR","msg":"failed","instanceId":"node-a"}` + "\n")
	if _, err := buffer.Write(line[:20]); err != nil {
		t.Fatal(err)
	}
	if _, err := buffer.Write(line[20:]); err != nil {
		t.Fatal(err)
	}
	_, _ = buffer.Write([]byte("malformed\n"))
	page := buffer.Query(context.Background(), Filter{From: time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), Limit: 2})
	if len(page.Items) != 1 || page.Items[0].Level != "ERROR" {
		t.Fatalf("unexpected page: %#v", page)
	}
	if got := NewBuffer(0); got != nil {
		t.Fatalf("disabled buffer = %v", got)
	}
}
