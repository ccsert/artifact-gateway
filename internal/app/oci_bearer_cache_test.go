package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func bearerTestMember() repository.Member {
	member := proxyRedirectMember(nil)
	member.AllowedHosts = []string{redirectOrigin, redirectCDN}
	member.RepositoryID = "synthetic-repository"
	member.OCIBearer = &repository.OCIBearer{Realm: "https://" + redirectOrigin + "/token", Service: "registry"}
	return member
}
func bearerRead(ctx context.Context, client UpstreamClient, member repository.Member) error {
	response, err := client.Fetch(ctx, http.MethodGet, member, "demo", "manifests", "latest", http.Header{})
	if err != nil {
		return err
	}
	defer closeProxyRedirectResponse(response)
	_, err = io.Copy(io.Discard, response.Body)
	return err
}
func TestOCIBearerPublicFailures(t *testing.T) {
	canonical := `Bearer realm="https://` + redirectOrigin + `/token",service="registry",scope="repository:demo:pull"`
	for _, tc := range []struct {
		name, challenge, body string
		retry401              bool
		want                  error
	}{
		{"missing-token", canonical, `{}`, false, errOCIBearerToken},
		{"conflicting-token", canonical, `{"token":"one","access_token":"two"}`, false, errOCIBearerToken},
		{"bad-json", canonical, `{`, false, errOCIBearerToken},
		{"zero-expiry", canonical, `{"token":"token","expires_in":0}`, false, errOCIBearerToken},
		{"expired", canonical, fmt.Sprintf(`{"token":"token","expires_in":60,"issued_at":%q}`, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)), false, errOCIBearerToken},
		{"future", canonical, fmt.Sprintf(`{"token":"token","issued_at":%q}`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339)), false, errOCIBearerToken},
		{"wrong-scope", strings.Replace(canonical, ":pull", ":push", 1), `{"token":"token"}`, false, errOCIBearerChallenge},
		{"wrong-service", strings.Replace(canonical, `service="registry"`, `service="other"`, 1), `{"token":"token"}`, false, errOCIBearerChallenge},
		{"unknown-realm", strings.Replace(canonical, redirectOrigin, redirectDenied, 1), `{"token":"token"}`, false, errOCIBearerChallenge},
		{"multiple-challenges", canonical + `, Basic realm="other"`, `{"token":"token"}`, false, errOCIBearerChallenge},
		{"oversized-body", canonical, strings.Repeat("x", ociBearerMaxBody+1), false, errOCIBearerToken},
		{"oversized-token", canonical, fmt.Sprintf(`{"token":%q}`, strings.Repeat("x", ociBearerMaxToken+1)), false, errOCIBearerToken},
		{"retry-unauthorized", canonical, `{"token":"token"}`, true, errOCIBearerRetry},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var exchanges atomic.Int32
			fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					exchanges.Add(1)
					io.WriteString(w, tc.body)
					return
				}
				if r.Header.Get("Authorization") != "" && !tc.retry401 {
					io.WriteString(w, "ok")
					return
				}
				w.Header().Set("WWW-Authenticate", tc.challenge)
				w.WriteHeader(401)
			})
			fixture.client.OCIBearerCache = NewOCIBearerTokenCache()
			if err := bearerRead(context.Background(), fixture.client, bearerTestMember()); !errors.Is(err, tc.want) {
				t.Fatalf("error=%v want %v", err, tc.want)
			}
			if exchanges.Load() > 1 {
				t.Fatalf("unbounded exchanges=%d", exchanges.Load())
			}
		})
	}
}

func TestOCIBearerPublicCacheSingleflightCancellationIsolation(t *testing.T) {
	var exchanges atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			exchanges.Add(1)
			once.Do(func() { close(started) })
			<-release
			io.WriteString(w, `{"token":"token","expires_in":3600,"issued_at":"`+time.Now().Add(-10*time.Minute).UTC().Format(time.RFC3339)+`"}`)
			return
		}
		if r.Header.Get("Authorization") == "Bearer token" {
			io.WriteString(w, "ok")
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="https://`+redirectOrigin+`/token",service="registry"`)
		w.WriteHeader(401)
	})
	fixture.client.OCIBearerCache = NewOCIBearerTokenCache()
	member := bearerTestMember()
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { first <- bearerRead(ctx, fixture.client, member) }()
	<-started
	const waiters = 12
	results := make(chan error, waiters)
	for i := 0; i < waiters; i++ {
		go func() { results <- bearerRead(context.Background(), fixture.client, member) }()
	}
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter=%v", err)
	}
	close(release)
	for i := 0; i < waiters; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if err := bearerRead(context.Background(), fixture.client, member); err != nil {
		t.Fatal(err)
	}
	if exchanges.Load() != 1 {
		t.Fatalf("exchanges=%d want 1", exchanges.Load())
	}
	member.RepositoryID = "other-repository"
	if err := bearerRead(context.Background(), fixture.client, member); err != nil {
		t.Fatal(err)
	}
	if exchanges.Load() != 2 {
		t.Fatalf("repository isolation exchanges=%d want 2", exchanges.Load())
	}
}

func TestOCIBearerPublicIssuerRedirectPolicy(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		t.Run(fmt.Sprint(allowed), func(t *testing.T) {
			var issued atomic.Int32
			fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Host == redirectCDN {
					issued.Add(1)
					if r.Header.Get("Authorization") != "" {
						t.Error("issuer redirect leaked authorization")
					}
					io.WriteString(w, `{"token":"token"}`)
					return
				}
				if r.URL.Path == "/token" {
					http.Redirect(w, r, "https://"+redirectCDN+"/issue", http.StatusTemporaryRedirect)
					return
				}
				if r.Header.Get("Authorization") == "Bearer token" {
					io.WriteString(w, "ok")
					return
				}
				w.Header().Set("WWW-Authenticate", `Bearer realm="https://`+redirectOrigin+`/token",service="registry"`)
				w.WriteHeader(401)
			})
			member := bearerTestMember()
			if !allowed {
				member.AllowedHosts = nil
			}
			err := bearerRead(context.Background(), fixture.client, member)
			if allowed && err != nil {
				t.Fatal(err)
			}
			if !allowed && (!errors.Is(err, errOCIBearerExchange) || issued.Load() != 0) {
				t.Fatalf("policy err=%v issued=%d", err, issued.Load())
			}
		})
	}
}

func TestOCIBearerPublicRevocationAndShortExpiry(t *testing.T) {
	for _, short := range []bool{false, true} {
		t.Run(fmt.Sprint(short), func(t *testing.T) {
			var exchanges atomic.Int32
			var revoked atomic.Bool
			fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					number := exchanges.Add(1)
					ttl := 60
					if short {
						ttl = 1
					}
					fmt.Fprintf(w, `{"token":"token-%d","expires_in":%d}`, number, ttl)
					return
				}
				authorization := r.Header.Get("Authorization")
				if authorization != "" && (!revoked.Load() || authorization != "Bearer token-1") {
					io.WriteString(w, "ok")
					return
				}
				w.Header().Set("WWW-Authenticate", `Bearer realm="https://`+redirectOrigin+`/token",service="registry"`)
				w.WriteHeader(401)
			})
			fixture.client.OCIBearerCache = NewOCIBearerTokenCache()
			member := bearerTestMember()
			if err := bearerRead(context.Background(), fixture.client, member); err != nil {
				t.Fatal(err)
			}
			if !short {
				revoked.Store(true)
			}
			if err := bearerRead(context.Background(), fixture.client, member); err != nil {
				t.Fatal(err)
			}
			if exchanges.Load() != 2 {
				t.Fatalf("refresh exchanges=%d want 2", exchanges.Load())
			}
			if !short {
				if err := bearerRead(context.Background(), fixture.client, member); err != nil {
					t.Fatal(err)
				}
				if exchanges.Load() != 2 {
					t.Fatalf("fresh token not retained: %d", exchanges.Load())
				}
			}
		})
	}
}

func TestOCIBearerPublicIndependentIssuerRequiresAllowedHost(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		t.Run(fmt.Sprint(allowed), func(t *testing.T) {
			var issued atomic.Int32
			fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Host == redirectCDN {
					issued.Add(1)
					io.WriteString(w, `{"token":"token"}`)
					return
				}
				if r.Header.Get("Authorization") == "Bearer token" {
					io.WriteString(w, "ok")
					return
				}
				w.Header().Set("WWW-Authenticate", `Bearer realm="https://`+redirectCDN+`/token",service="registry"`)
				w.WriteHeader(401)
			})
			member := bearerTestMember()
			member.OCIBearer.Realm = "https://" + redirectCDN + "/token"
			if !allowed {
				member.AllowedHosts = nil
			}
			err := bearerRead(context.Background(), fixture.client, member)
			if allowed && err != nil {
				t.Fatal(err)
			}
			if !allowed && (!errors.Is(err, errOCIBearerExchange) || issued.Load() != 0) {
				t.Fatalf("independent issuer policy err=%v issued=%d", err, issued.Load())
			}
		})
	}
}
