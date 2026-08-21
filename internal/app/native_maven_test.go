package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
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

func TestNativeMavenValidatePOMRejectsTrailingDocument(t *testing.T) {
	pom := []byte("<project><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.0.0</version></project><extra/>")
	sum := sha256.Sum256(pom)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	objects := NewMemoryOCIObjectStore()
	if err := objects.Put(context.Background(), "native/maven/sha256/"+strings.TrimPrefix(digest, "sha256:"), pom); err != nil {
		t.Fatal(err)
	}
	handler := newNativeMavenHandler(repository.NewMemoryStore(), objects, testAuthenticator())
	session := repository.MavenPublishSession{
		Coordinate: "org.example:widget:1.0.0",
		PomObject:  "widget-1.0.0.pom",
		Objects:    []repository.MavenDeclaredObject{{Name: "widget-1.0.0.pom", Digest: digest, Size: int64(len(pom))}},
	}
	if err := handler.validatePOM(context.Background(), session); err == nil {
		t.Fatal("validatePOM() error=nil, want invalid XML error")
	}
}

func (s *failOnceMavenCommitStore) CommitMavenPublishSession(ctx context.Context, id string, assets []repository.MavenAsset) (repository.MavenArtifact, error) {
	if s.fail {
		s.fail = false
		return repository.MavenArtifact{}, errors.New("postgres promotion unavailable")
	}
	return s.MemoryStore.CommitMavenPublishSession(ctx, id, assets)
}

func (s *failOnceMavenCommitStore) CommitMavenPublishSessionIdempotently(ctx context.Context, id, key, payload string, assets []repository.MavenAsset) (repository.MavenArtifact, bool, error) {
	if s.fail {
		s.fail = false
		return repository.MavenArtifact{}, false, errors.New("control plane unavailable")
	}
	return s.MemoryStore.CommitMavenPublishSessionIdempotently(ctx, id, key, payload, assets)
}

func TestNativeMavenPublishIsInvisibleUntilCommitAndAuditedOnRead(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "releases", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	enablePublicationScan(t, store, repo.ID)
	objects := NewMemoryOCIObjectStore()
	h := NewGatewayHandler(publicationScanDependencies(Dependencies{NativeMavenObjectStore: objects}, repository.FormatMaven), store, TestAdapter{}, testAuthenticator())
	pom := []byte("<project><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.0.0</version></project>")
	sum := sha256.Sum256(pom)
	pomDigest := "sha256:" + hex.EncodeToString(sum[:])
	// The exact request body length is derived below to avoid coupling the test
	// to the illustrative POM text above.
	request := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/publish-sessions", bytes.NewBufferString(""))
	body, _ := json.Marshal(nativeMavenSessionRequest{Format: "maven", Coordinate: "org.example:widget:1.0.0", PomObject: "widget-1.0.0.pom", Objects: []repository.MavenDeclaredObject{{Name: "widget-1.0.0.pom", Digest: pomDigest, Size: int64(len(pom))}}})
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
	requirePublicationScan(t, store, repo.ID, repository.FormatMaven, "org.example:widget:1.0.0", pomDigest)
	served := httptest.NewRecorder()
	h.ServeHTTP(served, read)
	if served.Code != http.StatusOK || !bytes.Equal(served.Body.Bytes(), pom) {
		t.Fatalf("read=%d %q", served.Code, served.Body.String())
	}
	foundReadAudit := false
	for _, audit := range store.Audits {
		foundReadAudit = foundReadAudit || audit.Operation == "get" && audit.Resource == "org/example/widget/1.0.0/widget-1.0.0.pom" && audit.Outcome == repository.AuditResolved
	}
	if served.Header().Get("ETag") == "" || !foundReadAudit {
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

func TestNativeMavenProtocolPublishesDirectlyByDefault(t *testing.T) {
	store := repository.NewMemoryStore()
	_, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "deploys", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	handler := newNativeMavenHandler(store, objects, testAuthenticator())
	put := func(name, body string) {
		t.Helper()
		request := httptest.NewRequest(http.MethodPut, "/repository/maven/deploys/org/example/widget/1.2.0/"+name, strings.NewReader(body))
		request.SetBasicAuth("maven", "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusCreated {
			t.Fatalf("PUT %s=%d %s", name, response.Code, response.Body.String())
		}
	}
	get := func(name string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/repository/maven/deploys/org/example/widget/1.2.0/"+name, nil)
		request.SetBasicAuth("maven", "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	pom := "<project><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.2.0</version></project>"
	put("widget-1.2.0.pom", pom)
	if response := get("widget-1.2.0.pom"); response.Code != http.StatusOK || response.Body.String() != pom {
		t.Fatalf("direct POM read=%d %q", response.Code, response.Body.String())
	}
	put("widget-1.2.0.jar", "jar bytes")
	if response := get("widget-1.2.0.jar"); response.Code != http.StatusOK || response.Body.String() != "jar bytes" {
		t.Fatalf("direct JAR read=%d %q", response.Code, response.Body.String())
	}

	commit := httptest.NewRequest(http.MethodPost, "/repository/maven/deploys/coordinates/org.example:widget:1.2.0:commit", strings.NewReader(`{"expectedAssetNames":["widget-1.2.0.pom","widget-1.2.0.jar"]}`))
	commit.SetBasicAuth("maven", "resolver-secret")
	commit.Header.Set("Idempotency-Key", "not-required")
	committed := httptest.NewRecorder()
	handler.ServeHTTP(committed, commit)
	if committed.Code != http.StatusConflict || !strings.Contains(committed.Body.String(), "publication_commit_disabled") {
		t.Fatalf("disabled commit=%d %s", committed.Code, committed.Body.String())
	}
}

func TestNativeMavenDirectSnapshotMetadataClosesTheBuildSession(t *testing.T) {
	store := repository.NewMemoryStore()
	_, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "deploys", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	handler := newNativeMavenHandler(store, NewMemoryOCIObjectStore(), testAuthenticator())
	put := func(path, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPut, "/repository/maven/deploys/org/example/widget/1.0-SNAPSHOT/"+path, strings.NewReader(body))
		request.SetBasicAuth("maven", "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	pom := func(description string) string {
		return "<project><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.0-SNAPSHOT</version><description>" + description + "</description></project>"
	}
	if response := put("widget-1.0-20260821.010101-1.pom", pom("first")); response.Code != http.StatusCreated {
		t.Fatalf("first snapshot=%d %s", response.Code, response.Body.String())
	}
	if response := put("maven-metadata.xml", "<metadata/>"); response.Code != http.StatusCreated {
		t.Fatalf("snapshot metadata=%d %s", response.Code, response.Body.String())
	}
	if response := put("widget-1.0-20260821.010102-2.pom", pom("second")); response.Code != http.StatusCreated {
		t.Fatalf("second snapshot=%d %s", response.Code, response.Body.String())
	}
	metadata := httptest.NewRequest(http.MethodGet, "/repository/maven/deploys/org/example/widget/1.0-SNAPSHOT/maven-metadata.xml", nil)
	metadata.SetBasicAuth("maven", "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, metadata)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "<buildNumber>2</buildNumber>") {
		t.Fatalf("second snapshot metadata=%d %s", response.Code, response.Body.String())
	}
}

func TestNativeMavenDirectSnapshotAcceptsGradleJarBeforePOM(t *testing.T) {
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "deploys", Format: repository.FormatMaven}); err != nil {
		t.Fatal(err)
	}
	handler := newNativeMavenHandler(store, NewMemoryOCIObjectStore(), testAuthenticator())
	put := func(name, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPut, "/repository/maven/deploys/org/example/gradle-widget/2.0.0-SNAPSHOT/"+name, strings.NewReader(body))
		request.SetBasicAuth("maven", "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	const timestamp = "20260821.084531-1"
	if response := put("gradle-widget-2.0.0-"+timestamp+".jar", "gradle bytecode"); response.Code != http.StatusCreated {
		t.Fatalf("Gradle snapshot JAR=%d %s", response.Code, response.Body.String())
	}
	pom := `<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion><groupId>org.example</groupId><artifactId>gradle-widget</artifactId><version>2.0.0-SNAPSHOT</version></project>`
	if response := put("gradle-widget-2.0.0-"+timestamp+".pom", pom); response.Code != http.StatusCreated {
		t.Fatalf("Gradle snapshot POM after JAR=%d %s", response.Code, response.Body.String())
	}
}

func TestNativeMavenProtocolPutPublishesAssetsAndMetadata(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, _ := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "deploys", Format: repository.FormatMaven, MavenStrictPublication: true})
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
	commit := func(expected, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/repository/maven/deploys/coordinates/org.example:widget:1.2.0:commit", strings.NewReader(expected))
		r.SetBasicAuth("maven", "resolver-secret")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if result := commit(`{"expectedAssetNames":["widget-1.2.0.pom"]}`, "coordinate-commit"); result.Code != http.StatusConflict {
		t.Fatalf("incomplete expected assets=%d %s", result.Code, result.Body.String())
	}
	if result := commit(`{"expectedAssetNames":["widget-1.2.0.pom","widget-1.2.0.jar"]}`, "coordinate-commit"); result.Code != http.StatusOK {
		t.Fatalf("commit=%d %s", result.Code, result.Body.String())
	}
	if result := commit(`{"expectedAssetNames":["widget-1.2.0.jar","widget-1.2.0.pom"]}`, "coordinate-commit"); result.Code != http.StatusOK {
		t.Fatalf("idempotent commit=%d %s", result.Code, result.Body.String())
	}
	if result := commit(`{"expectedAssetNames":["widget-1.2.0.jar","widget-1.2.0.pom"]}`, "different-coordinate-commit"); result.Code != http.StatusConflict || !strings.Contains(result.Body.String(), "idempotency_conflict") {
		t.Fatalf("different idempotency key=%d %s", result.Code, result.Body.String())
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

func TestNativeMavenProtocolReplacesExpiredOpenSessionBeforeStaging(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "deploys", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	expired := repository.MavenPublishSession{
		ID:           uuid.NewString(),
		RepositoryID: repo.ID,
		Coordinate:   "org.example:widget:1.2.0",
		Publisher:    "maven",
		PomObject:    "widget-1.2.0.pom",
		State:        "open",
		ExpiresAt:    time.Now().Add(-time.Minute),
		Objects:      []repository.MavenDeclaredObject{{Name: "widget-1.2.0.pom", Digest: "sha256:stale", Size: 1}},
	}
	if _, err = store.CreateMavenPublishSession(context.Background(), expired); err != nil {
		t.Fatal(err)
	}

	h := newNativeMavenHandler(store, NewMemoryOCIObjectStore(), testAuthenticator())
	pom := "<project><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.2.0</version></project>"
	request := httptest.NewRequest(http.MethodPut, "/repository/maven/deploys/org/example/widget/1.2.0/widget-1.2.0.pom", strings.NewReader(pom))
	request.SetBasicAuth("maven", "resolver-secret")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("retry PUT=%d %s", response.Code, response.Body.String())
	}

	stale, err := store.GetMavenPublishSession(context.Background(), expired.ID)
	if err != nil || stale.State != "expired" {
		t.Fatalf("expired session=%#v err=%v", stale, err)
	}
	fresh, err := store.FindOpenMavenPublishSession(context.Background(), repo.ID, expired.Coordinate, expired.Publisher)
	if err != nil || fresh.ID == expired.ID || len(fresh.Objects) != 1 || fresh.Objects[0].Digest == "sha256:stale" {
		t.Fatalf("fresh session=%#v err=%v", fresh, err)
	}
}

func TestNativeMavenProtocolFixtureCoversReleaseSnapshotAndFailedCoordinates(t *testing.T) {
	store := repository.NewMemoryStore()
	_, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "deploys", Format: repository.FormatMaven, MavenStrictPublication: true})
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
	code, rootMetadata := read("org/example/widget/maven-metadata.xml")
	if code != http.StatusOK {
		t.Fatalf("root metadata = %d %s", code, rootMetadata)
	}
	if !strings.Contains(rootMetadata, "<latest>2.0.0-SNAPSHOT</latest>") || !strings.Contains(rootMetadata, "<release>1.2.3</release>") {
		t.Fatalf("root metadata must keep SNAPSHOT out of release: %s", rootMetadata)
	}
	if strings.Count(rootMetadata, "<version>2.0.0-SNAPSHOT</version>") != 1 {
		t.Fatalf("root metadata must list a SNAPSHOT version once: %s", rootMetadata)
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

func TestNativeMavenCoordinateBrowseSearchProjection(t *testing.T) {
	store := repository.NewMemoryStore()
	mavenRepo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "maven-browse", Format: repository.FormatMaven, MavenStrictPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	rawRepo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "raw-browse", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{NativeMavenObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, authenticator)
	if _, err := store.ReplaceRepositoryGrants(context.Background(), mavenRepo.ID, []repository.RepositoryGrant{
		{Principal: "maven", Scopes: []string{"repositories:write"}},
		{Principal: "maven-browser", Scopes: []string{"repositories:read"}},
	}, "1"); err != nil {
		t.Fatal(err)
	}
	publish := func(version string) {
		t.Helper()
		base := "/repository/maven/" + mavenRepo.Name + "/org/example/widget/" + version + "/widget-" + version
		for name, body := range map[string]string{
			".pom": "<project><groupId>org.example</groupId><artifactId>widget</artifactId><version>" + version + "</version></project>",
			".jar": "widget-" + version,
		} {
			req := httptest.NewRequest(http.MethodPut, base+name, strings.NewReader(body))
			req.SetBasicAuth("maven", "resolver-secret")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != http.StatusCreated {
				t.Fatalf("stage %s%s=%d %s", version, name, response.Code, response.Body.String())
			}
		}
		commit := httptest.NewRequest(http.MethodPost, "/repository/maven/"+mavenRepo.Name+"/coordinates/org.example:widget:"+version+":commit", strings.NewReader(`{"expectedAssetNames":["widget-`+version+`.pom","widget-`+version+`.jar"]}`))
		commit.SetBasicAuth("maven", "resolver-secret")
		commit.Header.Set("Idempotency-Key", "browse-"+version)
		committed := httptest.NewRecorder()
		handler.ServeHTTP(committed, commit)
		if committed.Code != http.StatusOK {
			t.Fatalf("commit %s=%d %s", version, committed.Code, committed.Body.String())
		}
	}
	publish("1.0.0")
	publish("2.0.0")
	browserToken := authenticator.IssueToken("maven-browser")
	request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+mavenRepo.ID+"/maven/coordinates?q=org.example:widget:&pageSize=1", nil)
	authorize(request, browserToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var page struct {
		Items []struct {
			Coordinate string `json:"coordinate"`
			Publisher  string `json:"publisher"`
		} `json:"items"`
		NextPageToken string `json:"nextPageToken"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].Coordinate != "org.example:widget:1.0.0" || page.NextPageToken == "" {
		t.Fatalf("browse=%d body=%s page=%#v", response.Code, response.Body.String(), page)
	}
	if page.Items[0].Publisher != "maven" {
		t.Fatalf("publisher=%q body=%s", page.Items[0].Publisher, response.Body.String())
	}
	next := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+mavenRepo.ID+"/maven/coordinates?q=org.example:widget:&pageSize=1&pageToken="+url.QueryEscape(page.NextPageToken), nil)
	authorize(next, browserToken)
	nextResponse := httptest.NewRecorder()
	handler.ServeHTTP(nextResponse, next)
	if nextResponse.Code != http.StatusOK || !strings.Contains(nextResponse.Body.String(), `"org.example:widget:2.0.0"`) {
		t.Fatalf("next=%d %s", nextResponse.Code, nextResponse.Body.String())
	}
	crossQuery := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+mavenRepo.ID+"/maven/coordinates?q=other&pageToken="+url.QueryEscape(page.NextPageToken), nil)
	authorize(crossQuery, browserToken)
	crossQueryResponse := httptest.NewRecorder()
	handler.ServeHTTP(crossQueryResponse, crossQuery)
	if crossQueryResponse.Code != http.StatusBadRequest || !strings.Contains(crossQueryResponse.Body.String(), "invalid_page_token") {
		t.Fatalf("cross query=%d %s", crossQueryResponse.Code, crossQueryResponse.Body.String())
	}
	invalidQuery := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+mavenRepo.ID+"/maven/coordinates?q=../invalid", nil)
	authorize(invalidQuery, browserToken)
	invalidQueryResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidQueryResponse, invalidQuery)
	if invalidQueryResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid query=%d %s", invalidQueryResponse.Code, invalidQueryResponse.Body.String())
	}
	nonMaven := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+rawRepo.ID+"/maven/coordinates", nil)
	authorize(nonMaven, browserToken)
	nonMavenResponse := httptest.NewRecorder()
	handler.ServeHTTP(nonMavenResponse, nonMaven)
	if nonMavenResponse.Code != http.StatusNotFound {
		t.Fatalf("non Maven=%d %s", nonMavenResponse.Code, nonMavenResponse.Body.String())
	}
}

func TestNativeMavenProtocolCommitRetryAfterControlPlaneFailure(t *testing.T) {
	base := repository.NewMemoryStore()
	repo, err := base.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "deploys", Format: repository.FormatMaven, MavenStrictPublication: true})
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

func TestNativeMavenAnonymousReadPolicy(t *testing.T) {
	store := repository.NewMemoryStore()
	enableAnonymousAccess(t, store)
	publicRepo, _ := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "public", Format: repository.FormatMaven, AnonymousRead: true})
	privateRepo, _ := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "private", Format: repository.FormatMaven})
	objects := NewMemoryOCIObjectStore()
	asset := []byte("jar")
	sum := sha256.Sum256(asset)
	key := "native/maven/sha256/" + hex.EncodeToString(sum[:])
	_ = objects.Put(context.Background(), key, asset)
	for _, repo := range []repository.HostedRepository{publicRepo, privateRepo} {
		sessionID := "session-" + repo.Name
		if _, err := store.CreateMavenPublishSession(context.Background(), repository.MavenPublishSession{ID: sessionID, RepositoryID: repo.ID, Coordinate: "org.example:widget:1.0.0", State: "open", Objects: []repository.MavenDeclaredObject{{Name: "widget-1.0.0.jar", Digest: "sha256:" + hex.EncodeToString(sum[:]), Size: int64(len(asset))}}, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		_ = store.MarkMavenPublishObject(context.Background(), sessionID, "widget-1.0.0.jar", key)
		_, _ = store.CommitMavenPublishSession(context.Background(), sessionID, []repository.MavenAsset{{RepositoryID: repo.ID, Path: "org/example/widget/1.0.0/widget-1.0.0.jar", ObjectKey: key, Digest: "sha256:" + hex.EncodeToString(sum[:]), Size: int64(len(asset))}})
	}
	open := newNativeMavenHandler(store, objects, Authenticator{})
	public := httptest.NewRecorder()
	open.ServeHTTP(public, httptest.NewRequest(http.MethodGet, "/repository/maven/public/org/example/widget/1.0.0/widget-1.0.0.jar", nil))
	if public.Code != http.StatusOK || public.Body.String() != "jar" {
		t.Fatalf("public anonymous=%d body=%q", public.Code, public.Body.String())
	}
	w := httptest.NewRecorder()
	open.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/repository/maven/private/org/example/widget/1.0.0/widget-1.0.0.jar", nil))
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
	if _, err := store.ReplaceRepositoryGrants(context.Background(), repo.ID, []repository.RepositoryGrant{{Principal: "maven", Scopes: []string{"repositories:read"}, ResourcePrefix: "org.example"}}, "1"); err != nil {
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
	metadata := httptest.NewRequest(http.MethodGet, "/repository/maven/releases/org/example/widget/maven-metadata.xml", nil)
	metadata.SetBasicAuth("maven", "resolver-secret")
	metadataResponse := httptest.NewRecorder()
	h.ServeHTTP(metadataResponse, metadata)
	if metadataResponse.Code != http.StatusOK || !strings.Contains(metadataResponse.Body.String(), "<version>1.0.0</version>") {
		t.Fatalf("metadata=%d body=%s", metadataResponse.Code, metadataResponse.Body.String())
	}
	other := httptest.NewRequest(http.MethodGet, "/repository/maven/releases/com/other/widget/1.0.0/widget-1.0.0.jar", nil)
	other.SetBasicAuth("maven", "resolver-secret")
	otherResponse := httptest.NewRecorder()
	h.ServeHTTP(otherResponse, other)
	if otherResponse.Code != http.StatusForbidden {
		t.Fatalf("other read=%d body=%s", otherResponse.Code, otherResponse.Body.String())
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
	if !strings.Contains(metricResponse.Body.String(), `artifact_gateway_repository_authorization_denials_total{format="maven",authorization_source="repository_grants",authorization_reason="scope_not_granted"} 2`) {
		t.Fatalf("Maven authorization metric=%s", metricResponse.Body.String())
	}
}

func TestNativeMavenProtocolSessionsArePublisherScoped(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "releases", Format: repository.FormatMaven, MavenStrictPublication: true})
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
	metrics := &Metrics{}
	maintenance := NativeMavenMaintenance{Store: store, Objects: objects, Now: func() time.Time { return time.Now().Add(25 * time.Hour) }, Metrics: metrics}
	if err := maintenance.Collect(context.Background()); err == nil {
		t.Fatal("collector must report failed object deletion")
	}
	jobs, err := store.ListLifecycleJobs(context.Background(), "repo", 10)
	if err != nil || len(jobs) != 1 || jobs[0].State != repository.LifecycleJobRetrying {
		t.Fatalf("retrying jobs=%#v err=%v", jobs, err)
	}
	if _, err = store.RunLifecycleJobNow(context.Background(), "repo", jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	objects.fail = false
	if err := maintenance.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = objects.Get(context.Background(), key); err == nil {
		t.Fatal("collector did not retry the retained intent")
	}
	if metrics.backgroundOperations[backgroundOperationLifecycle][backgroundOperationMaven][backgroundOperationStarted].Load() != 3 ||
		metrics.backgroundOperations[backgroundOperationLifecycle][backgroundOperationMaven][backgroundOperationFailed].Load() != 1 ||
		metrics.backgroundOperations[backgroundOperationLifecycle][backgroundOperationMaven][backgroundOperationCompleted].Load() != 2 ||
		metrics.backgroundInFlight[backgroundOperationLifecycle][backgroundOperationMaven].Load() != 0 {
		t.Fatalf("Maven lifecycle metrics started=%d failed=%d completed=%d in_flight=%d",
			metrics.backgroundOperations[backgroundOperationLifecycle][backgroundOperationMaven][backgroundOperationStarted].Load(),
			metrics.backgroundOperations[backgroundOperationLifecycle][backgroundOperationMaven][backgroundOperationFailed].Load(),
			metrics.backgroundOperations[backgroundOperationLifecycle][backgroundOperationMaven][backgroundOperationCompleted].Load(),
			metrics.backgroundInFlight[backgroundOperationLifecycle][backgroundOperationMaven].Load())
	}
}

func TestNativeMavenSnapshotMultiBuildPublish(t *testing.T) {
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "deploys", Format: repository.FormatMaven, MavenStrictPublication: true}); err != nil {
		t.Fatal(err)
	}
	h := newNativeMavenHandler(store, NewMemoryOCIObjectStore(), testAuthenticator())

	request := func(method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.SetBasicAuth("maven", "resolver-secret")
		for key, value := range headers {
			r.Header.Set(key, value)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	put := func(version, name, content string) {
		t.Helper()
		if w := request(http.MethodPut, "/repository/maven/deploys/org/example/widget/"+version+"/"+name, content, nil); w.Code != http.StatusCreated {
			t.Fatalf("PUT %s = %d %s", name, w.Code, w.Body.String())
		}
	}
	commit := func(version string, assets []string, key string) *httptest.ResponseRecorder {
		t.Helper()
		encoded, _ := json.Marshal(nativeMavenCoordinateCommitRequest{ExpectedAssetNames: assets})
		return request(http.MethodPost, "/repository/maven/deploys/coordinates/org.example:widget:"+version+":commit", string(encoded), map[string]string{"Idempotency-Key": key})
	}
	read := func(path string) (int, string) {
		w := request(http.MethodGet, "/repository/maven/deploys/"+path, "", nil)
		return w.Code, w.Body.String()
	}

	// Two publishes of the same -SNAPSHOT coordinate produce two builds.
	const snapshot = "1.0-SNAPSHOT"
	pom := "<project><groupId>org.example</groupId><artifactId>widget</artifactId><version>" + snapshot + "</version></project>"
	put(snapshot, "widget-1.0-SNAPSHOT.pom", pom)
	put(snapshot, "widget-1.0-SNAPSHOT.jar", "snapshot jar build one")
	first := commit(snapshot, []string{"widget-1.0-SNAPSHOT.pom", "widget-1.0-SNAPSHOT.jar"}, "snapshot-build-1")
	if first.Code != http.StatusOK {
		t.Fatalf("first snapshot commit = %d %s", first.Code, first.Body.String())
	}
	var firstArtifact repository.MavenArtifact
	if err := json.Unmarshal(first.Body.Bytes(), &firstArtifact); err != nil {
		t.Fatal(err)
	}
	if firstArtifact.BuildNumber != 1 {
		t.Fatalf("first build number = %d, want 1 (%s)", firstArtifact.BuildNumber, first.Body.String())
	}

	put(snapshot, "widget-1.0-SNAPSHOT.pom", pom)
	put(snapshot, "widget-1.0-SNAPSHOT.jar", "snapshot jar build two")
	put(snapshot, "widget-1.0-SNAPSHOT-sources.jar", "snapshot sources build two")
	put(snapshot, "widget-1.0-SNAPSHOT-javadoc.jar", "snapshot javadoc build two")
	second := commit(snapshot, []string{"widget-1.0-SNAPSHOT.pom", "widget-1.0-SNAPSHOT.jar", "widget-1.0-SNAPSHOT-sources.jar", "widget-1.0-SNAPSHOT-javadoc.jar"}, "snapshot-build-2")
	if second.Code != http.StatusOK {
		t.Fatalf("second snapshot commit = %d %s", second.Code, second.Body.String())
	}
	var secondArtifact repository.MavenArtifact
	if err := json.Unmarshal(second.Body.Bytes(), &secondArtifact); err != nil {
		t.Fatal(err)
	}
	if secondArtifact.BuildNumber != 2 {
		t.Fatalf("second build number = %d, want 2 (%s)", secondArtifact.BuildNumber, second.Body.String())
	}

	// Maven version-level metadata describes the newest build once per
	// extension/classifier pair. Its <value> is the timestamped version, not a
	// filename that repeats artifactId.
	code, metadata := read("org/example/widget/1.0-SNAPSHOT/maven-metadata.xml")
	if code != http.StatusOK {
		t.Fatalf("snapshot metadata = %d %s", code, metadata)
	}
	metadataSum := sha256.Sum256([]byte(metadata))
	code, metadataChecksum := read("org/example/widget/1.0-SNAPSHOT/maven-metadata.xml.sha256")
	if code != http.StatusOK || strings.TrimSpace(metadataChecksum) != hex.EncodeToString(metadataSum[:]) {
		t.Fatalf("snapshot metadata checksum = (%d, %q), want generated SHA-256", code, metadataChecksum)
	}
	if !strings.Contains(metadata, "<buildNumber>2</buildNumber>") {
		t.Fatalf("metadata lacks latest buildNumber 2: %s", metadata)
	}
	type snapshotVersion struct {
		Extension  string `xml:"extension"`
		Classifier string `xml:"classifier"`
		Value      string `xml:"value"`
	}
	var parsed struct {
		Versioning struct {
			SnapshotVersions []snapshotVersion `xml:"snapshotVersions>snapshotVersion"`
		} `xml:"versioning"`
	}
	if err := xml.Unmarshal([]byte(metadata), &parsed); err != nil {
		t.Fatalf("parse snapshot metadata: %v\n%s", err, metadata)
	}
	value := "1.0-" + secondArtifact.CreatedAt.UTC().Format("20060102.150405") + "-2"
	want := map[string]string{
		"pom:":        pom,
		"jar:":        "snapshot jar build two",
		"jar:sources": "snapshot sources build two",
		"jar:javadoc": "snapshot javadoc build two",
	}
	if len(parsed.Versioning.SnapshotVersions) != len(want) {
		t.Fatalf("snapshotVersions = %#v, want exactly the latest %d assets", parsed.Versioning.SnapshotVersions, len(want))
	}
	for _, item := range parsed.Versioning.SnapshotVersions {
		key := item.Extension + ":" + item.Classifier
		body, ok := want[key]
		if !ok {
			t.Fatalf("unexpected snapshotVersion %#v", item)
		}
		if item.Value != value {
			t.Fatalf("snapshotVersion %#v value = %q, want timestamped version %q", item, item.Value, value)
		}
		name := "widget-" + item.Value
		if item.Classifier != "" {
			name += "-" + item.Classifier
		}
		name += "." + item.Extension
		code, got := read("org/example/widget/1.0-SNAPSHOT/" + name)
		if code != http.StatusOK || got != body {
			t.Fatalf("metadata asset %s = (%d, %q), want (200, %q)", name, code, got, body)
		}
	}

	// Older immutable timestamped builds remain directly addressable even
	// though normal Maven resolution only advertises the newest build.
	firstValue := "widget-1.0-" + firstArtifact.CreatedAt.UTC().Format("20060102.150405") + "-1.jar"
	if code, body := read("org/example/widget/1.0-SNAPSHOT/" + firstValue); code != http.StatusOK || body != "snapshot jar build one" {
		t.Fatalf("first timestamped jar = (%d, %q), want original build", code, body)
	}

	// Releases stay immutable: a second commit of the same release coordinate
	// is still rejected with 409 coordinate_exists.
	const release = "1.0.0"
	releasePOM := "<project><groupId>org.example</groupId><artifactId>widget</artifactId><version>" + release + "</version></project>"
	put(release, "widget-1.0.0.pom", releasePOM)
	put(release, "widget-1.0.0.jar", "release jar")
	if w := commit(release, []string{"widget-1.0.0.pom", "widget-1.0.0.jar"}, "release-1"); w.Code != http.StatusOK {
		t.Fatalf("release commit = %d %s", w.Code, w.Body.String())
	}
	put(release, "widget-1.0.0.pom", releasePOM)
	put(release, "widget-1.0.0.jar", "release jar changed")
	if w := commit(release, []string{"widget-1.0.0.pom", "widget-1.0.0.jar"}, "release-2"); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "coordinate_exists") {
		t.Fatalf("release republish = %d %s, want 409 coordinate_exists", w.Code, w.Body.String())
	}
}

func TestNativeMavenSnapshotTombstoneScopesToSingleBuild(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "deploys", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	h := newNativeMavenHandler(store, objects, testAuthenticator())

	publish := func(jarBody, key string) repository.MavenArtifact {
		t.Helper()
		pom := []byte("<project><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.0-SNAPSHOT</version></project>")
		pomSum := sha256.Sum256(pom)
		jarSum := sha256.Sum256([]byte(jarBody))
		pomKey := "native/maven/sha256/" + hex.EncodeToString(pomSum[:])
		jarKey := "native/maven/sha256/" + hex.EncodeToString(jarSum[:])
		if err := objects.Put(context.Background(), pomKey, pom); err != nil {
			t.Fatal(err)
		}
		if err := objects.Put(context.Background(), jarKey, []byte(jarBody)); err != nil {
			t.Fatal(err)
		}
		session := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: "org.example:widget:1.0-SNAPSHOT", Publisher: "maven", PomObject: "widget-1.0-SNAPSHOT.pom", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{
			{Name: "widget-1.0-SNAPSHOT.pom", Digest: "sha256:" + hex.EncodeToString(pomSum[:]), Size: int64(len(pom))},
			{Name: "widget-1.0-SNAPSHOT.jar", Digest: "sha256:" + hex.EncodeToString(jarSum[:]), Size: int64(len(jarBody))},
		}}
		if _, err := store.CreateMavenPublishSession(context.Background(), session); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkMavenPublishObject(context.Background(), session.ID, "widget-1.0-SNAPSHOT.pom", pomKey); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkMavenPublishObject(context.Background(), session.ID, "widget-1.0-SNAPSHOT.jar", jarKey); err != nil {
			t.Fatal(err)
		}
		artifact, err := store.CommitMavenPublishSession(context.Background(), session.ID, []repository.MavenAsset{
			{RepositoryID: repo.ID, Path: "org/example/widget/1.0-SNAPSHOT/widget-1.0-SNAPSHOT.pom", ObjectKey: pomKey, Digest: "sha256:" + hex.EncodeToString(pomSum[:]), Size: int64(len(pom))},
			{RepositoryID: repo.ID, Path: "org/example/widget/1.0-SNAPSHOT/widget-1.0-SNAPSHOT.jar", ObjectKey: jarKey, Digest: "sha256:" + hex.EncodeToString(jarSum[:]), Size: int64(len(jarBody))},
		})
		if err != nil {
			t.Fatal(err)
		}
		return artifact
	}
	first := publish("build one jar", "one")
	second := publish("build two jar", "two")
	if first.BuildNumber != 1 || second.BuildNumber != 2 {
		t.Fatalf("build numbers = %d/%d, want 1/2", first.BuildNumber, second.BuildNumber)
	}

	read := func(path string) (int, string) {
		r := httptest.NewRequest(http.MethodGet, "/repository/maven/deploys/"+path, nil)
		r.SetBasicAuth("maven", "resolver-secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	prefix := func(build repository.MavenArtifact) string {
		return "org/example/widget/1.0-SNAPSHOT/widget-1.0-" + build.CreatedAt.UTC().Format("20060102.150405") + "-" + strconv.Itoa(build.BuildNumber)
	}
	if code, body := read(prefix(first) + ".jar"); code != http.StatusOK || body != "build one jar" {
		t.Fatalf("build 1 jar = %d %q", code, body)
	}
	if code, body := read(prefix(second) + ".jar"); code != http.StatusOK || body != "build two jar" {
		t.Fatalf("build 2 jar = %d %q", code, body)
	}

	// Tombstoning build 1 leaves build 2 fully readable.
	if _, err := store.TombstoneMavenArtifact(context.Background(), repo.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	if code, _ := read(prefix(first) + ".jar"); code != http.StatusNotFound {
		t.Fatalf("tombstoned build 1 jar = %d, want 404", code)
	}
	if code, body := read(prefix(second) + ".jar"); code != http.StatusOK || body != "build two jar" {
		t.Fatalf("surviving build 2 jar = %d %q", code, body)
	}
	if code, body := read(prefix(second) + ".pom"); code != http.StatusOK {
		t.Fatalf("surviving build 2 pom = %d %q", code, body)
	}
}
