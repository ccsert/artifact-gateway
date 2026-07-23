package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type markFailMavenStore struct{ *repository.MemoryStore }

func (s markFailMavenStore) MarkMavenPublishObject(context.Context, string, string, string) error {
	return errors.New("database unavailable")
}

type failingDeleteObjectStore struct {
	*MemoryOCIObjectStore
	fail bool
}

func (s *failingDeleteObjectStore) Delete(ctx context.Context, key string) error {
	if s.fail {
		return errors.New("object store unavailable")
	}
	return s.MemoryOCIObjectStore.Delete(ctx, key)
}

func TestNativeMavenPublishIsInvisibleUntilCommitAndAuditedOnRead(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "releases", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	h := newNativeMavenHandler(store, objects, testAuthenticator())
	pom := []byte("<project><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.0.0</version></project>")
	sum := sha256.Sum256(pom)
	request := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/publish-sessions", bytes.NewBufferString(`{"format":"maven","coordinate":"org.example:widget:1.0.0","pomObject":"widget-1.0.0.pom","objects":[{"name":"widget-1.0.0.pom","digest":"sha256:`+hex.EncodeToString(sum[:])+`","size":`+"42"+`}]} `))
	// The exact request body length is derived below to avoid coupling the test
	// to the illustrative POM text above.
	request = httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/publish-sessions", bytes.NewBufferString(""))
	body, _ := json.Marshal(nativeMavenSessionRequest{Format: "maven", Coordinate: "org.example:widget:1.0.0", PomObject: "widget-1.0.0.pom", Objects: []repository.MavenDeclaredObject{{Name: "widget-1.0.0.pom", Digest: "sha256:" + hex.EncodeToString(sum[:]), Size: int64(len(pom))}}})
	request.Body = io.NopCloser(bytes.NewReader(body))
	authorize(request, "admin-secret")
	request.Header.Set("Idempotency-Key", "native-maven-invisible")
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
	assets := map[string]string{
		"widget-1.2.0.pom": "<project><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.2.0</version></project>",
		"widget-1.2.0.jar": "jar bytes",
	}
	for asset, content := range assets {
		r := httptest.NewRequest(http.MethodPut, "/repository/maven/deploys/org/example/widget/1.2.0/"+asset, bytes.NewBufferString(content))
		r.SetBasicAuth("maven", "resolver-secret")
		r.Header.Set("X-Gateway-Publish-Complete", "true")
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
	if out.Code != http.StatusNotFound {
		t.Fatalf("staged asset=%d %q", out.Code, out.Body.String())
	}
	commit := func(expected string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/repository/maven/deploys/coordinates/org.example:widget:1.2.0:commit", strings.NewReader(expected))
		r.SetBasicAuth("maven", "resolver-secret")
		r.Header.Set("Idempotency-Key", "coordinate-commit")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if result := commit(`{"expectedAssetNames":["widget-1.2.0.pom"]}`); result.Code != http.StatusConflict {
		t.Fatalf("incomplete expected assets=%d %s", result.Code, result.Body.String())
	}
	if result := commit(`{"expectedAssetNames":["widget-1.2.0.pom","widget-1.2.0.jar"]}`); result.Code != http.StatusOK {
		t.Fatalf("commit=%d %s", result.Code, result.Body.String())
	}
	if result := commit(`{"expectedAssetNames":["widget-1.2.0.jar","widget-1.2.0.pom"]}`); result.Code != http.StatusOK {
		t.Fatalf("idempotent commit=%d %s", result.Code, result.Body.String())
	}
	out = httptest.NewRecorder()
	h.ServeHTTP(out, get)
	if out.Code != http.StatusOK || out.Body.String() != assets["widget-1.2.0.jar"] {
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

func TestNativeMavenSessionIdempotencyReplaysAndRejectsDifferentPayload(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, _ := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "idempotent", Format: repository.FormatMaven})
	h := newNativeMavenHandler(store, NewMemoryOCIObjectStore(), testAuthenticator())
	object := []byte("pom")
	sum := sha256.Sum256(object)
	body := nativeMavenSessionRequest{Format: "maven", Coordinate: "org.example:widget:1.0.0", PomObject: "widget-1.0.0.pom", Objects: []repository.MavenDeclaredObject{{Name: "widget-1.0.0.pom", Digest: "sha256:" + hex.EncodeToString(sum[:]), Size: int64(len(object))}}}
	encoded, _ := json.Marshal(body)
	create := func(value []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/publish-sessions", bytes.NewReader(value))
		authorize(r, "admin-secret")
		r.Header.Set("Idempotency-Key", "retry-key")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	first, replay := create(encoded), create(encoded)
	if first.Code != http.StatusCreated || replay.Code != http.StatusCreated || first.Body.String() != replay.Body.String() {
		t.Fatalf("replay first=%d %q replay=%d %q", first.Code, first.Body.String(), replay.Code, replay.Body.String())
	}
	body.Coordinate = "org.example:widget:2.0.0"
	different, _ := json.Marshal(body)
	if conflict := create(different); conflict.Code != http.StatusConflict {
		t.Fatalf("conflict=%d %s", conflict.Code, conflict.Body.String())
	}
}

func TestNativeMavenRejectsAnonymousReads(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, _ := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "anonymous", Format: repository.FormatMaven})
	objects := NewMemoryOCIObjectStore()
	asset := []byte("jar")
	sum := sha256.Sum256(asset)
	key := "native/maven/sha256/" + hex.EncodeToString(sum[:])
	_ = objects.Put(context.Background(), key, asset)
	store.CreateMavenPublishSession(context.Background(), repository.MavenPublishSession{ID: "session", RepositoryID: repo.ID, Coordinate: "org.example:widget:1.0.0", State: "open", Objects: []repository.MavenDeclaredObject{{Name: "widget-1.0.0.jar", Digest: "sha256:" + hex.EncodeToString(sum[:]), Size: int64(len(asset))}}, ExpiresAt: time.Now().Add(time.Hour)})
	_ = store.MarkMavenPublishObject(context.Background(), "session", "widget-1.0.0.jar", key)
	_, _ = store.CommitMavenPublishSession(context.Background(), "session", []repository.MavenAsset{{RepositoryID: repo.ID, Path: "org/example/widget/1.0.0/widget-1.0.0.jar", ObjectKey: key, Digest: "sha256:" + hex.EncodeToString(sum[:]), Size: int64(len(asset))}})
	open := newNativeMavenHandler(store, objects, Authenticator{})
	w := httptest.NewRecorder()
	open.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/repository/maven/anonymous/org/example/widget/1.0.0/widget-1.0.0.jar", nil))
	if w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("anonymous read=%d", w.Code)
	}
}

func TestNativeMavenProtocolSessionsArePublisherScoped(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "releases", Format: repository.FormatMaven})
	h := newNativeMavenHandler(store, NewMemoryOCIObjectStore(), Authenticator{ResolverToken: "resolver-secret", RepositoryWriters: map[string][]string{"alice": {"releases"}, "bob": {"releases"}}})
	put := func(actor, name, body string) int {
		r := httptest.NewRequest(http.MethodPut, "/repository/maven/releases/org/example/widget/1.0.0/"+name, strings.NewReader(body))
		r.SetBasicAuth(actor, "resolver-secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	pom := "<project><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.0.0</version></project>"
	if got := put("alice", "widget-1.0.0.pom", pom); got != http.StatusCreated {
		t.Fatalf("alice stage pom=%d", got)
	}
	if got := put("bob", "widget-1.0.0.jar", "bob jar"); got != http.StatusCreated {
		t.Fatalf("bob stage jar=%d", got)
	}
	commit := httptest.NewRequest(http.MethodPost, "/repository/maven/releases/coordinates/org.example:widget:1.0.0:commit", strings.NewReader(`{"expectedAssetNames":["widget-1.0.0.jar"]}`))
	commit.SetBasicAuth("bob", "resolver-secret")
	commit.Header.Set("Idempotency-Key", "bob-commit")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, commit)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bob must not commit alice's POM, got %d %s", w.Code, w.Body.String())
	}
}

func TestNativeMavenProtocolStagesIntentBeforeObjectBytes(t *testing.T) {
	base := repository.NewMemoryStore()
	_, _ = base.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "releases", Format: repository.FormatMaven})
	objects := NewMemoryOCIObjectStore()
	h := newNativeMavenHandler(markFailMavenStore{base}, objects, testAuthenticator())
	r := httptest.NewRequest(http.MethodPut, "/repository/maven/releases/org/example/widget/1.0.0/widget-1.0.0.jar", strings.NewReader("jar"))
	r.SetBasicAuth("maven", "resolver-secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("stage failure=%d", w.Code)
	}
	sum := sha256.Sum256([]byte("jar"))
	if _, err := objects.Get(context.Background(), "native/maven/sha256/"+hex.EncodeToString(sum[:])); err == nil {
		t.Fatal("object bytes were written although durable staging intent failed")
	}
}

func TestNativeMavenCollectorRetainsIntentUntilObjectDeleteSucceeds(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := &failingDeleteObjectStore{MemoryOCIObjectStore: NewMemoryOCIObjectStore(), fail: true}
	key := "native/maven/sha256/deadbeef"
	_ = objects.Put(context.Background(), key, []byte("staged"))
	_, _ = store.CreateMavenPublishSession(context.Background(), repository.MavenPublishSession{ID: "expired", RepositoryID: "repo", Coordinate: "org.example:widget:1.0.0", Publisher: "alice", State: "open", ExpiresAt: time.Now().Add(-time.Hour)})
	if err := store.MarkMavenPublishObject(context.Background(), "expired", "widget-1.0.0.jar", key); err != nil {
		t.Fatal(err)
	}
	maintenance := NativeMavenMaintenance{Store: store, Objects: objects, Now: func() time.Time { return time.Now().Add(25 * time.Hour) }}
	if err := maintenance.Collect(context.Background()); err == nil {
		t.Fatal("collector must report failed object deletion")
	}
	objects.fail = false
	if err := maintenance.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := objects.Get(context.Background(), key); err == nil {
		t.Fatal("collector did not retry the retained intent")
	}
}
