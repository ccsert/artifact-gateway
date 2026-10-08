//go:build mavenclient

package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

const redirectMavenClientPlugin = "gateway.fixture:maven-client-fixture:1.0.0:resolve-fixture"
const redirectMavenClientPluginPath = "gateway/fixture/maven-client-fixture/1.0.0/maven-client-fixture-1.0.0"

// This optional gate runs the installed official Maven CLI. Its only plugin is
// compiled from repository-owned Java against the installed Maven API and
// seeded locally. Both dependency-resolution rounds start with an empty local
// repository except for that plugin, so Maven's cache cannot hide Gateway reads.
func TestMavenClientProxyRedirectGatewayColdHotCache(t *testing.T) {
	mvn, err := exec.LookPath("mvn")
	if err != nil {
		t.Fatal("Maven redirect client gate requires an installed mvn and JDK; no dependencies are downloaded")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root := t.TempDir()
	environment := redirectMavenClientEnvironment(t, root)
	plugin := buildRedirectMavenClientPlugin(t, ctx, mvn, root, environment)
	assets := redirectMavenClientAssets(t)
	for _, route := range []string{"proxy", "group"} {
		t.Run(route, func(t *testing.T) {
			upstream := newRedirectCacheUpstream(t, assets, false)
			gateway, name := newRedirectCacheGateway(t, repository.FormatMaven, route, upstream)
			work := filepath.Join(root, route)
			pom := filepath.Join(work, "pom.xml")
			settings := filepath.Join(work, "settings.xml")
			globalSettings := filepath.Join(work, "global-settings.xml")
			writeRedirectMavenClientFile(t, globalSettings, []byte(`<settings xmlns="http://maven.apache.org/SETTINGS/1.2.0"/>`))
			// Explicit user/global settings and user.home isolate the CLI from
			// real ~/.m2 settings, identities, extensions and local artifacts.
			writeRedirectMavenClientFile(t, settings, []byte(fmt.Sprintf(`<settings xmlns="http://maven.apache.org/SETTINGS/1.2.0"><mirrors><mirror><id>owned-gateway</id><mirrorOf>*</mirrorOf><url>%s/maven/%s</url></mirror></mirrors></settings>`, gateway.URL, name)))
			writeRedirectMavenClientFile(t, pom, []byte(fmt.Sprintf(`<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion><groupId>gateway.fixture</groupId><artifactId>redirect-resolver</artifactId><version>1</version><repositories><repository><id>owned-gateway</id><url>%s/maven/%s</url><releases><checksumPolicy>fail</checksumPolicy></releases><snapshots><enabled>false</enabled></snapshots></repository></repositories><dependencies><dependency><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.0</version></dependency></dependencies></project>`, gateway.URL, name)))
			coldRequests := 0
			for _, round := range []string{"cold", "hot"} {
				t.Run(round, func(t *testing.T) {
					local := filepath.Join(work, round, "repository")
					writeRedirectMavenClientFile(t, filepath.Join(local, redirectMavenClientPluginPath+".jar"), plugin)
					writeRedirectMavenClientFile(t, filepath.Join(local, redirectMavenClientPluginPath+".pom"), []byte(`<project><modelVersion>4.0.0</modelVersion><groupId>gateway.fixture</groupId><artifactId>maven-client-fixture</artifactId><version>1.0.0</version><packaging>maven-plugin</packaging></project>`))
					out := runRedirectMavenClientCommand(t, ctx, mvn, work, environment,
						"-B", "-ntp", "-Dstyle.color=never", "-s", settings, "-gs", globalSettings,
						"-f", pom, "-Dmaven.repo.local="+local, redirectMavenClientPlugin)
					if !bytes.Contains(out, []byte("Gateway fixture dependency resolution completed.")) || !bytes.Contains(out, []byte("BUILD SUCCESS")) {
						t.Fatalf("official Maven did not complete fixture dependency resolution:\n%s", out)
					}
					for _, suffix := range []string{"pom", "jar"} {
						path := "/org/example/widget/1.0/widget-1.0." + suffix
						body, err := os.ReadFile(filepath.Join(local, filepath.FromSlash(strings.TrimPrefix(path, "/"))))
						if err != nil || !bytes.Equal(body, assets[path].body) {
							t.Fatalf("official Maven resolved different %s bytes: %v", suffix, err)
						}
					}
					requests := upstream.snapshot()
					if round == "cold" {
						coldRequests = len(requests)
						assertRedirectMavenClientColdRequests(t, requests, assets)
					} else if len(requests) != coldRequests {
						t.Fatalf("fresh Maven local repository added %d upstream requests on hot Gateway cache", len(requests)-coldRequests)
					}
					t.Logf("official Maven %s/%s resolved exact POM/JAR bytes; upstream requests=%d", route, round, len(requests))
				})
			}
		})
	}
}

func redirectMavenClientEnvironment(t *testing.T, root string) []string {
	t.Helper()
	userHome := filepath.Join(root, "isolated-user")
	temporary := filepath.Join(root, "tmp")
	for _, dir := range []string{userHome, temporary} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	// No inherited MAVEN_ARGS, proxy variables, JAVA_TOOL_OPTIONS or credentials.
	// Skip shell rc files; Java user.home and explicit settings cover Maven config.
	return []string{"PATH=" + os.Getenv("PATH"), "LANG=C", "TMPDIR=" + temporary,
		"MAVEN_SKIP_RC=true", "MAVEN_OPTS=-Duser.home=" + userHome}
}

func writeRedirectMavenClientFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
}

func runRedirectMavenClientCommand(t *testing.T, ctx context.Context, binary, directory string, environment []string, args ...string) []byte {
	t.Helper()
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir, command.Env = directory, environment
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("owned Maven fixture command failed (%s): %v\n%s", filepath.Base(binary), err, out)
	}
	return out
}

func buildRedirectMavenClientPlugin(t *testing.T, ctx context.Context, mvn, root string, environment []string) []byte {
	t.Helper()
	version := runRedirectMavenClientCommand(t, ctx, mvn, root, environment, "-B", "-ntp", "-Dstyle.color=never", "-v")
	var mavenHome, javaHome string
	for _, line := range strings.Split(string(version), "\n") {
		if strings.HasPrefix(line, "Maven home: ") {
			mavenHome = strings.TrimSpace(strings.TrimPrefix(line, "Maven home: "))
		}
		if strings.HasPrefix(line, "Java version: ") {
			_, runtime, ok := strings.Cut(line, "runtime: ")
			if ok {
				javaHome = strings.TrimSpace(runtime)
			}
		}
	}
	if mavenHome == "" || javaHome == "" {
		t.Fatalf("installed Maven did not report its Maven/JDK paths:\n%s", version)
	}
	t.Logf("official client prerequisites:\n%s", version)
	apis, err := filepath.Glob(filepath.Join(mavenHome, "lib", "maven-plugin-api-*.jar"))
	if err != nil || len(apis) != 1 {
		t.Fatalf("installed Maven plugin API is unavailable: %v", err)
	}
	source, err := filepath.Abs(filepath.Join("..", "..", "scripts", "maven-client-fixture", "ResolveMojo.java"))
	if err != nil {
		t.Fatal(err)
	}
	classes := filepath.Join(root, "plugin-classes")
	if err := os.MkdirAll(classes, 0700); err != nil {
		t.Fatal(err)
	}
	runRedirectMavenClientCommand(t, ctx, filepath.Join(javaHome, "bin", "javac"), root, environment,
		"--release", "17", "-classpath", apis[0], "-d", classes, source)
	var archive bytes.Buffer
	jar := zip.NewWriter(&archive)
	entries := map[string]string{
		"com/artifactgateway/fixture/ResolveMojo.class": filepath.Join(classes, "com", "artifactgateway", "fixture", "ResolveMojo.class"),
		"META-INF/maven/plugin.xml":                     filepath.Join(filepath.Dir(source), "plugin.xml"),
	}
	for name, path := range entries {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		entry, err := jar.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := jar.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes()
}

func redirectMavenClientAssets(t *testing.T) map[string]redirectCacheAsset {
	t.Helper()
	var body bytes.Buffer
	jar := zip.NewWriter(&body)
	entry, err := jar.Create("META-INF/MANIFEST.MF")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(entry, "Manifest-Version: 1.0\n\n"); err != nil {
		t.Fatal(err)
	}
	if err := jar.Close(); err != nil {
		t.Fatal(err)
	}
	assets := map[string]redirectCacheAsset{
		"/org/example/widget/1.0/widget-1.0.pom": {body: []byte(`<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.0</version><packaging>jar</packaging></project>`), mediaType: "application/xml"},
		"/org/example/widget/1.0/widget-1.0.jar": {body: body.Bytes(), mediaType: "application/java-archive"},
	}
	for _, path := range []string{"/org/example/widget/1.0/widget-1.0.pom", "/org/example/widget/1.0/widget-1.0.jar"} {
		asset := assets[path]
		asset.digest = redirectCacheSHA256(asset.body)
		assets[path] = asset
		checksums := map[string]string{".md5": fmt.Sprintf("%x", md5.Sum(asset.body)), ".sha1": fmt.Sprintf("%x", sha1.Sum(asset.body)), ".sha256": fmt.Sprintf("%x", sha256.Sum256(asset.body)), ".sha512": fmt.Sprintf("%x", sha512.Sum512(asset.body))}
		for suffix, value := range checksums {
			checksum := []byte(value + "\n")
			assets[path+suffix] = redirectCacheAsset{checksum, "text/plain", redirectCacheSHA256(checksum)}
		}
	}
	return assets
}

func assertRedirectMavenClientColdRequests(t *testing.T, requests []proxyRedirectRequest, assets map[string]redirectCacheAsset) {
	t.Helper()
	counts := make(map[string]int)
	for _, request := range requests {
		if request.method != http.MethodGet || request.headers.Get("Authorization") != "" || request.headers.Get("Proxy-Authorization") != "" {
			t.Fatalf("official Maven upstream request must be anonymous GET: %+v", request)
		}
		path := request.path
		if request.host == redirectCDN {
			path = strings.TrimPrefix(path, "/download")
		} else if request.host != redirectOrigin {
			t.Fatalf("unexpected upstream host %s", request.host)
		}
		if _, ok := assets[path]; !ok {
			t.Fatalf("official Maven requested an unseeded artifact/plugin: %s", request.path)
		}
		counts[request.host+path]++
	}
	for _, path := range []string{"/org/example/widget/1.0/widget-1.0.pom", "/org/example/widget/1.0/widget-1.0.jar"} {
		if counts[redirectOrigin+path] != 1 || counts[redirectCDN+path] != 1 {
			t.Fatalf("official Maven %s must cross the approved CDN redirect once: counts=%v", path, counts)
		}
	}
	for path := range assets {
		origin, cdn := counts[redirectOrigin+path], counts[redirectCDN+path]
		if origin != cdn || origin > 1 {
			t.Fatalf("official Maven %s origin/CDN=%d/%d, want one matching pair when requested", path, origin, cdn)
		}
	}
}
