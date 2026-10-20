//go:build integration

package nuget

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
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type durableFixture struct {
	module  *Module
	store   *repository.PostgresStore
	objects *objectstore.RustFSStore
	repo    repository.HostedRepository
}

var nugetIntegrationBucket = "nuget-integration-" + uuid.NewString()

func digestFor(body []byte) string {
	digest := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func newDurableFixture(t *testing.T) durableFixture {
	t.Helper()
	if os.Getenv("TEST_DATABASE_URL") == "" || os.Getenv("TEST_RUSTFS_ENDPOINT") == "" {
		t.Skip("owned PostgreSQL and RustFS are required")
	}
	store, err := repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	objects, err := objectstore.NewRustFSStore(os.Getenv("TEST_RUSTFS_ENDPOINT"), os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY"), nugetIntegrationBucket)
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(t.Context()); err != nil {
		t.Fatal(err)
	}
	repo, err := store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "nuget-" + uuid.NewString(), Format: repository.FormatNuGet, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	return durableFixture{newDurableModule(t, store, objects), store, objects, repo}
}

func newDurableModule(t *testing.T, store *repository.PostgresStore, objects objectstore.Store) *Module {
	t.Helper()
	auth := authorization.Authenticator{AdminToken: "nuget-test-admin", AdminActor: "operator"}
	m, err := New(Options{Store: store, Repositories: store, Objects: objects, Authenticator: auth, Authorizer: authorization.RepositoryAuthorizer{Grants: store, Legacy: auth}, Audit: store, Anonymous: store})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func reserveDurable(t *testing.T, f durableFixture, body []byte) repository.NuGetSession {
	t.Helper()
	in := publicationRequest{PackageID: "Restart", Version: "1.0.0", Digest: digestFor(body), Size: int64(len(body))}
	encoded, _ := json.Marshal(in)
	w := request(t, f.module.Preview(), "POST", "/api/v2/previews/nuget/repositories/"+f.repo.ID+"/publication-sessions", encoded, map[string]string{"Authorization": "Bearer nuget-test-admin", "Idempotency-Key": uuid.NewString()})
	var session repository.NuGetSession
	var response struct{ ID string }
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &response) != nil {
		t.Fatalf("reserve=%d %s", w.Code, w.Body.String())
	}
	session, err := f.store.GetNuGetSession(t.Context(), response.ID)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func TestPostgresRustFSNuGetStagedRestartAndLostCommitResponse(t *testing.T) {
	f := newDurableFixture(t)
	body := packageFixture(t, "Restart", "1.0.0", `<dependencies><group targetFramework="net10.0"><dependency id="Dependency" version="[1.0.0]"/></group></dependencies>`)
	session := reserveDurable(t, f, body)
	base := "/api/v2/previews/nuget/repositories/" + f.repo.ID + "/publication-sessions/" + session.ID
	w := request(t, f.module.Preview(), "PUT", base+"/package", body, bearer)
	if w.Code != 200 {
		t.Fatalf("stage=%d %s", w.Code, w.Body.String())
	}
	path := "/nuget/" + f.repo.Name + "/v3/flat/restart/1.0.0/restart.1.0.0.nupkg"
	if w = request(t, f.module, "GET", path, nil, bearer); w.Code != 404 {
		t.Fatalf("staged visible=%d", w.Code)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	m := newDurableModule(t, reopened, f.objects)
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := httptest.NewRecorder()
		m.Preview().ServeHTTP(recorder, r)
		lost := false
		once.Do(func() { lost = true })
		if lost {
			if recorder.Code != 200 {
				t.Errorf("executed commit=%d %s", recorder.Code, recorder.Body.String())
			}
			conn, _, e := w.(http.Hijacker).Hijack()
			if e == nil {
				_ = conn.Close()
			}
			return
		}
		for k, v := range recorder.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(recorder.Code)
		_, _ = w.Write(recorder.Body.Bytes())
	}))
	defer server.Close()
	for attempt := 0; attempt < 2; attempt++ {
		r, _ := http.NewRequest("POST", server.URL+base+"/commit", nil)
		r.Header.Set("Authorization", "Bearer nuget-test-admin")
		resp, err := server.Client().Do(r)
		if attempt == 0 {
			if err == nil {
				_ = resp.Body.Close()
				t.Fatal("commit response was not dropped")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("replay=%d %s", resp.StatusCode, data)
		}
	}
	w = request(t, m, "GET", path, nil, bearer)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), body) {
		t.Fatalf("reopened bytes=%d", w.Code)
	}
	pub, err := reopened.GetNuGetPublication(t.Context(), f.repo.ID, "restart", "1.0.0")
	if err != nil || pub.Digest != digestFor(body) || !bytes.Contains(pub.MetadataJSON, []byte(`"Dependency"`)) || !bytes.Contains(pub.Nuspec, []byte(`version="[1.0.0]"`)) {
		t.Fatalf("reopened immutable metadata=%+v %v", pub, err)
	}
	capacity, err := reopened.GetRepositoryCapacity(t.Context(), f.repo.ID)
	if err != nil || capacity.UsedBytes != int64(len(body)) || capacity.ObjectCount != 1 {
		t.Fatalf("capacity=%+v %v", capacity, err)
	}
}

type lostNuGetObjectReply struct {
	objectstore.Store
	fail bool
}

func (s *lostNuGetObjectReply) PutVerifiedReader(ctx context.Context, key string, r io.Reader, n int64, digest string) error {
	if err := s.Store.PutVerifiedReader(ctx, key, r, n, digest); err != nil {
		return err
	}
	if s.fail {
		s.fail = false
		return errors.New("injected upload reply loss")
	}
	return nil
}

func TestPostgresRustFSNuGetLostUploadResponseAndConcurrentQuota(t *testing.T) {
	f := newDurableFixture(t)
	body := packageFixture(t, "Restart", "1.0.0", "")
	session := reserveDurable(t, f, body)
	base := "/api/v2/previews/nuget/repositories/" + f.repo.ID + "/publication-sessions/" + session.ID
	lost := &lostNuGetObjectReply{Store: f.objects, fail: true}
	m := newDurableModule(t, f.store, lost)
	if w := request(t, m.Preview(), "PUT", base+"/package", body, bearer); w.Code != 503 {
		t.Fatalf("lost upload=%d", w.Code)
	}
	if items, err := f.store.ListNuGetPublications(t.Context(), f.repo.ID, "", 10, ""); err != nil || len(items) != 0 {
		t.Fatalf("failed stage visible=%+v %v", items, err)
	}
	if w := request(t, m.Preview(), "PUT", base+"/package", body, bearer); w.Code != 200 {
		t.Fatalf("upload replay=%d %s", w.Code, w.Body.String())
	}
	if w := request(t, m.Preview(), "POST", base+"/commit", nil, bearer); w.Code != 200 {
		t.Fatalf("commit=%d %s", w.Code, w.Body.String())
	}
	if _, err := f.store.ReplaceRepositoryCapacityQuota(t.Context(), f.repo.ID, int64(len(body))+10); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	other, err := repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := f.store
			if i%2 == 1 {
				s = other
			}
			_, _, err := s.ReserveNuGetPublication(t.Context(), repository.NuGetSession{ID: uuid.NewString(), RepositoryID: f.repo.ID, PackageID: "quota", Version: string(rune('1'+i)) + ".0.0", Digest: "sha256:" + strings.Repeat("1", 64), Size: 10, Actor: "operator", ExpiresAt: time.Now().UTC().Add(time.Hour)})
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, repository.ErrQuotaExceeded) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("quota admission winners=%d", success)
	}
}

type blockingNuGetDelete struct {
	objectstore.Store
	key     string
	entered chan string
	release chan struct{}
	once    sync.Once
}

type observedNuGetPublicationLock struct {
	repository.NativeNuGetStore
	attempted chan struct{}
	once      sync.Once
}

func (s *observedNuGetPublicationLock) LockNuGetPublication(ctx context.Context, repo, id, version string) (context.Context, func(), error) {
	s.once.Do(func() { close(s.attempted) })
	return s.NativeNuGetStore.LockNuGetPublication(ctx, repo, id, version)
}

func (s *blockingNuGetDelete) Delete(ctx context.Context, key string) error {
	if key == s.key {
		s.once.Do(func() {
			s.entered <- key
			select {
			case <-s.release:
			case <-ctx.Done():
			}
		})
	}
	return s.Store.Delete(ctx, key)
}

func TestPostgresRustFSNuGetCollectionAndPublicationSerialize(t *testing.T) {
	f := newDurableFixture(t)
	body := packageFixture(t, "Restart", "1.0.0", "")
	session := reserveDurable(t, f, body)
	base := "/api/v2/previews/nuget/repositories/" + f.repo.ID + "/publication-sessions/" + session.ID
	if w := request(t, f.module.Preview(), "PUT", base+"/package", body, bearer); w.Code != 200 {
		t.Fatal(w.Code)
	}
	blocking := &blockingNuGetDelete{Store: f.objects, key: session.ObjectKey, entered: make(chan string, 1), release: make(chan struct{})}
	maintenance, _ := NewMaintenance(f.store, blocking)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- maintenance.Collect(ctx, time.Now().Add(2*time.Hour)) }()
	select {
	case key := <-blocking.entered:
		if key != session.ObjectKey {
			t.Fatal(key)
		}
	case <-ctx.Done():
		t.Fatal("collector did not reach object barrier")
	}
	other, err := repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	m := newDurableModule(t, other, f.objects)
	attempted := make(chan struct{})
	m.store = &observedNuGetPublicationLock{NativeNuGetStore: other, attempted: attempted}
	payload, ct := multipartPackage(t, body)
	pushed := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		pushed <- request(t, m, "PUT", "/nuget/"+f.repo.Name+"/api/v2/package/", payload, map[string]string{"Content-Type": ct, "X-NuGet-ApiKey": "nuget-test-admin"})
	}()
	select {
	case <-attempted:
	case <-ctx.Done():
		t.Fatal("replacement never attempted its publication lock")
	}
	select {
	case response := <-pushed:
		t.Fatalf("publication crossed collection lock=%d", response.Code)
	case <-time.After(50 * time.Millisecond):
	}
	close(blocking.release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case response := <-pushed:
		if response.Code != 201 {
			t.Fatalf("replacement=%d %s", response.Code, response.Body.String())
		}
	case <-ctx.Done():
		t.Fatal("replacement did not resume")
	}
	if err = maintenance.Collect(ctx, time.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	pub, err := other.GetNuGetPublication(ctx, f.repo.ID, "restart", "1.0.0")
	if err != nil || pub.ObjectKey == session.ObjectKey {
		t.Fatalf("replacement=%+v %v", pub, err)
	}
	if data, err := f.objects.Get(ctx, pub.ObjectKey); err != nil || !bytes.Equal(data, body) {
		t.Fatalf("visible bytes removed: %v", err)
	}
	if _, err = f.objects.Stat(ctx, session.ObjectKey); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("expired bytes=%v", err)
	}
}
