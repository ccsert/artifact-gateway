package operationalog

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestRouteProjectionRequiresThisBuffersServerRegistration(t *testing.T) {
	const template = "GET /api/v2/repositories/{repositoryId}"
	for _, test := range []struct{ name, payload, want string }{
		{"registered", `"route":"` + template + `"`, template},
		{"unmatched", `"route":"unmatched"`, "unmatched"},
		{"raw path", `"route":"/private-path"`, ""},
		{"URL", `"route":"https://private-path?token=private-query"`, ""},
		{"nested", `"route":{"value":"private-path"}`, ""},
		{"number", `"route":502`, ""},
		{"null", `"route":null`, ""},
		{"redacted", `"route":"[REDACTED]"`, ""},
		{"control", `"route":"/private-path\n"`, ""},
		{"long", `"route":"/` + strings.Repeat("x", 256) + `"`, ""},
		{"alternate key casing", `"Route":"/private-path"`, ""},
		{"shadowed key", `"Route":"/private-path","route":"` + template + `"`, template},
	} {
		t.Run(test.name, func(t *testing.T) {
			b := NewBuffer(4)
			b.RegisterRouteTemplate(template)
			_, _ = b.Write([]byte(`{"time":"2026-10-03T00:00:00Z","instanceId":"node","msg":"event",` + test.payload + "}\n"))
			filter := Filter{Limit: 4}
			// Explicitly use an open historical window, independent of wall time.
			filter.To = b.entries[0].Time.Add(1)
			page := b.Query(context.Background(), filter)
			if len(page.Items) != 1 || page.Items[0].Route != test.want {
				t.Fatalf("route projection=%v", page.Items)
			}
			encoded, _ := json.Marshal(page.Items)
			if strings.Contains(string(encoded), "private-") {
				t.Fatal("raw route leaked in result")
			}
			filter.Keyword = "private-path"
			if len(b.Query(context.Background(), filter).Items) != 0 {
				t.Fatal("rejected route leaked through keyword matching")
			}
		})
	}
	other := NewBuffer(1)
	_, _ = other.Write([]byte(`{"time":"2026-10-03T00:00:00Z","instanceId":"node","route":"` + template + `"}` + "\n"))
	if other.entries[0].Route != "" {
		t.Fatal("route registration leaked across buffers")
	}
}

func TestRouteRegistrationRejectsURLAndUnboundedValues(t *testing.T) {
	b := NewBuffer(1)
	for _, pattern := range []string{"", "https://example.invalid/path", "/path?token=private-query", "/path#fragment", "/path\n", "/路径", "/" + strings.Repeat("x", 256)} {
		b.RegisterRouteTemplate(pattern)
	}
	if len(b.routes) != 0 {
		t.Fatalf("invalid templates registered: %v", b.routes)
	}
	NewBuffer(0).RegisterRouteTemplate("GET /livez")
}
