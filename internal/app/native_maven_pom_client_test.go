//go:build mavenclient

package app

import (
	"bytes"
	"context"
	"fmt"
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

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type pomCompatUpstream struct {
	UpstreamClient
	mu      sync.Mutex
	fetched map[string]int
}

func (c *pomCompatUpstream) FetchMaven(ctx context.Context, method string, member repository.Member, path string, headers http.Header) (*http.Response, error) {
	c.mu.Lock()
	c.fetched[path]++
	c.mu.Unlock()
	return c.UpstreamClient.FetchMaven(ctx, method, member, path, headers)
}

// An empty Maven local repository must consume the unchanged Hosted POMs,
// resolve their parents and a real transitive dependency tree. Plugin and
// unrelated dependencies use a public Central proxy, never the user's cache.
func TestMavenClientHostedPOMCompatibilityColdCache(t *testing.T) {
	mvn, err := exec.LookPath("mvn")
	if err != nil {
		t.Fatal("mandatory POM compatibility client gate requires Maven")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	endpoint := os.Getenv("MAVEN_POM_COMPAT_UPSTREAM")
	if endpoint == "" {
		endpoint = "https://repo.maven.apache.org/maven2"
	}
	upstreamURL, err := url.Parse(endpoint)
	if err != nil || upstreamURL.Scheme != "https" || upstreamURL.User != nil || upstreamURL.RawQuery != "" || upstreamURL.Fragment != "" ||
		(upstreamURL.Host != "repo.maven.apache.org" && upstreamURL.Host != "maven-central.storage-download.googleapis.com") || upstreamURL.Path != "/maven2" {
		t.Fatal("POM client upstream must be a credential-free public Maven Central endpoint")
	}
	store := repository.NewMemoryStore()
	hosted, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "deploys", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "central", Format: repository.FormatMaven, Type: repository.RepositoryTypeProxy,
		Endpoint: endpoint, AllowedHosts: []string{upstreamURL.Host},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.CreateHostedGroupIdempotently(ctx, repository.HostedGroup{
		ID: uuid.NewString(), Name: "pom-compat", Format: repository.FormatMaven,
		Members: []repository.GroupMember{{RepositoryID: hosted.ID, Position: 0}, {RepositoryID: proxy.ID, Position: 1}},
	}, "fixture", "pom-compat", "pom-compat")
	if err != nil {
		t.Fatal(err)
	}
	upstream := &pomCompatUpstream{fetched: make(map[string]int)}
	handler := NewGatewayHandlerWithCaches(Dependencies{NativeMavenObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator(),
		NewDefaultOCICache(NewMemoryOCIObjectStore(), nil), NewDefaultMavenCache(NewMemoryOCIObjectStore(), []string{upstreamURL.Host}), upstream)
	server := httptest.NewServer(handler)
	defer server.Close()
	files := map[string]string{
		"org/apache/commons/commons-parent/39/commons-parent-39.pom":                           "commons-parent-39.pom",
		"org/apache/commons/commons-parent/48/commons-parent-48.pom":                           "commons-parent-48.pom",
		"org/apache/commons/commons-parent/64/commons-parent-64.pom":                           "commons-parent-64.pom",
		"org/objenesis/objenesis/3.3/objenesis-3.3.pom":                                        "objenesis-3.3.pom",
		"org/flywaydb/flyway-database-postgresql/11.7.2/flyway-database-postgresql-11.7.2.pom": "flyway-database-postgresql-11.7.2.pom",
	}
	for path, file := range files {
		pom, err := os.ReadFile("testdata/maven-pom-compat/" + file)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPut, "/repository/maven/deploys/"+path, bytes.NewReader(pom))
		request.SetBasicAuth("maven", "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusCreated {
			t.Fatalf("publish original %s=%d %s", file, response.Code, response.Body.String())
		}
	}
	root := t.TempDir()
	settings, globalSettings, pom := filepath.Join(root, "settings.xml"), filepath.Join(root, "global-settings.xml"), filepath.Join(root, "pom.xml")
	local := filepath.Join(root, "empty-maven-repository")
	writeRedirectMavenClientFile(t, globalSettings, []byte(`<settings xmlns="http://maven.apache.org/SETTINGS/1.2.0"/>`))
	writeRedirectMavenClientFile(t, settings, []byte(fmt.Sprintf(`<settings><mirrors><mirror><id>owned-gateway</id><mirrorOf>*</mirrorOf><url>%s/maven/pom-compat</url></mirror></mirrors><servers><server><id>owned-gateway</id><username>maven</username><password>resolver-secret</password></server></servers></settings>`, server.URL)))
	writeRedirectMavenClientFile(t, pom, []byte(`<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion><groupId>gateway.fixture</groupId><artifactId>pom-compat-checks</artifactId><version>1</version><packaging>pom</packaging><modules><module>parent-39</module><module>parent-64</module><module>consumer</module></modules></project>`))
	for _, version := range []string{"39", "64"} {
		writeRedirectMavenClientFile(t, filepath.Join(root, "parent-"+version, "pom.xml"), []byte(fmt.Sprintf(`<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion><parent><groupId>org.apache.commons</groupId><artifactId>commons-parent</artifactId><version>%s</version><relativePath/></parent><groupId>gateway.fixture</groupId><artifactId>parent-%s</artifactId><version>1</version><packaging>pom</packaging></project>`, version, version)))
	}
	writeRedirectMavenClientFile(t, filepath.Join(root, "consumer", "pom.xml"), []byte(`<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion><parent><groupId>org.apache.commons</groupId><artifactId>commons-parent</artifactId><version>48</version><relativePath/></parent><groupId>gateway.fixture</groupId><artifactId>pom-compat-consumer</artifactId><version>1</version><dependencies><dependency><groupId>org.objenesis</groupId><artifactId>objenesis</artifactId><version>3.3</version></dependency><dependency><groupId>org.flywaydb</groupId><artifactId>flyway-database-postgresql</artifactId><version>11.7.2</version></dependency></dependencies></project>`))
	if _, err := os.Stat(local); !os.IsNotExist(err) {
		t.Fatal("Maven repository must be empty before the client starts")
	}
	out := runRedirectMavenClientCommand(t, ctx, mvn, root, redirectMavenClientEnvironment(t, root),
		"-B", "-ntp", "-Dstyle.color=never", "-s", settings, "-gs", globalSettings, "-f", pom, "-Dmaven.repo.local="+local,
		"org.apache.maven.plugins:maven-dependency-plugin:3.8.1:tree", "org.apache.maven.plugins:maven-dependency-plugin:3.8.1:resolve")
	for _, expected := range []string{"BUILD SUCCESS", "org.objenesis:objenesis:jar:3.3:compile", "org.flywaydb:flyway-database-postgresql:jar:11.7.2:compile", "org.flywaydb:flyway-core:jar:11.7.2:compile"} {
		if !strings.Contains(string(out), expected) {
			t.Fatalf("real Maven did not resolve expected dependency %q:\n%s", expected, out)
		}
	}
	for path, file := range files {
		original, err := os.ReadFile("testdata/maven-pom-compat/" + file)
		if err != nil {
			t.Fatal(err)
		}
		resolved, err := os.ReadFile(filepath.Join(local, filepath.FromSlash(path)))
		if err != nil || !bytes.Equal(original, resolved) {
			t.Fatalf("Maven changed or did not consume Hosted %s: %v", file, err)
		}
		upstream.mu.Lock()
		fetched := upstream.fetched[path]
		upstream.mu.Unlock()
		if fetched != 0 {
			t.Fatalf("Maven bypassed the Hosted fixture for %s", file)
		}
	}
	for _, path := range []string{
		"org/objenesis/objenesis/3.3/objenesis-3.3.jar",
		"org/flywaydb/flyway-database-postgresql/11.7.2/flyway-database-postgresql-11.7.2.jar",
		"org/flywaydb/flyway-core/11.7.2/flyway-core-11.7.2.jar",
	} {
		resolved, err := os.ReadFile(filepath.Join(local, filepath.FromSlash(path)))
		if err != nil || len(resolved) == 0 {
			t.Fatalf("Maven did not download dependency bytes for %s: %v", path, err)
		}
		request := httptest.NewRequest(http.MethodGet, "/maven/pom-compat/"+path, nil)
		request.SetBasicAuth("maven", "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !bytes.Equal(resolved, response.Body.Bytes()) {
			t.Fatalf("real Maven downloaded different Gateway bytes for %s: %d", path, response.Code)
		}
	}
	t.Logf("real Maven resolved original Hosted POMs and parent/transitive dependency tree from an empty cache:\n%s", out)
}
