package app

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

const redirectCacheIndexType = "application/vnd.oci.image.index.v1+json"
const redirectCacheManifestType = "application/vnd.oci.image.manifest.v1+json"
const redirectCacheConfigType = "application/vnd.oci.image.config.v1+json"
const redirectCacheLayerType = "application/vnd.oci.image.layer.v1.tar"

type redirectCacheAsset struct {
	body      []byte
	mediaType string
	digest    string
}

type redirectCachePlatform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
}

type redirectCacheDescriptor struct {
	MediaType string                 `json:"mediaType"`
	Digest    string                 `json:"digest"`
	Size      int64                  `json:"size"`
	Platform  *redirectCachePlatform `json:"platform,omitempty"`
}

type redirectCacheDocument struct {
	SchemaVersion int                       `json:"schemaVersion"`
	MediaType     string                    `json:"mediaType"`
	Manifests     []redirectCacheDescriptor `json:"manifests,omitempty"`
	Config        *redirectCacheDescriptor  `json:"config,omitempty"`
	Layers        []redirectCacheDescriptor `json:"layers,omitempty"`
}

func redirectCacheSHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func marshalRedirectCacheJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func redirectCacheOCIImage(t *testing.T) map[string]redirectCacheAsset {
	t.Helper()
	assets := make(map[string]redirectCacheAsset)
	index := redirectCacheDocument{SchemaVersion: 2, MediaType: redirectCacheIndexType}
	for _, architecture := range []string{"amd64", "arm64"} {
		var layer bytes.Buffer
		archive := tar.NewWriter(&layer)
		payload := []byte("synthetic linux/" + architecture + "\n")
		if err := archive.WriteHeader(&tar.Header{Name: "platform.txt", Mode: 0644, Size: int64(len(payload)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write(payload); err != nil {
			t.Fatal(err)
		}
		if err := archive.Close(); err != nil {
			t.Fatal(err)
		}
		layerDigest := redirectCacheSHA256(layer.Bytes())
		config := marshalRedirectCacheJSON(t, map[string]any{
			"architecture": architecture, "os": "linux",
			"rootfs": map[string]any{"type": "layers", "diff_ids": []string{layerDigest}},
		})
		configDigest := redirectCacheSHA256(config)
		manifest := marshalRedirectCacheJSON(t, redirectCacheDocument{
			SchemaVersion: 2, MediaType: redirectCacheManifestType,
			Config: &redirectCacheDescriptor{MediaType: redirectCacheConfigType, Digest: configDigest, Size: int64(len(config))},
			Layers: []redirectCacheDescriptor{{MediaType: redirectCacheLayerType, Digest: layerDigest, Size: int64(layer.Len())}},
		})
		manifestDigest := redirectCacheSHA256(manifest)
		assets["/v2/demo/manifests/"+manifestDigest] = redirectCacheAsset{manifest, redirectCacheManifestType, manifestDigest}
		assets["/v2/demo/blobs/"+configDigest] = redirectCacheAsset{config, redirectCacheConfigType, configDigest}
		assets["/v2/demo/blobs/"+layerDigest] = redirectCacheAsset{layer.Bytes(), redirectCacheLayerType, layerDigest}
		index.Manifests = append(index.Manifests, redirectCacheDescriptor{
			MediaType: redirectCacheManifestType, Digest: manifestDigest, Size: int64(len(manifest)),
			Platform: &redirectCachePlatform{OS: "linux", Architecture: architecture},
		})
	}
	body := marshalRedirectCacheJSON(t, index)
	assets["/v2/demo/manifests/latest"] = redirectCacheAsset{body, redirectCacheIndexType, redirectCacheSHA256(body)}
	return assets
}

// Content reads are anonymous and never return a 401 or perform an upstream
// authentication exchange. Every approved origin/CDN connection still verifies
// TLS through the previously established owned-listener fixture.
func newRedirectCacheUpstream(t *testing.T, assets map[string]redirectCacheAsset, oci bool) *proxyRedirectFixture {
	t.Helper()
	return newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if r.Host == redirectOrigin {
			if _, ok := assets[path]; !ok {
				http.NotFound(w, r)
				return
			}
			http.Redirect(w, r, "https://"+redirectCDN+"/download"+path, http.StatusTemporaryRedirect)
			return
		}
		asset, ok := assets[strings.TrimPrefix(path, "/download")]
		if r.Host != redirectCDN || !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", asset.mediaType)
		w.Header().Set("Content-Length", strconv.Itoa(len(asset.body)))
		w.Header().Set("ETag", `"`+asset.digest+`"`)
		if oci {
			w.Header().Set("Docker-Content-Digest", asset.digest)
		}
		_, _ = w.Write(asset.body)
	})
}

func newRedirectCacheGateway(t *testing.T, format repository.Format, route string, upstream *proxyRedirectFixture) (*httptest.Server, string) {
	t.Helper()
	store := repository.NewMemoryStore()
	enableAnonymousAccess(t, store)
	proxy, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "redirect-cache-proxy", Name: "redirect-cache-proxy", Format: format,
		Type: repository.RepositoryTypeProxy, Endpoint: "https://" + redirectOrigin,
		AllowedHosts: []string{redirectOrigin, redirectCDN}, AnonymousRead: true,
		EgressProxy: &repository.EgressProxy{Mode: repository.EgressProxyModeDirect},
	})
	if err != nil {
		t.Fatal(err)
	}
	name := proxy.Name
	if route == "group" {
		group, _, err := store.CreateHostedGroupIdempotently(context.Background(), repository.HostedGroup{
			ID: "redirect-cache-group", Name: "redirect-cache-group", Format: format, AnonymousRead: true,
			Members: []repository.GroupMember{{RepositoryID: proxy.ID}},
		}, "fixture", "redirect-cache-group", "fixture")
		if err != nil {
			t.Fatal(err)
		}
		name = group.Name
	}
	objects := NewMemoryOCIObjectStore()
	ociCache := NewOCICache(objects, time.Hour, time.Hour, time.Hour, []string{redirectOrigin})
	mavenCache := NewMavenCache(objects, time.Hour, time.Hour, time.Hour, time.Hour, []string{redirectOrigin})
	gateway := httptest.NewServer(NewGatewayHandlerWithCaches(
		Dependencies{NativeOCIObjectStore: objects, NativeMavenObjectStore: objects}, store, TestAdapter{}, testAuthenticator(),
		ociCache, mavenCache, upstream.client,
	))
	t.Cleanup(gateway.Close)
	return gateway, name
}

func getRedirectCacheAsset(t *testing.T, gateway *httptest.Server, path string, expected redirectCacheAsset, oci bool) []byte {
	t.Helper()
	// Each call creates a fresh HTTP request; no authentication header or token
	// endpoint is involved. This is an httptest HTTP client, not an OCI/Maven CLI.
	request, err := http.NewRequest(http.MethodGet, gateway.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", expected.mediaType)
	response, err := gateway.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer closeProxyRedirectResponse(response)
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || !bytes.Equal(body, expected.body) || redirectCacheSHA256(body) != expected.digest {
		t.Fatalf("GET %s status=%d bytes=%d digest=%s err=%v", path, response.StatusCode, len(body), redirectCacheSHA256(body), err)
	}
	if response.Header.Get("WWW-Authenticate") != "" || response.Header.Get("Content-Type") != expected.mediaType {
		t.Fatalf("GET %s response headers=%v", path, response.Header)
	}
	if oci && response.Header.Get("Docker-Content-Digest") != expected.digest {
		t.Fatalf("GET %s content digest=%q, want %q", path, response.Header.Get("Docker-Content-Digest"), expected.digest)
	}
	return body
}

func assertRedirectCacheColdHotCounts(t *testing.T, upstream *proxyRedirectFixture, assets map[string]redirectCacheAsset, expectedRequests int) {
	t.Helper()
	requests := upstream.snapshot()
	if len(requests) != expectedRequests {
		t.Fatalf("upstream requests=%d, want %d (hot reads must add none)", len(requests), expectedRequests)
	}
	for path := range assets {
		origin, cdn := 0, 0
		for _, request := range requests {
			if request.method != http.MethodGet || request.headers.Get("Authorization") != "" || request.headers.Get("Proxy-Authorization") != "" {
				t.Fatalf("upstream request must remain anonymous GET: %+v", request)
			}
			if request.host == redirectOrigin && request.path == path {
				origin++
			}
			if request.host == redirectCDN && request.path == "/download"+path {
				cdn++
			}
		}
		if origin != 1 || cdn != 1 {
			t.Fatalf("%s origin/CDN requests=%d/%d, want 1/1", path, origin, cdn)
		}
	}
}

func TestProxyRedirectOCIMultiplatformGatewayColdHotCache(t *testing.T) {
	for _, route := range []string{"proxy", "group"} {
		t.Run(route, func(t *testing.T) {
			assets := redirectCacheOCIImage(t)
			upstream := newRedirectCacheUpstream(t, assets, true)
			gateway, name := newRedirectCacheGateway(t, repository.FormatOCI, route, upstream)
			for _, round := range []string{"cold", "hot"} {
				t.Run(round, func(t *testing.T) {
					body := getRedirectCacheAsset(t, gateway, "/v2/"+name+"/demo/manifests/latest", assets["/v2/demo/manifests/latest"], true)
					var index redirectCacheDocument
					if err := json.Unmarshal(body, &index); err != nil || index.SchemaVersion != 2 || index.MediaType != redirectCacheIndexType || len(index.Manifests) != 2 {
						t.Fatalf("invalid two-platform index: %+v err=%v", index, err)
					}
					for i, descriptor := range index.Manifests {
						architecture := []string{"amd64", "arm64"}[i]
						if descriptor.Platform == nil || descriptor.Platform.OS != "linux" || descriptor.Platform.Architecture != architecture {
							t.Fatalf("platform=%+v, want linux/%s", descriptor.Platform, architecture)
						}
						manifestAsset := assets["/v2/demo/manifests/"+descriptor.Digest]
						body := getRedirectCacheAsset(t, gateway, "/v2/"+name+"/demo/manifests/"+descriptor.Digest, manifestAsset, true)
						var manifest redirectCacheDocument
						if err := json.Unmarshal(body, &manifest); err != nil || manifest.SchemaVersion != 2 || manifest.MediaType != redirectCacheManifestType || descriptor.Size != int64(len(body)) || manifest.Config == nil || len(manifest.Layers) != 1 {
							t.Fatalf("invalid platform manifest: %+v err=%v", manifest, err)
						}
						for _, child := range append([]redirectCacheDescriptor{*manifest.Config}, manifest.Layers...) {
							blob := assets["/v2/demo/blobs/"+child.Digest]
							body := getRedirectCacheAsset(t, gateway, "/v2/"+name+"/demo/blobs/"+child.Digest, blob, true)
							if child.Size != int64(len(body)) || child.MediaType != blob.mediaType {
								t.Fatalf("blob descriptor=%+v size=%d content type=%q", child, len(body), blob.mediaType)
							}
						}
					}
					assertRedirectCacheColdHotCounts(t, upstream, assets, 14)
				})
			}
		})
	}
}

func TestProxyRedirectMavenGatewayColdHotCache(t *testing.T) {
	var jar bytes.Buffer
	archive := zip.NewWriter(&jar)
	manifest, err := archive.Create("META-INF/MANIFEST.MF")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(manifest, "Manifest-Version: 1.0\n\n"); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	pom := []byte(`<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.0</version><packaging>jar</packaging></project>`)
	paths := []string{"/org/example/widget/1.0/widget-1.0.pom", "/org/example/widget/1.0/widget-1.0.jar"}
	assets := map[string]redirectCacheAsset{
		paths[0]: {pom, "application/xml", redirectCacheSHA256(pom)},
		paths[1]: {jar.Bytes(), "application/java-archive", redirectCacheSHA256(jar.Bytes())},
	}
	for _, route := range []string{"proxy", "group"} {
		t.Run(route, func(t *testing.T) {
			upstream := newRedirectCacheUpstream(t, assets, false)
			gateway, name := newRedirectCacheGateway(t, repository.FormatMaven, route, upstream)
			for _, round := range []string{"cold", "hot"} {
				t.Run(round, func(t *testing.T) {
					for _, path := range paths {
						getRedirectCacheAsset(t, gateway, "/maven/"+name+path, assets[path], false)
					}
					assertRedirectCacheColdHotCounts(t, upstream, assets, 4)
				})
			}
		})
	}
}
