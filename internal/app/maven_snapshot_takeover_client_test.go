//go:build mavenclient

package app

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
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
	request := func(method, name, body, actor, password string) (int, http.Header, []byte) {
		t.Helper()
		r, err := http.NewRequestWithContext(ctx, method, server.URL+"/repository/maven/"+f.repo.Name+"/org/example/widget/1.0-SNAPSHOT/"+name, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.SetBasicAuth(actor, password)
		response, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, response.Header, data
	}
	get := func(name string) []byte {
		t.Helper()
		status, _, body := request(http.MethodGet, name, "", "maven", "resolver-secret")
		if status != 200 {
			t.Fatalf("GET %s=%d %s", name, status, body)
		}
		return body
	}
	checkAsset := func(name string, body []byte) {
		t.Helper()
		if !bytes.Equal(get(name), body) {
			t.Fatalf("bytes changed %s", name)
		}
		status, header, head := request(http.MethodHead, name, "", "maven", "resolver-secret")
		if status != 200 || header.Get("Content-Length") != fmt.Sprint(len(body)) || len(head) != 0 {
			t.Fatalf("HEAD %s=%d length=%s body=%d", name, status, header.Get("Content-Length"), len(head))
		}
		checksums := map[string]string{".md5": fmt.Sprintf("%x", md5.Sum(body)), ".sha1": fmt.Sprintf("%x", sha1.Sum(body)), ".sha256": fmt.Sprintf("%x", sha256.Sum256(body)), ".sha512": fmt.Sprintf("%x", sha512.Sum512(body))}
		for suffix, expected := range checksums {
			if actual := strings.TrimSpace(string(get(name + suffix))); actual != expected {
				t.Fatalf("checksum %s%s=%q want %q", name, suffix, actual, expected)
			}
		}
	}
	original := get("maven-metadata.xml")
	selected := f.files["org/example/widget/1.0-SNAPSHOT/widget-1.0-20260101.000000-7.jar"]
	checkAsset("widget-1.0-SNAPSHOT.jar", selected)
	if status, _, _ := request(http.MethodPut, "widget-1.0-20261006.100000-9.jar", "unauthorized", "maven", "wrong-secret"); status != 401 {
		t.Fatalf("bad credential=%d", status)
	}
	if status, _, _ := request(http.MethodPut, "widget-1.0-20261006.100000-9.jar", "unauthorized", "reader", "resolver-secret"); status != 403 {
		t.Fatalf("reader publication=%d", status)
	}
	if err := deploy(); err == nil {
		t.Fatal("ordinary Maven deployment bypassed protected import")
	}
	if !bytes.Equal(get("maven-metadata.xml"), original) {
		t.Fatal("protected deploy changed source current")
	}
	f.takeOver(t)
	checkAsset("widget-1.0-SNAPSHOT.jar", selected)
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
		built, err := os.ReadFile(filepath.Join(root, "target/widget-1.0-SNAPSHOT.jar"))
		if err != nil || !bytes.Equal(resolved, built) {
			t.Fatalf("resolver did not retrieve this completed build: %v", err)
		}
		checkAsset("widget-1.0-SNAPSHOT.jar", built)
	}
	current := get("maven-metadata.xml")
	if status, _, body := request(http.MethodPut, "maven-metadata.xml", string(oldMetadata), "maven", "resolver-secret"); status != 201 {
		t.Fatalf("delayed real metadata=%d %s", status, body)
	}
	if !bytes.Equal(get("maven-metadata.xml"), current) {
		t.Fatal("delayed real client metadata regressed current")
	}
	for path, body := range f.files {
		if strings.HasSuffix(path, "maven-metadata.xml") {
			continue
		}
		name := path[strings.LastIndexByte(path, '/')+1:]
		checkAsset(name, body)
	}
}
