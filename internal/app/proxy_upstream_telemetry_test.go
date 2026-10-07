package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestProxyRedirectTelemetryOmitsTargetsQueriesAndRawErrors(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := trace.NewTracerProvider(trace.WithSyncer(exporter))
	oldProvider, oldPropagation := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(oldProvider)
		otel.SetTextMapPropagator(oldPropagation)
		_ = provider.Shutdown(context.Background())
	})
	for _, fail := range []bool{false, true} {
		fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Traceparent") == "" {
				t.Error("proxy request did not propagate trace context")
			}
			if r.URL.Path != "/content" {
				http.Redirect(w, r, "https://"+redirectCDN+"/content?signature=synthetic-signed-query", http.StatusFound)
				return
			}
			_, _ = io.WriteString(w, "artifact")
		})
		if fail {
			fixture.hooks.DialContext = func(context.Context, string, string) (net.Conn, error) {
				return nil, errors.New("synthetic-secret-dial-error")
			}
		}
		response, err := proxyRedirectFetch(context.Background(), fixture.client, "maven", http.MethodGet,
			proxyRedirectMember(&repository.EgressProxy{Mode: repository.EgressProxyModeDirect}), nil)
		closeProxyRedirectResponse(response)
		if (err != nil) != fail {
			t.Fatalf("failure=%t: %v", fail, err)
		}
	}
	spans := exporter.GetSpans()
	if len(spans) != 3 {
		t.Fatalf("spans = %d, want two download hops and one failed dial", len(spans))
	}
	encoded, err := json.Marshal(spans)
	if err != nil {
		t.Fatal(err)
	}
	for _, sensitive := range []string{redirectOrigin, redirectCDN, "synthetic-signed-query", "synthetic-secret-dial-error"} {
		if strings.Contains(string(encoded), sensitive) {
			t.Fatalf("proxy telemetry contains synthetic sensitive marker %q", sensitive)
		}
	}
}

func TestProxyRedirectTransportCannotRestoreCookiesOrBypassBoundDial(t *testing.T) {
	for _, format := range []string{"maven", "oci"} {
		t.Run(format, func(t *testing.T) {
			fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Host == redirectOrigin {
					http.Redirect(w, r, "https://"+redirectCDN+"/content", http.StatusFound)
					return
				}
				_, _ = io.WriteString(w, "artifact")
			})
			jar, err := cookiejar.New(nil)
			if err != nil {
				t.Fatal(err)
			}
			origin, _ := url.Parse("https://" + redirectOrigin)
			jar.SetCookies(origin, []*http.Cookie{{Name: "synthetic-session", Value: "synthetic-cookie", Domain: "redirect-test.example", Path: "/", Secure: true}})
			fixture.client.HTTPClient.Jar = jar
			response, err := proxyRedirectFetch(context.Background(), fixture.client, format, http.MethodGet, proxyRedirectMember(nil), nil)
			closeProxyRedirectResponse(response)
			if err != nil {
				t.Fatal(err)
			}
			if got := fixture.snapshot(); len(got) != 2 || got[0].headers.Get("Cookie") != "" || got[1].headers.Get("Cookie") != "" {
				t.Fatalf("caller cookie jar reached an upstream: %+v", got)
			}
			if fixture.client.HTTPClient.Jar != jar {
				t.Fatal("caller cookie jar changed")
			}
			base := fixture.client.HTTPClient.Transport.(*http.Transport)
			base.DialTLSContext = func(context.Context, string, string) (net.Conn, error) {
				t.Error("inherited TLS dial bypassed approved connection binding")
				return nil, errors.New("synthetic TLS dial override")
			}
			response, err = proxyRedirectFetch(context.Background(), fixture.client, format, http.MethodGet, proxyRedirectMember(nil), nil)
			closeProxyRedirectResponse(response)
			if err == nil || len(fixture.snapshot()) != 2 || base.DialTLSContext == nil {
				t.Fatal("TLS dialing override must fail before an upstream request, preserving the caller transport")
			}
		})
	}
}

func TestProxyRedirectClientCanReturnLastResponse(t *testing.T) {
	fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://"+redirectCDN+"/content")
		w.WriteHeader(http.StatusFound)
		_, _ = io.WriteString(w, "redirect response")
	})
	fixture.client.HTTPClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	response, err := proxyRedirectFetch(context.Background(), fixture.client, "maven", http.MethodGet,
		proxyRedirectMember(&repository.EgressProxy{Mode: repository.EgressProxyModeDirect}), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeProxyRedirectResponse(response)
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusFound || string(body) != "redirect response" {
		t.Fatalf("last response: status=%d body=%q error=%v", response.StatusCode, body, err)
	}
	if hops := len(fixture.snapshot()); hops != 1 {
		t.Fatalf("hops=%d, want one", hops)
	}
}
