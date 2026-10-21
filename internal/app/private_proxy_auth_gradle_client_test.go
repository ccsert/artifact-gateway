//go:build integration && mavenclient && gradleclient

package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

const privateGradleVersion = "9.3.1"

// Gradle receives only the Gateway resolver identity. Source credentials enter
// through the public management API and are decrypted by the real Maven proxy.
// Every invocation starts with a distinct, empty Gradle and Java user home.
func TestPostgresRustFSPrivateProxyGradleOfficialClient(t *testing.T) {
	gradle := os.Getenv("PRIVATE_AUTH_GRADLE")
	if !filepath.IsAbs(gradle) {
		t.Fatal("private Gradle acceptance requires the pinned, owned distribution")
	}
	for _, scheme := range []repository.UpstreamAuthScheme{repository.UpstreamAuthSchemeBasic, repository.UpstreamAuthSchemeBearer} {
		for _, route := range []string{"proxy", "group"} {
			t.Run(string(scheme)+"/"+route, func(t *testing.T) {
				privateGradleCredentialsContract(t, gradle, scheme, route)
			})
		}
	}
}

func privateGradleCredentialsContract(t *testing.T, gradle string, scheme repository.UpstreamAuthScheme, route string) {
	t.Helper()
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", upstreamAuthTestKey)
	const firstSecret = "synthetic-gradle-source-v1"
	const secondSecret = "synthetic-gradle-source-v2"
	const wrongSecret = "synthetic-gradle-source-wrong"
	assets := privateGradleAssets(t)
	backend := newPrivateProxyPostgresBackend(t)
	store, cache := backend.store, backend.cache
	var expected atomic.Value
	expected.Store(privateProxyAuthorization(scheme, firstSecret))
	var refusal, fallbackVisits, sourceRefusals, lastSourceRefusal atomic.Int32
	upstream := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("client or egress credentials escaped into the source request")
		}
		path := r.URL.Path
		if r.Host == redirectDenied {
			fallbackVisits.Add(1)
		} else if r.Host == redirectCDN || strings.HasPrefix(path, "/returned/") {
			if r.Header.Get("Authorization") != "" {
				t.Error("a cross-origin hop, including its return, regained source authorization")
			}
		} else if r.Header.Get("Authorization") != expected.Load().(string) || refusal.Load() != 0 {
			status := int(refusal.Load())
			if status == 0 {
				status = http.StatusUnauthorized
			}
			sourceRefusals.Add(1)
			lastSourceRefusal.Store(int32(status))
			http.Error(w, firstSecret+secondSecret+wrongSecret, status)
			return
		}
		switch {
		case strings.HasPrefix(path, "/same/"):
			path = strings.TrimPrefix(path, "/same")
			if strings.HasSuffix(path, "leaf-1.0.jar") {
				http.Redirect(w, r, "https://"+redirectCDN+"/download"+path, http.StatusTemporaryRedirect)
				return
			}
		case strings.HasPrefix(path, "/download/"):
			path = strings.TrimPrefix(path, "/download")
			http.Redirect(w, r, "https://"+redirectOrigin+"/returned"+path, http.StatusTemporaryRedirect)
			return
		case strings.HasPrefix(path, "/returned/"):
			path = strings.TrimPrefix(path, "/returned")
		default:
			if r.Host != redirectDenied {
				http.Redirect(w, r, "/same"+path, http.StatusTemporaryRedirect)
				return
			}
		}
		asset, found := assets[path]
		if !found {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", asset.mediaType)
		w.Header().Set("Content-Length", fmt.Sprint(len(asset.body)))
		if r.Method != http.MethodHead {
			_, _ = w.Write(asset.body)
		}
	})
	name := "gradle-" + uuid.NewString()
	newHandler := func() http.Handler {
		return newTestGateway(t, GatewayOptions{Dependencies: Dependencies{}, Store: store, Adapter: TestAdapter{},
			Authenticator: privateProxyAuthenticator(name), Storage: ObjectStores{repository.FormatMaven: backend.objects},
			Upstream: upstream.client, Caches: GatewayCaches{Maven: NewDefaultMavenCache(cache, []string{redirectOrigin, redirectCDN, redirectDenied})}})
	}
	handler := newHandler()
	create := func(repoName, host string) repository.HostedRepository {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"name": repoName, "format": "maven", "type": "proxy", "endpoint": "https://" + host, "allowedHosts": []string{host, redirectCDN}})
		response := ociBearerManagementRequest(handler, http.MethodPost, "/api/v2/repositories", string(body), "", uuid.NewString())
		var repo repository.HostedRepository
		if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &repo) != nil || repo.ID == "" || repo.Version == "" {
			t.Fatalf("create private Gradle source through API=%d %s", response.Code, response.Body.String())
		}
		return repo
	}
	proxy := create(name, redirectOrigin)
	if route == "group" {
		fallback := create("fallback-"+uuid.NewString(), redirectDenied)
		name = "group-" + uuid.NewString()
		body := fmt.Sprintf(`{"name":%q,"format":"maven","members":[{"repositoryId":%q,"position":0},{"repositoryId":%q,"position":1}]}`, name, proxy.ID, fallback.ID)
		response := ociBearerManagementRequest(handler, http.MethodPost, "/api/v2/groups", body, "", uuid.NewString())
		if response.Code != http.StatusCreated {
			t.Fatalf("create Gradle Group through API=%d %s", response.Code, response.Body.String())
		}
		handler = newHandler()
	}
	assertNoSecret := func(t *testing.T, body []byte) {
		t.Helper()
		for _, secret := range []string{firstSecret, secondSecret, wrongSecret, privateProxyAuthorization(scheme, firstSecret), privateProxyAuthorization(scheme, secondSecret)} {
			if bytes.Contains(body, []byte(secret)) {
				t.Fatal("private Gradle response, client output or audit leaked a source secret")
			}
		}
	}
	patch := func(secret string) {
		t.Helper()
		auth := map[string]string{"scheme": string(scheme), "secret": secret}
		if scheme == repository.UpstreamAuthSchemeBasic {
			auth["username"] = "source-reader"
		}
		if secret == "" {
			auth = map[string]string{"scheme": "none"}
		}
		body, _ := json.Marshal(map[string]any{"upstreamAuth": auth})
		response := ociBearerManagementRequest(handler, http.MethodPatch, "/api/v2/repositories/"+proxy.ID, string(body), proxy.Version, "")
		assertNoSecret(t, response.Body.Bytes())
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &proxy) != nil {
			t.Fatalf("patch private Gradle source through API=%d %s", response.Code, response.Body.String())
		}
		persisted, err := store.GetHostedRepository(t.Context(), proxy.ID)
		if err != nil || (secret != "" && (persisted.UpstreamAuth == nil || persisted.UpstreamAuth.Secret == "" || persisted.UpstreamAuth.Secret == secret)) || (secret == "" && persisted.UpstreamAuth != nil) {
			t.Fatal("management API did not persist the requested encrypted credential or explicit removal")
		}
		// Model a Gateway process restart without discarding PostgreSQL cache
		// metadata or RustFS bytes. This also starts a fresh local breaker owner.
		handler = newHandler()
	}
	read := func(t *testing.T, path string, want int) {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/maven/"+name+path, nil)
		request.SetBasicAuth("private-client", "private-client-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		assertNoSecret(t, response.Body.Bytes())
		if response.Code != want {
			t.Fatalf("private Gradle native read=%d want=%d", response.Code, want)
		}
		if want == http.StatusOK && !bytes.Equal(response.Body.Bytes(), assets[path].body) {
			t.Fatal("private Gradle native read returned incomplete or different metadata/object bytes")
		}
	}
	run := func(t *testing.T, round string, success bool) {
		t.Helper()
		server := httptest.NewServer(handler)
		defer server.Close()
		before, beforeRefusals := len(upstream.snapshot()), sourceRefusals.Load()
		out := runPrivateGradleClient(t, gradle, server.URL+"/maven/"+name, scheme, route, round, assets, success)
		assertNoSecret(t, out)
		if fallbackVisits.Load() != 0 {
			t.Fatal("a private Gradle request fell through to the anonymous Group member")
		}
		if !success {
			if sourceRefusals.Load() <= beforeRefusals {
				t.Fatal("Gradle rejection did not reach and reject the protected source")
			}
			evidence, err := json.Marshal(map[string]any{"scheme": scheme, "route": route, "round": round,
				"sourceRequests": len(upstream.snapshot()) - before, "sourceStatus": lastSourceRefusal.Load(),
				"gatewayStatus": http.StatusBadGateway, "anonymousMemberRequests": fallbackVisits.Load(), "output": string(out)})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("PRIVATE_GRADLE_REJECTION %s", evidence)
		}
	}

	t.Run("missing", func(t *testing.T) { run(t, "missing", false) })
	patch(firstSecret)
	beforeCold := len(upstream.snapshot())
	t.Run("cold", func(t *testing.T) { run(t, "cold", true) })
	requested := make(map[string]bool)
	var crossOrigin, returned bool
	for _, request := range upstream.snapshot()[beforeCold:] {
		if request.host == redirectOrigin && request.headers.Get("Authorization") == privateProxyAuthorization(scheme, firstSecret) {
			requested[request.path] = true
		}
		crossOrigin = crossOrigin || request.host == redirectCDN
		returned = returned || strings.HasPrefix(request.path, "/returned/")
	}
	for _, path := range []string{"/org/example/widget/maven-metadata.xml", "/org/example/widget/1.0/widget-1.0.pom", "/org/example/widget/1.0/widget-1.0.jar", "/org/example/core/1.0/core-1.0.pom", "/org/example/core/1.0/core-1.0.jar", "/org/example/leaf/1.0/leaf-1.0.pom", "/org/example/leaf/1.0/leaf-1.0.jar"} {
		if !requested[path] {
			t.Fatalf("official Gradle did not authenticate its complete metadata/three-level closure: %s", path)
		}
	}
	if !crossOrigin || !returned {
		t.Fatal("official Gradle did not exercise the permitted CDN and return redirect hops")
	}
	for path := range assets {
		read(t, path, http.StatusOK)
	}
	cold := len(upstream.snapshot())
	store, cache = backend.reload()
	handler = newHandler()
	t.Run("restarted-cache", func(t *testing.T) { run(t, "restarted-cache", true) })
	if len(upstream.snapshot()) != cold {
		t.Fatal("empty Gradle cache after Gateway/PG-store reconstruction fetched the source again")
	}
	patch(wrongSecret)
	t.Run("wrong", func(t *testing.T) { run(t, "wrong", false) })
	expected.Store(privateProxyAuthorization(scheme, secondSecret))
	patch(secondSecret)
	t.Run("rotated", func(t *testing.T) { run(t, "rotated", true) })
	if len(upstream.snapshot()) <= cold {
		t.Fatal("credential rotation trusted a cache bound to the previous source version")
	}
	stale := ociBearerManagementRequest(handler, http.MethodPatch, "/api/v2/repositories/"+proxy.ID, `{"upstreamAuth":{"scheme":"none"}}`, "1", "")
	if stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale Gradle credential mutation=%d", stale.Code)
	}
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(fmt.Sprintf("expired-source-%d", status), func(t *testing.T) {
			refusal.Store(int32(status))
			handler = newHandler()
			// An uncached coordinate must reauthorize even when the same source
			// already has verified content. Never return an anonymous member.
			before := len(upstream.snapshot())
			for attempt := range 4 {
				read(t, "/org/example/revoked/1.0/revoked-1.0.pom", http.StatusBadGateway)
				if attempt == 0 && len(upstream.snapshot()) <= before {
					t.Fatal("expired source acceptance did not reach the protected source")
				}
				if attempt == 0 {
					before = len(upstream.snapshot())
				} else if len(upstream.snapshot()) != before {
					t.Fatal("open-circuit requests retried or bypassed the source refusal")
				}
			}
			if fallbackVisits.Load() != 0 {
				t.Fatal("expired source or open circuit reached an anonymous Group member")
			}
		})
	}
	refusal.Store(0)
	patch("")
	t.Run("cleared", func(t *testing.T) { run(t, "cleared", false) })
	patch(secondSecret)
	t.Run("restored", func(t *testing.T) { run(t, "restored", true) })
	for path := range assets {
		read(t, path, http.StatusOK)
	}
	beforeStop := len(upstream.snapshot())
	upstream.stopSource()
	store, cache = backend.reload()
	handler = newHandler()
	t.Run("disconnected-cache", func(t *testing.T) { run(t, "disconnected-cache", true) })
	for path := range assets {
		read(t, path, http.StatusOK)
	}
	if len(upstream.snapshot()) != beforeStop || fallbackVisits.Load() != 0 {
		t.Fatal("verified Gradle cache after source shutdown tried an upstream or fallback")
	}
	audits, err := store.ListAudits(t.Context(), repository.AuditQuery{GroupName: name, Limit: 500})
	if err != nil || len(audits) == 0 {
		t.Fatal("private Gradle authorization and read audit evidence missing")
	}
	encoded, err := json.Marshal(audits)
	if err != nil {
		t.Fatal(err)
	}
	assertNoSecret(t, encoded)
}

func privateGradleAssets(t *testing.T) map[string]redirectCacheAsset {
	t.Helper()
	assets := make(map[string]redirectCacheAsset)
	for i, name := range []string{"widget", "core", "leaf"} {
		dependency := ""
		if i < 2 {
			next := []string{"core", "leaf"}[i]
			dependency = fmt.Sprintf("<dependencies><dependency><groupId>org.example</groupId><artifactId>%s</artifactId><version>1.0</version></dependency></dependencies>", next)
		}
		pom := fmt.Sprintf(`<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion><groupId>org.example</groupId><artifactId>%s</artifactId><version>1.0</version><packaging>jar</packaging>%s</project>`, name, dependency)
		var body bytes.Buffer
		jar := zip.NewWriter(&body)
		entry, err := jar.Create("META-INF/gateway-identity.txt")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Fprintf(entry, "org.example:%s:1.0\n", name); err != nil {
			t.Fatal(err)
		}
		if err := jar.Close(); err != nil {
			t.Fatal(err)
		}
		prefix := "/org/example/" + name + "/1.0/" + name + "-1.0"
		assets[prefix+".pom"] = redirectCacheAsset{body: []byte(pom), mediaType: "application/xml"}
		assets[prefix+".jar"] = redirectCacheAsset{body: body.Bytes(), mediaType: "application/java-archive"}
	}
	assets["/org/example/widget/maven-metadata.xml"] = redirectCacheAsset{body: []byte(`<metadata><groupId>org.example</groupId><artifactId>widget</artifactId><versioning><latest>1.0</latest><release>1.0</release><versions><version>1.0</version></versions><lastUpdated>20261021000000</lastUpdated></versioning></metadata>`), mediaType: "application/xml"}
	paths := make([]string, 0, len(assets))
	for path := range assets {
		paths = append(paths, path)
	}
	for _, path := range paths {
		asset := assets[path]
		asset.digest = redirectCacheSHA256(asset.body)
		assets[path] = asset
		for suffix, sum := range map[string]string{".sha1": fmt.Sprintf("%x", sha1.Sum(asset.body)), ".sha256": fmt.Sprintf("%x", sha256.Sum256(asset.body))} {
			body := []byte(sum + "\n")
			assets[path+suffix] = redirectCacheAsset{body: body, mediaType: "text/plain", digest: redirectCacheSHA256(body)}
		}
	}
	return assets
}

func runPrivateGradleClient(t *testing.T, gradle, endpoint string, scheme repository.UpstreamAuthScheme, route, round string, assets map[string]redirectCacheAsset, success bool) []byte {
	t.Helper()
	work := t.TempDir()
	home, gradleHome, temporary := filepath.Join(work, "home"), filepath.Join(work, "gradle-home"), filepath.Join(work, "tmp")
	for _, path := range []string{home, gradleHome, temporary} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	settings := "rootProject.name = 'private-upstream-gradle'\n"
	build := fmt.Sprintf(`repositories {
    maven {
        url = uri(%q)
        allowInsecureProtocol = true
        credentials { username = 'private-client'; password = 'private-client-secret' }
        authentication { basic(BasicAuthentication) }
        metadataSources { mavenPom(); ignoreGradleMetadataRedirection() }
    }
}
configurations { privateJars; privatePoms { transitive = false } }
dependencies {
    privateJars 'org.example:widget:1.+'
    privatePoms 'org.example:widget:1.0@pom'
    privatePoms 'org.example:core:1.0@pom'
    privatePoms 'org.example:leaf:1.0@pom'
}
tasks.register('resolvePrivate') {
    doLast {
        def resolved = configurations.privateJars.resolvedConfiguration.resolvedArtifacts
        def identities = resolved.collect { it.moduleVersion.id.toString() }.sort()
        if (identities != ['org.example:core:1.0', 'org.example:leaf:1.0', 'org.example:widget:1.0']) {
            throw new GradleException('Unexpected transitive dependency identities: ' + identities)
        }
        copy { from configurations.privateJars; from configurations.privatePoms; into layout.buildDirectory.dir('verified-closure') }
        file('identities.json').text = groovy.json.JsonOutput.toJson(identities)
        println('PRIVATE_GRADLE_RESOLUTION_COMPLETE')
    }
}
`, endpoint)
	writeRedirectMavenClientFile(t, filepath.Join(work, "settings.gradle"), []byte(settings))
	writeRedirectMavenClientFile(t, filepath.Join(work, "build.gradle"), []byte(build))
	// Gradle may start its own single-use JVM even with --no-daemon. Set both
	// launcher and daemon homes; neither Java process may inherit ~/.gradle.
	writeRedirectMavenClientFile(t, filepath.Join(gradleHome, "gradle.properties"), []byte("org.gradle.jvmargs=-Xmx256m -Duser.home="+home+" -Djava.io.tmpdir="+temporary+"\n"))
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, gradle, "--no-daemon", "--no-watch-fs", "--no-build-cache", "--no-configuration-cache", "--max-workers=1", "--console=plain", "--warning-mode=fail", "resolvePrivate")
	command.Dir = work
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "GRADLE_USER_HOME=" + gradleHome,
		"TMPDIR=" + temporary, "LANG=C", "GRADLE_OPTS=-Duser.home=" + home + " -Djava.io.tmpdir=" + temporary + " -Dorg.gradle.java.installations.auto-download=false"}
	out, err := command.CombinedOutput()
	if ctx.Err() != nil || (err == nil) != success || bytes.Contains(out, []byte("PRIVATE_GRADLE_RESOLUTION_COMPLETE")) != success {
		t.Fatalf("official Gradle %s expected success=%v: %v\n%s", round, success, err, out)
	}
	objects := make([]map[string]any, 0, 6)
	if success {
		if !bytes.Contains(out, []byte("BUILD SUCCESSFUL")) {
			t.Fatalf("official Gradle did not complete dependency resolution:\n%s", out)
		}
		for _, name := range []string{"widget", "core", "leaf"} {
			for _, suffix := range []string{"pom", "jar"} {
				filename := name + "-1.0." + suffix
				body, err := os.ReadFile(filepath.Join(work, "build", "verified-closure", filename))
				want := assets["/org/example/"+name+"/1.0/"+filename]
				if err != nil || len(body) != len(want.body) || !bytes.Equal(body, want.body) || redirectCacheSHA256(body) != want.digest {
					t.Fatalf("official Gradle %s complete %s differs: %v", round, filename, err)
				}
				sum := sha256.Sum256(body)
				objects = append(objects, map[string]any{"identity": "org.example:" + name + ":1.0", "file": filename, "size": len(body), "sha256": hex.EncodeToString(sum[:])})
			}
		}
		identities, err := os.ReadFile(filepath.Join(work, "identities.json"))
		if err != nil || string(identities) != `["org.example:core:1.0","org.example:leaf:1.0","org.example:widget:1.0"]` {
			t.Fatal("official Gradle transitive identity report missing or different")
		}
	} else if !bytes.Contains(out, []byte("Could not resolve")) || !bytes.Contains(out, []byte("BUILD FAILED")) || !bytes.Contains(out, []byte("status code 502")) {
		t.Fatalf("official Gradle failure did not reach dependency resolution:\n%s", out)
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i]["file"].(string) < objects[j]["file"].(string) })
	data, err := json.Marshal(map[string]any{"client": "Gradle", "version": privateGradleVersion, "mode": "owned-official-distribution", "scheme": scheme, "route": route, "round": round, "expectedSuccess": success, "emptyClientCache": true, "targetOnly": true, "fullPayloadCheck": success, "objects": objects})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("PRIVATE_GRADLE_CLIENT %s", data)
	return out
}
