package nexussource

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type sourceReadTransport func(*http.Request) (*http.Response, error)

func (f sourceReadTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type sourceReadError struct{ err error }

func (r sourceReadError) Read([]byte) (int, error) { return 0, r.err }
func (sourceReadError) Close() error               { return nil }

type sourceBodyTimeout struct{}

func (sourceBodyTimeout) Error() string   { return "synthetic private upstream read timeout" }
func (sourceBodyTimeout) Timeout() bool   { return true }
func (sourceBodyTimeout) Temporary() bool { return true }

// The deadline has elapsed, but the timer's cancellation notification has not
// run yet. Reading Deadline and Err are separate observations in real requests.
type sourceDelayedDeadline struct {
	context.Context
	deadline time.Time
}

func (c sourceDelayedDeadline) Deadline() (time.Time, bool) { return c.deadline, true }

func TestNexusReadTimeoutClassificationDoesNotDependOnCancellationNotification(t *testing.T) {
	active := context.Background()
	delayed := sourceDelayedDeadline{Context: active, deadline: time.Now().Add(-time.Hour)}
	cases := []struct {
		name string
		ctx  context.Context
		body io.ReadCloser
		code string
	}{
		{name: "body net timeout with active context", ctx: active, body: sourceReadError{&net.OpError{Op: "read", Net: "tcp", Err: sourceBodyTimeout{}}}, code: "source_timeout"},
		{name: "body wrapped deadline with active context", ctx: active, body: sourceReadError{fmt.Errorf("read response: %w", context.DeadlineExceeded)}, code: "source_timeout"},
		{name: "body canceled with active context", ctx: active, body: sourceReadError{context.Canceled}, code: "source_timeout"},
		{name: "deadline passed before empty EOF", ctx: delayed, body: io.NopCloser(strings.NewReader("")), code: "source_timeout"},
		{name: "deadline passed before valid JSON", ctx: delayed, body: io.NopCloser(strings.NewReader(`[]`)), code: "source_timeout"},
		{name: "active empty response is malformed", ctx: active, body: io.NopCloser(strings.NewReader("")), code: "source_invalid_response"},
		{name: "active invalid JSON is malformed", ctx: active, body: io.NopCloser(strings.NewReader(`{secret`)), code: "source_invalid_response"},
		{name: "active body EOF is malformed", ctx: active, body: sourceReadError{io.ErrUnexpectedEOF}, code: "source_invalid_response"},
		{name: "active body non-timeout error is malformed", ctx: active, body: sourceReadError{errors.New("synthetic private upstream detail")}, code: "source_invalid_response"},
		{name: "active valid response", ctx: active, body: io.NopCloser(strings.NewReader(`[]`))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.ctx.Err() != nil {
				t.Fatal("fixture must not rely on context cancellation")
			}
			s := session{ctx: tc.ctx, base: &url.URL{Scheme: "https", Host: "source.example.test"}, client: &http.Client{Transport: sourceReadTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: tc.body, Request: r}, nil
			})}}
			var out []repoRaw
			err := s.read("repositories", nil, &out)
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			requireCode(t, err, tc.code)
		})
	}
}

type sourceCanceledDecode struct{ cancel context.CancelFunc }

func (out *sourceCanceledDecode) UnmarshalJSON([]byte) error {
	out.cancel()
	return nil
}

func TestNexusReadRejectsJSONCompletedAfterOperationCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := session{ctx: ctx, base: &url.URL{Scheme: "https", Host: "source.example.test"}, client: &http.Client{Transport: sourceReadTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`[]`)), Request: r}, nil
	})}}
	requireCode(t, s.read("repositories", nil, &sourceCanceledDecode{cancel: cancel}), "source_timeout")
}
