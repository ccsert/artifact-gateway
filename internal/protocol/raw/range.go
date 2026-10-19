package raw

import (
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"slices"
	"strings"
)

const (
	maxRangeHeaderBytes = 8192
	maxRangeCount       = 16
	maxMultipartBytes   = 64 << 20
)

type byteInterval struct{ start, end int64 }

// parseRanges bounds work before allocating interval metadata. Duplicate,
// overlapping and adjacent ranges coalesce, so a client cannot amplify bytes
// by repeating an interval. Invalid syntax rejects the entire field; valid
// non-overlapping intervals outside the representation are ignored.
func parseRanges(value string, size int64) ([]byteInterval, bool) {
	if size <= 0 || len(value) > maxRangeHeaderBytes || !strings.HasPrefix(value, "bytes=") || strings.Count(value, ",") >= maxRangeCount {
		return nil, false
	}
	var intervals []byteInterval
	for _, member := range strings.Split(strings.TrimPrefix(value, "bytes="), ",") {
		member = strings.TrimSpace(member)
		parts := strings.SplitN(member, "-", 2)
		if len(parts) != 2 {
			return nil, false
		}
		// Validate even unsatisfiable members, including overflow and reversed
		// bounds, before deciding to omit them.
		if parts[0] != "" {
			start, err := parseRangeByte(parts[0])
			if err != nil {
				return nil, false
			}
			if parts[1] != "" {
				end, err := parseRangeByte(parts[1])
				if err != nil || end < start {
					return nil, false
				}
			}
			if start >= size {
				continue
			}
		}
		start, end, ok := parseRange("bytes="+member, size)
		if !ok {
			return nil, false
		}
		intervals = append(intervals, byteInterval{start, end})
	}
	if len(intervals) == 0 {
		return nil, false
	}
	slices.SortFunc(intervals, func(a, b byteInterval) int {
		if a.start < b.start {
			return -1
		}
		if a.start > b.start {
			return 1
		}
		return 0
	})
	merged := intervals[:1]
	for _, interval := range intervals[1:] {
		last := &merged[len(merged)-1]
		if interval.start <= last.end+1 {
			last.end = max(last.end, interval.end)
		} else {
			merged = append(merged, interval)
		}
	}
	// A single interval preserves the existing large single-range contract.
	// Only multipart responses have the aggregate 64 MiB payload limit.
	if len(merged) > 1 {
		var total int64
		for _, interval := range merged {
			length := interval.end - interval.start + 1
			if length > maxMultipartBytes-total {
				return nil, false
			}
			total += length
		}
	}
	return merged, true
}

func serveMultipart(w *statusWriter, r *http.Request, content Content, size int64, intervals []byteInterval) ServeResult {
	boundary := multipart.NewWriter(io.Discard).Boundary()
	contentType := content.ContentType
	if contentType == "" || len(contentType) > 1024 || strings.ContainsAny(contentType, "\r\n") {
		contentType = "application/octet-stream"
	}
	prefixes := make([]string, len(intervals))
	var length int64
	for i, interval := range intervals {
		prefix := "--" + boundary + "\r\nContent-Range: bytes " + utoa(uint64(interval.start)) + "-" + utoa(uint64(interval.end)) + "/" + utoa(uint64(size)) + "\r\nContent-Type: " + contentType + "\r\n\r\n"
		if i > 0 {
			prefix = "\r\n" + prefix
		}
		prefixes[i] = prefix
		length += int64(len(prefix)) + interval.end - interval.start + 1
	}
	closing := "\r\n--" + boundary + "--\r\n"
	length += int64(len(closing))
	// Open only the first interval before committing headers. Later streams
	// open sequentially against this same immutable source, never the path.
	reader, sourceSize, err := openContentRange(r.Context(), content, intervals[0].start, intervals[0].end-intervals[0].start+1)
	if err != nil || sourceSize != size {
		if reader != nil {
			_ = reader.Close()
		}
		http.Error(w, "Raw object unavailable", http.StatusInternalServerError)
		return w.result()
	}
	w.Header().Set("Content-Type", "multipart/byteranges; boundary="+boundary)
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Length", utoa(uint64(length)))
	w.Header().Del("Content-Range")
	w.WriteHeader(http.StatusPartialContent)
	for i, interval := range intervals {
		if i > 0 {
			if r.Context().Err() != nil {
				return w.result()
			}
			reader, sourceSize, err = openContentRange(r.Context(), content, interval.start, interval.end-interval.start+1)
			if err != nil || sourceSize != size {
				if reader != nil {
					_ = reader.Close()
				}
				return w.result()
			}
		}
		if _, err := io.WriteString(w, prefixes[i]); err != nil {
			_ = reader.Close()
			return w.result()
		}
		_, err = io.CopyN(w, contextReader{ctx: r.Context(), Reader: reader}, interval.end-interval.start+1)
		_ = reader.Close()
		if err != nil {
			// Do not write a closing delimiter after a failed/short read. The
			// advertised length remains incomplete so clients cannot accept a
			// successful multipart representation with missing bytes.
			return w.result()
		}
	}
	if r.Context().Err() == nil {
		_, _ = io.WriteString(w, closing)
	}
	return w.result()
}

type contextReader struct {
	ctx context.Context
	io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

func ifRangeMatches(value, etag string) bool {
	if value == "" {
		return true
	}
	value = strings.TrimSpace(value)
	return etag != "" && value == etag && !strings.HasPrefix(value, "W/") && !strings.HasPrefix(etag, "W/")
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
