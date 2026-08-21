// Package raw owns the Raw HTTP protocol grammar and response representation.
package raw

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

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
	Size        int64
	Source      ContentSource
}

type ContentSource interface {
	Open(context.Context) (io.ReadCloser, int64, error)
	OpenRange(context.Context, int64, int64) (io.ReadCloser, int64, error)
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
	size := content.Size
	if content.Source == nil {
		size = int64(len(content.Body))
	}
	ranges := r.Header.Values("Range")
	if len(ranges) > 0 && !ifRangeMatches(r.Header.Get("If-Range"), w.Header().Get("ETag")) {
		ranges = nil
	}
	if len(ranges) > 0 {
		if len(ranges) != 1 || strings.Contains(ranges[0], ",") {
			w.Header().Set("Content-Range", "bytes */"+utoa(uint64(size)))
			statusWriter.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return statusWriter.result()
		}
		start, end, ok := parseRange(ranges[0], size)
		if !ok {
			w.Header().Set("Content-Range", "bytes */"+utoa(uint64(size)))
			statusWriter.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return statusWriter.result()
		}
		length := end - start + 1
		var reader io.ReadCloser
		if r.Method != http.MethodHead {
			var err error
			reader, _, err = openContentRange(r.Context(), content, start, length)
			if err != nil {
				http.Error(statusWriter, "Raw object unavailable", http.StatusInternalServerError)
				return statusWriter.result()
			}
			defer func() { _ = reader.Close() }()
		}
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Range", "bytes "+utoa(uint64(start))+"-"+utoa(uint64(end))+"/"+utoa(uint64(size)))
		w.Header().Set("Content-Length", utoa(uint64(length)))
		statusWriter.WriteHeader(http.StatusPartialContent)
		if reader != nil {
			_, _ = io.CopyN(statusWriter, reader, length)
		}
		return statusWriter.result()
	}
	var reader io.ReadCloser
	if r.Method != http.MethodHead {
		var err error
		reader, _, err = openContent(r.Context(), content)
		if err != nil {
			http.Error(statusWriter, "Raw object unavailable", http.StatusInternalServerError)
			return statusWriter.result()
		}
		defer func() { _ = reader.Close() }()
	}
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Length", utoa(uint64(size)))
	statusWriter.WriteHeader(http.StatusOK)
	if reader != nil {
		_, _ = io.Copy(statusWriter, reader)
	}
	return statusWriter.result()
}

func ifRangeMatches(value, etag string) bool {
	if value == "" {
		return true
	}
	value = strings.TrimSpace(value)
	return etag != "" && value == etag && !strings.HasPrefix(value, "W/") && !strings.HasPrefix(etag, "W/")
}

func openContent(ctx context.Context, content Content) (io.ReadCloser, int64, error) {
	if content.Source != nil {
		return content.Source.Open(ctx)
	}
	return io.NopCloser(bytes.NewReader(content.Body)), int64(len(content.Body)), nil
}

func openContentRange(ctx context.Context, content Content, offset, length int64) (io.ReadCloser, int64, error) {
	if content.Source != nil {
		return content.Source.OpenRange(ctx, offset, length)
	}
	if offset < 0 || length < 0 || offset > int64(len(content.Body)) || length > int64(len(content.Body))-offset {
		return nil, 0, errors.New("raw content range is out of bounds")
	}
	return io.NopCloser(bytes.NewReader(content.Body[offset : offset+length])), int64(len(content.Body)), nil
}

func parseRange(value string, size int64) (int64, int64, bool) {
	if size < 0 || !strings.HasPrefix(value, "bytes=") {
		return 0, 0, false
	}
	parts := strings.SplitN(strings.TrimPrefix(value, "bytes="), "-", 2)
	if len(parts) != 2 || strings.Contains(value, ",") {
		return 0, 0, false
	}
	start, end := int64(0), size-1
	if parts[0] == "" {
		suffix, err := parseRangeByte(parts[1])
		if err != nil || suffix <= 0 || size == 0 {
			return 0, 0, false
		}
		if suffix < size {
			start = size - suffix
		}
		return start, end, true
	}
	valueStart, err := parseRangeByte(parts[0])
	if err != nil || valueStart >= size {
		return 0, 0, false
	}
	start = valueStart
	if parts[1] != "" {
		valueEnd, err := parseRangeByte(parts[1])
		if err != nil || valueEnd < start {
			return 0, 0, false
		}
		end = valueEnd
		if end >= size {
			end = size - 1
		}
	}
	return start, end, true
}

func parseRangeByte(value string) (int64, error) {
	if value == "" {
		return 0, errors.New("empty range")
	}
	var result int64
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, errors.New("invalid range")
		}
		digit := int64(char - '0')
		if result > ((1<<63-1)-digit)/10 {
			return 0, errors.New("range overflow")
		}
		result = result*10 + digit
	}
	return result, nil
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
