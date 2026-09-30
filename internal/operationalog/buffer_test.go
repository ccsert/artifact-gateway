package operationalog

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
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

func TestBufferDiscardsOversizedLinesUntilNewline(t *testing.T) {
	const limit = 16 * 1024
	tail := `{"time":"2026-09-29T00:00:00Z","instanceId":"node-a","msg":"tail"}`
	recovered := `{"time":"2026-09-29T00:00:00Z","instanceId":"node-a","msg":"recovered"}` + "\n"
	for _, test := range []struct {
		name   string
		chunks []string
	}{
		{"one oversized fragment", []string{strings.Repeat("x", limit+1), tail + "\n" + recovered}},
		{"overflow at boundary", []string{strings.Repeat("x", limit), "x", tail + "\n" + recovered}},
		{"many fragments", []string{strings.Repeat("x", limit/2), strings.Repeat("x", limit/2), "x", "", tail[:20], tail[20:], "\n" + recovered[:20], recovered[20:]}},
		{"repeated oversized fragments", []string{strings.Repeat("x", limit+1), strings.Repeat("x", limit+1), tail + "\n" + recovered}},
		{"multiple oversized lines", []string{strings.Repeat("x", limit+1), tail + "\n" + strings.Repeat("x", limit+1) + "\n" + recovered}},
	} {
		t.Run(test.name, func(t *testing.T) {
			buffer := NewBuffer(10)
			for _, chunk := range test.chunks {
				if n, err := buffer.Write([]byte(chunk)); err != nil || n != len(chunk) {
					t.Fatalf("Write returned (%d, %v), want (%d, nil)", n, err, len(chunk))
				}
			}
			page := buffer.Query(context.Background(), Filter{To: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), Limit: 10})
			if len(page.Items) != 1 || page.Items[0].Message != "recovered" || page.Items[0].Sequence != 1 {
				t.Fatalf("oversized line tail entered buffer or recovery failed: %#v", page)
			}
		})
	}
}

func TestBufferLineSizeBoundary(t *testing.T) {
	const limit = 16 * 1024
	line := `{"time":"2026-09-29T00:00:00Z","instanceId":"node-a","msg":"boundary"}`
	line += strings.Repeat(" ", limit-len(line))
	for _, test := range []struct {
		name   string
		chunks []string
		want   int
	}{
		{"exact limit in one write", []string{line + "\n"}, 1},
		{"exact limit split before newline", []string{line[:100], line[100:], "\n"}, 1},
		{"one byte above limit in one write", []string{line + " \n"}, 0},
		{"one byte above limit at newline", []string{line, " \n"}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			buffer := NewBuffer(2)
			for _, chunk := range test.chunks {
				if _, err := buffer.Write([]byte(chunk)); err != nil {
					t.Fatal(err)
				}
			}
			page := buffer.Query(context.Background(), Filter{To: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), Limit: 2})
			if len(page.Items) != test.want {
				t.Fatalf("got %d entries, want %d: %#v", len(page.Items), test.want, page)
			}
		})
	}
}
