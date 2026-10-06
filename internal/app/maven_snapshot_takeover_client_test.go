//go:build mavenclient

package app

import (
	"bytes"
	"context"
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

	testsupport "github.com/artifact-gateway/artifact-gateway/internal/testsupport/snapshotfixture"
)

func TestMavenClientSnapshotHistoryTakeoverAndDeploy(t *testing.T) {
	if _, err := exec.LookPath("mvn"); err != nil {
		t.Fatal("mandatory Maven client gate requires mvn")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	f := newTakeoverFixture(t, testsupport.SnapshotClientBundle)
	var mu sync.Mutex
	failMetadata := false
	var delayed []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/1.0-SNAPSHOT/maven-metadata.xml") {
			mu.Lock()
			fail := failMetadata
			mu.Unlock()
			if fail {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				mu.Lock()
				delayed = body
				mu.Unlock()
				http.Error(w, "synthetic interruption before completion", 503)
				return
			}
		}
		f.h.ServeHTTP(w, r)
	}))
	defer server.Close()
	root := t.TempDir()
	pom := filepath.Join(root, "pom.xml")
	settings := filepath.Join(root, "settings.xml")
	local := filepath.Join(root, "maven-cache")
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(settings, `<settings><servers><server><id>synthetic-snapshots</id><username>maven</username><password>resolver-secret</password></server></servers></settings>`)
	write(pom, fmt.Sprintf(`<project><modelVersion>4.0.0</modelVersion><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.0-SNAPSHOT</version><distributionManagement><snapshotRepository><id>synthetic-snapshots</id><url>%s/repository/maven/%s</url></snapshotRepository></distributionManagement><build><plugins><plugin><groupId>org.apache.maven.plugins</groupId><artifactId>maven-deploy-plugin</artifactId><version>3.1.4</version></plugin></plugins></build></project>`, server.URL, f.repo.Name))
	marker := filepath.Join(root, "src/main/resources/marker.txt")
	write(marker, "first client build")
	run := func(args ...string) error {
		command := exec.CommandContext(ctx, "mvn", args...)
		command.Dir = root
		out, err := command.CombinedOutput()
		if err != nil {
			t.Logf("Maven output:\n%s", out)
		}
		return err
	}
	deploy := func() error {
		return run("deploy", "-DskipTests", "-f", pom, "-s", settings, "-B", "-ntp", "-Dmaven.repo.local="+local)
	}
	get := func(name string) []byte {
		t.Helper()
		w := f.request(http.MethodGet, name, "")
		if w.Code != 200 {
			t.Fatalf("GET %s=%d %s", name, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	original := get("maven-metadata.xml")
	if err := deploy(); err == nil {
		t.Fatal("ordinary Maven deployment bypassed protected import")
	}
	if !bytes.Equal(get("maven-metadata.xml"), original) {
		t.Fatal("protected deploy changed source current")
	}
	f.takeOver(t)
	if !bytes.Equal(get("maven-metadata.xml"), original) {
		t.Fatal("takeover changed source current")
	}
	mu.Lock()
	failMetadata = true
	mu.Unlock()
	if err := deploy(); err == nil {
		t.Fatal("interrupted deployment unexpectedly succeeded")
	}
	mu.Lock()
	failMetadata = false
	mu.Unlock()
	mu.Lock()
	oldMetadata := append([]byte(nil), delayed...)
	mu.Unlock()
	if len(oldMetadata) == 0 {
		t.Fatal("client never reached version metadata completion")
	}
	if !bytes.Equal(get("maven-metadata.xml"), original) {
		t.Fatal("interrupted deployment changed source current")
	}
	// Fresh timestamps isolate reruns from the prior failed deployment receipt.
	time.Sleep(1100 * time.Millisecond)
	for i := 0; i < 2; i++ {
		write(marker, fmt.Sprintf("complete client build %d", i))
		if err := deploy(); err != nil {
			t.Fatal(err)
		}
		v, err := f.store.GetMavenSnapshotImport(ctx, f.repo.ID, f.plan.Coordinate)
		if err != nil || v.CurrentBuildNumber != 10+i {
			t.Fatalf("allocation=%+v %v", v, err)
		}
		// Evict the artifact cache while keeping plugin downloads reusable.
		if err = os.RemoveAll(filepath.Join(local, "org/example/widget")); err != nil {
			t.Fatal(err)
		}
		if err = run("org.apache.maven.plugins:maven-dependency-plugin:3.8.1:get", "-Dartifact=org.example:widget:1.0-SNAPSHOT", "-Dtransitive=false", "-DremoteRepositories=synthetic-snapshots::default::"+server.URL+"/repository/maven/"+f.repo.Name, "-f", pom, "-s", settings, "-B", "-ntp", "-U", "-Dmaven.repo.local="+local); err != nil {
			t.Fatal(err)
		}
		resolved, err := os.ReadFile(filepath.Join(local, "org/example/widget/1.0-SNAPSHOT/widget-1.0-SNAPSHOT.jar"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(resolved, get("widget-1.0-SNAPSHOT.jar")) {
			t.Fatal("fresh Maven resolver did not select current")
		}
	}
	current := get("maven-metadata.xml")
	if w := f.request(http.MethodPut, "maven-metadata.xml", string(oldMetadata)); w.Code != 201 {
		t.Fatalf("delayed real metadata=%d %s", w.Code, w.Body.String())
	}
	if !bytes.Equal(get("maven-metadata.xml"), current) {
		t.Fatal("delayed real client metadata regressed current")
	}
	for path, body := range f.files {
		if strings.HasSuffix(path, "maven-metadata.xml") {
			continue
		}
		name := path[strings.LastIndexByte(path, '/')+1:]
		if !bytes.Equal(get(name), body) {
			t.Fatalf("history changed %s", name)
		}
	}
	for _, suffix := range []string{".md5", ".sha1", ".sha256", ".sha512"} {
		if len(get("widget-1.0-SNAPSHOT.jar"+suffix)) == 0 {
			t.Fatal("missing current checksum")
		}
	}
}
