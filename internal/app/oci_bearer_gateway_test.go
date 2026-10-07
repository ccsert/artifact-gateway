//go:build ociclient

package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// This mandatory opt-in gate uses an explicit empty registry configuration.
// ORAS reads only the owned anonymous Gateway; the Gateway talks only to the
// verified synthetic TLS registry/token/CDN fixture. No real credential store
// or provider is involved, and each round begins with an empty OCI layout.
func TestOCIBearerORASMultiplatformColdHot(t *testing.T) {
	oras, err := exec.LookPath("oras")
	if err != nil {
		t.Fatal("OCI client acceptance requires oras")
	}
	for _, route := range []string{"proxy", "group"} {
		t.Run(route, func(t *testing.T) {
			assets := redirectCacheOCIImage(t)
			var exchanges atomic.Int32
			var offline atomic.Bool
			upstream := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if offline.Load() {
					t.Error("hot Gateway cache unexpectedly contacted the upstream")
					http.Error(w, "synthetic upstream offline", http.StatusServiceUnavailable)
					return
				}
				if r.URL.Path == "/token" {
					exchanges.Add(1)
					if r.Host != redirectOrigin || r.Method != http.MethodGet || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" ||
						r.URL.Query().Get("service") != "synthetic-registry" || r.URL.Query().Get("scope") != "repository:demo:pull" || len(r.URL.Query()) != 2 {
						t.Error("token request exceeded its synthetic issuer/pull binding")
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"token":"synthetic-oci-layout-token","expires_in":60}`)
					return
				}
				if r.Host == redirectOrigin {
					if r.Header.Get("Authorization") != "Bearer synthetic-oci-layout-token" {
						w.Header().Set("WWW-Authenticate", `Bearer realm="https://`+redirectOrigin+`/token",service="synthetic-registry",scope="repository:demo:pull"`)
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
					if _, exists := assets[r.URL.Path]; !exists {
						http.NotFound(w, r)
						return
					}
					http.Redirect(w, r, "https://"+redirectCDN+"/download"+r.URL.Path, http.StatusTemporaryRedirect)
					return
				}
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Proxy-Authorization") != "" {
					t.Error("bound registry token reached the CDN")
				}
				asset, exists := assets[strings.TrimPrefix(r.URL.Path, "/download")]
				if r.Host != redirectCDN || !exists {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", asset.mediaType)
				w.Header().Set("Content-Length", strconv.Itoa(len(asset.body)))
				w.Header().Set("Docker-Content-Digest", asset.digest)
				_, _ = w.Write(asset.body)
			})
			upstream.client.OCIBearerCache = NewOCIBearerTokenCache()
			store := repository.NewMemoryStore()
			enableAnonymousAccess(t, store)
			proxy, err := store.CreateHostedRepository(t.Context(), repository.HostedRepository{
				ID: "bearer-layout-proxy", Name: "bearer-layout-proxy", Format: repository.FormatOCI,
				Type: repository.RepositoryTypeProxy, Endpoint: "https://" + redirectOrigin,
				AllowedHosts: []string{redirectOrigin, redirectCDN}, AnonymousRead: true,
				EgressProxy: &repository.EgressProxy{Mode: repository.EgressProxyModeDirect},
				OCIBearer:   &repository.OCIBearer{Realm: "https://" + redirectOrigin + "/token", Service: "synthetic-registry"},
			})
			if err != nil {
				t.Fatal(err)
			}
			name := proxy.Name
			if route == "group" {
				group, _, err := store.CreateHostedGroupIdempotently(t.Context(), repository.HostedGroup{
					ID: "bearer-layout-group", Name: "bearer-layout-group", Format: repository.FormatOCI, AnonymousRead: true,
					Members: []repository.GroupMember{{RepositoryID: proxy.ID}},
				}, "synthetic-fixture", "bearer-layout-group", "synthetic-fixture")
				if err != nil {
					t.Fatal(err)
				}
				name = group.Name
			}
			objects := NewMemoryOCIObjectStore()
			gateway := httptest.NewServer(NewGatewayHandlerWithOCICache(
				Dependencies{NativeOCIObjectStore: objects}, store, TestAdapter{}, testAuthenticator(),
				NewOCICache(objects, time.Hour, time.Hour, time.Hour, []string{redirectOrigin}), upstream.client,
			))
			t.Cleanup(gateway.Close)
			root := t.TempDir()
			registryConfig := filepath.Join(root, "empty-registry-config.json")
			if err := os.WriteFile(registryConfig, []byte(`{"auths":{}}`), 0600); err != nil {
				t.Fatal(err)
			}
			coldRequests := 0
			for _, round := range []string{"cold", "hot"} {
				layout := filepath.Join(root, round+"-empty-layout")
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				command := exec.CommandContext(ctx, oras, "cp", "--no-tty", "--from-plain-http", "--from-registry-config", registryConfig,
					"--to-oci-layout", strings.TrimPrefix(gateway.URL, "http://")+"/"+name+"/demo:latest", layout+":latest")
				output, err := command.CombinedOutput()
				cancel()
				if err != nil {
					t.Fatalf("ORAS %s failed: %v\n%s", round, err, output)
				}
				for _, asset := range assets {
					body, err := os.ReadFile(filepath.Join(layout, "blobs", "sha256", strings.TrimPrefix(asset.digest, "sha256:")))
					if err != nil || !bytes.Equal(body, asset.body) || redirectCacheSHA256(body) != asset.digest {
						t.Fatalf("ORAS %s incomplete object %s: %v", round, asset.digest, err)
					}
				}
				if exchanges.Load() != 1 {
					t.Fatalf("ORAS %s token exchanges=%d, want one shared scope", round, exchanges.Load())
				}
				if round == "cold" {
					coldRequests = len(upstream.snapshot())
					if coldRequests < 15 {
						t.Fatalf("incomplete cold upstream chain: %d requests", coldRequests)
					}
					offline.Store(true)
				} else if got := len(upstream.snapshot()); got != coldRequests {
					t.Fatalf("hot requests=%d, want unchanged %d", got, coldRequests)
				}
				t.Log(fmt.Sprintf("ORAS %s %s: all seven index/manifest/config/layer objects verified; upstream requests=%d; exchanges=%d", route, round, len(upstream.snapshot()), exchanges.Load()))
			}
		})
	}
}
