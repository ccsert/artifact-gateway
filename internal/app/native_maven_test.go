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

type failingPutObjectStore struct{ *MemoryOCIObjectStore }

func (s failingPutObjectStore) Put(context.Context, string, []byte) error {
	return errors.New("object store unavailable")
}

func (s failingPutObjectStore) PutVerifiedReader(context.Context, string, io.Reader, int64, string) error {
	return errors.New("object store unavailable")
}

type failOnceMavenCommitStore struct {
	*repository.MemoryStore
	fail bool
}

func (s *failOnceMavenCommitStore) CommitMavenPublishSession(ctx context.Context, id string, assets []repository.MavenAsset) (repository.MavenArtifact, error) {
	if s.fail {
		s.fail = false
		return repository.MavenArtifact{}, errors.New("postgres promotion unavailable")
	}
	return s.MemoryStore.CommitMavenPublishSession(ctx, id, assets)
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
	// The exact request body length is derived below to avoid coupling the test
	// to the illustrative POM text above.
	request := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/publish-sessions", bytes.NewBufferString(""))
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

func TestNativeMavenProtocolFixtureCoversReleaseSnapshotAndFailedCoordinates(t *testing.T) {
	store := repository.NewMemoryStore()
	_, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "deploys", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(newNativeMavenHandler(store, NewMemoryOCIObjectStore(), testAuthenticator()))
	defer server.Close()

	request := func(method, path, body string, headers map[string]string) *http.Response {
		t.Helper()
		r, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.SetBasicAuth("maven", "resolver-secret")
		for key, value := range headers {
			r.Header.Set(key, value)
		}
		response, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	status := func(method, path, body string, headers map[string]string) (int, string) {
		t.Helper()
		response := request(method, path, body, headers)
		defer func() { _ = response.Body.Close() }()
		content, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, string(content)
	}
	put := func(version, name, content string) {
		t.Helper()
		code, response := status(http.MethodPut, "/repository/maven/deploys/org/example/widget/"+version+"/"+name, content, nil)
		if code != http.StatusCreated {
			t.Fatalf("PUT %s = %d %s", name, code, response)
		}
	}
	commit := func(version string, assets []string) (int, string) {
		encoded, err := json.Marshal(nativeMavenCoordinateCommitRequest{ExpectedAssetNames: assets})
		if err != nil {
			t.Fatal(err)
		}
		return status(http.MethodPost, "/repository/maven/deploys/coordinates/org.example:widget:"+version+":commit", string(encoded), map[string]string{"Idempotency-Key": "fixture-" + version})
	}
	read := func(path string) (int, string) {
		return status(http.MethodGet, "/repository/maven/deploys/"+path, "", nil)
	}

	const release = "1.2.3"
	pom := "<project><groupId>org.example</groupId><artifactId>widget</artifactId><version>" + release + "</version></project>"
	put(release, "widget-1.2.3.pom", pom)
	if code, _ := read("org/example/widget/1.2.3/widget-1.2.3.pom"); code != http.StatusNotFound {
		t.Fatalf("partial POM read = %d, want 404", code)
	}
	if code, _ := commit(release, []string{"widget-1.2.3.pom", "widget-1.2.3.jar"}); code != http.StatusConflict {
		t.Fatalf("partial coordinate commit = %d, want 409", code)
	}
	put(release, "widget-1.2.3.jar", "release jar")
	if code, _ := status(http.MethodPut, "/repository/maven/deploys/org/example/widget/1.2.3/widget-1.2.3.jar.sha256", "client sidecar", nil); code != http.StatusCreated {
		t.Fatalf("client sidecar compatibility upload = %d, want 201", code)
	}
	if code, _ := read("org/example/widget/1.2.3/widget-1.2.3.jar"); code != http.StatusNotFound {
		t.Fatalf("uncommitted JAR read = %d, want 404", code)
	}
	for _, path := range []string{
		"org/example/widget/1.2.3/widget-1.2.3.jar.sha256",
		"org/example/widget/maven-metadata.xml",
	} {
		if code, _ := read(path); code != http.StatusNotFound {
			t.Fatalf("partial coordinate read %s = %d, want 404", path, code)
		}
	}
	if code, response := commit(release, []string{"widget-1.2.3.pom", "widget-1.2.3.jar"}); code != http.StatusOK {
		t.Fatalf("release commit = %d %s", code, response)
	}
	for path, want := range map[string]string{
		"org/example/widget/1.2.3/widget-1.2.3.pom":        pom,
		"org/example/widget/1.2.3/widget-1.2.3.jar":        "release jar",
		"org/example/widget/1.2.3/widget-1.2.3.jar.sha256": "",
		"org/example/widget/maven-metadata.xml":            "<version>1.2.3</version>",
	} {
		code, response := read(path)
		if code != http.StatusOK || want != "" && !strings.Contains(response, want) {
			t.Fatalf("release read %s = %d %q", path, code, response)
		}
	}

	const snapshot = "2.0.0-SNAPSHOT"
	snapshotPOM := "<project><groupId>org.example</groupId><artifactId>widget</artifactId><version>" + snapshot + "</version></project>"
	put(snapshot, "widget-2.0.0-SNAPSHOT.pom", snapshotPOM)
	put(snapshot, "widget-2.0.0-SNAPSHOT.jar", "snapshot jar")
	if code, response := commit(snapshot, []string{"widget-2.0.0-SNAPSHOT.pom", "widget-2.0.0-SNAPSHOT.jar"}); code != http.StatusOK {
		t.Fatalf("snapshot commit = %d %s", code, response)
	}
	code, metadata := read("org/example/widget/2.0.0-SNAPSHOT/maven-metadata.xml")
	if code != http.StatusOK {
		t.Fatalf("snapshot metadata = %d %s", code, metadata)
	}
	start := strings.Index(metadata, "<value>")
	end := strings.Index(metadata, "</value>")
	if start < 0 || end <= start+len("<value>") {
		t.Fatalf("snapshot metadata lacks timestamped value: %s", metadata)
	}
	timestamped := metadata[start+len("<value>") : end]
	if code, response := read("org/example/widget/2.0.0-SNAPSHOT/widget-" + timestamped + ".jar"); code != http.StatusOK || response != "snapshot jar" {
		t.Fatalf("timestamped SNAPSHOT JAR = %d %q", code, response)
	}

	const broken = "3.0.0"
	put(broken, "widget-3.0.0.pom", "<project><groupId>org.example</groupId><artifactId>other</artifactId><version>3.0.0</version></project>")
	put(broken, "widget-3.0.0.jar", "broken jar")
	if code, _ := commit(broken, []string{"widget-3.0.0.pom", "widget-3.0.0.jar"}); code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid POM commit = %d, want 422", code)
	}
	if code, _ := read("org/example/widget/3.0.0/widget-3.0.0.jar"); code != http.StatusNotFound {
		t.Fatalf("failed coordinate read = %d, want 404", code)
	}

	failingStore := repository.NewMemoryStore()
	_, err = failingStore.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "deploys", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	failingServer := httptest.NewServer(newNativeMavenHandler(failingStore, failingPutObjectStore{NewMemoryOCIObjectStore()}, testAuthenticator()))
	defer failingServer.Close()
	failedPut, err := http.NewRequest(http.MethodPut, failingServer.URL+"/repository/maven/deploys/org/example/failed/4.0.0/failed-4.0.0.pom", strings.NewReader("<project><groupId>org.example</groupId><artifactId>failed</artifactId><version>4.0.0</version></project>"))
	if err != nil {
		t.Fatal(err)
	}
	failedPut.SetBasicAuth("maven", "resolver-secret")
	failedUpload, err := failingServer.Client().Do(failedPut)
	if err != nil {
		t.Fatal(err)
	}
	_ = failedUpload.Body.Close()
	if failedUpload.StatusCode != http.StatusInternalServerError {
		t.Fatalf("object-store upload failure = %d, want 500", failedUpload.StatusCode)
	}
	failedRead, err := http.NewRequest(http.MethodGet, failingServer.URL+"/repository/maven/deploys/org/example/failed/4.0.0/failed-4.0.0.pom", nil)
	if err != nil {
		t.Fatal(err)
	}
	failedRead.SetBasicAuth("maven", "resolver-secret")
	response, err := failingServer.Client().Do(failedRead)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("failed upload coordinate read = %d, want 404", response.StatusCode)
	}
}

func TestNativeMavenProtocolCommitRetryAfterControlPlaneFailure(t *testing.T) {
	base := repository.NewMemoryStore()
	repo, err := base.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "deploys", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	store := &failOnceMavenCommitStore{MemoryStore: base, fail: true}
	objects := NewMemoryOCIObjectStore()
	server := httptest.NewServer(newNativeMavenHandler(store, objects, testAuthenticator()))
	defer server.Close()

	request := func(method, path, body string) *http.Response {
		t.Helper()
		r, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.SetBasicAuth("maven", "resolver-secret")
		if method == http.MethodPost {
			r.Header.Set("Idempotency-Key", "retry-after-promotion-failure")
			r.Header.Set("Content-Type", "application/json")
		}
		response, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	status := func(method, path, body string) int {
		t.Helper()
		response := request(method, path, body)
		defer func() { _ = response.Body.Close() }()
		return response.StatusCode
	}
	const version = "4.0.0"
	pom := "<project><groupId>org.example</groupId><artifactId>retry</artifactId><version>" + version + "</version></project>"
	jar := "retry jar"
	for name, body := range map[string]string{"retry-4.0.0.pom": pom, "retry-4.0.0.jar": jar} {
		if code := status(http.MethodPut, "/repository/maven/deploys/org/example/retry/"+version+"/"+name, body); code != http.StatusCreated {
			t.Fatalf("stage %s = %d", name, code)
		}
	}
	jarDigest := sha256.Sum256([]byte(jar))
	if _, err := objects.Get(context.Background(), "native/maven/sha256/"+hex.EncodeToString(jarDigest[:])); err != nil {
		t.Fatalf("S3 object missing before promotion: %v", err)
	}
	commitPath := "/repository/maven/deploys/coordinates/org.example:retry:" + version + ":commit"
	commitBody := `{"expectedAssetNames":["retry-4.0.0.pom","retry-4.0.0.jar"]}`
	if code := status(http.MethodPost, commitPath, commitBody); code != http.StatusUnprocessableEntity {
		t.Fatalf("first commit = %d, want 422", code)
	}
	for _, path := range []string{
		"org/example/retry/4.0.0/retry-4.0.0.pom",
		"org/example/retry/4.0.0/retry-4.0.0.jar",
		"org/example/retry/4.0.0/retry-4.0.0.jar.sha256",
		"org/example/retry/maven-metadata.xml",
	} {
		if code := status(http.MethodGet, "/repository/maven/deploys/"+path, ""); code != http.StatusNotFound {
			t.Fatalf("failed promotion read %s = %d, want 404", path, code)
		}
	}
	if code := status(http.MethodPost, commitPath, commitBody); code != http.StatusOK {
		t.Fatalf("commit retry = %d, want 200", code)
	}
	for _, path := range []string{
		"org/example/retry/4.0.0/retry-4.0.0.pom",
		"org/example/retry/4.0.0/retry-4.0.0.jar",
		"org/example/retry/4.0.0/retry-4.0.0.jar.sha256",
		"org/example/retry/maven-metadata.xml",
	} {
		if code := status(http.MethodGet, "/repository/maven/deploys/"+path, ""); code != http.StatusOK {
			t.Fatalf("retried promotion read %s = %d, want 200", path, code)
		}
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
	if _, err := store.CreateMavenPublishSession(context.Background(), repository.MavenPublishSession{ID: "session", RepositoryID: repo.ID, Coordinate: "org.example:widget:1.0.0", State: "open", Objects: []repository.MavenDeclaredObject{{Name: "widget-1.0.0.jar", Digest: "sha256:" + hex.EncodeToString(sum[:]), Size: int64(len(asset))}}, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	_ = store.MarkMavenPublishObject(context.Background(), "session", "widget-1.0.0.jar", key)
	_, _ = store.CommitMavenPublishSession(context.Background(), "session", []repository.MavenAsset{{RepositoryID: repo.ID, Path: "org/example/widget/1.0.0/widget-1.0.0.jar", ObjectKey: key, Digest: "sha256:" + hex.EncodeToString(sum[:]), Size: int64(len(asset))}})
	open := newNativeMavenHandler(store, objects, Authenticator{})
	w := httptest.NewRecorder()
	open.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/repository/maven/anonymous/org/example/widget/1.0.0/widget-1.0.0.jar", nil))
	if w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("anonymous read=%d", w.Code)
	}
}

func TestNativeMavenUsesManagedRepositoryGrants(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "releases", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(context.Background(), repo.ID, []repository.RepositoryGrant{{Principal: "maven", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	asset := []byte("jar")
	sum := sha256.Sum256(asset)
	key := "native/maven/sha256/" + hex.EncodeToString(sum[:])
	if err := objects.Put(context.Background(), key, asset); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateMavenPublishSession(context.Background(), repository.MavenPublishSession{ID: "session", RepositoryID: repo.ID, Coordinate: "org.example:widget:1.0.0", State: "open", Objects: []repository.MavenDeclaredObject{{Name: "widget-1.0.0.jar", Digest: "sha256:" + hex.EncodeToString(sum[:]), Size: int64(len(asset))}}, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkMavenPublishObject(context.Background(), "session", "widget-1.0.0.jar", key); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitMavenPublishSession(context.Background(), "session", []repository.MavenAsset{{RepositoryID: repo.ID, Path: "org/example/widget/1.0.0/widget-1.0.0.jar", ObjectKey: key, Digest: "sha256:" + hex.EncodeToString(sum[:]), Size: int64(len(asset))}}); err != nil {
		t.Fatal(err)
	}
	metrics := &Metrics{}
	h := newNativeMavenHandler(store, objects, testAuthenticator()).withMetrics(metrics)
	get := httptest.NewRequest(http.MethodGet, "/repository/maven/releases/org/example/widget/1.0.0/widget-1.0.0.jar", nil)
	get.SetBasicAuth("maven", "resolver-secret")
	got := httptest.NewRecorder()
	h.ServeHTTP(got, get)
	if got.Code != http.StatusOK || got.Body.String() != "jar" {
		t.Fatalf("read=%d body=%s", got.Code, got.Body.String())
	}
	put := httptest.NewRequest(http.MethodPut, "/repository/maven/releases/org/example/widget/1.0.0/widget-1.0.0.jar", strings.NewReader("replacement"))
	put.SetBasicAuth("maven", "resolver-secret")
	denied := httptest.NewRecorder()
	h.ServeHTTP(denied, put)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("write=%d body=%s", denied.Code, denied.Body.String())
	}
	if len(store.Audits) == 0 {
		t.Fatal("expected authorization audit")
	}
	audit := store.Audits[len(store.Audits)-1]
	if audit.AuthorizationSource != "repository_grants" || audit.AuthorizationReason != "scope_not_granted" || audit.Format != "maven" {
		t.Fatalf("audit=%#v", audit)
	}
	metricResponse := httptest.NewRecorder()
	metrics.Handler(metricResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(metricResponse.Body.String(), `artifact_gateway_repository_authorization_denials_total{format="maven",authorization_source="repository_grants",authorization_reason="scope_not_granted"} 1`) {
		t.Fatalf("Maven authorization metric=%s", metricResponse.Body.String())
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
