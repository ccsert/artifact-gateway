//go:build integration

package helm_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	helmformat "github.com/ccsert/artifact-gateway/internal/format/helm"
	dist "github.com/ccsert/artifact-gateway/internal/helmdistribution"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
	"golang.org/x/crypto/openpgp"
	"golang.org/x/crypto/openpgp/packet"
)

func TestHelmFixedOfficialCLIReplicatedClosureTargetOnlyColdRestart(t *testing.T) {
	binary, dsn := os.Getenv("HELM_TEST_BINARY"), os.Getenv("TEST_DATABASE_URL")
	if binary == "" || dsn == "" || os.Getenv("TEST_RUSTFS_ENDPOINT") == "" {
		t.Skip("fixed Helm and owned PostgreSQL/RustFS required")
	}
	work := t.TempDir()
	commandOutputs := []map[string]any{}
	sourceRemoved, independent, reopened := false, false, false
	coldPhases := 0
	var requestMu sync.Mutex
	requests := []string{}
	t.Cleanup(func() {
		if evidence := os.Getenv("HELM_OWNED_EVIDENCE_DIR"); evidence != "" {
			report := map[string]any{"client": "Helm", "version": "v3.19.0", "fullGateway": true, "postgresRustFS": true, "sourceObjectsDeleted": sourceRemoved, "targetIndependentClosure": independent, "gatewayAndStoreReopened": reopened, "coldConsumerPhases": coldPhases, "passed": !t.Failed(), "targetOnly": true, "commandOutputs": commandOutputs, "gatewayRequests": requests}
			body, err := json.MarshalIndent(report, "", "  ")
			if err == nil {
				_ = os.WriteFile(filepath.Join(evidence, "helm-replication-client.json"), append(body, '\n'), 0600)
			}
		}
	})
	envFor := func(phase string) []string {
		root := filepath.Join(work, phase)
		for _, part := range []string{"config", "cache", "data", "plugins", "downloads"} {
			if err := os.MkdirAll(filepath.Join(root, part), 0700); err != nil {
				t.Fatal(err)
			}
		}
		kube := filepath.Join(root, "kubeconfig")
		writeOwned(t, kube, []byte("apiVersion: v1\nkind: Config\nclusters: []\ncontexts: []\nusers: []\n"))
		env := []string{}
		for _, v := range os.Environ() {
			if !strings.HasPrefix(v, "HELM_") && !strings.HasPrefix(v, "KUBECONFIG=") {
				env = append(env, v)
			}
		}
		return append(env, "HELM_CACHE_HOME="+filepath.Join(root, "cache"), "HELM_CONFIG_HOME="+filepath.Join(root, "config"), "HELM_DATA_HOME="+filepath.Join(root, "data"), "HELM_PLUGINS="+filepath.Join(root, "plugins"), "HELM_REPOSITORY_CONFIG="+filepath.Join(root, "config/repositories.yaml"), "HELM_REPOSITORY_CACHE="+filepath.Join(root, "cache/repository"), "HELM_REGISTRY_CONFIG="+filepath.Join(root, "config/registry.json"), "KUBECONFIG="+kube)
	}
	run := func(phase string, env []string, args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env = env
		cmd.Dir = work
		out, err := cmd.CombinedOutput()
		safe := append([]string(nil), args...)
		for i := 1; i < len(safe); i++ {
			if safe[i-1] == "--password" {
				safe[i] = "[owned credential]"
			}
		}
		commandOutputs = append(commandOutputs, map[string]any{"phase": phase, "args": safe, "output": string(out), "success": err == nil})
		if err != nil {
			t.Fatalf("Helm %s failed: %v\n%s", strings.Join(safe, " "), err, out)
		}
		return out
	}
	packEnv := envFor("packager")
	if version := strings.TrimSpace(string(run("packager", packEnv, "version", "--template", "{{.Version}}"))); version != "v3.19.0" {
		t.Fatalf("fixed Helm=%s", version)
	}
	chartDir := filepath.Join(work, "copied")
	if err := os.MkdirAll(filepath.Join(chartDir, "templates"), 0700); err != nil {
		t.Fatal(err)
	}
	writeOwned(t, filepath.Join(chartDir, "Chart.yaml"), []byte("apiVersion: v2\nname: copied\nversion: 1.2.3\ntype: application\ndescription: complete independent replicated chart\n"))
	writeOwned(t, filepath.Join(chartDir, "templates/owned.yaml"), []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Release.Name }}\ndata:\n  replica: independent\n"))
	signer, err := openpgp.NewEntity("Owned Helm Replication Fixture", "", "copy@example.invalid", &packet.Config{RSABits: 2048})
	if err != nil {
		t.Fatal(err)
	}
	privateFile := filepath.Join(work, "private.gpg")
	publicFile := filepath.Join(work, "public.gpg")
	for _, entry := range []struct {
		path    string
		private bool
	}{{privateFile, true}, {publicFile, false}} {
		f, e := os.OpenFile(entry.path, os.O_CREATE|os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal(e)
		}
		if entry.private {
			e = signer.SerializePrivate(f, nil)
		} else {
			e = signer.Serialize(f)
		}
		ce := f.Close()
		if e != nil || ce != nil {
			t.Fatalf("key serialization: %v %v", e, ce)
		}
	}
	run("packager", packEnv, "package", chartDir, "--sign", "--key", "Owned Helm Replication Fixture", "--keyring", privateFile, "--destination", work)
	chartPath := filepath.Join(work, "copied-1.2.3.tgz")
	chartBytes, err := os.ReadFile(chartPath)
	if err != nil {
		t.Fatal(err)
	}
	provBytes, err := os.ReadFile(chartPath + ".prov")
	if err != nil {
		t.Fatal(err)
	}
	chartSHA, chartSize := fileDigest(t, chartPath)
	provSHA, provSize := fileDigest(t, chartPath+".prov")
	store, err := repository.NewPostgresStore(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	objects, err := objectstore.NewRustFSStore(os.Getenv("TEST_RUSTFS_ENDPOINT"), os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY"), "helm-copy-client-"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(t.Context()); err != nil {
		t.Fatal(err)
	}
	token := "helm-copy-" + uuid.NewString()
	authority := func() authorization.Authenticator {
		return authorization.Authenticator{AdminToken: token, AdminActor: "helm-copy-client", APIKeys: store, ServiceAccounts: store, Users: store}
	}
	newServer := func() *httptest.Server {
		t.Helper()
		handler, e := app.NewGatewayHandler(app.GatewayOptions{Store: store, NPMMetadata: store, NPMRestore: store, Storage: app.SharedObjectStores(objects), Adapter: ownedAdapter{}, Authenticator: authority(), HelmPreviewEnabled: true, HelmStore: store, HelmGovernance: store, HelmSources: store, HelmDistribution: store, HelmObjects: objects})
		if e != nil {
			t.Fatal(e)
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestMu.Lock()
			requests = append(requests, r.Method+" "+r.URL.EscapedPath())
			requestMu.Unlock()
			handler.ServeHTTP(w, r)
		}))
		t.Cleanup(server.Close)
		return server
	}
	server := newServer()
	send := func(method, path, content string, body []byte, want int) []byte {
		t.Helper()
		r, e := http.NewRequestWithContext(t.Context(), method, server.URL+path, bytes.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", content)
		r.Header.Set("Idempotency-Key", uuid.NewString())
		client := server.Client()
		client.Timeout = 15 * time.Second
		response, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = response.Body.Close() }()
		data, e := io.ReadAll(response.Body)
		if e != nil || response.StatusCode != want {
			t.Fatalf("%s %s=%d want=%d %s %v", method, path, response.StatusCode, want, data, e)
		}
		return data
	}
	var source, target repository.HostedRepository
	for _, r := range []*repository.HostedRepository{&source, &target} {
		name := "copy-cli-" + uuid.NewString()
		body := send("POST", "/api/v2/previews/helm/repositories", "application/json", []byte(`{"name":"`+name+`"}`), 201)
		if json.Unmarshal(body, r) != nil || r.ID == "" {
			t.Fatal("repository receipt invalid")
		}
	}
	var body bytes.Buffer
	multipartWriter := multipart.NewWriter(&body)
	for _, asset := range []struct {
		field, name string
		data        []byte
	}{{"chart", "copied-1.2.3.tgz", chartBytes}, {"provenance", "copied-1.2.3.tgz.prov", provBytes}} {
		part, e := multipartWriter.CreateFormFile(asset.field, asset.name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = part.Write(asset.data); e != nil {
			t.Fatal(e)
		}
	}
	if err = multipartWriter.Close(); err != nil {
		t.Fatal(err)
	}
	send("PUT", "/api/v2/previews/helm/repositories/"+source.ID+"/charts/copied/1.2.3", multipartWriter.FormDataContentType(), body.Bytes(), 201)
	pinned := send("GET", "/api/v2/previews/helm/repositories/"+source.ID+"/charts/copied/1.2.3", "", nil, 200)
	var pin struct {
		Digest, ProvenanceDigest, GovernanceVersion string
		Size, ProvenanceSize                        int64
	}
	if json.Unmarshal(pinned, &pin) != nil || pin.Digest != chartSHA || pin.Size != int64(chartSize) || pin.ProvenanceDigest != provSHA || pin.ProvenanceSize != int64(provSize) {
		t.Fatalf("source pin=%s", pinned)
	}
	plan := dist.Plan{SourceRepositoryID: source.ID, TargetRepositoryID: target.ID, Items: []dist.Identity{{Name: "copied", Version: "1.2.3", Digest: pin.Digest, Size: pin.Size, ProvenanceDigest: pin.ProvenanceDigest, ProvenanceSize: pin.ProvenanceSize, SourceGovernanceVersion: pin.GovernanceVersion}}}
	encoded, _ := json.Marshal(plan)
	var job dist.Job
	if json.Unmarshal(send("POST", "/api/v2/previews/helm/replication-jobs", "application/json", encoded, 202), &job) != nil {
		t.Fatal("job receipt invalid")
	}
	auth := authority()
	module, err := helmformat.New(helmformat.Options{Distribution: store, Store: store, Governance: store, Repositories: store, Objects: objects, Authenticator: auth, Authorizer: authorization.RepositoryAuthorizer{Grants: store, Legacy: auth}, Audit: store, Anonymous: store, Capacities: store})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := module.ReplicationWorker("owned-client-copy")
	if err != nil {
		t.Fatal(err)
	}
	if ran, e := worker.RunOnce(t.Context()); !ran || e != nil {
		t.Fatalf("copy=%t %v", ran, e)
	}
	if json.Unmarshal(send("GET", "/api/v2/previews/helm/replication-jobs/"+job.ID, "", nil, 200), &job) != nil || job.State != "succeeded" {
		t.Fatalf("job=%+v", job)
	}
	src, err := store.GetHelmPublication(t.Context(), source.ID, "copied", "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	dst, err := store.GetHelmPublication(t.Context(), target.ID, "copied", "1.2.3")
	if err != nil || src.ObjectKey == dst.ObjectKey || src.ProvenanceKey == dst.ProvenanceKey || src.SessionID == dst.SessionID || !strings.Contains(dst.ObjectKey, target.ID) || !strings.Contains(dst.ProvenanceKey, target.ID) {
		t.Fatalf("not independent: %+v %v", dst, err)
	}
	independent = true
	coldConsume := func(phase string) {
		env := envFor(phase)
		targetURL := server.URL + "/helm/" + target.Name
		run(phase, env, "repo", "add", "target", targetURL, "--username", "owned", "--password", token)
		run(phase, env, "repo", "update")
		destination := filepath.Join(work, phase, "downloads")
		run(phase, env, "pull", "target/copied", "--version", "1.2.3", "--verify", "--keyring", publicFile, "--destination", destination)
		archive := filepath.Join(destination, "copied-1.2.3.tgz")
		got, size := fileDigest(t, archive)
		proof, psize := fileDigest(t, archive+".prov")
		if got != chartSHA || size != chartSize || proof != provSHA || psize != provSize {
			t.Fatal("consumer closure differs")
		}
		rendered := run(phase, env, "template", "copied-release", archive, "--namespace", "owned")
		if !bytes.Contains(rendered, []byte("replica: independent")) || !bytes.Contains(rendered, []byte("name: copied-release")) {
			t.Fatalf("template missing fixture: %s", rendered)
		}
	}
	coldConsume("before-restart")
	coldPhases++
	for _, key := range []string{src.ObjectKey, src.ProvenanceKey} {
		if err = objects.Delete(t.Context(), key); err != nil {
			t.Fatal(err)
		}
	}
	sourceRemoved = true
	server.Close()
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = repository.NewPostgresStore(dsn)
	if err != nil {
		t.Fatal(err)
	}
	server = newServer()
	reopened = true
	coldConsume("source-removed-restart")
	coldPhases++
	for _, path := range requests {
		if strings.Contains(path, "/helm/"+source.Name+"/") {
			t.Fatalf("consumer contacted source: %s", path)
		}
	}
	if c, e := store.GetRepositoryCapacity(t.Context(), target.ID); e != nil || c.UsedBytes != int64(chartSize+provSize) {
		t.Fatalf("target capacity=%+v %v", c, e)
	}
	if _, e := objects.Get(t.Context(), src.ObjectKey); !errors.Is(e, objectstore.ErrNotFound) {
		t.Fatalf("source chart absence=%v", e)
	}
	if _, e := objects.Get(t.Context(), src.ProvenanceKey); !errors.Is(e, objectstore.ErrNotFound) {
		t.Fatalf("source provenance absence=%v", e)
	}
}
