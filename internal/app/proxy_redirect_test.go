package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/egress"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

const redirectOrigin = "origin.redirect-test.example"
const redirectCDN = "cdn.redirect-test.example"
const redirectDenied = "denied.redirect-test.example"
const redirectProxy = "proxy.redirect-test.example"

type proxyRedirectRequest struct {
	host, path, method string
	headers            http.Header
}

type proxyRedirectFixture struct {
	client          UpstreamClient
	hooks           *egress.Hooks
	upstreamAddress string
	mu              sync.Mutex
	requests        []proxyRedirectRequest
	dials           []string
	proxyTargets    []string
	proxyRejected   map[string]bool
}

// These hooks describe only synthetic hosts and connect only to the fixture's
// own TLS listener. They never resolve DNS or dial an upstream outside the test.
// TLS still verifies the synthetic host against the fixture certificate.
func newProxyRedirectFixture(t *testing.T, handler http.HandlerFunc) *proxyRedirectFixture {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), DNSNames: []string{redirectOrigin, redirectCDN, redirectDenied},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	fixture := &proxyRedirectFixture{}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.mu.Lock()
		fixture.requests = append(fixture.requests, proxyRedirectRequest{
			host: r.Host, path: r.URL.Path, method: r.Method, headers: r.Header.Clone(),
		})
		fixture.mu.Unlock()
		handler(w, r)
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	server.StartTLS()
	fixture.upstreamAddress = strings.TrimPrefix(server.URL, "https://")
	addresses := map[string]string{
		redirectOrigin: "93.184.216.10", redirectCDN: "93.184.216.11", redirectDenied: "93.184.216.12", redirectProxy: "93.184.216.13",
	}
	oldLookup, oldDial, oldProxy := rawProxyLookupIP, rawProxyDialContext, rawProxyFromEnvironment
	rawProxyLookupIP = func(_ context.Context, _, host string) ([]net.IP, error) {
		address, ok := addresses[host]
		if !ok {
			return nil, errors.New("unmapped synthetic fixture host")
		}
		return []net.IP{net.ParseIP(address)}, nil
	}
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		fixture.mu.Lock()
		fixture.dials = append(fixture.dials, address)
		fixture.mu.Unlock()
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		known := false
		for name, ip := range addresses {
			known = known || host == name || host == ip
		}
		if !known {
			return nil, errors.New("unmapped synthetic fixture dial target")
		}
		return (&net.Dialer{}).DialContext(ctx, network, fixture.upstreamAddress)
	}
	rawProxyDialContext = dial
	rawProxyFromEnvironment = func(*http.Request) (*url.URL, error) { return nil, nil }
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = dial
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	hooks := rawEgressHooks()
	fixture.hooks = &hooks
	fixture.client = UpstreamClient{HTTPClient: &http.Client{Transport: transport, Timeout: time.Second}, EgressHooks: fixture.hooks}
	t.Cleanup(func() {
		rawProxyLookupIP, rawProxyDialContext, rawProxyFromEnvironment = oldLookup, oldDial, oldProxy
		transport.CloseIdleConnections()
		server.Close()
	})
	return fixture
}

func (f *proxyRedirectFixture) snapshot() []proxyRedirectRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]proxyRedirectRequest(nil), f.requests...)
}

func proxyRedirectFetch(ctx context.Context, client UpstreamClient, format, method string, member repository.Member, headers http.Header) (*http.Response, error) {
	if format == "maven" {
		return client.FetchMaven(ctx, method, member, "org/example/widget/1.0/widget-1.0.jar", headers)
	}
	return client.Fetch(ctx, method, member, "demo", "blobs", "sha256:synthetic", headers)
}

func proxyRedirectMember(proxy *repository.EgressProxy) repository.Member {
	return repository.Member{Type: repository.MemberProxy, Name: "synthetic-upstream", Endpoint: "https://" + redirectOrigin,
		AllowedHosts: []string{redirectOrigin, redirectCDN}, EgressProxy: proxy}
}

func closeProxyRedirectResponse(response *http.Response) {
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
}

func TestProxyRedirectApprovedChainPreservesDownloadRequest(t *testing.T) {
	for _, format := range []string{"maven", "oci"} {
		for _, mode := range proxyRedirectModes {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				t.Run(format+"/"+mode+"/"+method, func(t *testing.T) {
					fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
						switch r.URL.Path {
						case "/same-origin":
							http.Redirect(w, r, "https://"+redirectCDN+"/content?download=synthetic", http.StatusTemporaryRedirect)
						case "/content":
							w.Header().Set("Content-Type", "application/octet-stream")
							w.Header().Set("Content-Range", "bytes 4-7/8")
							w.Header().Set("ETag", `"download-v1"`)
							w.WriteHeader(http.StatusPartialContent)
							_, _ = io.WriteString(w, "load")
						default:
							http.Redirect(w, r, "/same-origin", http.StatusFound)
						}
					})
					proxy := configureProxyRedirectMode(t, fixture, mode)
					headers := http.Header{
						"Accept": {"application/octet-stream"}, "Range": {"bytes=4-7"},
						"If-None-Match": {`"previous-v1"`}, "If-Modified-Since": {"Wed, 21 Oct 2015 07:28:00 GMT"},
						"Authorization": {"Bearer synthetic-gateway-token"}, "Proxy-Authorization": {"Basic synthetic-proxy-token"},
					}
					response, err := proxyRedirectFetch(context.Background(), fixture.client, format, method, proxyRedirectMember(proxy), headers)
					if err != nil {
						closeProxyRedirectResponse(response)
						t.Fatalf("approved download chain: %v", err)
					}
					defer closeProxyRedirectResponse(response)
					body, err := io.ReadAll(response.Body)
					if err != nil || response.StatusCode != http.StatusPartialContent || response.Header.Get("Content-Range") != "bytes 4-7/8" || response.Header.Get("ETag") != `"download-v1"` {
						t.Fatalf("final response status=%d headers=%v body=%q err=%v", response.StatusCode, response.Header, body, err)
					}
					wantBody := "load"
					if method == http.MethodHead {
						wantBody = ""
					}
					if string(body) != wantBody {
						t.Fatalf("body=%q, want %q", body, wantBody)
					}
					requests := fixture.snapshot()
					if len(requests) != 3 || requests[2].host != redirectCDN {
						t.Fatalf("requests=%+v, want same-origin then approved cross-origin hop", requests)
					}
					for _, request := range requests {
						if request.method != method || request.headers.Get("Authorization") != "" || request.headers.Get("Proxy-Authorization") != "" {
							t.Fatalf("method or client credential boundary: %+v", request)
						}
						headerNames := []string{"Accept", "Range"}
						if format == "maven" {
							headerNames = append(headerNames, "If-None-Match", "If-Modified-Since")
						}
						for _, name := range headerNames {
							if request.headers.Get(name) != headers.Get(name) {
								t.Errorf("%s %s=%q, want %q", request.host, name, request.headers.Get(name), headers.Get(name))
							}
						}
					}
				})
			}
		}
	}
}

func TestProxyRedirectRejectsUnapprovedTargetBeforeRequest(t *testing.T) {
	for _, format := range []string{"maven", "oci"} {
		t.Run(format, func(t *testing.T) {
			fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.Host {
				case redirectOrigin:
					http.Redirect(w, r, "https://"+redirectCDN+"/approved", http.StatusFound)
				case redirectCDN:
					http.Redirect(w, r, "https://"+redirectDenied+"/unapproved?token=synthetic-private-query", http.StatusFound)
				default:
					_, _ = io.WriteString(w, "must not be requested")
				}
			})
			response, err := proxyRedirectFetch(context.Background(), fixture.client, format, http.MethodGet,
				proxyRedirectMember(&repository.EgressProxy{Mode: repository.EgressProxyModeDirect}), nil)
			closeProxyRedirectResponse(response)
			if err == nil || strings.Contains(err.Error(), "synthetic-private-query") {
				t.Fatalf("expected a safe redirect rejection, got %v", err)
			}
			requests := fixture.snapshot()
			if len(requests) != 2 || requests[1].host != redirectCDN {
				t.Fatalf("requests=%+v; approved hop must run and unapproved hop must not", requests)
			}
		})
	}
}

func TestProxyRedirectRejectsInvalidLocationBeforeRequest(t *testing.T) {
	for _, format := range []string{"maven", "oci"} {
		for _, test := range []struct{ name, location string }{
			{"downgrade", "http://" + redirectCDN + "/content"},
			{"userinfo", "https://synthetic-user:synthetic-password@" + redirectCDN + "/content"},
			{"scheme", "ftp://" + redirectCDN + "/content"},
			{"unapproved-port", "https://" + redirectOrigin + ":8443/content"},
			{"invalid-port", "https://" + redirectCDN + ":65536/content"},
			{"invalid-escape", "https://" + redirectCDN + "/%zz"},
		} {
			t.Run(format+"/"+test.name, func(t *testing.T) {
				fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Location", test.location)
					w.WriteHeader(http.StatusFound)
				})
				response, err := proxyRedirectFetch(context.Background(), fixture.client, format, http.MethodGet, proxyRedirectMember(nil), nil)
				closeProxyRedirectResponse(response)
				if err == nil || strings.Contains(err.Error(), "synthetic-password") {
					t.Fatalf("expected safe invalid-location rejection, got %v", err)
				}
				if requests := fixture.snapshot(); len(requests) != 1 {
					t.Fatalf("invalid redirect reached another target: %+v", requests)
				}
			})
		}
	}
}

func TestProxyRedirectRechecksDNSAtEachHop(t *testing.T) {
	for _, format := range []string{"maven", "oci"} {
		t.Run(format, func(t *testing.T) {
			fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Host == redirectOrigin {
					http.Redirect(w, r, "https://"+redirectCDN+"/content", http.StatusFound)
					return
				}
				_, _ = io.WriteString(w, "must not be requested")
			})
			lookup := fixture.hooks.LookupIP
			fixture.hooks.LookupIP = func(ctx context.Context, network, host string) ([]net.IP, error) {
				if host == redirectCDN {
					return []net.IP{net.ParseIP("10.20.30.40")}, nil
				}
				return lookup(ctx, network, host)
			}
			response, err := proxyRedirectFetch(context.Background(), fixture.client, format, http.MethodGet, proxyRedirectMember(nil), nil)
			closeProxyRedirectResponse(response)
			if err == nil || len(fixture.snapshot()) != 1 {
				t.Fatalf("private DNS result must reject before redirected request: err=%v requests=%+v", err, fixture.snapshot())
			}
		})
	}
}

func TestProxyRedirectLimitIsFiveHops(t *testing.T) {
	// Five redirects is the proposed #254 acceptance limit; it remains a
	// candidate contract until the implementation and HTTP suite are validated.
	for _, format := range []string{"maven", "oci"} {
		for _, redirects := range []int{5, 6} {
			t.Run(format+"/"+strconv.Itoa(redirects), func(t *testing.T) {
				fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
					hop, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/hop/"))
					if hop < redirects {
						http.Redirect(w, r, "/hop/"+strconv.Itoa(hop+1), http.StatusFound)
						return
					}
					_, _ = io.WriteString(w, "complete")
				})
				response, err := proxyRedirectFetch(context.Background(), fixture.client, format, http.MethodGet, proxyRedirectMember(nil), nil)
				defer closeProxyRedirectResponse(response)
				if redirects == 5 {
					if err != nil || response == nil || response.StatusCode != http.StatusOK {
						t.Fatalf("five redirects should succeed: response=%v err=%v", response, err)
					}
				} else if err == nil {
					t.Fatal("six redirects must fail at the configured bound")
				}
				if got := len(fixture.snapshot()); got != 6 {
					t.Fatalf("upstream requests=%d, want 6 including the initial request", got)
				}
			})
		}
	}
}

func TestProxyRedirectLoopStopsWithinBound(t *testing.T) {
	for _, format := range []string{"maven", "oci"} {
		t.Run(format, func(t *testing.T) {
			fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "/loop", http.StatusFound)
			})
			response, err := proxyRedirectFetch(context.Background(), fixture.client, format, http.MethodGet, proxyRedirectMember(nil), nil)
			closeProxyRedirectResponse(response)
			if err == nil || len(fixture.snapshot()) > 6 {
				t.Fatalf("loop must stop within five redirects: err=%v requests=%d", err, len(fixture.snapshot()))
			}
		})
	}
}

func TestProxyRedirectCancellationReachesCurrentHop(t *testing.T) {
	for _, format := range []string{"maven", "oci"} {
		t.Run(format, func(t *testing.T) {
			arrived := make(chan struct{})
			fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Host == redirectOrigin {
					http.Redirect(w, r, "https://"+redirectCDN+"/wait", http.StatusFound)
					return
				}
				close(arrived)
				<-r.Context().Done()
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				response, err := proxyRedirectFetch(ctx, fixture.client, format, http.MethodGet,
					proxyRedirectMember(&repository.EgressProxy{Mode: repository.EgressProxyModeDirect}), nil)
				closeProxyRedirectResponse(response)
				result <- err
			}()
			select {
			case <-arrived:
				cancel()
			case err := <-result:
				t.Fatalf("approved redirected hop was not reached: %v", err)
			case <-time.After(time.Second):
				t.Fatal("redirected hop was not reached before fixture deadline")
			}
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation=%v, want context.Canceled", err)
				}
			case <-time.After(time.Second):
				t.Fatal("cancellation did not stop the current hop")
			}
		})
	}
}

func TestProxyRedirectTimeoutBoundsWholeChain(t *testing.T) {
	for _, format := range []string{"maven", "oci"} {
		t.Run(format, func(t *testing.T) {
			fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
				timer := time.NewTimer(30 * time.Millisecond)
				defer timer.Stop()
				select {
				case <-r.Context().Done():
					return
				case <-timer.C:
				}
				if r.URL.Path != "/final" {
					http.Redirect(w, r, "/final", http.StatusFound)
					return
				}
				_, _ = io.WriteString(w, "complete")
			})
			fixture.client.HTTPClient.Timeout = 50 * time.Millisecond
			response, err := proxyRedirectFetch(context.Background(), fixture.client, format, http.MethodGet, proxyRedirectMember(nil), nil)
			closeProxyRedirectResponse(response)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("timeout=%v, want one deadline for the complete redirect chain", err)
			}
		})
	}
}
