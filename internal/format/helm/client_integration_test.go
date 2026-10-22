//go:build integration

package helm_test

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/testsupport"
	"github.com/google/uuid"
	"golang.org/x/crypto/openpgp"
	"golang.org/x/crypto/openpgp/packet"
)

func TestMain(m *testing.M) {
	if err := testsupport.ValidateIsolatedDatabaseURL(os.Getenv("TEST_DATABASE_URL")); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(m.Run())
}

type ownedAdapter struct{}

func (ownedAdapter) Available(context.Context, repository.Member, string) bool { return true }
func fileDigest(t *testing.T, path string) (string, int) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), len(data)
}
func writeOwned(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestHelmFixedOfficialCLIThroughGatewayPostgresRustFSAndRestart(t *testing.T) {
	binary := os.Getenv("HELM_TEST_BINARY")
	dsn := os.Getenv("TEST_DATABASE_URL")
	if binary == "" || dsn == "" || os.Getenv("TEST_RUSTFS_ENDPOINT") == "" {
		t.Skip("fixed HELM_TEST_BINARY and owned PostgreSQL/RustFS are required")
	}
	work := t.TempDir()
	commands := []string{}
	commandOutputs := []map[string]any{}
	t.Cleanup(func() {
		if dir := os.Getenv("HELM_OWNED_EVIDENCE_DIR"); dir != "" {
			encoded, e := json.MarshalIndent(commandOutputs, "", "  ")
			if e == nil {
				_ = os.WriteFile(filepath.Join(dir, "helm-cli-commands.json"), append(encoded, '\n'), 0600)
			}
		}
	})
	requests := []string{}
	var requestsMu sync.Mutex
	clientEnv := func(label string) []string {
		root := filepath.Join(work, label)
		for _, dir := range []string{"cache", "config", "data", "plugins", "downloads"} {
			if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
				t.Fatal(err)
			}
		}
		kube := filepath.Join(root, "kubeconfig")
		writeOwned(t, kube, []byte("apiVersion: v1\nkind: Config\nclusters: []\ncontexts: []\nusers: []\n"))
		env := []string{}
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "HELM_") && !strings.HasPrefix(value, "KUBECONFIG=") {
				env = append(env, value)
			}
		}
		return append(env, "HELM_CACHE_HOME="+filepath.Join(root, "cache"), "HELM_CONFIG_HOME="+filepath.Join(root, "config"), "HELM_DATA_HOME="+filepath.Join(root, "data"), "HELM_PLUGINS="+filepath.Join(root, "plugins"), "HELM_REPOSITORY_CONFIG="+filepath.Join(root, "config", "repositories.yaml"), "HELM_REPOSITORY_CACHE="+filepath.Join(root, "cache", "repository"), "HELM_REGISTRY_CONFIG="+filepath.Join(root, "config", "registry.json"), "KUBECONFIG="+kube)
	}
	run := func(env []string, expectedSuccess bool, args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env = env
		cmd.Dir = work
		output, err := cmd.CombinedOutput()
		safe := append([]string(nil), args...)
		for i := 1; i < len(safe); i++ {
			if safe[i-1] == "--password" {
				safe[i] = "[owned credential]"
			}
		}
		commands = append(commands, strings.Join(safe, " "))
		commandOutputs = append(commandOutputs, map[string]any{"command": strings.Join(safe, " "), "output": string(output), "succeeded": err == nil})
		if (err == nil) != expectedSuccess {
			t.Fatalf("Helm %s success=%t: %v\n%s", strings.Join(safe, " "), expectedSuccess, err, output)
		}
		return output
	}
	packageEnv := clientEnv("packager")
	if version := strings.TrimSpace(string(run(packageEnv, true, "version", "--template", "{{.Version}}"))); version != "v3.19.0" {
		t.Fatalf("fixed client required, received %q", version)
	}
	chartDir := filepath.Join(work, "owned")
	if err := os.MkdirAll(filepath.Join(chartDir, "templates"), 0700); err != nil {
		t.Fatal(err)
	}
	writeOwned(t, filepath.Join(chartDir, "Chart.yaml"), []byte("apiVersion: v2\nname: owned\nversion: 1.2.3\ntype: application\ndescription: Owned Helm cold-consumer fixture\n"))
	largeValues := make([]byte, 1200<<10)
	if _, err := crand.Read(largeValues); err != nil {
		t.Fatal(err)
	}
	writeOwned(t, filepath.Join(chartDir, "values.yaml"), []byte("owned: fixture\nunused: "+hex.EncodeToString(largeValues)+"\n"))
	writeOwned(t, filepath.Join(chartDir, "templates", "config.yaml"), []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Release.Name }}\ndata:\n  owned: {{ .Values.owned | quote }}\n"))
	signer, err := openpgp.NewEntity("Owned Helm CLI Fixture", "", "helm@example.invalid", &packet.Config{RSABits: 2048})
	if err != nil {
		t.Fatal(err)
	}
	private, err := os.OpenFile(filepath.Join(work, "signing-keyring.gpg"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err = signer.SerializePrivate(private, nil); err != nil {
		t.Fatal(err)
	}
	if err = private.Close(); err != nil {
		t.Fatal(err)
	}
	public, err := os.OpenFile(filepath.Join(work, "public-keyring.gpg"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err = signer.Serialize(public); err != nil {
		t.Fatal(err)
	}
	if err = public.Close(); err != nil {
		t.Fatal(err)
	}
	packageOutput := run(packageEnv, true, "package", chartDir, "--sign", "--key", "Owned Helm CLI Fixture", "--keyring", filepath.Join(work, "signing-keyring.gpg"), "--destination", work)
	if !bytes.Contains(packageOutput, []byte("Successfully packaged")) {
		t.Fatal("official helm package did not complete")
	}
	chartFile := filepath.Join(work, "owned-1.2.3.tgz")
	provFile := chartFile + ".prov"
	chartSHA, chartSize := fileDigest(t, chartFile)
	if chartSize <= 1<<20 {
		t.Fatal("owned client chart must exercise management upload above JSON body limit")
	}
	provSHA, provSize := fileDigest(t, provFile)
	chartBytes, err := os.ReadFile(chartFile)
	if err != nil {
		t.Fatal(err)
	}
	provBytes, err := os.ReadFile(provFile)
	if err != nil {
		t.Fatal(err)
	}
	store, err := repository.NewPostgresStore(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	objects, err := objectstore.NewRustFSStore(os.Getenv("TEST_RUSTFS_ENDPOINT"), os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY"), "helm-owned-"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(t.Context()); err != nil {
		t.Fatal(err)
	}
	token := "owned-helm-" + uuid.NewString()
	newServer := func() *httptest.Server {
		t.Helper()
		auth := authorization.Authenticator{AdminToken: token, AdminActor: "helm-fixture", APIKeys: store, ServiceAccounts: store}
		handler, e := app.NewGatewayHandler(app.GatewayOptions{Store: store, NPMMetadata: store, NPMRestore: store, Storage: app.SharedObjectStores(objects), Adapter: ownedAdapter{}, Authenticator: auth, HelmPreviewEnabled: true, HelmStore: store, HelmObjects: objects})
		if e != nil {
			t.Fatal(e)
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestsMu.Lock()
			requests = append(requests, r.Method+" "+r.URL.EscapedPath())
			requestsMu.Unlock()
			handler.ServeHTTP(w, r)
		}))
		t.Cleanup(server.Close)
		return server
	}
	server := newServer()
	send := func(method, path, credential, contentType string, body []byte, want int, versions ...string) []byte {
		t.Helper()
		r, e := http.NewRequestWithContext(t.Context(), method, server.URL+path, bytes.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		if credential != "" {
			r.Header.Set("Authorization", "Bearer "+credential)
		}
		if contentType != "" {
			r.Header.Set("Content-Type", contentType)
		}
		r.Header.Set("Idempotency-Key", "owned-helm-publication")
		if len(versions) > 0 {
			r.Header.Set("If-Match", versions[0])
		}
		response, e := server.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = response.Body.Close() }()
		data, e := io.ReadAll(response.Body)
		if e != nil {
			t.Fatal(e)
		}
		if response.StatusCode != want {
			t.Fatalf("%s %s=%d want %d: %s", method, path, response.StatusCode, want, data)
		}
		return data
	}
	name := "helm-cli-" + uuid.NewString()
	created := send("POST", "/api/v2/previews/helm/repositories", token, "application/json", []byte(`{"name":"`+name+`"}`), 201)
	var repo struct{ ID string }
	if json.Unmarshal(created, &repo) != nil || repo.ID == "" {
		t.Fatal("repository create response missing identity")
	}
	account, err := store.CreateServiceAccount(t.Context(), repository.ServiceAccount{ID: uuid.NewString(), Name: "helm-rotation-" + uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	createKey := func(label string) (string, repository.APIKey) {
		secret := "agc_helm_" + uuid.NewString()
		expires := time.Now().Add(time.Hour)
		key, e := store.CreateServiceAccountCredential(t.Context(), repository.APIKey{ID: uuid.NewString(), ServiceAccountID: account.ID, Name: label, SecretHash: authorization.HashAPIKey(secret), Roles: []string{}, ExpiresAt: &expires})
		if e != nil {
			t.Fatal(e)
		}
		return secret, key
	}
	blue, blueKey := createKey("blue")
	green, _ := createKey("green")
	grantPath := "/api/v2/repositories/" + repo.ID + "/grants"
	grantPrincipal := "service-account:" + account.ID
	grant := repository.RepositoryGrant{Principal: grantPrincipal, Scopes: []string{"repositories:read", "repositories:write"}, ResourcePrefix: "owned"}
	grantJSON, err := json.Marshal([]repository.RepositoryGrant{grant})
	if err != nil {
		t.Fatal(err)
	}
	publishPath := "/api/v2/previews/helm/repositories/" + repo.ID + "/charts/owned/1.2.3"
	send("PUT", publishPath, blue, "application/octet-stream", nil, 403)
	send("PUT", grantPath, token, "application/json", grantJSON, 200, "1")
	formData := func(chartName string, chart, provenance []byte) ([]byte, string) {
		t.Helper()
		var payload bytes.Buffer
		writer := multipart.NewWriter(&payload)
		for _, asset := range []struct {
			field, filename string
			data            []byte
		}{{"chart", chartName + "-1.2.3.tgz", chart}, {"provenance", chartName + "-1.2.3.tgz.prov", provenance}} {
			if asset.data == nil {
				continue
			}
			part, e := writer.CreateFormFile(asset.field, asset.filename)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = part.Write(asset.data); e != nil {
				t.Fatal(e)
			}
		}
		if e := writer.Close(); e != nil {
			t.Fatal(e)
		}
		return payload.Bytes(), writer.FormDataContentType()
	}
	payload, contentType := formData("owned", chartBytes, provBytes)
	for range 2 {
		send("PUT", publishPath, blue, contentType, payload, 201)
	}
	outsideDir := filepath.Join(work, "outside")
	if err = os.Mkdir(outsideDir, 0700); err != nil {
		t.Fatal(err)
	}
	writeOwned(t, filepath.Join(outsideDir, "Chart.yaml"), []byte("apiVersion: v2\nname: outside\nversion: 1.2.3\n"))
	run(packageEnv, true, "package", outsideDir, "--destination", work)
	outside, err := os.ReadFile(filepath.Join(work, "outside-1.2.3.tgz"))
	if err != nil {
		t.Fatal(err)
	}
	outsideBody, outsideType := formData("outside", outside, nil)
	outsidePublish := "/api/v2/previews/helm/repositories/" + repo.ID + "/charts/outside/1.2.3"
	send("PUT", outsidePublish, blue, outsideType, outsideBody, 403)
	send("PUT", outsidePublish, token, outsideType, outsideBody, 201)
	indexPath := "/helm/" + name + "/index.yaml"
	indexBefore := send("GET", indexPath, token, "", nil, 200)
	if !bytes.Contains(indexBefore, []byte(strings.TrimPrefix(chartSHA, "sha256:"))) || bytes.Contains(indexBefore, []byte("http://")) || bytes.Contains(indexBefore, []byte("https://")) {
		t.Fatal("index digest or target-only relative URL mismatch")
	}
	filtered := send("GET", indexPath, blue, "", nil, 200)
	if !bytes.Contains(filtered, []byte("owned:")) || bytes.Contains(filtered, []byte("outside:")) {
		t.Fatalf("HTTP-created chart grant did not filter index: %s", filtered)
	}
	send("GET", "/helm/"+name+"/charts/outside/1.2.3/outside-1.2.3.tgz", blue, "", nil, 403)
	if body := send("GET", "/api/v2/previews/helm/repositories/"+repo.ID+"/charts", blue, "", nil, 200); bytes.Contains(body, []byte(`"outside"`)) || !bytes.Contains(body, []byte(`"owned"`)) {
		t.Fatalf("preview browse ignored HTTP-created chart grant: %s", body)
	}
	send("GET", indexPath, "", "", nil, 401)
	expected := map[string]any{"chartSha256": chartSHA, "chartSize": chartSize, "provenanceSha256": provSHA, "provenanceSize": provSize}
	var uploaded struct {
		Digest           string
		Size             int
		ProvenanceDigest string
		ProvenanceSize   int
	}
	if json.Unmarshal(send("GET", publishPath, token, "", nil, 200), &uploaded) != nil || uploaded.Digest != chartSHA || uploaded.Size != chartSize || uploaded.ProvenanceDigest != provSHA || uploaded.ProvenanceSize != provSize {
		t.Fatalf("metadata mismatch: %+v", uploaded)
	}
	for _, path := range []string{"/api/v2/formats", "/api/v2/repositories", "/api/v2/repository-capacities", "/api/v2/public/repositories"} {
		if body := send("GET", path, token, "", nil, 200); bytes.Contains(body, []byte(repo.ID)) || bytes.Contains(body, []byte(`"helm"`)) {
			t.Fatalf("preview leaked into %s", path)
		}
	}
	// A complete HTTP handler and PostgreSQL connection are replaced. Only the
	// durable metadata database and RustFS bucket survive this restart boundary.
	server.Close()
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = repository.NewPostgresStore(dsn)
	if err != nil {
		t.Fatal(err)
	}
	server = newServer()
	if indexAfter := send("GET", indexPath, token, "", nil, 200); !bytes.Equal(indexBefore, indexAfter) {
		t.Fatal("index changed across restart without publication")
	}
	consumer := clientEnv("cold-consumer")
	run(consumer, true, "repo", "add", "owned", server.URL+"/helm/"+name, "--username", "owned", "--password", blue)
	run(consumer, true, "repo", "update")
	configured := run(consumer, true, "repo", "list", "--output", "json")
	var config []struct{ Name, URL string }
	if json.Unmarshal(configured, &config) != nil || len(config) != 1 || config[0].Name != "owned" || config[0].URL != server.URL+"/helm/"+name {
		t.Fatalf("client is not target-only: %s", configured)
	}
	destination := filepath.Join(work, "cold-consumer", "downloads")
	run(consumer, true, "pull", "owned/owned", "--version", "1.2.3", "--verify", "--keyring", filepath.Join(work, "public-keyring.gpg"), "--destination", destination)
	if sum, size := fileDigest(t, filepath.Join(destination, "owned-1.2.3.tgz")); sum != chartSHA || size != chartSize {
		t.Fatal("cold pull bytes differ")
	}
	if sum, size := fileDigest(t, filepath.Join(destination, "owned-1.2.3.tgz.prov")); sum != provSHA || size != provSize {
		t.Fatal("provenance bytes differ")
	}
	run(consumer, true, "verify", filepath.Join(destination, "owned-1.2.3.tgz"), "--keyring", filepath.Join(work, "public-keyring.gpg"))
	// Helm 3.19 install checks cluster discovery even for client dry-run.
	// An owned read-only API fixture supplies discovery; any write fails the
	// test. This proves cold package consumption/rendering, not a cluster install.
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
	kubeConfig := "apiVersion: v1\nkind: Config\nclusters:\n- name: owned\n  cluster:\n    server: " + kubeFixture.URL + "\ncontexts:\n- name: owned\n  context:\n    cluster: owned\n    namespace: owned\ncurrent-context: owned\nusers: []\n"
	writeOwned(t, filepath.Join(work, "cold-consumer", "kubeconfig"), []byte(kubeConfig))
	installed := run(consumer, true, "install", "owned-release", "owned/owned", "--version", "1.2.3", "--dry-run=client", "--disable-openapi-validation", "--verify", "--keyring", filepath.Join(work, "public-keyring.gpg"), "--namespace", "owned")
	if !bytes.Contains(installed, []byte("kind: ConfigMap")) || !bytes.Contains(installed, []byte("owned-release")) {
		t.Fatalf("cold dry-run install did not render original chart: %s", installed)
	}
	run(clientEnv("unauthorized-cold"), false, "repo", "add", "owned", server.URL+"/helm/"+name)
	// Rotate and revoke real service credentials. No local credential is reused
	// as a signing key or an upstream secret; Hosted performs no upstream fetch.
	greenConsumer := clientEnv("rotated-cold-consumer")
	run(greenConsumer, true, "repo", "add", "owned", server.URL+"/helm/"+name, "--username", "owned", "--password", green)
	send("GET", indexPath, blue, "", nil, 200)
	send("GET", indexPath, green, "", nil, 200)
	if _, err = store.RevokeServiceAccountCredential(t.Context(), account.ID, blueKey.ID); err != nil {
		t.Fatal(err)
	}
	send("GET", indexPath, blue, "", nil, 401)
	send("GET", indexPath, green, "", nil, 200)
	run(consumer, false, "pull", "owned/owned", "--version", "1.2.3", "--destination", filepath.Join(work, "revoked-credential-download"))
	send("DELETE", grantPath+"?principal="+url.QueryEscape(grantPrincipal)+"&resourcePrefix=owned", token, "", nil, 204)
	send("GET", indexPath, green, "", nil, 403)
	run(greenConsumer, false, "repo", "add", "owned", server.URL+"/helm/"+name, "--username", "owned", "--password", green, "--force-update")
	run(greenConsumer, false, "pull", "owned/owned", "--version", "1.2.3", "--destination", filepath.Join(work, "revoked-grant-download"))
	audit, err := store.ListAudits(t.Context(), repository.AuditQuery{Repository: name, Format: "helm", Operation: "helm.publish", Limit: 100})
	if err != nil || len(audit) != 2 {
		t.Fatalf("idempotent publication audit=%d %v", len(audit), err)
	}
	nonAdminPublished := false
	for _, record := range audit {
		if record.Actor == grantPrincipal && record.Resource == "owned@1.2.3" {
			nonAdminPublished = true
		}
		encoded, _ := json.Marshal(record)
		if bytes.Contains(encoded, []byte(token)) || bytes.Contains(encoded, []byte(blue)) || bytes.Contains(encoded, []byte("PRIVATE KEY")) {
			t.Fatal("secret leaked into audit")
		}
	}
	if !nonAdminPublished {
		t.Fatal("non-administrator publication was not attributed to the HTTP-granted service account")
	}
	if dir := os.Getenv("HELM_OWNED_EVIDENCE_DIR"); dir != "" {
		requestsMu.Lock()
		paths := append([]string(nil), requests...)
		requestsMu.Unlock()
		report := map[string]any{"client": "Helm", "version": "v3.19.0", "gatewayComposition": "full", "postgresRustFS": true, "targetOnly": true, "restartReadBack": true, "signatureVerifiedWithOwnedPublicKey": true, "dryRunInstallAgainstOwnedReadOnlyDiscoveryFixture": true, "kubernetesClusterInstallVerified": false, "kubernetesDiscoveryRequests": kubeRequests, "commandOutputs": commandOutputs, "credentialRotationAndRevocation": true, "grantCreatedAndRevokedThroughHTTP": true, "nonAdministratorPublishAndColdConsumer": true, "chartPrefixFilteredIndexBrowseAndDownloads": true, "revokedCredentialAndGrantRejectedByOfficialCLI": true, "publicationAuditCount": len(audit), "expected": expected, "commands": commands, "gatewayRequestPaths": paths}
		encoded, e := json.MarshalIndent(report, "", "  ")
		if e != nil {
			t.Fatal(e)
		}
		if err = os.WriteFile(filepath.Join(dir, "helm-cli.json"), append(encoded, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
