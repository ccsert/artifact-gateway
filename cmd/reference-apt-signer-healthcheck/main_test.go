package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type healthRoundTrip func(*http.Request) (*http.Response, error)

func (function healthRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestHealthEndpointAndStatusContract(t *testing.T) {
	if got := healthEndpoint("[::1]:19083", ""); got != "http://[::1]:19083/healthz" {
		t.Fatalf("endpoint=%q", got)
	}
	if got := healthEndpoint("", ""); got != "http://127.0.0.1:18083/healthz" {
		t.Fatalf("default endpoint=%q", got)
	}
	if got := healthEndpoint("127.0.0.1:18083", "https"); got != "https://127.0.0.1:18083/healthz" {
		t.Fatalf("TLS endpoint=%q", got)
	}
	if got := healthEndpoint("0.0.0.0:18083", "https"); got != "https://127.0.0.1:18083/healthz" {
		t.Fatalf("wildcard listener endpoint=%q", got)
	}
	client := &http.Client{Transport: healthRoundTrip(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	if err := check(context.Background(), client, "http://127.0.0.1:18083/healthz"); err != nil {
		t.Fatal(err)
	}
	client.Transport = healthRoundTrip(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})
	if err := check(context.Background(), client, "http://127.0.0.1:18083/healthz"); err == nil {
		t.Fatal("check() error=nil")
	}
}

func TestFetchPublicKeyRequiresPGPKeyResponse(t *testing.T) {
	var output strings.Builder
	client := &http.Client{Transport: healthRoundTrip(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "http://127.0.0.1:18083/v1/public-key" {
			t.Fatalf("request URL=%s", request.URL)
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/pgp-keys"}},
			Body: io.NopCloser(strings.NewReader("public-key")),
		}, nil
	})}
	if err := fetchPublicKey(context.Background(), client, publicKeyEndpoint("", ""), &output); err != nil || output.String() != "public-key" {
		t.Fatalf("output=%q err=%v", output.String(), err)
	}
}
