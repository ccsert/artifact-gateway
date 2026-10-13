package agctl

import (
	"strings"
	"testing"
)

func TestRedactTextRemovesCredentialsAndPreservesDiagnostics(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		secrets []string
		leaks   []string
		want    string
	}{
		{"bearer", "upstream rejected Bearer synthetic-bearer-token while reading identity", nil, []string{"synthetic-bearer-token"}, "while reading identity"},
		{"fields", `bad input: apiKey=synthetic-key; password="synthetic-password with spaces"; "secret": "synthetic-secret"`, nil, []string{"synthetic-key", "synthetic-password", "synthetic-secret"}, "bad input"},
		{"url", "unreachable https://synthetic-user:synthetic-password@gateway.example.com/gw?token=synthetic-query-token&limit=10#synthetic-fragment", nil, []string{"synthetic-user", "synthetic-password", "synthetic-query-token", "synthetic-fragment"}, "gateway.example.com/gw"},
		{"invalid url", "cannot parse https://synthetic-user:synthetic-password@[::1?token=synthetic-query-token", nil, []string{"synthetic-user", "synthetic-password", "synthetic-query-token"}, "cannot parse"},
		{"known secret", "upstream echoed synthetic+known/token and synthetic%2Bknown%2Ftoken", []string{"synthetic+known/token"}, []string{"synthetic+known/token", "synthetic%2Bknown%2Ftoken"}, "upstream echoed"},
		{"safe diagnostic", "cannot reach gateway.example.com: connection refused", nil, nil, "connection refused"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactText(tt.text, tt.secrets...)
			for _, leak := range tt.leaks {
				if strings.Contains(got, leak) {
					t.Fatalf("redacted text leaked %q: %s", leak, got)
				}
			}
			if !strings.Contains(got, tt.want) {
				t.Fatalf("redacted text lost diagnostic %q: %s", tt.want, got)
			}
		})
	}
}
