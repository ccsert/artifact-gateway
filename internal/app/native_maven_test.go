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
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestNativeMavenPublishIsInvisibleUntilCommitAndAuditedOnRead(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "releases", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	h := newNativeMavenHandler(store, objects, testAuthenticator())
	pom := []byte("<project><version>1.0.0</version></project>")
	sum := sha256.Sum256(pom)
	request := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/publish-sessions", bytes.NewBufferString(`{"format":"maven","coordinate":"org.example:widget:1.0.0","pomObject":"widget-1.0.0.pom","objects":[{"name":"widget-1.0.0.pom","digest":"sha256:`+hex.EncodeToString(sum[:])+`","size":`+"42"+`}]} `))
	// The exact request body length is derived below to avoid coupling the test
	// to the illustrative POM text above.
	request = httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/publish-sessions", bytes.NewBufferString(""))
	body, _ := json.Marshal(nativeMavenSessionRequest{Format: "maven", Coordinate: "org.example:widget:1.0.0", PomObject: "widget-1.0.0.pom", Objects: []repository.MavenDeclaredObject{{Name: "widget-1.0.0.pom", Digest: "sha256:" + hex.EncodeToString(sum[:]), Size: int64(len(pom))}}})
	request.Body = io.NopCloser(bytes.NewReader(body))
	authorize(request, "admin-secret")
	created := httptest.NewRecorder()
	h.ServeHTTP(created, request)
	if created.Code != http.StatusCreated {
		t.Fatalf("create session=%d %s", created.Code, created.Body.String())
	}
	var session repository.MavenPublishSession
	if err := json.NewDecoder(created.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	read := httptest.NewRequest(http.MethodGet, "/repository/maven/releases/org/example/widget/1.0.0/widget-1.0.0.pom", nil)
	read.SetBasicAuth("maven", "resolver-secret")
	absent := httptest.NewRecorder()
	h.ServeHTTP(absent, read)
	if absent.Code != http.StatusNotFound {
		t.Fatalf("uncommitted read=%d", absent.Code)
	}
	upload := httptest.NewRequest(http.MethodPut, "/api/v2/publish-sessions/"+session.ID+"/objects/widget-1.0.0.pom", bytes.NewReader(pom))
	authorize(upload, "admin-secret")
	uploaded := httptest.NewRecorder()
	h.ServeHTTP(uploaded, upload)
	if uploaded.Code != http.StatusNoContent {
		t.Fatalf("upload=%d %s", uploaded.Code, uploaded.Body.String())
	}
	commit := httptest.NewRequest(http.MethodPost, "/api/v2/publish-sessions/"+session.ID+":commit", nil)
	authorize(commit, "admin-secret")
	committed := httptest.NewRecorder()
	h.ServeHTTP(committed, commit)
	if committed.Code != http.StatusOK {
		t.Fatalf("commit=%d %s", committed.Code, committed.Body.String())
	}
	served := httptest.NewRecorder()
	h.ServeHTTP(served, read)
	if served.Code != http.StatusOK || !bytes.Equal(served.Body.Bytes(), pom) {
		t.Fatalf("read=%d %q", served.Code, served.Body.String())
	}
	if served.Header().Get("ETag") == "" || len(store.Audits) != 1 || store.Audits[0].Outcome != repository.AuditResolved {
		t.Fatalf("headers=%v audits=%#v", served.Header(), store.Audits)
	}
	checksumRead := httptest.NewRequest(http.MethodGet, "/repository/maven/releases/org/example/widget/1.0.0/widget-1.0.0.pom.sha256", nil)
	checksumRead.SetBasicAuth("maven", "resolver-secret")
	checksum := httptest.NewRecorder()
	h.ServeHTTP(checksum, checksumRead)
	if checksum.Code != http.StatusOK || checksum.Body.String() != hex.EncodeToString(sum[:])+"\n" {
		t.Fatalf("checksum=%d %q", checksum.Code, checksum.Body.String())
	}
}

func TestNativeMavenProtocolPutPublishesAssetsAndMetadata(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, _ := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "deploys", Format: repository.FormatMaven})
	h := newNativeMavenHandler(store, NewMemoryOCIObjectStore(), testAuthenticator())
	for _, asset := range []string{"widget-1.2.0.pom", "widget-1.2.0.jar"} {
		r := httptest.NewRequest(http.MethodPut, "/repository/maven/deploys/org/example/widget/1.2.0/"+asset, bytes.NewBufferString(asset))
		r.SetBasicAuth("maven", "resolver-secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusCreated {
			t.Fatalf("PUT %s=%d %s", asset, w.Code, w.Body.String())
		}
	}
	get := httptest.NewRequest(http.MethodGet, "/repository/maven/deploys/org/example/widget/1.2.0/widget-1.2.0.jar", nil)
	get.SetBasicAuth("maven", "resolver-secret")
	out := httptest.NewRecorder()
	h.ServeHTTP(out, get)
	if out.Code != 200 || out.Body.String() != "widget-1.2.0.jar" {
		t.Fatalf("asset=%d %q", out.Code, out.Body.String())
	}
	metadata := httptest.NewRequest(http.MethodGet, "/repository/maven/deploys/org/example/widget/maven-metadata.xml", nil)
	metadata.SetBasicAuth("maven", "resolver-secret")
	out = httptest.NewRecorder()
	h.ServeHTTP(out, metadata)
	if out.Code != 200 || !strings.Contains(out.Body.String(), "<version>1.2.0</version>") {
		t.Fatalf("metadata=%d %s", out.Code, out.Body.String())
	}
	_ = repo
}
