package app

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestProxyRedirectEveryModeRejectsUnapprovedHop(t *testing.T) {
	for _, format := range []string{"maven", "oci"} {
		for _, mode := range proxyRedirectModes {
			t.Run(format+"/"+mode, func(t *testing.T) {
				fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
					switch {
					case r.Host == redirectOrigin && r.URL.Path != "/same-origin":
						http.Redirect(w, r, "/same-origin", http.StatusFound)
					case r.Host == redirectOrigin:
						http.Redirect(w, r, "https://"+redirectCDN+"/approved", http.StatusTemporaryRedirect)
					case r.Host == redirectCDN:
						http.Redirect(w, r, "https://"+redirectDenied+"/rejected?secret=synthetic-private-query", http.StatusFound)
					default:
						_, _ = io.WriteString(w, "must not be requested")
					}
				})
				member := proxyRedirectMember(configureProxyRedirectMode(t, fixture, mode))
				response, err := proxyRedirectFetch(context.Background(), fixture.client, format, http.MethodGet, member, nil)
				closeProxyRedirectResponse(response)
				if err == nil || strings.Contains(err.Error(), "synthetic-private-query") || strings.Contains(err.Error(), redirectDenied) {
					t.Fatalf("expected safe unapproved-hop rejection, got %v", err)
				}
				requests := fixture.snapshot()
				if len(requests) != 3 || requests[2].host != redirectCDN {
					t.Fatalf("approved hops must run, denied hop must not: %+v", requests)
				}
				for _, target := range fixture.proxySnapshot() {
					if strings.HasPrefix(target, redirectDenied+":") {
						t.Fatalf("denied target reached proxy: %q", target)
					}
				}
			})
		}
	}
}

func TestProxyRedirectEveryModeRejectsPrivateAndReservedLiteral(t *testing.T) {
	// These are URL values only. The fixture cannot dial them; it records any
	// attempted target and allows physical connections only to its own server.
	addresses := []string{
		"0.0.0.0", "10.20.30.40", "100.64.0.1", "127.0.0.1", "169.254.0.1", "172.16.0.1", "192.168.1.1",
		"192.0.0.1", "192.0.2.1", "198.18.0.1", "198.51.100.1", "203.0.113.1", "224.0.0.1", "240.0.0.1", "255.255.255.255",
		"::", "::1", "fc00::1", "fe80::1", "ff02::1", "2001:db8::1", "::ffff:127.0.0.1",
	}
	for _, format := range []string{"maven", "oci"} {
		for _, mode := range proxyRedirectModes {
			for _, address := range addresses {
				t.Run(format+"/"+mode+"/"+address, func(t *testing.T) {
					target := &url.URL{Scheme: "https", Host: net.JoinHostPort(address, "443"), Path: "/rejected"}
					fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
						http.Redirect(w, r, target.String(), http.StatusFound)
					})
					lookup := fixture.hooks.LookupIP
					fixture.hooks.LookupIP = func(ctx context.Context, network, host string) ([]net.IP, error) {
						if ip := net.ParseIP(host); ip != nil {
							return []net.IP{ip}, nil
						}
						return lookup(ctx, network, host)
					}
					member := proxyRedirectMember(configureProxyRedirectMode(t, fixture, mode))
					member.AllowedHosts = append(member.AllowedHosts, target.Host)
					response, err := proxyRedirectFetch(context.Background(), fixture.client, format, http.MethodGet, member, nil)
					closeProxyRedirectResponse(response)
					if err == nil || strings.Contains(err.Error(), target.Host) {
						t.Fatalf("expected a safe address-policy rejection, got %v", err)
					}
					if got := len(fixture.snapshot()); got != 1 {
						t.Fatalf("reserved target reached upstream, requests=%d", got)
					}
					if got := len(fixture.proxySnapshot()); got > 1 {
						t.Fatalf("reserved target reached proxy: %v", fixture.proxySnapshot())
					}
					for _, dial := range fixture.dialSnapshot() {
						if dial != "93.184.216.10:443" {
							t.Fatalf("reserved target reached dial hook: %q", dial)
						}
					}
				})
			}
		}
	}
}

func TestProxyRedirectLocalDNSModesRecheckSameOriginChanges(t *testing.T) {
	for _, format := range []string{"maven", "oci"} {
		for _, mode := range []string{"default", "environment-no-proxy", "direct", "custom-no-proxy", "socks-local"} {
			for _, nextAddress := range []string{"10.20.30.40", "203.0.113.1", "2001:db8::1"} {
				t.Run(format+"/"+mode+"/"+nextAddress, func(t *testing.T) {
					fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
						http.Redirect(w, r, "/same-origin", http.StatusFound)
					})
					proxy := configureProxyRedirectMode(t, fixture, mode)
					lookup := fixture.hooks.LookupIP
					var originLookups atomic.Int32
					fixture.hooks.LookupIP = func(ctx context.Context, network, host string) ([]net.IP, error) {
						if host == redirectOrigin && originLookups.Add(1) > 1 {
							return []net.IP{net.ParseIP(nextAddress)}, nil
						}
						return lookup(ctx, network, host)
					}
					response, err := proxyRedirectFetch(context.Background(), fixture.client, format, http.MethodGet, proxyRedirectMember(proxy), nil)
					closeProxyRedirectResponse(response)
					if err == nil || strings.Contains(err.Error(), nextAddress) || originLookups.Load() < 2 || len(fixture.snapshot()) != 1 {
						t.Fatalf("changed DNS must reject before second request: err=%v lookups=%d requests=%+v", err, originLookups.Load(), fixture.snapshot())
					}
				})
			}
		}
	}
}

func TestProxyRedirectProxyDNSModesDoNotRequireLocalUpstreamLookup(t *testing.T) {
	// CONNECT proxies and SOCKS remote DNS own upstream DNS/address enforcement.
	// This establishes that boundary without claiming local hostname validation.
	for _, format := range []string{"maven", "oci"} {
		for _, mode := range []string{"environment-connect", "custom-http", "socks-remote"} {
			t.Run(format+"/"+mode, func(t *testing.T) {
				fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
					if r.Host == redirectOrigin {
						http.Redirect(w, r, "https://"+redirectCDN+"/content", http.StatusFound)
						return
					}
					_, _ = io.WriteString(w, "complete")
				})
				proxy := configureProxyRedirectMode(t, fixture, mode)
				lookup := fixture.hooks.LookupIP
				var upstreamLookups atomic.Int32
				fixture.hooks.LookupIP = func(ctx context.Context, network, host string) ([]net.IP, error) {
					if host == redirectOrigin || host == redirectCDN {
						upstreamLookups.Add(1)
						return nil, errors.New("upstream DNS is owned by synthetic proxy")
					}
					return lookup(ctx, network, host)
				}
				response, err := proxyRedirectFetch(context.Background(), fixture.client, format, http.MethodGet, proxyRedirectMember(proxy), nil)
				if err != nil {
					closeProxyRedirectResponse(response)
					t.Fatalf("proxy-owned DNS chain: %v", err)
				}
				defer closeProxyRedirectResponse(response)
				if response.StatusCode != http.StatusOK || upstreamLookups.Load() != 0 || len(fixture.proxySnapshot()) != 2 {
					t.Fatalf("status=%d local upstream lookups=%d proxy targets=%v", response.StatusCode, upstreamLookups.Load(), fixture.proxySnapshot())
				}
			})
		}
	}
}

func TestProxyRedirectProxyOwnedDNSRejectionStopsBeforeTunnel(t *testing.T) {
	for _, format := range []string{"maven", "oci"} {
		for _, mode := range []string{"environment-connect", "custom-http", "socks-remote"} {
			t.Run(format+"/"+mode, func(t *testing.T) {
				fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, "https://"+redirectCDN+"/content", http.StatusFound)
				})
				proxy := configureProxyRedirectMode(t, fixture, mode)
				// The synthetic proxy refuses a target whose own DNS/address policy
				// disallows it. The gateway does not claim local enforcement here.
				fixture.mu.Lock()
				fixture.proxyRejected = map[string]bool{redirectCDN + ":443": true}
				fixture.mu.Unlock()
				response, err := proxyRedirectFetch(context.Background(), fixture.client, format, http.MethodGet, proxyRedirectMember(proxy), nil)
				closeProxyRedirectResponse(response)
				if err == nil || strings.Contains(err.Error(), redirectCDN) || len(fixture.snapshot()) != 1 || len(fixture.proxySnapshot()) != 2 {
					t.Fatalf("proxy-owned DNS rejection: err=%v requests=%+v proxy=%v", err, fixture.snapshot(), fixture.proxySnapshot())
				}
			})
		}
	}
}

func TestProxyRedirectEveryModeRejectsInvalidChains(t *testing.T) {
	for _, format := range []string{"maven", "oci"} {
		for _, mode := range proxyRedirectModes {
			for _, behavior := range []string{"downgrade", "userinfo", "loop", "six-redirects"} {
				t.Run(format+"/"+mode+"/"+behavior, func(t *testing.T) {
					fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
						switch behavior {
						case "downgrade":
							http.Redirect(w, r, "http://"+redirectCDN+"/content", http.StatusFound)
						case "userinfo":
							http.Redirect(w, r, "https://synthetic-user:synthetic-password@"+redirectCDN+"/content", http.StatusFound)
						case "loop":
							http.Redirect(w, r, "/loop", http.StatusFound)
						case "six-redirects":
							hop, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/hop/"))
							if hop < 6 {
								http.Redirect(w, r, "/hop/"+strconv.Itoa(hop+1), http.StatusFound)
								return
							}
							_, _ = io.WriteString(w, "complete")
						}
					})
					proxy := configureProxyRedirectMode(t, fixture, mode)
					response, err := proxyRedirectFetch(context.Background(), fixture.client, format, http.MethodGet, proxyRedirectMember(proxy), nil)
					closeProxyRedirectResponse(response)
					if err == nil || strings.Contains(err.Error(), "synthetic-password") {
						t.Fatalf("expected safe invalid-chain rejection: %v", err)
					}
					requests := fixture.snapshot()
					limit := 6
					if behavior == "downgrade" || behavior == "userinfo" {
						limit = 1
					}
					if len(requests) > limit || len(fixture.proxySnapshot()) > limit {
						t.Fatalf("chain exceeded approved bound: requests=%d proxy=%v", len(requests), fixture.proxySnapshot())
					}
				})
			}
		}
	}
}

func TestProxyRedirectReappliesNoProxyAtEachHop(t *testing.T) {
	for _, format := range []string{"maven", "oci"} {
		for _, mode := range []string{"environment-connect", "custom-http"} {
			t.Run(format+"/"+mode, func(t *testing.T) {
				fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
					if r.Host == redirectOrigin {
						http.Redirect(w, r, "https://"+redirectCDN+"/content", http.StatusFound)
						return
					}
					_, _ = io.WriteString(w, "complete")
				})
				proxy := configureProxyRedirectMode(t, fixture, mode)
				if mode == "environment-connect" {
					selector := fixture.hooks.ProxyFromEnvironment
					fixture.hooks.ProxyFromEnvironment = func(r *http.Request) (*url.URL, error) {
						if r.URL.Hostname() == redirectCDN {
							return nil, nil
						}
						return selector(r)
					}
				} else {
					proxy.NoProxy = []string{redirectCDN}
				}
				response, err := proxyRedirectFetch(context.Background(), fixture.client, format, http.MethodGet, proxyRedirectMember(proxy), nil)
				if err != nil {
					closeProxyRedirectResponse(response)
					t.Fatalf("per-hop proxy bypass: %v", err)
				}
				defer closeProxyRedirectResponse(response)
				if got := fixture.proxySnapshot(); len(got) != 1 || got[0] != redirectOrigin+":443" {
					t.Fatalf("proxy targets=%v, want origin only", got)
				}
				if got := fixture.dialSnapshot(); len(got) != 1 || got[0] != "93.184.216.11:443" {
					t.Fatalf("direct targets=%v, want pinned CDN only", got)
				}
			})
		}
	}
}

func TestProxyRedirectExplicitPortApproval(t *testing.T) {
	for _, format := range []string{"maven", "oci"} {
		for _, mode := range proxyRedirectModes {
			for _, approved := range []bool{false, true} {
				name := "denied"
				if approved {
					name = "approved"
				}
				t.Run(format+"/"+mode+"/"+name, func(t *testing.T) {
					fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
						if r.Host == redirectOrigin {
							http.Redirect(w, r, "https://"+redirectCDN+":8443/content", http.StatusFound)
							return
						}
						_, _ = io.WriteString(w, "complete")
					})
					member := proxyRedirectMember(configureProxyRedirectMode(t, fixture, mode))
					if approved {
						member.AllowedHosts = append(member.AllowedHosts, redirectCDN+":8443")
					}
					response, err := proxyRedirectFetch(context.Background(), fixture.client, format, http.MethodGet, member, nil)
					defer closeProxyRedirectResponse(response)
					if approved {
						if err != nil || len(fixture.snapshot()) != 2 || response.StatusCode != http.StatusOK {
							t.Fatalf("explicitly approved port must work: err=%v requests=%+v", err, fixture.snapshot())
						}
					} else if err == nil || len(fixture.snapshot()) != 1 || len(fixture.proxySnapshot()) > 1 {
						t.Fatalf("hostname approval must not approve port 8443: err=%v requests=%+v proxy=%v", err, fixture.snapshot(), fixture.proxySnapshot())
					}
				})
			}
		}
	}
}

func TestProxyRedirectTrustedDialHooksRemainBoundAndClientUnchanged(t *testing.T) {
	for _, format := range []string{"maven", "oci"} {
		t.Run(format, func(t *testing.T) {
			fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Host == redirectOrigin {
					http.Redirect(w, r, "https://"+redirectCDN+"/content", http.StatusFound)
					return
				}
				_, _ = io.WriteString(w, "complete")
			})
			base := fixture.client.HTTPClient.Transport.(*http.Transport)
			base.Proxy = func(*http.Request) (*url.URL, error) {
				return nil, errors.New("base proxy must be replaced by selected policy")
			}
			base.DialContext = func(context.Context, string, string) (net.Conn, error) {
				return nil, errors.New("base dial must be replaced by trusted hooks")
			}
			var redirectChecks atomic.Int32
			fixture.client.HTTPClient.CheckRedirect = func(*http.Request, []*http.Request) error {
				redirectChecks.Add(1)
				return nil
			}
			member := proxyRedirectMember(&repository.EgressProxy{Mode: repository.EgressProxyModeDirect})
			response, err := proxyRedirectFetch(context.Background(), fixture.client, format, http.MethodGet, member, nil)
			if err != nil {
				closeProxyRedirectResponse(response)
				t.Fatalf("trusted hook chain: %v", err)
			}
			defer closeProxyRedirectResponse(response)
			if got := fixture.dialSnapshot(); len(got) != 2 || got[0] != "93.184.216.10:443" || got[1] != "93.184.216.11:443" {
				t.Fatalf("connection binding=%v", got)
			}
			if fixture.client.HTTPClient.Transport != base || base.Proxy == nil || fixture.client.HTTPClient.CheckRedirect == nil || base.TLSClientConfig.RootCAs == nil || redirectChecks.Load() != 1 {
				t.Fatal("fetch must preserve the supplied client, TLS roots and existing callbacks")
			}
		})
	}
}

func TestProxyRedirectInheritedPolicyRejectsBeforeNextRequest(t *testing.T) {
	for _, format := range []string{"maven", "oci"} {
		for _, mode := range proxyRedirectModes {
			t.Run(format+"/"+mode, func(t *testing.T) {
				fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, "https://"+redirectCDN+"/content", http.StatusFound)
				})
				member := proxyRedirectMember(configureProxyRedirectMode(t, fixture, mode))
				rejected := errors.New("synthetic-client-policy-rejection")
				var redirectChecks atomic.Int32
				fixture.client.HTTPClient.CheckRedirect = func(next *http.Request, via []*http.Request) error {
					redirectChecks.Add(1)
					if next.URL.Hostname() != redirectCDN || len(via) != 1 || via[0].URL.Hostname() != redirectOrigin {
						return errors.New("synthetic callback received incorrect redirect history")
					}
					return rejected
				}
				response, err := proxyRedirectFetch(context.Background(), fixture.client, format, http.MethodGet, member, nil)
				closeProxyRedirectResponse(response)
				if !errors.Is(err, rejected) || strings.Contains(err.Error(), rejected.Error()) || redirectChecks.Load() != 1 || len(fixture.snapshot()) != 1 || len(fixture.proxySnapshot()) > 1 {
					t.Fatalf("inherited policy must reject safely before target request: err=%v checks=%d requests=%+v proxy=%v", err, redirectChecks.Load(), fixture.snapshot(), fixture.proxySnapshot())
				}
			})
		}
	}
}

func TestProxyRedirectCancellationStopsDNSBeforeDial(t *testing.T) {
	for _, format := range []string{"maven", "oci"} {
		t.Run(format, func(t *testing.T) {
			fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "https://"+redirectCDN+"/content", http.StatusFound)
			})
			lookup := fixture.hooks.LookupIP
			arrived, release := make(chan struct{}), make(chan struct{})
			defer close(release)
			fixture.hooks.LookupIP = func(ctx context.Context, network, host string) ([]net.IP, error) {
				if host != redirectCDN {
					return lookup(ctx, network, host)
				}
				close(arrived)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-release:
					return nil, errors.New("fixture released an uncancelled DNS lookup")
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				response, err := proxyRedirectFetch(ctx, fixture.client, format, http.MethodGet, proxyRedirectMember(nil), nil)
				closeProxyRedirectResponse(response)
				result <- err
			}()
			select {
			case <-arrived:
				cancel()
			case err := <-result:
				t.Fatalf("approved redirected DNS was not reached: %v", err)
			case <-time.After(time.Second):
				t.Fatal("DNS fixture was not reached")
			}
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) || len(fixture.snapshot()) != 1 || len(fixture.dialSnapshot()) != 1 {
					t.Fatalf("DNS cancellation=%v requests=%v dials=%v", err, fixture.snapshot(), fixture.dialSnapshot())
				}
			case <-time.After(time.Second):
				t.Fatal("cancellation did not stop redirected DNS")
			}
		})
	}
}

func TestProxyRedirectEveryModeCancellationAndTimeout(t *testing.T) {
	for _, format := range []string{"maven", "oci"} {
		for _, mode := range proxyRedirectModes {
			for _, stop := range []string{"cancel", "timeout"} {
				t.Run(format+"/"+mode+"/"+stop, func(t *testing.T) {
					arrived := make(chan struct{})
					fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
						if r.Host == redirectOrigin {
							http.Redirect(w, r, "https://"+redirectCDN+"/wait", http.StatusFound)
							return
						}
						close(arrived)
						<-r.Context().Done()
					})
					member := proxyRedirectMember(configureProxyRedirectMode(t, fixture, mode))
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					if stop == "timeout" {
						fixture.client.HTTPClient.Timeout = 100 * time.Millisecond
					}
					result := make(chan error, 1)
					go func() {
						response, err := proxyRedirectFetch(ctx, fixture.client, format, http.MethodGet, member, nil)
						closeProxyRedirectResponse(response)
						result <- err
					}()
					select {
					case <-arrived:
						if stop == "cancel" {
							cancel()
						}
					case err := <-result:
						t.Fatalf("approved hop was not reached before %s: %v", stop, err)
					case <-time.After(time.Second):
						t.Fatal("redirected fixture hop was not reached")
					}
					select {
					case err := <-result:
						want := context.Canceled
						if stop == "timeout" {
							want = context.DeadlineExceeded
						}
						if !errors.Is(err, want) || len(fixture.snapshot()) != 2 {
							t.Fatalf("%s=%v, want %v after reaching approved current hop; requests=%+v", stop, err, want, fixture.snapshot())
						}
					case <-time.After(time.Second):
						t.Fatalf("%s did not stop the current hop", stop)
					}
				})
			}
		}
	}
}

func TestProxyRedirectDefaultClientRejectsHTTPInitialEndpoint(t *testing.T) {
	for _, format := range []string{"maven", "oci"} {
		t.Run(format, func(t *testing.T) {
			fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, "must not be requested")
			})
			member := proxyRedirectMember(nil)
			member.Endpoint = "http://" + redirectOrigin
			// The default client has no HTTP fixture exception; rejected URLs
			// cannot invoke even the trusted owned-listener dial hook.
			response, err := proxyRedirectFetch(context.Background(), fixture.client, format, http.MethodGet, member, nil)
			closeProxyRedirectResponse(response)
			if err == nil || fixture.client.AllowHTTPForTesting || len(fixture.snapshot()) != 0 || len(fixture.dialSnapshot()) != 0 || len(fixture.proxySnapshot()) != 0 {
				t.Fatalf("default HTTP endpoint must reject before any request/dial: err=%v requests=%+v dials=%v", err, fixture.snapshot(), fixture.dialSnapshot())
			}
		})
	}
}
