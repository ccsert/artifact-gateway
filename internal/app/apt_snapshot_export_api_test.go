package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/aptpublication"
	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func aptArchiveAPIFixture(t *testing.T) (*repository.MemoryStore, objectstore.Store, repository.HostedRepository, repository.APTRepositorySnapshot) {
	t.Helper()
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "archive-api", Format: repository.FormatAPT, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	objects := objectstore.NewMemoryStore()
	body := aptManagementDebianPackage(t, "Package: widget\nVersion: 1.0-1\nArchitecture: amd64\n")
	sum := sha256.Sum256(body)
	manager := aptpublication.NewManager(store, objects)
	session, _, err := manager.CreateSession(ctx, aptpublication.CreateSessionInput{RepositoryID: repo.ID, Suite: "stable", Component: "main", Publisher: "publisher", ObjectName: "widget_1.0-1_amd64.deb", DeclaredDigest: "sha256:" + hex.EncodeToString(sum[:]), DeclaredSize: int64(len(body)), IdempotencyKey: "archive-api"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = manager.UploadPackageAs(ctx, session.ID, session.ObjectName, bytes.NewReader(body), int64(len(body)), "publisher"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := aptpublication.NewPublisher(store, objects, aptManagementSigner{}).Publish(ctx, aptpublication.PublishSnapshotInput{ID: uuid.NewString(), RepositoryID: repo.ID, Suite: "stable", Sequence: 1, SessionIDs: []string{session.ID}, Actor: "publisher", CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	return store, objects, repo, snapshot
}

func TestAPTArchiveExportRequiresAdminAndRepositoryOwnershipWithoutSigner(t *testing.T) {
	t.Parallel()
	store, objects, repo, snapshot := aptArchiveAPIFixture(t)
	ctx := context.Background()
	if _, err := store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{
		{Principal: "reader", Scopes: []string{"repositories:read"}},
		{Principal: "writer", Scopes: []string{"repositories:write"}},
	}, "1"); err != nil {
		t.Fatal(err)
	}
	auth := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{NativeAPTObjectStore: objects}, store, TestAdapter{}, auth)
	path := "/api/v2/repositories/" + repo.ID + "/apt/snapshots/" + snapshot.ID + "/archive"
	for _, tc := range []struct {
		token string
		want  int
	}{{"", 401}, {auth.IssueToken("reader"), 403}, {auth.IssueToken("writer"), 403}, {"admin-secret", 200}} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		if tc.token != "" {
			authorize(request, tc.token)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != tc.want {
			t.Fatalf("status=%d want=%d body=%s", response.Code, tc.want, response.Body.String())
		}
		if tc.want != 200 {
			continue
		}
		if response.Header().Get("Content-Type") != aptSnapshotArchiveContentType || response.Header().Get("Content-Length") != strconv.Itoa(response.Body.Len()) || response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("Content-Disposition") != `attachment; filename="apt-stable-1.tar"` {
			t.Fatalf("headers=%v", response.Header())
		}
		manifest, err := aptpublication.VerifySnapshotArchive(ctx, bytes.NewReader(response.Body.Bytes()))
		if err != nil || manifest.Snapshot.ID != snapshot.ID {
			t.Fatalf("manifest=%#v error=%v", manifest, err)
		}
	}
	audits, err := store.ListAudits(ctx, repository.AuditQuery{Repository: repo.Name, Operation: "apt.repository_snapshot.export", Limit: 10})
	if err != nil || len(audits) != 1 || audits[0].Representation != snapshot.ReleaseDigest {
		t.Fatalf("audits=%#v error=%v", audits, err)
	}
	other, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "other-archive", Format: repository.FormatAPT, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, strings.Replace(path, repo.ID, other.ID, 1), nil)
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 404 || response.Header().Get("Content-Disposition") != "" {
		t.Fatalf("cross-repository status=%d", response.Code)
	}
	assets, err := store.ListAPTSnapshotAssets(ctx, snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.Delete(ctx, assets[0].ObjectKey); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, path, nil)
	authorize(request, "admin-secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 409 || !strings.Contains(response.Body.String(), "snapshot_corrupt") || response.Header().Get("Content-Disposition") != "" {
		t.Fatalf("corrupt status=%d body=%s", response.Code, response.Body.String())
	}
}

type changingAPTArchiveObjects struct {
	objectstore.Store
	key   string
	opens int
}

func (s *changingAPTArchiveObjects) Open(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	reader, size, err := s.Store.Open(ctx, key)
	if err != nil || key != s.key {
		return reader, size, err
	}
	s.opens++
	if s.opens == 1 {
		return reader, size, nil
	}
	body, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		return nil, 0, err
	}
	body[0] ^= 0xff
	return io.NopCloser(bytes.NewReader(body)), size, nil
}

func TestAPTArchiveStreamingCorruptionAbortsHTTPDownload(t *testing.T) {
	t.Parallel()
	store, objects, repo, snapshot := aptArchiveAPIFixture(t)
	assets, err := store.ListAPTSnapshotAssets(context.Background(), snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	changing := &changingAPTArchiveObjects{Store: objects, key: assets[0].ObjectKey}
	server := httptest.NewServer(NewGatewayHandler(Dependencies{NativeAPTObjectStore: changing}, store, TestAdapter{}, testAuthenticator()))
	defer server.Close()
	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/v2/repositories/"+repo.ID+"/apt/snapshots/"+snapshot.ID+"/archive", nil)
	if err != nil {
		t.Fatal(err)
	}
	authorize(request, "admin-secret")
	response, err := server.Client().Do(request)
	if err == nil {
		defer func() { _ = response.Body.Close() }()
		_, err = io.ReadAll(response.Body)
	}
	if err == nil {
		t.Fatal("corrupt export looked like a completed HTTP download")
	}
	audits, auditErr := store.ListAudits(context.Background(), repository.AuditQuery{Repository: repo.Name, Operation: "apt.repository_snapshot.export", Limit: 10})
	if auditErr != nil || len(audits) != 0 {
		t.Fatalf("incomplete download has success audit: %#v error=%v", audits, auditErr)
	}
}
