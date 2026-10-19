package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/scanning"
	"github.com/google/uuid"
)

type conanClientBackend struct {
	store        GatewayStore
	publications repository.ConanNativePublicationStore
	objects      ObjectStore
}

func TestConan2ClientUploadsAndInstallsNative(t *testing.T) {
	store := repository.NewMemoryStore()
	backend := conanClientBackend{store, store, objectstore.NewMemoryStore()}
	exerciseConanNativeClient(t, backend, func() conanClientBackend { return backend })
}

func exerciseConanNativeClient(t *testing.T, backend conanClientBackend, reopen func() conanClientBackend) {
	t.Helper()
	conan := os.Getenv("CONAN_BINARY")
	if conan == "" {
		t.Skip("set CONAN_BINARY to run the pinned Conan 2 client")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	store, objects := backend.store, backend.objects
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "conan-upload-" + uuid.NewString()[:8], Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		// This fixture proves automatic scan enqueueing without running a
		// worker. Do not leave its pending jobs for later global-claim tests.
		// A function defer runs before the reopened backend's t.Cleanup closes
		// its pools, and reads the current store after either service restart.
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		jobs, cleanupErr := store.ListLifecycleJobs(cleanupCtx, repo.ID, 100)
		if cleanupErr != nil {
			t.Errorf("list owned Conan fixture jobs for cleanup: %v", cleanupErr)
			return
		}
		for _, job := range jobs {
			if job.RepositoryID != repo.ID || job.Kind != repository.LifecycleJobScan {
				t.Errorf("unexpected job in owned Conan fixture: %+v", job)
				continue
			}
			switch job.State {
			case repository.LifecycleJobPending, repository.LifecycleJobRetrying:
				cancelled, cancelErr := store.CancelLifecycleJob(cleanupCtx, repo.ID, job.ID)
				if cancelErr != nil || cancelled.ID != job.ID || cancelled.RepositoryID != repo.ID || cancelled.State != repository.LifecycleJobCancelled {
					t.Errorf("cancel owned Conan fixture job %s: %+v %v", job.ID, cancelled, cancelErr)
				}
			case repository.LifecycleJobCompleted, repository.LifecycleJobFailed, repository.LifecycleJobCancelled:
			default:
				t.Errorf("owned Conan fixture job was unexpectedly claimed: %+v", job)
			}
		}
	}()
	group, _, err := store.CreateHostedGroupIdempotently(ctx, repository.HostedGroup{ID: uuid.NewString(), Name: "conan-read-" + uuid.NewString()[:8], Format: repository.FormatConan, Members: []repository.GroupMember{{RepositoryID: repo.ID}}}, "fixture", uuid.NewString(), "native-conan-group")
	if err != nil {
		t.Fatal(err)
	}
	account, err := store.CreateServiceAccount(ctx, repository.ServiceAccount{ID: uuid.NewString(), Name: "conan-native-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	const credential = "agc_312_isolated_fixture_credential"
	if _, err = store.CreateServiceAccountCredential(ctx, repository.APIKey{ID: uuid.NewString(), ServiceAccountID: account.ID, Name: "conan", SecretHash: authorization.HashAPIKey(credential), Roles: []string{}}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: "service-account:" + account.ID, Scopes: []string{"repositories:write"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	policy := repository.DefaultRepositorySecurityPolicy()
	policy.AutoScanOnPublish = true
	if _, err = store.ReplaceRepositorySecurityPolicy(ctx, repo.ID, policy, policy.Version); err != nil {
		t.Fatal(err)
	}
	build := func() http.Handler {
		auth := testAuthenticator()
		if secret := os.Getenv("TEST_CONAN_TOKEN_SECRET"); secret != "" {
			auth.ConanTokenSecret = secret
		}
		auth.APIKeys = store
		return newTestGateway(t, GatewayOptions{Dependencies: Dependencies{ArtifactScanner: scanning.ScannerFunc(func(context.Context, scanning.Artifact) (scanning.Report, error) { return scanning.Report{}, nil }), ArtifactScannerFormats: []repository.Format{repository.FormatConan}}, Store: store, ConanPublications: backend.publications, Adapter: TestAdapter{}, Authenticator: auth, Storage: ObjectStores{repository.FormatConan: objects}})
	}
	var handlerMu sync.RWMutex
	handler := build()
	var interrupted atomic.Bool
	var interruptSources atomic.Bool
	var inputsMu sync.Mutex
	wireInputs := map[string][]byte{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/conan_sources.tgz") && interruptSources.Load() {
			_, _ = io.CopyN(io.Discard, r.Body, max(1, r.ContentLength/2))
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("interrupt source upload: %v", err)
				return
			}
			_ = connection.Close()
			interrupted.Store(true)
			return
		}
		if r.Method == http.MethodPut {
			// Freeze the official producer's wire bytes before any Gateway
			// code or target store can transform or publish them.
			input, readErr := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
			_ = r.Body.Close()
			if readErr != nil || len(input) > 1<<20 {
				t.Errorf("producer input capture failed: %v", readErr)
				http.Error(w, "fixture input capture failed", 400)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(input))
			inputsMu.Lock()
			previous, exists := wireInputs[r.URL.EscapedPath()]
			if exists && !bytes.Equal(previous, input) {
				t.Error("producer changed an immutable upload input across retries")
			}
			wireInputs[r.URL.EscapedPath()] = bytes.Clone(input)
			inputsMu.Unlock()
		}

		handlerMu.RLock()
		current := handler
		handlerMu.RUnlock()
		current.ServeHTTP(w, r)
	}))
	defer server.Close()
	restart := func() {
		t.Helper()
		backend = reopen()
		store, objects = backend.store, backend.objects
		handlerMu.Lock()
		handler = build()
		handlerMu.Unlock()
	}
	producer, consumer, installer, work := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	recipe := `from conan import ConanFile
from conan.tools.files import copy
import os
class Fixture(ConanFile):
    name = "gateway-upload"
    version = "1.0"
    package_type = "header-library"
    exports = "LICENSE"
    exports_sources = "gateway.h"
    def package(self):
        copy(self, "gateway.h", self.source_folder, os.path.join(self.package_folder, "include"))
`
	const header = "#define GATEWAY_UPLOAD 312\n"
	for name, content := range map[string]string{"conanfile.py": recipe, "gateway.h": header, "LICENSE": "isolated Conan publication fixture\n"} {
		if err = os.WriteFile(filepath.Join(work, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	execute := func(home string, args ...string) (string, error) {
		command := exec.CommandContext(ctx, conan, args...)
		command.Dir = work
		command.Env = append(conanFixtureEnvironment(home), "CONAN_LOGIN_USERNAME_GATEWAY=fixture", "CONAN_PASSWORD_GATEWAY="+credential)
		output, err := command.CombinedOutput()
		return string(output), err
	}
	run := func(home string, args ...string) string {
		t.Helper()
		output, err := execute(home, args...)
		if err != nil {
			t.Fatalf("conan %v: %v\n%s", args, err, output)
		}
		return output
	}
	if got := run(producer, "--version"); !strings.Contains(got, "Conan version 2.21.0") {
		t.Fatalf("unexpected client: %s", got)
	}
	for _, home := range []string{producer, consumer, installer} {
		if err = os.MkdirAll(filepath.Join(home, "profiles"), 0700); err != nil {
			t.Fatal(err)
		}
		// This header-only package executes no compiler. A fixed, minimal profile
		// avoids host compiler discovery changing the fixture's package identity.
		if err = os.WriteFile(filepath.Join(home, "profiles", "default"), []byte("[settings]\nos=Linux\narch=x86_64\n"), 0600); err != nil {
			t.Fatal(err)
		}
		run(home, "remote", "remove", "conancenter")
		remote := repo.Name
		if home != producer {
			remote = group.Name
		}
		run(home, "remote", "add", "gateway", server.URL+"/conan/"+remote)
		if result := run(home, "remote", "auth", "gateway", "--force", "--format=json", "-cc", "core:non_interactive=True"); strings.Contains(result, `"error"`) {
			t.Fatalf("official authentication failed: %s", result)
		}
	}
	run(producer, "create", ".", "--user=fixture", "--channel=stable", "-nr")
	producerList := filepath.Join(work, "producer-list.json")
	run(producer, "list", "gateway-upload/1.0@fixture/stable#*:*#*", "--format=json", "--out-file="+producerList)
	sourceIdentity := readConanFixtureIdentity(t, producerList, "Local Cache")

	interruptSources.Store(true)
	uploadArgs := []string{"upload", "gateway-upload/1.0@fixture/stable#*:*#*", "-r=gateway", "--confirm", "-cc", "core.upload:retry=0"}
	if output, err := execute(producer, uploadArgs...); err == nil || !interrupted.Load() {
		t.Fatalf("interrupted upload succeeded: %s", output)
	}
	interruptSources.Store(false)
	const reference = "gateway-upload/1.0/fixture/stable"
	if revisions, err := store.ListConanRecipeRevisions(ctx, repo.ID, reference); err != nil || len(revisions) != 0 {
		t.Fatalf("partial revision became visible: %v %v", revisions, err)
	}
	restart()
	run(producer, uploadArgs...)
	restart()

	remoteList := filepath.Join(work, "remote-list.json")
	run(consumer, "list", "gateway-upload/1.0@fixture/stable#*:*#*", "-r=gateway", "--format=json", "--out-file="+remoteList)
	if got := readConanFixtureIdentity(t, remoteList, "gateway"); got != sourceIdentity {
		t.Fatalf("remote inventory changed producer identity: got=%+v want=%+v", got, sourceIdentity)
	}
	run(consumer, "download", "gateway-upload/1.0@fixture/stable", "-r=gateway")
	// Installer has a distinct empty artifact cache and only the target remote.
	run(installer, "install", "--requires=gateway-upload/1.0@fixture/stable", "-r=gateway", "--build=never")
	revisions, err := store.ListConanRecipeRevisions(ctx, repo.ID, reference)
	if err != nil || len(revisions) != 1 || revisions[0].Revision != sourceIdentity.recipe {
		t.Fatalf("recipe inventory=%v err=%v", revisions, err)
	}
	revision := revisions[0]
	ids, err := store.ListConanPackageIDs(ctx, repo.ID, reference, revision.Revision)
	if err != nil || len(ids) != 1 || ids[0] != sourceIdentity.packageID {
		t.Fatalf("package inventory=%v err=%v", ids, err)
	}
	packages, err := store.ListConanPackageRevisions(ctx, repo.ID, reference, revision.Revision, ids[0])
	if err != nil || len(packages) != 1 || packages[0].Revision != sourceIdentity.packageRevision {
		t.Fatalf("package revisions=%v err=%v", packages, err)
	}
	fullRef := "gateway-upload/1.0@fixture/stable#" + sourceIdentity.recipe + ":" + sourceIdentity.packageID + "#" + sourceIdentity.packageRevision
	for _, home := range []string{consumer, installer} {
		packagePath := strings.TrimSpace(run(home, "cache", "path", fullRef))
		got, err := os.ReadFile(filepath.Join(packagePath, "include", "gateway.h"))
		if err != nil || string(got) != header {
			t.Fatalf("cold installed bytes=%q err=%v", got, err)
		}
	}
	recipeAssets, err := store.ListConanRecipeAssets(ctx, repo.ID, reference, revision.Revision)
	if err != nil {
		t.Fatal(err)
	}
	packageAssets, err := store.ListConanPackageAssets(ctx, repo.ID, reference, revision.Revision, ids[0], packages[0].Revision)
	if err != nil {
		t.Fatal(err)
	}

	recipeRoot := "/conan/" + repo.Name + "/v2/conans/" + reference + "/revisions/" + sourceIdentity.recipe
	packageRoot := recipeRoot + "/packages/" + sourceIdentity.packageID + "/revisions/" + sourceIdentity.packageRevision
	expectedPaths := map[string]bool{}
	for _, name := range []string{"conanfile.py", "conan_export.tgz", "conan_sources.tgz", "conanmanifest.txt"} {
		expectedPaths[recipeRoot+"/files/"+name] = true
	}
	for _, name := range []string{"conaninfo.txt", "conan_package.tgz", "conanmanifest.txt"} {
		expectedPaths[packageRoot+"/files/"+name] = true
	}
	inputsMu.Lock()
	expectedInputs := make(map[string][]byte, len(wireInputs))
	for path, input := range wireInputs {
		expectedInputs[path] = bytes.Clone(input)
	}
	inputsMu.Unlock()
	if len(expectedInputs) != 7 || len(recipeAssets) != 4 || len(packageAssets) != 3 {
		t.Fatalf("exact producer/target asset membership failed: producer=%d recipe=%d package=%d", len(expectedInputs), len(recipeAssets), len(packageAssets))
	}
	for path := range expectedPaths {
		if _, ok := expectedInputs[path]; !ok {
			t.Fatalf("official producer did not supply required fixture input %s", path)
		}
	}
	seenPaths := map[string]bool{}
	for _, asset := range append(recipeAssets, packageAssets...) {
		root := recipeRoot
		if asset.PackageID != "" {
			if asset.PackageID != sourceIdentity.packageID || asset.PackageRevision != sourceIdentity.packageRevision {
				t.Fatalf("package asset remapped producer identity: %+v", asset)
			}
			root = packageRoot
		} else if asset.PackageRevision != "" {
			t.Fatalf("recipe asset contains package identity: %+v", asset)
		}
		path := root + "/files/" + asset.Path
		expected, ok := expectedInputs[path]
		if !ok || !expectedPaths[path] || seenPaths[path] || asset.RepositoryID != repo.ID || asset.Reference != reference || asset.RecipeRevision != sourceIdentity.recipe {
			t.Fatalf("missing, extra or remapped target asset %s: %+v", path, asset)
		}
		seenPaths[path] = true
		sourceSum := sha256.Sum256(expected)
		sourceDigest := "sha256:" + hex.EncodeToString(sourceSum[:])
		if asset.Digest != sourceDigest || asset.Size != int64(len(expected)) {
			t.Fatalf("metadata differs from producer bytes for %s: %+v", path, asset)
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.SetBasicAuth("fixture", credential)
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		stored, storedErr := objects.Get(ctx, asset.ObjectKey)
		if readErr != nil || storedErr != nil || response.StatusCode != http.StatusOK || !bytes.Equal(data, expected) || !bytes.Equal(stored, expected) || len(data) != len(expected) || sha256.Sum256(data) != sourceSum || sha256.Sum256(stored) != sourceSum {
			t.Fatalf("producer→HTTP/S3 bytes differ for %s: status=%d err=%v %v", path, response.StatusCode, readErr, storedErr)
		}
	}
	if len(seenPaths) != len(expectedPaths) {
		t.Fatal("target omitted a producer upload asset")
	}
	audits, err := store.ListAudits(ctx, repository.AuditQuery{Repository: repo.Name, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	publications := 0
	for _, audit := range audits {
		if audit.Operation != "publish" {
			continue
		}
		publications++
		session, err := store.GetConanPublishSession(ctx, audit.Evidence["publicationSessionId"])
		if err != nil || !session.Native || session.State != "committed" || audit.Actor != "service-account:"+account.ID || session.Publisher != audit.Actor {
			t.Fatalf("publication audit disconnected from durable session: %+v %v", audit, err)
		}
	}
	if publications != 2 {
		t.Fatalf("publication audits=%d, want one recipe and one package", publications)
	}
	jobs, err := store.ListLifecycleJobs(ctx, repo.ID, 100)
	if err != nil || len(jobs) != 2 {
		t.Fatalf("native publication bypassed automatic recipe/package scanning: jobs=%v err=%v", jobs, err)
	}
	t.Logf("official Conan 2.21.0 interrupted/restarted publication and cold install: %s; %d complete assets verified", fullRef, len(recipeAssets)+len(packageAssets))
}

func conanFixtureEnvironment(home string) []string {
	environment := make([]string, 0, len(os.Environ()))
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		lower := strings.ToLower(name)
		if strings.HasPrefix(strings.ToUpper(name), "CONAN_") || lower == "http_proxy" || lower == "https_proxy" || lower == "all_proxy" || lower == "no_proxy" {
			continue
		}
		environment = append(environment, value)
	}
	return append(environment, "CONAN_HOME="+home, "NO_PROXY=127.0.0.1,localhost,::1")
}

type conanFixtureIdentity struct{ recipe, packageID, packageRevision string }

func readConanFixtureIdentity(t *testing.T, filename, origin string) conanFixtureIdentity {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]map[string]struct {
		Revisions map[string]struct {
			Packages map[string]struct {
				Revisions map[string]json.RawMessage `json:"revisions"`
			} `json:"packages"`
		} `json:"revisions"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("official inventory JSON: %v: %s", err, data)
	}
	references := document[origin]
	ref, ok := references["gateway-upload/1.0@fixture/stable"]
	if !ok || len(references) != 1 || len(ref.Revisions) != 1 {
		t.Fatalf("official inventory is not one recipe revision: %s", data)
	}
	identity := conanFixtureIdentity{}
	for revision, recipe := range ref.Revisions {
		identity.recipe = revision
		if len(recipe.Packages) != 1 {
			t.Fatalf("official inventory is not one package: %s", data)
		}
		for id, pkg := range recipe.Packages {
			identity.packageID = id
			if len(pkg.Revisions) != 1 {
				t.Fatalf("official inventory is not one package revision: %s", data)
			}
			for prev := range pkg.Revisions {
				identity.packageRevision = prev
			}
		}
	}
	return identity
}
