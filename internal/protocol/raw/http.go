// Package raw owns the Raw HTTP protocol grammar and response representation.
package raw

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/v2contract"
)

// Client fetches a complete canonical Raw representation from a member.
// Credential and transport policy remain in the runtime adapter.
type Client interface {
	FetchRaw(context.Context, string, repository.Member, string, http.Header) (*http.Response, error)
}

type Content struct {
	Body        []byte
	Digest      string
	ContentType string
}

type ServeResult struct {
	Status int
	Bytes  int64
}

// ParsePath accepts only a canonical Raw resource below a named group.
func ParsePath(path string) (string, string, bool) {
	const prefix = "/raw/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) < 2 || parts[0] == "" {
		return "", "", false
	}
	resource, ok := v2contract.NewCanonicalResource(strings.Join(parts[1:], "/"))
	if !ok {
		return "", "", false
	}
	return parts[0], resource.String(), true
}

// ParseDirectoryPath accepts a canonical directory prefix for Native Raw
// listing. A trailing slash is required so it cannot collide with a file path.
func ParseDirectoryPath(path string) (string, string, bool) {
	const prefix = "/raw/"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, "/") {
		return "", "", false
	}
	rest := strings.TrimSuffix(strings.TrimPrefix(path, prefix), "/")
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", "", false
	}
	if len(parts) == 1 {
		return parts[0], "", true
	}
	resource, ok := v2contract.NewCanonicalResource(strings.Join(parts[1:], "/"))
	if !ok {
		return "", "", false
	}
	return parts[0], resource.String() + "/", true
}

// ValidChecksum verifies the canonical hexadecimal checksum sidecar formats
// served by Raw repositories.
func ValidChecksum(path string, body []byte) bool {
	value := strings.TrimSuffix(string(body), "\n")
	wantLength := 64
	if strings.HasSuffix(path, ".sha512") {
		wantLength = 128
	}
	if len(value) != wantLength {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// MemberProxyAllowed validates an explicitly configured proxy member before
// the runtime creates a network request.
func MemberProxyAllowed(member repository.Member) bool {
	u, err := url.Parse(member.Endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" {
		return false
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && privateIP(ip) {
		return false
	}
	for _, host := range member.AllowedHosts {
		if strings.EqualFold(strings.TrimSpace(host), u.Hostname()) {
			return true
		}
	}
	return false
}

// ServeContent writes Raw's single canonical representation, applying HTTP
// conditional and Range semantics locally.
func ServeContent(w http.ResponseWriter, r *http.Request, name string, content Content) ServeResult {
	statusWriter := &statusWriter{ResponseWriter: w}
	w.Header().Set("Content-Type", content.ContentType)
	etag := `"sha256-` + content.Digest + `"`
	w.Header().Set("ETag", etag)
	digest, _ := hex.DecodeString(content.Digest)
	w.Header().Set("Digest", "sha-256="+base64.StdEncoding.EncodeToString(digest))
	if r.Header.Get("If-None-Match") == etag {
		statusWriter.WriteHeader(http.StatusNotModified)
		return statusWriter.result()
	}
	if ranges := r.Header.Values("Range"); len(ranges) > 0 {
		if len(ranges) != 1 || strings.Contains(ranges[0], ",") {
			statusWriter.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return statusWriter.result()
		}
		http.ServeContent(statusWriter, r, name, time.Time{}, bytes.NewReader(content.Body))
		return statusWriter.result()
	}
	w.Header().Set("Content-Length", utoa(uint64(len(content.Body))))
	statusWriter.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = statusWriter.Write(content.Body)
	}
	return statusWriter.result()
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(body)
	w.bytes += int64(n)
	return n, err
}

func (w *statusWriter) result() ServeResult {
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return ServeResult{Status: status}
	}
	return ServeResult{Status: status, Bytes: w.bytes}
}

func utoa(value uint64) string {
	if value == 0 {
		return "0"
	}
	var buffer [20]byte
	index := len(buffer)
	for value > 0 {
		index--
		buffer[index] = byte('0' + value%10)
		value /= 10
	}
	return string(buffer[index:])
}
