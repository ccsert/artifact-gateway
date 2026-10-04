package operationalog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestFailureDiagnosticsProjectionAndKeywordBounds(t *testing.T) {
	pairs := map[string]string{"upstream_configuration_invalid": "prepare", "upstream_policy_rejected": "prepare", "upstream_egress_failed": "egress", "upstream_transport_failed": "fetch", "upstream_status_rejected": "fetch", "upstream_body_failed": "body", "unknown": "unknown"}
	for code, phase := range pairs {
		b := NewBuffer(4)
		logger := NewLogger(b, "node", "session")
		logger.Error("request failed", "errorCode", code, "phase", phase)
		page := b.Query(context.Background(), Filter{Limit: 1, RollingFrom: true, RollingTo: true, Keyword: code})
		if len(page.Items) != 1 || page.Items[0].ErrorCode != code || page.Items[0].Phase != phase {
			t.Fatalf("pair %s/%s = %v", code, phase, page.Items)
		}
	}
	for _, fields := range []string{
		`"errorCode":"private-keyword","phase":"fetch"`,
		`"errorCode":"upstream_transport_failed","phase":"private-keyword"`,
		`"errorCode":"upstream_transport_failed","phase":"body"`,
		`"errorCode":{"nested":"private-keyword"},"phase":"fetch"`,
		`"errorCode":"upstream_transport_failed","phase":42`,
		`"errorCode":"upstream_transport_failed"`,
		`"phase":"fetch"`,
		`"ERRORCODE":"private-keyword","PHASE":"fetch"`,
		`"ctx":{"errorCode":"private-keyword","phase":"fetch"}`,
		`"ctx":[{"errorCode":"private-keyword","phase":"fetch"}]`,
		`"errorCode":"` + strings.Repeat("x", 600) + `","phase":"fetch"`,
	} {
		b := NewBuffer(4)
		line := fmt.Sprintf(`{"time":%q,"level":"ERROR","msg":"failure","instanceId":"node","sessionId":"session",%s}`+"\n", time.Now().UTC().Format(time.RFC3339Nano), fields)
		_, _ = b.Write([]byte(line))
		filter := Filter{Limit: 1, RollingFrom: true, RollingTo: true}
		for _, forward := range []bool{false, true} {
			filter.Forward = forward
			page := b.Query(context.Background(), filter)
			if len(page.Items) != 1 || page.Items[0].ErrorCode != "" || page.Items[0].Phase != "" {
				t.Fatalf("rejected %s snapshot/follow=%v", fields, page.Items)
			}
			for _, keyword := range []string{"private-keyword", "upstream_transport_failed", "fetch"} {
				filtered := filter
				filtered.Keyword = keyword
				if len(b.Query(context.Background(), filtered).Items) != 0 {
					t.Fatalf("rejected %s searchable via %s", fields, keyword)
				}
			}
		}
	}
	// Finite values with alternate casing match encoding/json, without retaining
	// duplicate invalid keys in the search representation.
	b := NewBuffer(2)
	_, _ = fmt.Fprintf(b, `{"time":%q,"level":"ERROR","msg":"failure","instanceId":"node","errorCode":"private-keyword","ERRORCODE":"upstream_status_rejected","PHASE":"fetch"}`+"\n", time.Now().UTC().Format(time.RFC3339Nano))
	page := b.Query(context.Background(), Filter{Limit: 1, RollingFrom: true, RollingTo: true})
	if len(page.Items) != 1 || page.Items[0].ErrorCode != "upstream_status_rejected" {
		t.Fatalf("alternate casing=%v", page.Items)
	}
	if len(b.Query(context.Background(), Filter{Limit: 1, RollingFrom: true, RollingTo: true, Keyword: "private-keyword"}).Items) != 0 {
		t.Fatal("duplicate invalid key became a keyword")
	}
}

func TestFailureDiagnosticsStdoutRedaction(t *testing.T) {
	var stdout bytes.Buffer
	logger := NewLogger(&stdout, "node", "session")
	logger.Error("failure", slog.String("errorCode", "upstream_transport_failed"), slog.String("phase", "fetch"), slog.Any("error", fmt.Errorf("private-error")), slog.String("ErrorCode", "private-code"), slog.String("PHASE", "private-phase"), slog.Group("phase", slog.String("value", "private-nested-phase")), slog.Group("ctx", slog.String("errorCode", "upstream_status_rejected"), slog.String("phase", "private-nested")))
	if strings.Contains(stdout.String(), "private-") {
		t.Fatalf("stdout leaked: %s", stdout.String())
	}
	var line map[string]any
	if json.Unmarshal(stdout.Bytes(), &line) != nil || line["errorCode"] != "upstream_transport_failed" || line["phase"].(map[string]any)["value"] != "[redacted]" || line["error"] != "[redacted]" {
		t.Fatalf("stdout=%s", stdout.String())
	}
	stdout.Reset()
	logger.WithGroup("credentials").Error("failure", "errorCode", "upstream_transport_failed", "phase", "fetch")
	if strings.Contains(stdout.String(), "upstream_transport_failed") || strings.Contains(stdout.String(), `"fetch"`) {
		t.Fatal("safe code bypassed sensitive ancestor")
	}
}

func TestFailureSearchPreservesNumbersAndDoesNotReuseRingSlot(t *testing.T) {
	b := NewBuffer(1)
	write := func(message, fields string) {
		_, _ = fmt.Fprintf(b, `{"time":%q,"level":"ERROR","msg":%q,"instanceId":"node",%s}`+"\n", time.Now().UTC().Format(time.RFC3339Nano), message, fields)
	}
	query := func(keyword string) Page {
		return b.Query(context.Background(), Filter{Limit: 1, RollingFrom: true, RollingTo: true, Keyword: keyword})
	}
	write("alpha", `"extra":1`)
	write("beta", `"extra":1e1000,"errorCode":{"private":"private-keyword","number":1e1000},"phase":"fetch"`)
	if len(query("alpha").Items) != 0 || len(query("beta").Items) != 1 || len(query("private-keyword").Items) != 0 {
		t.Fatal("overflowing JSON number reused prior search or exposed rejected fields")
	}
	write("integer", `"extra":9007199254740993`)
	if len(query("9007199254740993").Items) != 1 || len(query("9007199254740992").Items) != 0 {
		t.Fatal("search rounded a JSON integer")
	}
}
