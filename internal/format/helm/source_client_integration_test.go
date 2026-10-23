//go:build integration

package helm_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/app"
	"github.com/ccsert/artifact-gateway/internal/authorization"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	helmprotocol "github.com/ccsert/artifact-gateway/internal/protocol/helm"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/secrets"
	"github.com/ccsert/artifact-gateway/internal/upstreamauth"
	"github.com/google/uuid"
	"go.yaml.in/yaml/v3"
	"golang.org/x/crypto/openpgp"
	"golang.org/x/crypto/openpgp/packet"
)

type helmOwnedUpstream struct{ client *http.Client }

func (c helmOwnedUpstream) FetchHelm(ctx context.Context, repo repository.HostedRepository, target string, headers http.Header) (*http.Response, error) {
	r, e := http.NewRequestWithContext(ctx, "GET", target, nil)
	if e != nil {
		return nil, e
	}
	r.Header = headers.Clone()
	if r.Header == nil {
		r.Header = http.Header{}
	}
	if e = upstreamauth.ApplyOrigin(r, repo.Endpoint, repo.UpstreamAuth); e != nil {
		return nil, e
	}
	return c.client.Do(r)
}
func TestHelmFixedOfficialCLIProxyGroupFrozenSourceOfflinePostgresRustFSRestart(t *testing.T) {
	binary, dsn := os.Getenv("HELM_TEST_BINARY"), os.Getenv("TEST_DATABASE_URL")
	if binary == "" || dsn == "" || os.Getenv("TEST_RUSTFS_ENDPOINT") == "" {
		t.Skip("owned fixed Helm, PostgreSQL and RustFS required")
	}
	t.Setenv(secrets.KeyEnv, strings.Repeat("3", 64))
	work := t.TempDir()
	commands := []map[string]any{}
	envFor := func(label string) []string {
		root := filepath.Join(work, label)
		for _, dir := range []string{"cache", "config", "data", "plugins", "downloads"} {
			if e := os.MkdirAll(filepath.Join(root, dir), 0700); e != nil {
				t.Fatal(e)
			}
		}
		writeOwned(t, filepath.Join(root, "kubeconfig"), []byte("apiVersion: v1\nkind: Config\nclusters: []\ncontexts: []\nusers: []\n"))
		env := []string{}
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "HELM_") && !strings.HasPrefix(value, "KUBECONFIG=") {
				env = append(env, value)
			}
		}
		return append(env, "HELM_CACHE_HOME="+filepath.Join(root, "cache"), "HELM_CONFIG_HOME="+filepath.Join(root, "config"), "HELM_DATA_HOME="+filepath.Join(root, "data"), "HELM_PLUGINS="+filepath.Join(root, "plugins"), "HELM_REPOSITORY_CONFIG="+filepath.Join(root, "config", "repositories.yaml"), "HELM_REPOSITORY_CACHE="+filepath.Join(root, "cache", "repository"), "HELM_REGISTRY_CONFIG="+filepath.Join(root, "config", "registry.json"), "KUBECONFIG="+filepath.Join(root, "kubeconfig"))
	}
	run := func(env []string, want bool, args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env = env
		cmd.Dir = work
		out, e := cmd.CombinedOutput()
		safe := append([]string{}, args...)
		for i := 1; i < len(safe); i++ {
			if safe[i-1] == "--password" {
				safe[i] = "[owned credential]"
			}
		}
		commands = append(commands, map[string]any{"command": strings.Join(safe, " "), "succeeded": e == nil, "output": string(out)})
		if (e == nil) != want {
			t.Fatalf("Helm %s: %v\n%s", strings.Join(safe, " "), e, out)
		}
		return out
	}
	packageEnv := envFor("packager")
	if string(run(packageEnv, true, "version", "--template", "{{.Version}}")) != "v3.19.0" {
		t.Fatal("fixed Helm client required")
	}
	chartDir := filepath.Join(work, "owned")
	if e := os.MkdirAll(filepath.Join(chartDir, "templates"), 0700); e != nil {
		t.Fatal(e)
	}
	writeOwned(t, filepath.Join(chartDir, "Chart.yaml"), []byte("apiVersion: v2\nname: owned\nversion: 1.2.3\ndescription: original proxy chart\n"))
	writeOwned(t, filepath.Join(chartDir, "templates", "config.yaml"), []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Release.Name }}\ndata:\n  owned: proxy\n"))
	key, e := openpgp.NewEntity("Owned Helm Proxy Fixture", "", "proxy@example.invalid", &packet.Config{RSABits: 2048})
	if e != nil {
		t.Fatal(e)
	}
	private, e := os.OpenFile(filepath.Join(work, "private.gpg"), os.O_CREATE|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	if e = key.SerializePrivate(private, nil); e != nil {
		t.Fatal(e)
	}
	_ = private.Close()
	public, e := os.OpenFile(filepath.Join(work, "public.gpg"), os.O_CREATE|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	if e = key.Serialize(public); e != nil {
		t.Fatal(e)
	}
	_ = public.Close()
	run(packageEnv, true, "package", chartDir, "--sign", "--key", "Owned Helm Proxy Fixture", "--keyring", filepath.Join(work, "private.gpg"), "--destination", work)
	chart, e := os.ReadFile(filepath.Join(work, "owned-1.2.3.tgz"))
	if e != nil {
		t.Fatal(e)
	}
	prov, e := os.ReadFile(filepath.Join(work, "owned-1.2.3.tgz.prov"))
	if e != nil {
		t.Fatal(e)
	}
	chartSHA, chartSize := fileDigest(t, filepath.Join(work, "owned-1.2.3.tgz"))
	provSHA, provSize := fileDigest(t, filepath.Join(work, "owned-1.2.3.tgz.prov"))
	inspection, e := helmprotocol.Inspect(bytes.NewReader(chart))
	if e != nil {
		t.Fatal(e)
	}
	index, e := yaml.Marshal(struct {
		APIVersion string                               `yaml:"apiVersion"`
		Entries    map[string][]helmprotocol.IndexEntry `yaml:"entries"`
	}{"v1", map[string][]helmprotocol.IndexEntry{"owned": {{Metadata: inspection.Metadata, Digest: strings.TrimPrefix(chartSHA, "sha256:"), URLs: []string{"assets/owned-1.2.3.tgz"}}}}})
	if e != nil {
		t.Fatal(e)
	}
	var feedMu sync.Mutex
	upstreamRequests := []string{}
	upstreamSecret := "upstream-owned-" + uuid.NewString()
	mutated := false
	feed := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		feedMu.Lock()
		defer feedMu.Unlock()
		upstreamRequests = append(upstreamRequests, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer "+upstreamSecret {
			w.WriteHeader(401)
			return
		}
		if mutated {
			w.WriteHeader(403)
			return
		}
		switch r.URL.Path {
		case "/index.yaml":
			w.Header().Set("ETag", `"original-index"`)
			_, _ = w.Write(index)
		case "/assets/owned-1.2.3.tgz":
			_, _ = w.Write(chart)
		case "/assets/owned-1.2.3.tgz.prov":
			_, _ = w.Write(prov)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(feed.Close)
	store, e := repository.NewPostgresStore(dsn)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = store.Close() })
	objects, e := objectstore.NewRustFSStore(os.Getenv("TEST_RUSTFS_ENDPOINT"), os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY"), "helm-proxy-"+uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if e = objects.EnsureBucket(t.Context()); e != nil {
		t.Fatal(e)
	}
	token := "local-owned-" + uuid.NewString()
	var mu sync.Mutex
	requests := []string{}
	compose := func() *httptest.Server {
		auth := authorization.Authenticator{AdminToken: token, AdminActor: "helm-proxy-fixture"}
		handler, e := app.NewGatewayHandler(app.GatewayOptions{Store: store, NPMMetadata: store, NPMRestore: store, Storage: app.SharedObjectStores(objects), Adapter: ownedAdapter{}, Authenticator: auth, HelmPreviewEnabled: true, HelmStore: store, HelmGovernance: store, HelmSources: store, HelmUpstream: helmOwnedUpstream{feed.Client()}, HelmObjects: objects})
		if e != nil {
			t.Fatal(e)
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			requests = append(requests, r.Method+" "+r.URL.Path)
			mu.Unlock()
			handler.ServeHTTP(w, r)
		}))
		t.Cleanup(server.Close)
		return server
	}
	gateway := compose()
	send := func(method, path string, body []byte, content string, want int) []byte {
		t.Helper()
		r, e := http.NewRequestWithContext(t.Context(), method, gateway.URL+path, bytes.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Authorization", "Bearer "+token)
		if content != "" {
			r.Header.Set("Content-Type", content)
		}
		response, e := gateway.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = response.Body.Close() }()
		out, e := io.ReadAll(response.Body)
		if e != nil {
			t.Fatal(e)
		}
		if response.StatusCode != want {
			t.Fatalf("%s %s=%d %s", method, path, response.StatusCode, out)
		}
		return out
	}
	name := "helm-proxy-" + uuid.NewString()
	body, _ := json.Marshal(map[string]any{"name": name, "type": "proxy", "endpoint": feed.URL + "/index.yaml", "upstreamAuth": map[string]any{"scheme": "bearer", "secret": upstreamSecret}})
	var proxy struct{ ID, Version string }
	reply := send("POST", "/api/v2/previews/helm/repositories", body, "application/json", 201)
	if json.Unmarshal(reply, &proxy) != nil || proxy.ID == "" || bytes.Contains(reply, []byte(upstreamSecret)) {
		t.Fatal("proxy identity or secret redaction")
	}
	hostedName := "helm-hosted-" + uuid.NewString()
	var hosted struct{ ID string }
	if json.Unmarshal(send("POST", "/api/v2/previews/helm/repositories", []byte(`{"name":"`+hostedName+`"}`), "application/json", 201), &hosted) != nil {
		t.Fatal("hosted create")
	}
	// A second valid chart at the same version has different original bytes.
	writeOwned(t, filepath.Join(chartDir, "Chart.yaml"), []byte("apiVersion: v2\nname: owned\nversion: 1.2.3\ndescription: conflicting hosted chart\n"))
	run(packageEnv, true, "package", chartDir, "--destination", work)
	other, e := os.ReadFile(filepath.Join(work, "owned-1.2.3.tgz"))
	if e != nil {
		t.Fatal(e)
	}
	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	part, e := writer.CreateFormFile("chart", "owned-1.2.3.tgz")
	if e != nil {
		t.Fatal(e)
	}
	_, _ = part.Write(other)
	_ = writer.Close()
	send("PUT", "/api/v2/previews/helm/repositories/"+hosted.ID+"/charts/owned/1.2.3", form.Bytes(), writer.FormDataContentType(), 201)
	groupName := "helm-group-" + uuid.NewString()
	body, _ = json.Marshal(map[string]any{"name": groupName, "members": []string{proxy.ID, hosted.ID}})
	var group struct{ ID, Version string }
	if json.Unmarshal(send("POST", "/api/v2/previews/helm/groups", body, "application/json", 201), &group) != nil {
		t.Fatal("group create")
	}
	rawIndex := send("GET", "/helm/"+groupName+"/index.yaml", nil, "", 200)
	var parsed struct {
		Entries map[string][]struct {
			URLs []string `yaml:"urls"`
		} `yaml:"entries"`
	}
	if yaml.Unmarshal(rawIndex, &parsed) != nil || len(parsed.Entries["owned"]) != 1 || len(parsed.Entries["owned"][0].URLs) != 1 {
		t.Fatal("group index")
	}
	frozenPath := parsed.Entries["owned"][0].URLs[0]
	if !strings.HasPrefix(frozenPath, "/helm/"+groupName+"/snapshots/") || bytes.Contains(rawIndex, []byte(feed.URL)) {
		t.Fatal("non Gateway source URL")
	}
	consume := func(label string) {
		env := envFor(label)
		run(env, true, "repo", "add", "owned", gateway.URL+"/helm/"+groupName, "--username", "owned", "--password", token)
		run(env, true, "repo", "update")
		configured := run(env, true, "repo", "list", "--output", "json")
		var config []struct{ Name, URL string }
		if json.Unmarshal(configured, &config) != nil || len(config) != 1 || config[0].URL != gateway.URL+"/helm/"+groupName {
			t.Fatal("not target-only")
		}
		destination := filepath.Join(work, label, "downloads")
		run(env, true, "pull", "owned/owned", "--version", "1.2.3", "--verify", "--keyring", filepath.Join(work, "public.gpg"), "--destination", destination)
		if sum, size := fileDigest(t, filepath.Join(destination, "owned-1.2.3.tgz")); sum != chartSHA || size != chartSize {
			t.Fatal("source chart digest/size differs")
		}
		if sum, size := fileDigest(t, filepath.Join(destination, "owned-1.2.3.tgz.prov")); sum != provSHA || size != provSize {
			t.Fatal("source provenance digest/size differs")
		}
		run(env, true, "template", "owned-render", "owned/owned", "--version", "1.2.3", "--verify", "--keyring", filepath.Join(work, "public.gpg"))
		kube := helmProxyKube(t)
		kubeConfig := "apiVersion: v1\nkind: Config\nclusters:\n- name: owned\n  cluster:\n    server: " + kube.URL + "\ncontexts:\n- name: owned\n  context:\n    cluster: owned\n    namespace: owned\ncurrent-context: owned\nusers: []\n"
		writeOwned(t, filepath.Join(work, label, "kubeconfig"), []byte(kubeConfig))
		rendered := run(env, true, "install", "owned-proxy-release", "owned/owned", "--version", "1.2.3", "--dry-run=client", "--disable-openapi-validation", "--verify", "--keyring", filepath.Join(work, "public.gpg"), "--namespace", "owned")
		if !bytes.Contains(rendered, []byte("kind: ConfigMap")) || !bytes.Contains(rendered, []byte("owned-proxy-release")) {
			t.Fatal("mixed Group dry-run failed to render original chart")
		}

	}
	consume("cold-before-restart")
	// Member reordering cannot switch the already indexed coordinate.
	body, _ = json.Marshal(map[string]any{"name": groupName, "version": group.Version, "members": []string{hosted.ID, proxy.ID}})
	send("PUT", "/api/v2/previews/helm/groups/"+group.ID, body, "application/json", 200)
	feedMu.Lock()
	mutated = true
	feedMu.Unlock()
	if got := send("GET", frozenPath, nil, "", 200); !bytes.Equal(got, chart) {
		t.Fatal("frozen asset followed changed upstream")
	}
	feed.Close()
	doc, e := store.GetHelmSourceDocument(t.Context(), proxy.ID, proxy.Version)
	if e != nil {
		t.Fatal(e)
	}
	doc.FetchedAt = time.Now().Add(-time.Minute)
	doc.ExpiresAt = time.Now().Add(-time.Second)
	if e = store.PutHelmSourceDocument(t.Context(), doc); e != nil {
		t.Fatal(e)
	}
	gateway.Close()
	if e = store.Close(); e != nil {
		t.Fatal(e)
	}
	store, e = repository.NewPostgresStore(dsn)
	if e != nil {
		t.Fatal(e)
	}
	gateway = compose()
	consume("cold-offline-after-restart")
	governancePath := "/api/v2/previews/helm/repositories/" + proxy.ID + "/charts/owned/1.2.3/governance"
	govern := func(action, version string) {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"expectedDigest": chartSHA, "expectedGovernanceVersion": version, "action": action, "reason": "Owned mixed Group official Helm governance fixture"})
		send("POST", governancePath, body, "application/json", 200)
	}
	govern("delete", "1")
	for _, path := range []string{frozenPath, frozenPath + ".prov"} {
		send("GET", path, nil, "", 404)
	}
	if index := send("GET", "/helm/"+groupName+"/index.yaml", nil, "", 200); bytes.Contains(index, []byte("owned:")) {
		t.Fatal("mixed Group tombstone fell through to reordered Hosted member")
	}
	hiddenEnv := envFor("cold-governed-group-tombstone")
	run(hiddenEnv, true, "repo", "add", "owned", gateway.URL+"/helm/"+groupName, "--username", "owned", "--password", token)
	run(hiddenEnv, false, "pull", "owned/owned", "--version", "1.2.3", "--destination", filepath.Join(work, "denied-group-tombstone"))
	// Governance, independent read policy and immutable authority survive a
	// second real PostgreSQL/handler restart with the original source offline.
	gateway.Close()
	if e = store.Close(); e != nil {
		t.Fatal(e)
	}
	store, e = repository.NewPostgresStore(dsn)
	if e != nil {
		t.Fatal(e)
	}
	gateway = compose()
	var state struct {
		GovernanceVersion string
		Tombstoned        bool
	}
	if json.Unmarshal(send("GET", governancePath, nil, "", 200), &state) != nil || state.GovernanceVersion != "2" || !state.Tombstoned {
		t.Fatal("tombstone lost across restart")
	}
	send("GET", frozenPath, nil, "", 404)
	govern("restore", "2")
	govern("quarantine", "3")
	send("PUT", "/api/v2/previews/helm/repositories/"+proxy.ID+"/quarantine-read-policy", []byte(`{"expectedVersion":"1","enabled":true}`), "application/json", 200)
	send("GET", frozenPath, nil, "", 404)
	if index := send("GET", "/helm/"+groupName+"/index.yaml", nil, "", 200); bytes.Contains(index, []byte("owned:")) {
		t.Fatal("quarantined mixed Group fell through")
	}
	govern("release", "4")
	consume("cold-restored-governance-after-restart")
	// A caller with an empty target-only cache and no credential cannot discover.
	run(envFor("unauthorized"), false, "repo", "add", "owned", gateway.URL+"/helm/"+groupName)
	metadata, e := store.GetHelmPublication(t.Context(), proxy.ID, "owned", "1.2.3")
	if e != nil || metadata.Digest != chartSHA || metadata.Size != int64(chartSize) || metadata.ProvenanceDigest != provSHA || metadata.ProvenanceSize != int64(provSize) {
		t.Fatalf("cache metadata=%+v %v", metadata, e)
	}
	feedMu.Lock()
	sourceRequests := append([]string{}, upstreamRequests...)
	feedMu.Unlock()
	mu.Lock()
	gatewayRequests := append([]string{}, requests...)
	mu.Unlock()
	if len(sourceRequests) != 3 {
		t.Fatalf("duplicate or unfrozen source fetches=%v", sourceRequests)
	}
	if dir := os.Getenv("HELM_OWNED_EVIDENCE_DIR"); dir != "" {
		evidence := map[string]any{"client": "Helm v3.19.0", "scope": "Gateway/PostgreSQL/RustFS private Proxy and mixed Group", "chartSha256": chartSHA, "chartSize": chartSize, "provenanceSha256": provSHA, "provenanceSize": provSize, "commands": commands, "gatewayRequests": gatewayRequests, "upstreamRequests": sourceRequests, "fixedSource": proxy.ID, "sourceReorderPreserved": true, "governanceTombstoneQuarantineAndRestore": true, "governanceRestartReadBack": true, "coldGovernedGroupNoFallback": true, "offlineAfterRestart": true, "emptyClientCache": true, "targetOnlyConfig": true, "clusterInstallVerified": false, "rendering": "helm install client dry-run with owned GET-only discovery"}
		data, _ := json.MarshalIndent(evidence, "", "  ")
		if e = os.WriteFile(filepath.Join(dir, "helm-proxy-group-client.json"), append(data, '\n'), 0600); e != nil {
			t.Fatal(e)
		}
	}
}

func helmProxyKube(t *testing.T) *httptest.Server {
	t.Helper()
	kubeRequests := []string{}
	var kubeMu sync.Mutex
	kubeFixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kubeMu.Lock()
		kubeRequests = append(kubeRequests, r.Method+" "+r.URL.Path)
		kubeMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method != "GET" {
			t.Errorf("dry-run attempted Kubernetes write %s %s", r.Method, r.URL.Path)
			w.WriteHeader(405)
			return
		}
		var response any
		switch r.URL.Path {
		case "/version":
			response = map[string]any{"major": "1", "minor": "34", "gitVersion": "v1.34.0", "gitCommit": "owned-fixture", "platform": "linux/amd64"}
		case "/api":
			response = map[string]any{"apiVersion": "v1", "kind": "APIVersions", "versions": []string{"v1"}, "serverAddressByClientCIDRs": []any{}}
		case "/apis":
			response = map[string]any{"apiVersion": "v1", "kind": "APIGroupList", "groups": []any{}}
		case "/api/v1":
			response = map[string]any{"apiVersion": "v1", "kind": "APIResourceList", "groupVersion": "v1", "resources": []map[string]any{{"name": "configmaps", "singularName": "", "namespaced": true, "kind": "ConfigMap", "verbs": []string{"get", "list", "create"}}, {"name": "secrets", "singularName": "", "namespaced": true, "kind": "Secret", "verbs": []string{"get", "list", "create"}}}}
		case "/api/v1/namespaces/owned/secrets":
			response = map[string]any{"apiVersion": "v1", "kind": "SecretList", "metadata": map[string]any{"resourceVersion": "1"}, "items": []any{}}
		default:
			w.WriteHeader(404)
			response = map[string]any{"apiVersion": "v1", "kind": "Status", "status": "Failure", "reason": "NotFound", "code": 404}
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(kubeFixture.Close)

	return kubeFixture
}
