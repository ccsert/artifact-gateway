package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCheckAcceptsOnlyNoContent(t *testing.T) {
	client := &http.Client{Transport: healthRoundTrip(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.String() != "http://127.0.0.1:18082/livez" {
			t.Fatalf("request=%s %s", request.Method, request.URL)
		}
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	if err := check(context.Background(), client, "http://127.0.0.1:18082/livez"); err != nil {
		t.Fatal(err)
	}

	client.Transport = healthRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})
	if err := check(context.Background(), client, "http://127.0.0.1:18082/livez"); err == nil {
		t.Fatal("check() error=nil")
	}
}

func TestCheckPropagatesTransportFailure(t *testing.T) {
	want := errors.New("unreachable")
	client := &http.Client{Transport: healthRoundTrip(func(*http.Request) (*http.Response, error) { return nil, want })}
	if err := check(context.Background(), client, "http://127.0.0.1:18082/livez"); !errors.Is(err, want) {
		t.Fatalf("error=%v", err)
	}
}

func TestHealthEndpointUsesConfiguredLoopbackAddress(t *testing.T) {
	if got := healthEndpoint("[::1]:19082"); got != "http://[::1]:19082/livez" {
		t.Fatalf("endpoint=%q", got)
	}
	if got := healthEndpoint(""); got != "http://127.0.0.1:18082/livez" {
		t.Fatalf("default endpoint=%q", got)
	}
}

type healthRoundTrip func(*http.Request) (*http.Response, error)

func (function healthRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
