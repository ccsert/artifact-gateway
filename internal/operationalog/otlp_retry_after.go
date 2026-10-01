package operationalog

import (
	"errors"
	"net/http"
	"strconv"
	"time"
)

// The pinned official v0.20.0 exporter interprets Retry-After integers as
// nanoseconds instead of HTTP seconds and does not parse HTTP dates. Adapt
// only its private response header; the official exporter still owns retries.
// Delays beyond the export budget prevent retry instead of extending it.
type otlpRetryAfterTransport struct {
	base    http.RoundTripper
	maximum time.Duration
}

func (t otlpRetryAfterTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil || response == nil {
		return response, err
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
