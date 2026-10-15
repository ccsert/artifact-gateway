package agctl

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

// readDocument performs one GET and returns the server document unchanged
// for JSON output together with its typed form for human rendering, so a
// field the client does not know yet still reaches scripts and agents.
func readDocument[T any](ctx context.Context, client *apiClient, path string, query url.Values) (json.RawMessage, T, error) {
	var typed T
	var raw json.RawMessage
	if err := client.getJSON(ctx, path, query, &raw); err != nil {
		return nil, typed, err
	}
	if err := json.Unmarshal(raw, &typed); err != nil || len(raw) == 0 || string(raw) == "null" {
		return nil, typed, &APIError{Code: "invalid_response", HTTPStatus: 200, Message: "server returned an unreadable JSON response"}
	}
	return raw, typed, nil
}

// documentCommand is a single-read command: build the request from the
// arguments, read it, and print it.
type documentCommand[T any] struct {
	request func(env *commandContext) (string, url.Values, error)
	render  func(io.Writer, T) error
	meta    func(T) *envelopeMeta
}

func (d documentCommand[T]) run(ctx context.Context, env *commandContext) error {
	path, query, err := d.request(env)
	if err != nil {
		return err
	}
	client, err := env.client()
	if err != nil {
		return err
	}
	raw, typed, err := readDocument[T](ctx, client, path, query)
	if err != nil {
		return err
	}
	var meta *envelopeMeta
	if d.meta != nil {
		meta = d.meta(typed)
	}
	return env.render(raw, meta, func(w io.Writer) error { return d.render(w, typed) })
}

// requireFlag rejects a missing required flag before any request.
func requireFlag(name, value, hint string) error {
	if strings.TrimSpace(value) == "" {
		return &UsageError{Message: "missing required flag --" + name, Hint: hint}
	}
	return nil
}

func requireRepo(repo string) error {
	return requireFlag("repo", repo, "pass the repository ID with --repo; agctl repo list shows it")
}

func optional[T any](value *T) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(*value)
}

func formatTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func formatBytes(value *int64) string {
	if value == nil {
		return ""
	}
	return humanBytes(*value)
}

func humanBytes(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(size)/float64(div), "KMGTPE"[exp])
}

func shortDigest(digest string) string {
	if len(digest) > 19 && strings.HasPrefix(digest, "sha256:") {
		return digest[:19]
	}
	return digest
}

func emptyLine(w io.Writer, noun string) error {
	_, err := fmt.Fprintf(w, "no %s\n", noun)
	return err
}

func keyValues(w io.Writer, lines [][2]string) error {
	width := 0
	for _, line := range lines {
		if len(line[0]) > width {
			width = len(line[0])
		}
	}
	for _, line := range lines {
		if line[1] == "" {
			continue
		}
		if _, err := fmt.Fprintf(w, "%-*s %s\n", width+1, line[0]+":", line[1]); err != nil {
			return err
		}
	}
	return nil
}
