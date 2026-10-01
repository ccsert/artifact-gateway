package operationalog

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"
)

// The pinned official v0.20.0 exporter interprets Retry-After integers as
// nanoseconds instead of HTTP seconds and does not parse HTTP dates. Adapt
// only its private response header; the official exporter still owns retries.
// Delays beyond the export budget prevent retry instead of extending it.
type otlpHTTPTransport struct {
	base    http.RoundTripper
	maximum time.Duration
}

func (t otlpHTTPTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil || response == nil {
		return response, err
	}
	if response.StatusCode >= 200 && response.StatusCode <= 299 {
		contentType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if err != nil || contentType != "application/x-protobuf" {
			// Empty protobuf success is valid. A nonempty HTML/JSON or unknown
			// response must not bypass the official exporter's protobuf parser.
			if response.Body != nil {
				var first [1]byte
				n, readErr := io.ReadFull(response.Body, first[:])
				closeErr := response.Body.Close()
				if n != 0 || readErr != io.EOF || closeErr != nil {
					return nil, errors.New("invalid OTLP Logs success response")
				}
			}
			copy := *response
			copy.Body = http.NoBody
			response = &copy
		}
		copy := *response
		copy.Header = response.Header.Clone()
		if copy.Header == nil {
			copy.Header = make(http.Header)
		}
		copy.Header.Set("Content-Type", "application/x-protobuf")
		return &copy, nil
	}
	raw := response.Header.Get("Retry-After")
	var delay time.Duration
	if seconds, err := strconv.ParseUint(raw, 10, 64); err == nil {
		if seconds > uint64(t.maximum/time.Second) {
			delay = t.maximum
		} else {
			delay = time.Duration(seconds) * time.Second
		}
	} else if errors.Is(err, strconv.ErrRange) {
		delay = t.maximum
	} else if date, err := http.ParseTime(raw); err == nil {
		delay = max(0, min(time.Until(date), t.maximum))
	} else {
		return response, nil
	}
	copy := *response
	copy.Header = response.Header.Clone()
	copy.Header.Set("Retry-After", strconv.FormatInt(int64(delay), 10))
	return &copy, nil
}
