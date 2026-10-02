package operationalog

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
)

// Validate success response content before it reaches the official exporter.
// The patched exporter owns HTTP Retry-After parsing and bounded retries.
type otlpHTTPTransport struct {
	base http.RoundTripper
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
	// The patched SDK handles seconds and HTTP dates, but ParseInt ignores
	// unsigned decimal values above int64. Saturate only that overflow case.
	seconds, parseErr := strconv.ParseUint(response.Header.Get("Retry-After"), 10, 64)
	if seconds > 1<<63-1 && (parseErr == nil || errors.Is(parseErr, strconv.ErrRange)) {
		copy := *response
		copy.Header = response.Header.Clone()
		copy.Header.Set("Retry-After", "9223372036854775807")
		return &copy, nil
	}
	return response, nil
}
