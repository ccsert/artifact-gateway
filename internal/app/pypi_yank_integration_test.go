//go:build integration

package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type pypiYankBackup struct {
	Repository repository.HostedRepository
	Group      string
	Files      []repository.PyPIFile
	Objects    map[string][]byte
}

func pypiYankDurableFixture(t *testing.T, bucket string) (*repository.PostgresStore, *objectstore.RustFSStore) {
	t.Helper()
	if os.Getenv("TEST_DATABASE_URL") == "" || os.Getenv("TEST_RUSTFS_ENDPOINT") == "" {
		t.Skip("isolated PostgreSQL/RustFS environment is required")
	}
	store, err := repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	objects, err := objectstore.NewRustFSStore(os.Getenv("TEST_RUSTFS_ENDPOINT"), os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY"), bucket)
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(t.Context()); err != nil {
		t.Fatal(err)
	}
	return store, objects
}

func TestPyPIYankPostgresRustFSRestartDistributionAndBackup(t *testing.T) {
	bucket := "pypi-yank-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	first, objects := pypiYankDurableFixture(t, bucket)
	second, secondObjects := pypiYankDurableFixture(t, bucket)
	repo, err := first.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "pypi-yank-" + uuid.NewString()[:8], Format: repository.FormatPyPI})
	if err != nil {
		t.Fatal(err)
	}
	file := repository.PyPIFile{RepositoryID: repo.ID, Project: "pg-yank-widget", Version: "1.0", Filename: "pg_yank_widget-1.0-py3-none-any.whl", FileType: "bdist_wheel"}
	data := pypiFixtureWheel(t, "pg_yank_widget", "1.0")
	sum := sha256.Sum256(data)
	file.Digest = fmt.Sprintf("sha256:%x", sum)
	file.ObjectKey = "native/pypi/sha256/" + fmt.Sprintf("%x", sum)
	file.Size = int64(len(data))
	if err = objects.Put(t.Context(), file.ObjectKey, data); err != nil {
		t.Fatal(err)
	}
	if _, err = first.PublishPyPIFile(t.Context(), file); err != nil {
		t.Fatal(err)
	}
	group, _, err := first.CreateHostedGroupIdempotently(t.Context(), repository.HostedGroup{ID: uuid.NewString(), Name: "pypi-yank-group-" + uuid.NewString()[:8], Format: repository.FormatPyPI, Members: []repository.GroupMember{{RepositoryID: repo.ID, Position: 0}}}, "admin", uuid.NewString(), "yank-group")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = first.ReplaceRepositoryGrants(t.Context(), repo.ID, []repository.RepositoryGrant{{Principal: "build-agent", Scopes: []string{"repositories:read", "repositories:write"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	newHandler := func(store *repository.PostgresStore, objects ObjectStore) http.Handler {
		return newTestGateway(t, GatewayOptions{Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatPyPI: objects}})
	}
	firstHandler, secondHandler := newHandler(first, objects), newHandler(second, secondObjects)
	path := "/api/v2/repositories/" + repo.ID + "/pypi/projects/pg-yank-widget/versions/1.0/yank"
	current := pypiYankRequest(firstHandler, "GET", path, "", "", "resolver-secret")
	changed := pypiYankRequest(secondHandler, "PATCH", path, `{"yanked":true,"reason":"restart-safe ABI warning"}`, current.Header().Get("ETag"), "resolver-secret")
	if current.Code != 200 || changed.Code != 200 {
		t.Fatalf("cross-Gateway yank=%d/%d %s", current.Code, changed.Code, changed.Body.String())
	}
	if stale := pypiYankRequest(firstHandler, "PATCH", path, `{"yanked":false}`, current.Header().Get("ETag"), "resolver-secret"); stale.Code != 412 {
		t.Fatalf("stale Gateway=%d %s", stale.Code, stale.Body.String())
	}
	_ = first.Close()
	_ = second.Close()
	restarted, restartedObjects := pypiYankDurableFixture(t, bucket)
	handler := newHandler(restarted, restartedObjects)
	for _, root := range []string{repo.Name, group.Name} {
		index := pypiYankRequest(handler, "GET", "/pypi/"+root+"/simple/pg-yank-widget/", "", "", "resolver-secret")
		if index.Code != 200 || !strings.Contains(index.Body.String(), `data-yanked="restart-safe ABI warning"`) {
			t.Fatalf("persisted Simple=%d %s", index.Code, index.Body.String())
		}
		response := pypiYankRequest(handler, "GET", "/pypi/"+root+"/packages/"+file.Filename, "", "", "resolver-secret")
		if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), data) || sha256.Sum256(response.Body.Bytes()) != sum {
			t.Fatalf("yanked bytes after restart=%d/%d", response.Code, response.Body.Len())
		}
	}
	// Distribution copies advisory metadata while preserving immutable bytes.
	for _, kind := range []string{"promotions", "replications"} {
		target, createErr := restarted.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "yank-" + kind + "-" + uuid.NewString()[:8], Format: repository.FormatPyPI})
		if createErr != nil {
			t.Fatal(createErr)
		}
		req := httptest.NewRequest("POST", "/api/v2/repositories/"+repo.ID+"/"+kind, strings.NewReader(`{"targetRepositoryId":"`+target.ID+`","coordinate":"pg-yank-widget@1.0","digest":"`+file.Digest+`"}`))
		authorize(req, "admin-secret")
		req.Header.Set("Idempotency-Key", uuid.NewString())
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != 202 {
			t.Fatalf("distribution=%d %s", response.Code, response.Body.String())
		}
		if kind == "promotions" {
			err = (NativePyPIPromotion{Store: restarted}).RunJobs(t.Context(), 100)
		} else {
			err = (PyPIReplication{Store: restarted, Source: restartedObjects, Destination: restartedObjects}).RunJobs(t.Context(), 100)
		}
		if err != nil {
			t.Fatal(err)
		}
		copied, copyErr := restarted.GetPyPIFile(t.Context(), target.ID, file.Filename)
		if copyErr != nil || !copied.Yanked || copied.YankReason != "restart-safe ABI warning" || copied.Digest != file.Digest || copied.Size != file.Size {
			t.Fatalf("distributed metadata=%+v %v", copied, copyErr)
		}
		copyBytes, readErr := restartedObjects.Get(t.Context(), copied.ObjectKey)
		if readErr != nil || !bytes.Equal(copyBytes, data) {
			t.Fatalf("distributed bytes=%d %v", len(copyBytes), readErr)
		}
	}
	if _, err = restarted.TombstonePyPIVersion(t.Context(), repo.ID, file.Project, file.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.RestorePyPIVersion(t.Context(), repo.ID, file.Project, file.Version); err != nil {
		t.Fatal(err)
	}
	files, err := restarted.ListPyPIProjectFiles(t.Context(), repo.ID, file.Project)
	if err != nil || !files[0].Yanked || files[0].YankReason != "restart-safe ABI warning" {
		t.Fatalf("restored metadata=%+v %v", files, err)
	}
	if _, err = restarted.ReplaceRepositoryGrants(t.Context(), repo.ID, []repository.RepositoryGrant{{Principal: "build-agent", Scopes: []string{"repositories:read"}}}, "2"); err != nil {
		t.Fatal(err)
	}
	if denied := pypiYankRequest(handler, "PATCH", path, `{"yanked":false}`, changed.Header().Get("ETag"), "resolver-secret"); denied.Code != 403 || strings.Contains(denied.Body.String(), "restart-safe") {
		t.Fatalf("revoked writer=%d %s", denied.Code, denied.Body.String())
	}
	audits, err := restarted.ListAudits(t.Context(), repository.AuditQuery{Repository: repo.Name, Operation: "pypi.yank"})
	if err != nil || len(audits) != 1 || audits[0].Actor != "build-agent" || audits[0].RequestID == "" {
		t.Fatalf("durable mutation audit=%+v %v", audits, err)
	}
	if destination := os.Getenv("ARTIFACT_GATEWAY_PYPI_BACKUP_MANIFEST"); destination != "" {
		manifest := pypiYankBackup{Repository: repo, Group: group.Name, Files: files, Objects: map[string][]byte{file.ObjectKey: data}}
		encoded, marshalErr := json.Marshal(manifest)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if err = os.WriteFile(destination, encoded, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

// The owned acceptance runner restores an actual pg_dump into an empty
// database. This test uses only that database and a separate empty S3 bucket.
func TestPyPIYankRestoredDatabaseAndIndependentObjects(t *testing.T) {
	path := os.Getenv("ARTIFACT_GATEWAY_PYPI_RESTORE_MANIFEST")
	if path == "" {
		t.Skip("owned pg_dump restore manifest is required")
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest pypiYankBackup
	if err = json.Unmarshal(encoded, &manifest); err != nil {
		t.Fatal(err)
	}
	store, objects := pypiYankDurableFixture(t, "pypi-restored-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:20])
	for key, data := range manifest.Objects {
		if err = objects.Put(t.Context(), key, data); err != nil {
			t.Fatal(err)
		}
	}
	handler := newTestGateway(t, GatewayOptions{Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatPyPI: objects}})
	for _, expected := range manifest.Files {
		actual, readErr := store.GetPyPIFile(t.Context(), manifest.Repository.ID, expected.Filename)
		if readErr != nil || !actual.CreatedAt.Equal(expected.CreatedAt) || !actual.CachedAt.Equal(expected.CachedAt) || !actual.DeletedAt.Equal(expected.DeletedAt) || !actual.CollectedAt.Equal(expected.CollectedAt) {
			t.Fatalf("pg_dump lost timestamps=%+v want=%+v err=%v", actual, expected, readErr)
		}
		actual.CreatedAt, actual.CachedAt, actual.DeletedAt, actual.CollectedAt = expected.CreatedAt, expected.CachedAt, expected.DeletedAt, expected.CollectedAt
		if actual != expected {
			t.Fatalf("pg_dump lost file metadata=%+v want=%+v", actual, expected)
		}
		for _, root := range []string{manifest.Repository.Name, manifest.Group} {
			index := pypiYankRequest(handler, "GET", "/pypi/"+root+"/simple/"+expected.Project+"/", "", "", "resolver-secret")
			if index.Code != 200 || !strings.Contains(index.Body.String(), `data-yanked="`+expected.YankReason+`"`) {
				t.Fatalf("restored index=%d %s", index.Code, index.Body.String())
			}
			response := pypiYankRequest(handler, "GET", "/pypi/"+root+"/packages/"+expected.Filename, "", "", "resolver-secret")
			data := manifest.Objects[expected.ObjectKey]
			sum := sha256.Sum256(response.Body.Bytes())
			if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), data) || int64(response.Body.Len()) != expected.Size || fmt.Sprintf("sha256:%x", sum) != expected.Digest {
				t.Fatalf("restored independent S3 identity=%d %x", response.Code, sum)
			}
		}
	}
}
