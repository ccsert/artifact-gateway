//go:build integration && mavenclient

package pgstore_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
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
	"github.com/google/uuid"
)

const migrationMavenVersion = "3.9.16"
const migrationCurlVersion = "8.14.1"
const migrationClientPluginPath = "gateway/fixture/maven-client-fixture/1.0.0/maven-client-fixture-1.0.0"

// This mandatory client gate is separate from Go HTTP checks. Only the small
// repository-owned resolver plugin is seeded locally; every migrated parent,
// POM, transitive dependency and JAR starts absent, with a Gateway-only mirror.
func TestPostgresMigrationTargetOnlyMavenClient(t *testing.T) {
	requireMigrationClientServices(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root := t.TempDir()
	environment := migrationClientEnvironment(t, root)
	mvn := os.Getenv("MIGRATION_MAVEN_BIN")
	if mvn == "" {
		var err error
		mvn, err = exec.LookPath("mvn")
		if err != nil {
			t.Fatal("the migration client gate requires the pinned official Maven and a JDK")
		}
	}
	version := migrationClientCommand(t, ctx, mvn, root, environment, "-B", "-ntp", "-Dstyle.color=never", "-v")
	if !strings.HasPrefix(string(version), "Apache Maven "+migrationMavenVersion+" (") {
		t.Fatalf("the migration client gate requires Maven %s:\n%s", migrationMavenVersion, version)
	}
	t.Logf("fixed official Maven client:\n%s", version)
	plugin := migrationResolverPlugin(t, ctx, root, environment, version)
	jar := migrationClientJAR(t)
	files := map[string][]byte{
		"com/gateway/migrated/parent/1.0/parent-1.0.pom": []byte(`<project><modelVersion>4.0.0</modelVersion><groupId>com.gateway.migrated</groupId><artifactId>parent</artifactId><version>1.0</version><packaging>pom</packaging></project>`),
		"org/example/leaf/1.0/leaf-1.0.pom":              []byte(`<project><modelVersion>4.0.0</modelVersion><groupId>org.example</groupId><artifactId>leaf</artifactId><version>1.0</version></project>`),
		"org/example/leaf/1.0/leaf-1.0.jar":              jar,
		"org/example/widget/1.0/widget-1.0.pom":          []byte(`<project><modelVersion>4.0.0</modelVersion><parent><groupId>com.gateway.migrated</groupId><artifactId>parent</artifactId><version>1.0</version><relativePath/></parent><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.0</version><dependencies><dependency><groupId>org.example</groupId><artifactId>leaf</artifactId><version>1.0</version></dependency></dependencies></project>`),
		"org/example/widget/1.0/widget-1.0.jar":          jar,
	}
	f := newFixture(t, "maven2", files)
	batch := f.start()
	f.run(batch)
	requireState(t, *batch, "completed")
	if batch.VisibleIdentities != 3 || batch.Counts.Committed != 3 {
		t.Fatalf("expected exactly three migrated release identities: %+v", batch)
	}
	f.closeSource()
	server, token, paths := migrationClientGateway(t, f)
	settings, globalSettings, pom := filepath.Join(root, "settings.xml"), filepath.Join(root, "global-settings.xml"), filepath.Join(root, "pom.xml")
	base := server.URL + "/repository/maven/" + f.repo.Name + "/"
	migrationClientFile(t, globalSettings, []byte(`<settings/>`))
	migrationClientFile(t, settings, []byte(fmt.Sprintf(`<settings><mirrors><mirror><id>owned-migration-target</id><mirrorOf>*</mirrorOf><url>%s</url></mirror></mirrors><servers><server><id>owned-migration-target</id><username>migration-reader</username><password>%s</password></server></servers></settings>`, base, token)))
	migrationClientFile(t, pom, []byte(`<project><modelVersion>4.0.0</modelVersion><groupId>gateway.fixture</groupId><artifactId>migration-resolver</artifactId><version>1</version><dependencies><dependency><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.0</version></dependency></dependencies></project>`))
	local := filepath.Join(root, "cold-repository")
	migrationClientFile(t, filepath.Join(local, migrationClientPluginPath+".jar"), plugin)
	migrationClientFile(t, filepath.Join(local, migrationClientPluginPath+".pom"), []byte(`<project><modelVersion>4.0.0</modelVersion><groupId>gateway.fixture</groupId><artifactId>maven-client-fixture</artifactId><version>1.0.0</version><packaging>maven-plugin</packaging></project>`))
	for p := range files {
		if _, err := os.Stat(filepath.Join(local, filepath.FromSlash(p))); !os.IsNotExist(err) {
			t.Fatalf("migrated coordinate must be absent before Maven starts: %s (%v)", p, err)
		}
	}
	out := migrationClientCommand(t, ctx, mvn, root, environment,
		"-B", "-ntp", "-Dstyle.color=never", "-s", settings, "-gs", globalSettings, "-f", pom,
		"-Dmaven.repo.local="+local, "gateway.fixture:maven-client-fixture:1.0.0:resolve-fixture")
	if !bytes.Contains(out, []byte("BUILD SUCCESS")) || !bytes.Contains(out, []byte("Gateway fixture dependency resolution completed.")) {
		t.Fatalf("official Maven did not resolve migrated dependencies:\n%s", out)
	}
	for p, want := range files {
		got, err := os.ReadFile(filepath.Join(local, filepath.FromSlash(p)))
		if err != nil {
			t.Fatal(err)
		}
		migrationClientBytes(t, p, got, want)
		if !paths.requested("/repository/maven/" + f.repo.Name + "/" + p) {
			t.Fatalf("Maven bypassed the native Hosted target for %s", p)
		}
	}
	t.Logf("source TLS listener stopped; Gateway-only cold Maven resolved parent, transitive POMs and JARs; manifest sha256=%s", batch.ManifestSHA256)
}

func TestPostgresMigrationTargetOnlyRawClient(t *testing.T) {
	requireMigrationClientServices(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	root := t.TempDir()
	environment := migrationClientEnvironment(t, root)
	curl := os.Getenv("MIGRATION_CURL_BIN")
	if curl == "" {
		t.Fatal("the migration Raw client gate requires the pinned official curl; use make nexus-migration-client-e2e")
	}
	version := migrationClientCommand(t, ctx, curl, root, environment, "--disable", "--version")
	if !strings.HasPrefix(string(version), "curl "+migrationCurlVersion+" ") {
		t.Fatalf("the migration Raw client gate requires curl %s:\n%s", migrationCurlVersion, version)
	}
	t.Logf("fixed official Raw client version: %s", strings.SplitN(string(version), "\n", 2)[0])
	files := map[string][]byte{"release.bin": []byte("migrated-raw-bytes\x00\xff"), "nested/release+build.txt": []byte("complete-body\n")}
	f := newFixture(t, "raw", files)
	batch := f.start()
	f.run(batch)
	requireState(t, *batch, "completed")
	f.closeSource()
	server, token, paths := migrationClientGateway(t, f)
	for p, want := range files {
		output := filepath.Join(root, filepath.FromSlash(p))
		if _, err := os.Stat(output); !os.IsNotExist(err) {
			t.Fatal("Raw output must not exist before curl")
		}
		if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
			t.Fatal(err)
		}
		migrationClientCommand(t, ctx, curl, root, environment,
			"--disable", "--noproxy", "*", "--proto", "=http", "--fail", "--silent", "--show-error", "--max-time", "30",
			"--user", "migration-reader:"+token, "--output", output, server.URL+"/raw/"+f.repo.Name+"/"+p)
		got, err := os.ReadFile(output)
		if err != nil {
			t.Fatal(err)
		}
		migrationClientBytes(t, p, got, want)
		if !paths.requested("/raw/" + f.repo.Name + "/" + p) {
			t.Fatalf("curl bypassed the native Raw target for %s", p)
		}
	}
	t.Logf("source TLS listener stopped; target-only cold curl returned complete Raw bytes; manifest sha256=%s", batch.ManifestSHA256)
}

func requireMigrationClientServices(t *testing.T) {
	t.Helper()
	for _, key := range []string{"TEST_DATABASE_URL", "TEST_RUSTFS_ENDPOINT", "TEST_RUSTFS_ACCESS_KEY", "TEST_RUSTFS_SECRET_KEY"} {
		if os.Getenv(key) == "" {
			t.Fatalf("mandatory migration client gate requires isolated %s; use make nexus-migration-client-e2e", key)
		}
	}
}

type migrationClientRequests struct {
	mu    sync.Mutex
	paths map[string]int
}

func (r *migrationClientRequests) requested(path string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.paths[path] > 0
}

func migrationClientGateway(t *testing.T, f *fixture) (*httptest.Server, string, *migrationClientRequests) {
	t.Helper()
	token := uuid.NewString()
	handler, err := app.NewGatewayHandler(app.GatewayOptions{Store: f.native, Storage: app.SharedObjectStores(f.objects), Adapter: app.TestAdapter{}, Authenticator: app.Authenticator{ResolverToken: token, RepositoryReaders: map[string][]string{"migration-reader": {f.repo.Name}}}})
	if err != nil {
		t.Fatal(err)
	}
	requests := &migrationClientRequests{paths: map[string]int{}}
	prefix := "/raw/" + f.repo.Name + "/"
	if f.remote.format == "maven2" {
		prefix = "/repository/maven/" + f.repo.Name + "/"
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) || !strings.HasPrefix(r.URL.Path, prefix) {
			t.Errorf("target-only client attempted an unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "fixture route denied", 403)
			return
		}
		requests.mu.Lock()
		requests.paths[r.URL.Path]++
		requests.mu.Unlock()
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server, token, requests
}

func migrationClientBytes(t *testing.T, path string, got, want []byte) {
	t.Helper()
	if len(got) != len(want) || sha256.Sum256(got) != sha256.Sum256(want) || !bytes.Equal(got, want) {
		t.Fatalf("target-only client bytes differ: %s size=%d expected=%d", path, len(got), len(want))
	}
	t.Logf("inventory path=%s size=%d sha256=%x", path, len(got), sha256.Sum256(got))
}

func migrationClientEnvironment(t *testing.T, root string) []string {
	t.Helper()
	home, temporary := filepath.Join(root, "client-home"), filepath.Join(root, "tmp")
	for _, directory := range []string{home, temporary} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	// No user settings, shell rc, Maven arguments, proxy or credential inheritance.
	return []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "LANG=C", "TMPDIR=" + temporary, "MAVEN_SKIP_RC=true", "MAVEN_OPTS=-Duser.home=" + home}
}

func migrationClientFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
}

func migrationClientCommand(t *testing.T, ctx context.Context, binary, directory string, environment []string, args ...string) []byte {
	t.Helper()
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir, command.Env = directory, environment
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("owned migration client %s failed: %v\n%s", filepath.Base(binary), err, out)
	}
	return out
}

func migrationResolverPlugin(t *testing.T, ctx context.Context, root string, environment []string, version []byte) []byte {
	t.Helper()
	var mavenHome, javaHome string
	for _, line := range strings.Split(string(version), "\n") {
		if strings.HasPrefix(line, "Maven home: ") {
			mavenHome = strings.TrimSpace(strings.TrimPrefix(line, "Maven home: "))
		}
		if strings.HasPrefix(line, "Java version: ") {
			_, javaHome, _ = strings.Cut(line, "runtime: ")
			javaHome = strings.TrimSpace(javaHome)
		}
	}
	apis, err := filepath.Glob(filepath.Join(mavenHome, "lib", "maven-plugin-api-*.jar"))
	if mavenHome == "" || javaHome == "" || err != nil || len(apis) != 1 {
		t.Fatalf("pinned Maven must expose its JDK and plugin API: %v", err)
	}
	source, err := filepath.Abs(filepath.Join("..", "..", "..", "scripts", "maven-client-fixture", "ResolveMojo.java"))
	if err != nil {
		t.Fatal(err)
	}
	classes := filepath.Join(root, "plugin-classes")
	if err := os.MkdirAll(classes, 0700); err != nil {
		t.Fatal(err)
	}
	migrationClientCommand(t, ctx, filepath.Join(javaHome, "bin", "javac"), root, environment, "--release", "17", "-classpath", apis[0], "-d", classes, source)
	var archive bytes.Buffer
	jar := zip.NewWriter(&archive)
	for name, path := range map[string]string{"com/artifactgateway/fixture/ResolveMojo.class": filepath.Join(classes, "com", "artifactgateway", "fixture", "ResolveMojo.class"), "META-INF/maven/plugin.xml": filepath.Join(filepath.Dir(source), "plugin.xml")} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		entry, err := jar.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = entry.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err = jar.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes()
}

func migrationClientJAR(t *testing.T) []byte {
	t.Helper()
	var content bytes.Buffer
	jar := zip.NewWriter(&content)
	entry, err := jar.Create("META-INF/MANIFEST.MF")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.WriteString(entry, "Manifest-Version: 1.0\n\n"); err != nil {
		t.Fatal(err)
	}
	if err = jar.Close(); err != nil {
		t.Fatal(err)
	}
	return content.Bytes()
}
