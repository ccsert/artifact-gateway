package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/aptpublication"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type aptManagementSigner struct{}

func (aptManagementSigner) SignRelease(_ context.Context, request aptpublication.SignReleaseRequest) (aptpublication.SignReleaseResult, error) {
	release, err := io.ReadAll(request.Release)
	if err != nil {
		return aptpublication.SignReleaseResult{}, err
	}
	return aptpublication.SignReleaseResult{
		InRelease: append([]byte("signed\n"), release...), Detached: []byte("detached:" + request.ReleaseDigest),
		SignerIdentity: "apt-release@example.test", KeyFingerprint: strings.Repeat("a", 40), Algorithm: "fixture-sha256",
	}, nil
}

func TestAPTPublicationManagementStagesPackageWithoutProtocolVisibility(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "apt-releases", Format: repository.FormatAPT, Type: repository.RepositoryTypeHosted,
	})
	if err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	handler := NewGatewayHandler(Dependencies{NativeAPTObjectStore: objects, APTSigner: aptManagementSigner{}}, store, TestAdapter{}, testAuthenticator())
	deb := aptManagementDebianPackage(t, "Package: widget\nVersion: 1:2.0-3\nArchitecture: amd64\n")
	digestBytes := sha256.Sum256(deb)
	digest := "sha256:" + hex.EncodeToString(digestBytes[:])
	body := fmt.Sprintf(`{"suite":"stable","component":"main","objectName":"widget_2.0-3_amd64.deb","declaredDigest":%q,"declaredSize":%d,"expectedIdentity":"widget@1:2.0-3#amd64"}`, digest, len(deb))

	create := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/apt/publication-sessions", strings.NewReader(body))
	authorize(create, "admin-secret")
	create.Header.Set("Content-Type", "application/json")
	create.Header.Set("Idempotency-Key", "build-42")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create=%d body=%s", created.Code, created.Body.String())
	}
	var session struct {
		ID, RepositoryID, Suite, Component, Publisher, ObjectName, DeclaredDigest, ExpectedIdentity, State string
		DeclaredSize                                                                                       int64
	}
	if err = json.Unmarshal(created.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.ID == "" || session.RepositoryID != repo.ID || session.Suite != "stable" || session.Component != "main" ||
		session.Publisher != "alice" || session.ObjectName != "widget_2.0-3_amd64.deb" || session.DeclaredDigest != digest ||
		session.DeclaredSize != int64(len(deb)) || session.ExpectedIdentity != "widget@1:2.0-3#amd64" || session.State != "open" {
		t.Fatalf("session=%#v", session)
	}

	replay := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/apt/publication-sessions", strings.NewReader(body))
	authorize(replay, "admin-secret")
	replay.Header.Set("Content-Type", "application/json")
	replay.Header.Set("Idempotency-Key", "build-42")
	replayed := httptest.NewRecorder()
	handler.ServeHTTP(replayed, replay)
	if replayed.Code != http.StatusCreated || !strings.Contains(replayed.Body.String(), `"id":"`+session.ID+`"`) {
		t.Fatalf("replay=%d body=%s", replayed.Code, replayed.Body.String())
	}

	conflict := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/apt/publication-sessions", strings.NewReader(strings.Replace(body, `"component":"main"`, `"component":"contrib"`, 1)))
	authorize(conflict, "admin-secret")
	conflict.Header.Set("Content-Type", "application/json")
	conflict.Header.Set("Idempotency-Key", "build-42")
	conflicted := httptest.NewRecorder()
	handler.ServeHTTP(conflicted, conflict)
	if conflicted.Code != http.StatusConflict || !strings.Contains(conflicted.Body.String(), `"code":"idempotency_conflict"`) {
		t.Fatalf("conflict=%d body=%s", conflicted.Code, conflicted.Body.String())
	}

	upload := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/apt/publication-sessions/"+session.ID+"/package", bytes.NewReader(deb))
	upload.ContentLength = -1
	authorize(upload, "admin-secret")
	upload.Header.Set("Content-Type", "application/vnd.debian.binary-package")
	uploaded := httptest.NewRecorder()
	handler.ServeHTTP(uploaded, upload)
	if uploaded.Code != http.StatusOK || !strings.Contains(uploaded.Body.String(), `"canonicalIdentity":"widget@1:2.0-3#amd64"`) || !strings.Contains(uploaded.Body.String(), `"digest":"`+digest+`"`) {
		t.Fatalf("upload=%d body=%s", uploaded.Code, uploaded.Body.String())
	}

	get := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/apt/publication-sessions/"+session.ID, nil)
	authorize(get, "admin-secret")
	got := httptest.NewRecorder()
	handler.ServeHTTP(got, get)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"state":"staged"`) {
		t.Fatalf("get=%d body=%s", got.Code, got.Body.String())
	}

	wrongMedia := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/apt/publication-sessions/"+session.ID+"/package", bytes.NewReader(deb))
	authorize(wrongMedia, "admin-secret")
	wrongMedia.Header.Set("Content-Type", "application/octet-stream")
	unsupported := httptest.NewRecorder()
	handler.ServeHTTP(unsupported, wrongMedia)
	if unsupported.Code != http.StatusUnsupportedMediaType || !strings.Contains(unsupported.Body.String(), `"code":"unsupported_media_type"`) {
		t.Fatalf("wrong media type=%d body=%s", unsupported.Code, unsupported.Body.String())
	}

	read := httptest.NewRequest(http.MethodGet, "/apt/"+repo.Name+"/pool/main/w/widget/widget_2.0-3_amd64.deb", nil)
	authorize(read, "admin-secret")
	protocol := httptest.NewRecorder()
	handler.ServeHTTP(protocol, read)
	if protocol.Code != http.StatusNotFound {
		t.Fatalf("staged package became protocol-visible: status=%d body=%s", protocol.Code, protocol.Body.String())
	}

	publishBody := fmt.Sprintf(`{"suite":"stable","sequence":1,"publicationSessionIds":[%q]}`, session.ID)
	publish := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/apt/snapshots", strings.NewReader(publishBody))
	authorize(publish, "admin-secret")
	publish.Header.Set("Content-Type", "application/json")
	publish.Header.Set("Idempotency-Key", "stable-1")
	published := httptest.NewRecorder()
	handler.ServeHTTP(published, publish)
	if published.Code != http.StatusCreated || !strings.Contains(published.Body.String(), `"state":"visible"`) ||
		!strings.Contains(published.Body.String(), `"signerIdentity":"apt-release@example.test"`) {
		t.Fatalf("publish=%d body=%s", published.Code, published.Body.String())
	}
	var snapshot struct{ ID string }
	if err = json.Unmarshal(published.Body.Bytes(), &snapshot); err != nil || snapshot.ID == "" {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}

	replaySnapshot := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/apt/snapshots", strings.NewReader(publishBody))
	authorize(replaySnapshot, "admin-secret")
	replaySnapshot.Header.Set("Content-Type", "application/json")
	replaySnapshot.Header.Set("Idempotency-Key", "stable-1")
	replayedSnapshot := httptest.NewRecorder()
	handler.ServeHTTP(replayedSnapshot, replaySnapshot)
	if replayedSnapshot.Code != http.StatusCreated || !strings.Contains(replayedSnapshot.Body.String(), `"id":"`+snapshot.ID+`"`) {
		t.Fatalf("snapshot replay=%d body=%s", replayedSnapshot.Code, replayedSnapshot.Body.String())
	}

	conflictingSnapshot := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/apt/snapshots", strings.NewReader(strings.Replace(publishBody, `"sequence":1`, `"sequence":2`, 1)))
	authorize(conflictingSnapshot, "admin-secret")
	conflictingSnapshot.Header.Set("Content-Type", "application/json")
	conflictingSnapshot.Header.Set("Idempotency-Key", "stable-1")
	snapshotConflict := httptest.NewRecorder()
	handler.ServeHTTP(snapshotConflict, conflictingSnapshot)
	if snapshotConflict.Code != http.StatusConflict || !strings.Contains(snapshotConflict.Body.String(), `"code":"idempotency_conflict"`) {
		t.Fatalf("snapshot conflict=%d body=%s", snapshotConflict.Code, snapshotConflict.Body.String())
	}

	for _, path := range []string{"dists/stable/InRelease", "dists/stable/Release", "pool/main/w/widget/widget_2.0-3_amd64.deb"} {
		request := httptest.NewRequest(http.MethodGet, "/apt/"+repo.Name+"/"+path, nil)
		authorize(request, "admin-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("published path %s=%d body=%s", path, response.Code, response.Body.String())
		}
	}

	audits, err := store.ListAudits(ctx, repository.AuditQuery{Repository: repo.Name, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	var createdAudit, stagedAudit, publishedAudit bool
	for _, audit := range audits {
		createdAudit = createdAudit || audit.Operation == "apt.publication_session.create" && audit.Actor == "alice" && audit.Status == http.StatusCreated
		stagedAudit = stagedAudit || audit.Operation == "apt.publication_package.stage" && audit.Actor == "alice" && audit.Resource == "widget@1:2.0-3#amd64" && audit.Representation == digest && audit.Status == http.StatusOK
		publishedAudit = publishedAudit || audit.Operation == "apt.repository_snapshot.publish" && audit.Actor == "alice" && audit.Resource == "stable" && audit.Status == http.StatusOK
	}
	if !createdAudit || !stagedAudit || !publishedAudit {
		t.Fatalf("publication audits missing: %#v", audits)
	}
}

func TestAPTPublicationManagementRequiresRepositoryWriteAndCanonicalScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "apt-private", Format: repository.FormatAPT, Type: repository.RepositoryTypeHosted,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: "reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{NativeAPTObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, authenticator)
	digest := "sha256:" + strings.Repeat("a", 64)
	body := fmt.Sprintf(`{"suite":"stable","component":"main","objectName":"widget.deb","declaredDigest":%q,"declaredSize":7}`, digest)

	readOnly := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/apt/publication-sessions", strings.NewReader(body))
	authorize(readOnly, authenticator.IssueToken("reader"))
	readOnly.Header.Set("Idempotency-Key", "reader-build")
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, readOnly)
	if denied.Code != http.StatusForbidden || !strings.Contains(denied.Body.String(), `"code":"access_denied"`) {
		t.Fatalf("read-only create=%d body=%s", denied.Code, denied.Body.String())
	}

	invalid := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/apt/publication-sessions", strings.NewReader(strings.Replace(body, `"suite":"stable"`, `"suite":".hidden"`, 1)))
	authorize(invalid, "admin-secret")
	invalid.Header.Set("Idempotency-Key", "invalid-scope")
	rejected := httptest.NewRecorder()
	handler.ServeHTTP(rejected, invalid)
	if rejected.Code != http.StatusBadRequest || !strings.Contains(rejected.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("invalid scope=%d body=%s", rejected.Code, rejected.Body.String())
	}
}

func TestAPTHostedPublicationCanStartFromSupportedRepositoryProvisioningAPI(t *testing.T) {
	t.Parallel()
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{NativeAPTObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator())

	createRepository := httptest.NewRequest(http.MethodPost, "/api/v2/repositories", strings.NewReader(`{"name":"apt-preview","format":"apt","type":"hosted"}`))
	authorize(createRepository, "admin-secret")
	createRepository.Header.Set("Idempotency-Key", "create-apt-preview")
	createdRepository := httptest.NewRecorder()
	handler.ServeHTTP(createdRepository, createRepository)
	if createdRepository.Code != http.StatusCreated {
		t.Fatalf("create APT Hosted repository=%d body=%s", createdRepository.Code, createdRepository.Body.String())
	}
	var repo repository.HostedRepository
	if err := json.Unmarshal(createdRepository.Body.Bytes(), &repo); err != nil || repo.ID == "" || repo.Type != repository.RepositoryTypeHosted {
		t.Fatalf("repository=%#v err=%v", repo, err)
	}

	digest := "sha256:" + strings.Repeat("a", 64)
	createSession := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/apt/publication-sessions", strings.NewReader(fmt.Sprintf(`{"suite":"stable","component":"main","objectName":"widget.deb","declaredDigest":%q,"declaredSize":7}`, digest)))
	authorize(createSession, "admin-secret")
	createSession.Header.Set("Idempotency-Key", "preview-build")
	createdSession := httptest.NewRecorder()
	handler.ServeHTTP(createdSession, createSession)
	if createdSession.Code != http.StatusCreated || !strings.Contains(createdSession.Body.String(), `"state":"open"`) {
		t.Fatalf("create publication session=%d body=%s", createdSession.Code, createdSession.Body.String())
	}
}

func aptManagementDebianPackage(t *testing.T, control string) []byte {
	t.Helper()
	controlArchive := aptManagementTarGzip(t, "./control", []byte(control))
	dataArchive := aptManagementTarGzip(t, "", nil)
	var deb bytes.Buffer
	deb.WriteString("!<arch>\n")
	aptManagementWriteARMember(t, &deb, "debian-binary", []byte("2.0\n"))
	aptManagementWriteARMember(t, &deb, "control.tar.gz", controlArchive)
	aptManagementWriteARMember(t, &deb, "data.tar.gz", dataArchive)
	return deb.Bytes()
}

func aptManagementTarGzip(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	if name != "" {
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func aptManagementWriteARMember(t *testing.T, output io.Writer, name string, body []byte) {
	t.Helper()
	header := fmt.Sprintf("%-16s%-12d%-6d%-6d%-8o%-10d`\n", name+"/", 0, 0, 0, 0o100644, len(body))
	if _, err := io.WriteString(output, header); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Write(body); err != nil {
		t.Fatal(err)
	}
	if len(body)%2 != 0 {
		if _, err := io.WriteString(output, "\n"); err != nil {
			t.Fatal(err)
		}
	}
}
