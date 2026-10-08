package app

import (
	"context"
	"io"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestOCIBearerSyntheticColdRead(t *testing.T) {
	var exchanges atomic.Int32
	fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			exchanges.Add(1)
			if r.URL.Query().Get("service") != "synthetic-registry" || r.URL.Query().Get("scope") != "repository:demo:pull" {
				t.Error("token request is not bound to the registry pull scope")
			}
			if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.URL.Query().Has("offline_token") || r.URL.Query().Has("account") {
				t.Error("token request carries client credentials or requests a durable identity")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"token":"synthetic-short-token","expires_in":60}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer synthetic-short-token" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="https://`+redirectOrigin+`/token",service="synthetic-registry",scope="repository:demo:pull"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, "synthetic-content")
	})
	member := proxyRedirectMember(nil)
	member.OCIBearer = &repository.OCIBearer{Realm: "https://" + redirectOrigin + "/token", Service: "synthetic-registry"}
	response, err := fixture.client.Fetch(context.Background(), http.MethodGet, member, "demo", "manifests", "latest", http.Header{
		"Authorization": {"Bearer synthetic-inbound-token"}, "Cookie": {"synthetic-client-cookie"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeProxyRedirectResponse(response)
	content, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || string(content) != "synthetic-content" || exchanges.Load() != 1 {
		t.Fatalf("cold read = status %d, body %q, exchanges %d; want 200, synthetic-content, 1", response.StatusCode, content, exchanges.Load())
	}
}
