//go:build integration && ocidiscoveryclient

package app

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	"github.com/ccsert/artifact-gateway/internal/ocigraph"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func ociDiscoveryClientCommand(ctx context.Context, environment []string, input []byte, binary string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, binary, args...)
	command.Env = environment
	command.Stdin = bytes.NewReader(input)
	return command.CombinedOutput()
}

func requireDiscoveryClient(t *testing.T, ctx context.Context, environment []string, binary string, args ...string) []byte {
	t.Helper()
	body, err := ociDiscoveryClientCommand(ctx, environment, nil, binary, args...)
	if err != nil {
		t.Fatalf("official %s failed: %v\n%s", filepath.Base(binary), err, body)
	}
	return body
}

// Every Docker pull uses a new daemon and volume. The user's daemon only hosts
// the labelled fixture; it never receives the image under test or credentials.
func ociDiscoveryDockerPull(t *testing.T, reference, credential string, config, imageLayer []byte, wantSuccess bool) {
	t.Helper()
	project, image := os.Getenv("OCI_DISCOVERY_PROJECT"), os.Getenv("OCI_DISCOVERY_DIND_IMAGE")
	if project == "" || !strings.HasPrefix(image, "docker.io/library/docker@sha256:") {
		t.Fatal("owned project and immutable official Docker image are required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	name := project + "-client-" + uuid.NewString()[:8]
	volume := name + "-data"
	work := t.TempDir()
	environment := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + work, "TMPDIR=" + work, "DOCKER_HOST=" + os.Getenv("DOCKER_HOST"), "DOCKER_CONFIG=" + os.Getenv("DOCKER_CONFIG"), "LANG=C", "NO_PROXY=localhost,127.0.0.1"}
	run := func(args ...string) []byte { return requireDiscoveryClient(t, ctx, environment, "docker", args...) }
	label := "ag.oci-discovery-owner=" + project
	// Cleanup resolves only a fresh name bearing both exact ownership labels.
	// This also covers a failed create/start with no usable container receipt.
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		for _, resource := range []string{"container", "volume"} {
			target := name
			if resource == "volume" {
				target = volume
			}
			data, err := ociDiscoveryClientCommand(cleanupCtx, environment, nil, "docker", resource, "inspect", target)
			if err != nil {
				continue
			}
			var found []struct {
				ID     string            `json:"Id"`
				Name   string            `json:"Name"`
				Labels map[string]string `json:"Labels"`
				Config struct {
					Labels map[string]string `json:"Labels"`
				} `json:"Config"`
			}
			if json.Unmarshal(data, &found) != nil || len(found) != 1 {
				t.Error("unable to verify owned Docker cleanup receipt")
				continue
			}
			labels := found[0].Labels
			if resource == "container" {
				labels = found[0].Config.Labels
			}
			if labels["ag.oci-discovery-owner"] != project || labels["com.docker.compose.project"] != project {
				t.Error("Docker cleanup ownership mismatch")
				continue
			}
			args := []string{resource, "rm"}
			if resource == "container" {
				args = append(args, "--force")
			}
			args = append(args, target)
			if data, err := ociDiscoveryClientCommand(cleanupCtx, environment, nil, "docker", args...); err != nil {
				t.Errorf("owned Docker cleanup: %v %s", err, data)
			}
		}
	}()
	run("volume", "create", "--label", label, "--label", "com.docker.compose.project="+project, volume)
	host, _, found := strings.Cut(reference, "/")
	if !found {
		t.Fatal("registry reference is required")
	}
	run("run", "--detach", "--pull=never", "--privileged", "--name", name, "--label", label, "--label", "com.docker.compose.project="+project, "--network", project+"_default", "--add-host", "host.docker.internal:host-gateway", "--mount", "type=volume,src="+volume+",dst=/var/lib/docker", "--entrypoint", "dockerd", image, "--host=unix:///var/run/docker.sock", "--storage-driver=vfs", "--bridge=none", "--iptables=false", "--ip-masq=false", "--insecure-registry="+host)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, err := ociDiscoveryClientCommand(ctx, environment, nil, "docker", "exec", name, "docker", "info")
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			logCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			logs, _ := ociDiscoveryClientCommand(logCtx, environment, nil, "docker", "logs", name)
			t.Fatalf("owned Docker daemon unavailable: %s", logs)
		case <-ticker.C:
		}
	}
	version := run("exec", name, "docker", "version", "--format", "{{.Client.Version}}/{{.Server.Version}}")
	if strings.TrimSpace(string(version)) != "29.7.2/29.7.2" {
		t.Fatalf("official Docker pin differs: %s", version)
	}
	if inventory := run("exec", name, "docker", "image", "ls", "--all", "--quiet"); len(bytes.TrimSpace(inventory)) != 0 {
		t.Fatal("Docker target image cache was not empty")
	}
	t.Log("official Docker: pinned fresh daemon and empty image cache verified")
	if out, err := ociDiscoveryClientCommand(ctx, environment, []byte(credential), "docker", "exec", "-i", name, "docker", "--config", "/client", "login", "--username", "owned-client", "--password-stdin", host); err != nil {
		t.Fatalf("owned Docker credential login: %v %s", err, out)
	}
	out, err := ociDiscoveryClientCommand(ctx, environment, nil, "docker", "exec", name, "docker", "--config", "/client", "pull", reference)
	if !wantSuccess {
		if err == nil {
			t.Fatal("restricted Docker pull unexpectedly succeeded")
		}
		if current := run("exec", name, "docker", "image", "ls", "--all", "--quiet"); len(bytes.TrimSpace(current)) != 0 {
			t.Fatal("denied Docker pull published an image")
		}
		return
	}
	if err != nil {
		t.Fatalf("official Docker pull: %v %s", err, out)
	}
	if inspect := run("exec", name, "docker", "image", "inspect", reference, "--format", "{{.Id}}"); strings.TrimSpace(string(inspect)) != ocigraph.Digest(config) {
		t.Fatalf("Docker image config identity differs: %s", inspect)
	}
	var sourceConfig struct {
		RootFS struct {
			DiffIDs []string `json:"diff_ids"`
		} `json:"rootfs"`
	}
	var actualDiffIDs []string
	if json.Unmarshal(config, &sourceConfig) != nil || len(imageLayer) == 0 || len(sourceConfig.RootFS.DiffIDs) != 1 || sourceConfig.RootFS.DiffIDs[0] != ocigraph.Digest(imageLayer) {
		t.Fatal("owned source image config did not bind its exact original layer")
	}
	layers := run("exec", name, "docker", "image", "inspect", reference, "--format", "{{json .RootFS.Layers}}")
	if json.Unmarshal(layers, &actualDiffIDs) != nil || len(actualDiffIDs) != 1 || actualDiffIDs[0] != sourceConfig.RootFS.DiffIDs[0] {
		t.Fatalf("Docker image original layer identity differs: %s", layers)
	}
	t.Log("official Docker: digest pull and exact config identity verified")
	// Export only the image ID already proved to equal the original config.
	command := exec.CommandContext(ctx, "docker", "exec", name, "docker", "image", "save", ocigraph.Digest(config))
	command.Env = environment
	var stderr bytes.Buffer
	command.Stderr = &stderr
	saved, err := command.Output()
	if err != nil {
		t.Fatalf("official Docker image save failed: %v\n%s", err, stderr.Bytes())
	}
	archive := tar.NewReader(bytes.NewReader(saved))
	matched := false
	layerMatched := false
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Size > 1<<20 {
			t.Fatal("unexpected Docker fixture archive size")
		}
		data, err := io.ReadAll(archive)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(data, config) {
			matched = true
		}
		if bytes.Equal(data, imageLayer) {
			layerMatched = true
		}
	}
	if !matched {
		t.Fatal("official Docker archive did not retain the exact source config bytes")
	}
	if !layerMatched {
		t.Fatal("official Docker archive did not retain the exact source layer tar bytes")
	}
	t.Log("official Docker: empty daemon cache, digest pull, exact original config and layer tar bytes")
}

func ociDiscoveryOriginalImageLayer(t *testing.T) []byte {
	t.Helper()
	var body bytes.Buffer
	archive := tar.NewWriter(&body)
	payload := []byte("owned OCI discovery image layer\n")
	if err := archive.WriteHeader(&tar.Header{Name: "oci-discovery-proof.txt", Mode: 0644, Size: int64(len(payload)), ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func TestPostgresRustFSOCIDiscoveryOfficialORASAndDocker(t *testing.T) {
	oras := os.Getenv("OCI_DISCOVERY_ORAS")
	if !filepath.IsAbs(oras) {
		t.Fatal("owned pinned ORAS binary is required")
	}
	for _, scheme := range []repository.UpstreamAuthScheme{repository.UpstreamAuthSchemeBasic, repository.UpstreamAuthSchemeBearer} {
		t.Run(string(scheme), func(t *testing.T) {
			imageLayer := ociDiscoveryOriginalImageLayer(t)
			artifact := newDiscoveryArtifactFixtureWithImageLayer(t, `{"SPDXID":"official-discovery-sbom"}`, imageLayer)
			var requests atomic.Int32
			var offline atomic.Bool
			const sourceSecret = "owned-discovery-source-secret"
			upstream := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if offline.Load() {
					w.WriteHeader(503)
					return
				}
				if r.Header.Get("Authorization") != privateProxyAuthorization(scheme, sourceSecret) {
					t.Error("discovery source credential missing or leaked client identity")
					w.WriteHeader(401)
					return
				}
				if r.URL.Path == "/v2/_catalog" {
					_, _ = io.WriteString(w, `{"repositories":["team/app","private/secret"]}`)
					return
				}
				artifact.serve(w, r)
			})
			upstream.client.HTTPClient.Timeout = 30 * time.Second
			f := newPostgresDiscoveryFixture(t)
			repo := f.proxy(t, "https://"+redirectOrigin)
			repo.UpstreamAuth = &repository.UpstreamAuth{Scheme: scheme, Secret: sealedUpstreamSecret(t, sourceSecret)}
			if scheme == repository.UpstreamAuthSchemeBasic {
				repo.UpstreamAuth.Username = "source-reader"
			}
			var err error
			repo, err = f.store.UpdateHostedRepository(t.Context(), repo, repo.Version)
			if err != nil {
				t.Fatal(err)
			}
			group, _, err := f.store.CreateHostedGroupIdempotently(t.Context(), repository.HostedGroup{ID: uuid.NewString(), Name: "discovery-group-" + uuid.NewString()[:8], Format: repository.FormatOCI, Members: []repository.GroupMember{{RepositoryID: repo.ID}}}, "fixture-admin", uuid.NewString(), "fixture")
			if err != nil {
				t.Fatal(err)
			}
			account, err := f.store.CreateServiceAccount(t.Context(), repository.ServiceAccount{ID: uuid.NewString(), Name: "discovery-client-" + uuid.NewString()[:8]})
			if err != nil {
				t.Fatal(err)
			}
			credential := "agc_discovery_" + uuid.NewString()
			expires := time.Now().UTC().Add(time.Hour)
			if _, err = f.store.CreateServiceAccountCredential(t.Context(), repository.APIKey{ID: uuid.NewString(), ServiceAccountID: account.ID, Name: "owned-client", SecretHash: authorization.HashAPIKey(credential), Roles: []string{}, ExpiresAt: &expires}); err != nil {
				t.Fatal(err)
			}
			if _, err = f.store.ReplaceRepositoryGrants(t.Context(), repo.ID, []repository.RepositoryGrant{{Principal: "service-account:" + account.ID, Scopes: []string{"repositories:read"}, ResourcePrefix: "team/"}}, "1"); err != nil {
				t.Fatal(err)
			}
			var cursor string
			for _, round := range []string{"cold", "restart-offline"} {
				if round != "cold" {
					f.close(t)
					f.open(t)
					offline.Store(true)
				}
				handler := f.handler(t, upstream.client)
				server := httptest.NewUnstartedServer(handler)
				listener, err := net.Listen("tcp4", "0.0.0.0:0")
				if err != nil {
					t.Fatal(err)
				}
				server.Listener = listener
				server.Start()
				before := requests.Load()
				catalogPath := "/v2/_catalog?n=1"
				if cursor != "" {
					catalogPath = cursor
				}
				catalog := discoveryRequest(t, handler, catalogPath, credential)
				if catalog.Code != 200 || strings.Contains(catalog.Body.String(), "private/secret") {
					server.Close()
					t.Fatalf("official principal catalog=%d %s", catalog.Code, catalog.Body.String())
				}
				if cursor == "" {
					cursor, err = ociDiscoveryNext(catalog.Header().Values("Link"))
					if err != nil || cursor == "" {
						server.Close()
						t.Fatal("filtered durable catalog page missing")
					}
				}
				for _, name := range []string{repo.Name, group.Name} {
					work := t.TempDir()
					environment := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + work, "TMPDIR=" + work, "LANG=C", "NO_PROXY=127.0.0.1,localhost,host.docker.internal"}
					registry := filepath.Join(work, "registry.json")
					auth, _ := json.Marshal(map[string]any{"auths": map[string]any{strings.TrimPrefix(server.URL, "http://"): map[string]string{"auth": base64.StdEncoding.EncodeToString([]byte("owned-client:" + credential))}}})
					if err := os.WriteFile(registry, auth, 0600); err != nil {
						server.Close()
						t.Fatal(err)
					}
					ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
					reference := strings.TrimPrefix(server.URL, "http://") + "/" + name + "/team/app"
					if round == "cold" && name == repo.Name {
						listed := requireDiscoveryClient(t, ctx, environment, oras, "repo", "ls", "--plain-http", "--registry-config", registry, strings.TrimPrefix(server.URL, "http://"))
						if !bytes.Contains(listed, []byte(repo.Name+"/team/app")) || !bytes.Contains(listed, []byte(group.Name+"/team/app")) || bytes.Contains(listed, []byte("private/secret")) {
							cancel()
							server.Close()
							t.Fatalf("official ORAS Catalog omitted callable names or leaked private names: %s", listed)
						}
					}
					found := requireDiscoveryClient(t, ctx, environment, oras, "discover", "--plain-http", "--registry-config", registry, "--distribution-spec", "v1.1-referrers-api", "--format", "json", reference+"@"+artifact.subject.Digest)
					if !json.Valid(found) || !bytes.Contains(found, []byte(artifact.refs[0].Digest)) {
						cancel()
						server.Close()
						t.Fatalf("official ORAS discovery did not find exact artifact: %s", found)
					}
					output := filepath.Join(work, "empty-pull")
					requireDiscoveryClient(t, ctx, environment, oras, "pull", "--plain-http", "--registry-config", registry, "--output", output, reference+"@"+artifact.refs[0].Digest)
					if body, err := os.ReadFile(filepath.Join(output, "sbom.spdx.json")); err != nil || !bytes.Equal(body, []byte(`{"SPDXID":"official-discovery-sbom"}`)) {
						cancel()
						server.Close()
						t.Fatalf("official ORAS original SBOM bytes: %v", err)
					}
					layout := filepath.Join(work, "empty-layout")
					for i, digest := range []string{artifact.subject.Digest, artifact.refs[0].Digest} {
						requireDiscoveryClient(t, ctx, environment, oras, "cp", "--no-tty", "--from-plain-http", "--from-registry-config", registry, "--to-oci-layout", reference+"@"+digest, layout+fmt.Sprint(":root", i))
					}
					for digest, object := range artifact.objects {
						body, err := os.ReadFile(filepath.Join(layout, "blobs", "sha256", strings.TrimPrefix(digest, "sha256:")))
						if err != nil || !bytes.Equal(body, object.body) || ocigraph.Digest(body) != digest {
							cancel()
							server.Close()
							t.Fatalf("official ORAS complete original closure %s differs: %v", digest, err)
						}
					}
					t.Logf("official ORAS %s %s: Catalog/discover/pull and complete original OCI-layout bytes verified", round, name)
					cancel()
					root := artifact.objects[artifact.subject.Digest]
					var manifest struct {
						Config struct {
							Digest string `json:"digest"`
						} `json:"config"`
					}
					if err := json.Unmarshal(root.body, &manifest); err != nil {
						server.Close()
						t.Fatal(err)
					}
					_, port, err := net.SplitHostPort(listener.Addr().String())
					if err != nil {
						server.Close()
						t.Fatal(err)
					}
					ociDiscoveryDockerPull(t, "host.docker.internal:"+port+"/"+name+"/team/app@"+artifact.subject.Digest, credential, artifact.objects[manifest.Config.Digest].body, imageLayer, true)
					t.Logf("official ORAS+Docker %s %s: original digest/bytes, empty consumer caches", round, name)
				}
				if round != "cold" && requests.Load() != before {
					server.Close()
					t.Fatalf("offline official consumption contacted source %d/%d", requests.Load(), before)
				}
				denied := discoveryRequest(t, handler, "/v2/"+group.Name+"/private/secret/referrers/"+artifact.subject.Digest, credential)
				if denied.Code != 403 {
					server.Close()
					t.Fatalf("cross-prefix private source=%d %s", denied.Code, denied.Body.String())
				}
				if round != "cold" {
					_, port, err := net.SplitHostPort(listener.Addr().String())
					if err != nil {
						server.Close()
						t.Fatal(err)
					}
					ociDiscoveryDockerPull(t, "host.docker.internal:"+port+"/"+group.Name+"/private/secret@"+artifact.subject.Digest, credential, nil, nil, false)
					if requests.Load() != before {
						server.Close()
						t.Fatal("restricted Docker pull contacted the private source")
					}
				}
				server.Close()
			}
		})
	}
}
