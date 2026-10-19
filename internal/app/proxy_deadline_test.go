package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/repository"
)

// A context timer can reach its fixed deadline before the runtime delivers its
// cancellation notification. Keep that notification pending to reproduce the
// response race without depending on scheduler load or changing the deadline.
type proxyPendingDeadlineContext struct {
	context.Context
	deadline time.Time
	done     chan struct{}
}

func (c proxyPendingDeadlineContext) Deadline() (time.Time, bool) { return c.deadline, true }
func (c proxyPendingDeadlineContext) Done() <-chan struct{}       { return c.done }
func (c proxyPendingDeadlineContext) Err() error                  { return nil }

type proxyDeadlineTransport func(*http.Request) (*http.Response, error)

func (f proxyDeadlineTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type proxyDeadlineBody struct {
	io.Reader
	closed bool
}

func (b *proxyDeadlineBody) Close() error {
	b.closed = true
	return nil
}

func TestProxyRejectsResponsePastDeadlineBeforeDone(t *testing.T) {
	for _, format := range []string{"maven", "oci"} {
		t.Run(format, func(t *testing.T) {
			deadline := time.Now().Add(10 * time.Millisecond)
			ctx := proxyPendingDeadlineContext{Context: context.Background(), deadline: deadline, done: make(chan struct{})}
			body := &proxyDeadlineBody{Reader: strings.NewReader("late content")}
			var responseContext context.Context
			client := UpstreamClient{AllowHTTPForTesting: true, HTTPClient: &http.Client{
				Timeout: time.Second,
				Transport: proxyDeadlineTransport(func(request *http.Request) (*http.Response, error) {
					responseContext = request.Context()
					timer := time.NewTimer(time.Until(deadline))
					defer timer.Stop()
					<-timer.C
					if time.Now().Before(deadline) || request.Context().Err() != nil {
						t.Fatal("fixture must return after the fixed deadline while Done is still pending")
					}
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, Request: request}, nil
				}),
			}}
			member := repository.Member{Type: repository.MemberProxy, Endpoint: "http://deadline.fixture.example"}
			response, err := proxyRedirectFetch(ctx, client, format, http.MethodGet, member, nil)
			closedBeforeCaller := body.closed
			closeProxyRedirectResponse(response)
			if response != nil || !errors.Is(err, context.DeadlineExceeded) || !closedBeforeCaller {
				t.Fatalf("late response=%v error=%v closed=%v, want no response, deadline exceeded and closed body", response != nil, err, closedBeforeCaller)
			}
			if !errors.Is(responseContext.Err(), context.Canceled) {
				t.Fatal("rejecting a response must release its context while the parent Done is still pending")
			}
		})
	}
}

func TestProxyRejectsResponseAfterCancellation(t *testing.T) {
	for _, format := range []string{"maven", "oci"} {
		t.Run(format, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			body := &proxyDeadlineBody{Reader: strings.NewReader("canceled content")}
			client := UpstreamClient{AllowHTTPForTesting: true, HTTPClient: &http.Client{
				Timeout: time.Second,
				Transport: proxyDeadlineTransport(func(request *http.Request) (*http.Response, error) {
					cancel()
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, Request: request}, nil
				}),
			}}
			member := repository.Member{Type: repository.MemberProxy, Endpoint: "http://deadline.fixture.example"}
			response, err := proxyRedirectFetch(ctx, client, format, http.MethodGet, member, nil)
			closedBeforeCaller := body.closed
			closeProxyRedirectResponse(response)
			if response != nil || !errors.Is(err, context.Canceled) || !closedBeforeCaller {
				t.Fatalf("canceled response=%v error=%v closed=%v, want no response, canceled and closed body", response != nil, err, closedBeforeCaller)
			}
		})
	}
}

func TestProxyResponseBeforeDeadlineRemainsReadable(t *testing.T) {
	for _, format := range []string{"maven", "oci"} {
		t.Run(format, func(t *testing.T) {
			body := &proxyDeadlineBody{Reader: strings.NewReader("timely content")}
			var responseContext context.Context
			client := UpstreamClient{AllowHTTPForTesting: true, HTTPClient: &http.Client{
				Timeout: time.Second,
				Transport: proxyDeadlineTransport(func(request *http.Request) (*http.Response, error) {
					responseContext = request.Context()
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, Request: request}, nil
				}),
			}}
			member := repository.Member{Type: repository.MemberProxy, Endpoint: "http://deadline.fixture.example"}
			response, err := proxyRedirectFetch(context.Background(), client, format, http.MethodGet, member, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer closeProxyRedirectResponse(response)
			if body.closed || responseContext.Err() != nil {
				t.Fatal("timely response must remain open for the caller")
			}
			content, err := io.ReadAll(response.Body)
			if err != nil || string(content) != "timely content" {
				t.Fatalf("content=%q error=%v", content, err)
			}
			closeProxyRedirectResponse(response)
			if !body.closed || !errors.Is(responseContext.Err(), context.Canceled) {
				t.Fatal("closing the response must close its body and release the context")
			}
		})
	}
}
