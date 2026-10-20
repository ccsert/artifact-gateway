//go:build integration && mavenclient && ociclient

package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/repository"
)

func TestPostgresRustFSPrivateProxyOfficialClients(t *testing.T) {
	mvn, err := exec.LookPath("mvn")
	if err != nil {
		t.Fatal("private upstream client acceptance requires official Maven")
	}
	oras, err := exec.LookPath("oras")
	if err != nil {
		t.Fatal("private upstream client acceptance requires official ORAS")
	}
	root := t.TempDir()
	environment := redirectMavenClientEnvironment(t, root)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	plugin := buildRedirectMavenClientPlugin(t, ctx, mvn, root, environment)
	version := runRedirectMavenClientCommand(t, ctx, oras, root, environment, "version")
	t.Logf("official OCI client prerequisites:\n%s", version)
	for _, format := range []repository.Format{repository.FormatMaven, repository.FormatOCI} {
		for _, scheme := range []repository.UpstreamAuthScheme{repository.UpstreamAuthSchemeBasic, repository.UpstreamAuthSchemeBearer} {
			for _, route := range []string{"proxy", "group"} {
				for _, issuer := range []bool{false, true} {
					if issuer && format != repository.FormatOCI {
						continue
					}
					t.Run(fmt.Sprintf("%s/%s/%s/issuer=%v", format, scheme, route, issuer), func(t *testing.T) {
						backend := newPrivateProxyPostgresBackend(t)
						if format == repository.FormatMaven {
							backend.assets = redirectMavenClientAssets(t)
						}
						backend.client = func(t *testing.T, handler http.Handler, name, round string, assets map[string]redirectCacheAsset) {
							t.Helper()
							server := httptest.NewServer(handler)
							defer server.Close()
							work := t.TempDir()
							if format == repository.FormatMaven {
								settings, globalSettings, pom := filepath.Join(work, "settings.xml"), filepath.Join(work, "global-settings.xml"), filepath.Join(work, "pom.xml")
								local := filepath.Join(work, "empty-target-repository")
								writeRedirectMavenClientFile(t, globalSettings, []byte(`<settings xmlns="http://maven.apache.org/SETTINGS/1.2.0"/>`))
								writeRedirectMavenClientFile(t, settings, []byte(fmt.Sprintf(`<settings><mirrors><mirror><id>owned-gateway</id><mirrorOf>*</mirrorOf><url>%s/maven/%s</url></mirror></mirrors><servers><server><id>owned-gateway</id><username>private-client</username><password>private-client-secret</password></server></servers></settings>`, server.URL, name)))
								writeRedirectMavenClientFile(t, pom, []byte(`<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion><groupId>gateway.fixture</groupId><artifactId>private-source-consumer</artifactId><version>1</version><dependencies><dependency><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.0</version></dependency></dependencies></project>`))
								writeRedirectMavenClientFile(t, filepath.Join(local, redirectMavenClientPluginPath+".jar"), plugin)
								writeRedirectMavenClientFile(t, filepath.Join(local, redirectMavenClientPluginPath+".pom"), []byte(`<project><modelVersion>4.0.0</modelVersion><groupId>gateway.fixture</groupId><artifactId>maven-client-fixture</artifactId><version>1.0.0</version><packaging>maven-plugin</packaging></project>`))
								out := runRedirectMavenClientCommand(t, ctx, mvn, work, environment, "-B", "-ntp", "-Dstyle.color=never", "-s", settings, "-gs", globalSettings, "-f", pom, "-Dmaven.repo.local="+local, redirectMavenClientPlugin)
								if !bytes.Contains(out, []byte("Gateway fixture dependency resolution completed.")) || !bytes.Contains(out, []byte("BUILD SUCCESS")) {
									t.Fatalf("official Maven did not complete fixture resolution:\n%s", out)
								}
								for _, suffix := range []string{"pom", "jar"} {
									sourcePath := "/org/example/widget/1.0/widget-1.0." + suffix
									body, err := os.ReadFile(filepath.Join(local, filepath.FromSlash(strings.TrimPrefix(sourcePath, "/"))))
									if err != nil || !bytes.Equal(body, assets[sourcePath].body) || redirectCacheSHA256(body) != assets[sourcePath].digest {
										t.Fatalf("official Maven %s %s bytes differ: %v", round, suffix, err)
									}
								}
								t.Logf("Maven %s: empty target cache, exact source POM/JAR plus checksum bytes", round)
							} else {
								registry := filepath.Join(work, "owned-registry.json")
								data, _ := json.Marshal(map[string]any{"auths": map[string]any{strings.TrimPrefix(server.URL, "http://"): map[string]string{"auth": base64.StdEncoding.EncodeToString([]byte("private-client:private-client-secret"))}}})
								if err := os.WriteFile(registry, data, 0600); err != nil {
									t.Fatal(err)
								}
								layout := filepath.Join(work, "empty-layout")
								command := exec.CommandContext(ctx, oras, "cp", "--no-tty", "--from-plain-http", "--from-registry-config", registry, "--to-oci-layout", strings.TrimPrefix(server.URL, "http://")+"/"+name+"/demo:latest", layout+":latest")
								command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + work, "TMPDIR=" + work, "LANG=C"}
								out, err := command.CombinedOutput()
								if err != nil {
									t.Fatalf("official ORAS %s: %v\n%s", round, err, out)
								}
								for _, asset := range assets {
									body, err := os.ReadFile(filepath.Join(layout, "blobs", "sha256", strings.TrimPrefix(asset.digest, "sha256:")))
									if err != nil || !bytes.Equal(body, asset.body) || redirectCacheSHA256(body) != asset.digest {
										t.Fatalf("official ORAS %s closure %s bytes differ: %v", round, asset.digest, err)
									}
								}
								t.Logf("ORAS %s: empty layout, exact seven source index/manifest/config/layer objects", round)
							}
						}
						privateProxyCredentialsContract(t, privateProxyCase{format, scheme, route, issuer}, backend)
					})
				}
			}
		}
	}
}
