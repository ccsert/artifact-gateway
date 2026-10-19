package app

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestConanNativeManifestOnlyReplay(t *testing.T) {
	store := repository.NewMemoryStore()
	exerciseConanNativeManifestOnlyReplay(t, conanClientBackend{store, store, objectstore.NewMemoryStore()})
}

func exerciseConanNativeManifestOnlyReplay(t *testing.T, backend conanClientBackend) {
	t.Helper()
	for _, kind := range []string{"recipe", "package"} {
		t.Run(kind, func(t *testing.T) {
			for _, actor := range []string{"build-agent", "other-writer"} {
				t.Run("lost_ack_"+actor, func(t *testing.T) {
					f := newConanNativeReplayFixture(t, backend, kind)
					// Rebuild the Gateway before retrying only the final PUT. The
					// durable published tree, not an in-process session, is the oracle.
					f.handler = f.gateway(t, backend.publications)
					w := f.request(t, http.MethodPut, "conanmanifest.txt", actor, f.bodies["conanmanifest.txt"])
					f.assertCommitted(t, w, actor, true)
					f.assertBytes(t)
				})
			}
			t.Run("different_manifest_cannot_replace_immutable_bytes", func(t *testing.T) {
				f := newConanNativeReplayFixture(t, backend, kind)
				changed := bytes.Replace(f.bodies["conanmanifest.txt"], []byte("1700000000"), []byte("1700000001"), 1)
				w := f.request(t, http.MethodPut, "conanmanifest.txt", "other-writer", changed)
				if w.Code != http.StatusConflict {
					t.Fatalf("different immutable manifest=%d %s", w.Code, w.Body.String())
				}
				f.assertBytes(t)
				f.assertNoPublicationAudit(t, w.Header().Get("X-Artifact-Gateway-Publish-Session"))
			})
			t.Run("concurrent_bad_put_is_not_repaired_over", func(t *testing.T) {
				f := newConanNativeReplayFixture(t, backend, kind)
				barrier := &conanReplayBeginBarrier{ConanPublicationStore: backend.publications, began: make(chan struct{}), proceed: make(chan struct{}, 1)}
				f.handler = f.gateway(t, barrier)
				defer close(barrier.proceed)
				result := make(chan *httptest.ResponseRecorder, 1)
				go func() {
					result <- f.request(t, http.MethodPut, "conanmanifest.txt", "build-agent", f.bodies["conanmanifest.txt"])
				}()
				select {
				case <-barrier.began:
				case <-time.After(5 * time.Second):
					t.Fatal("retry did not reach the snapshot barrier")
				}
				name := "conanfile.py"
				if kind == "package" {
					name = "conaninfo.txt"
				}
				bad := f.request(t, http.MethodPut, name, "build-agent", []byte("conflicting concurrent bytes"))
				if bad.Code != http.StatusCreated {
					t.Fatalf("concurrent PUT=%d %s", bad.Code, bad.Body.String())
				}
				barrier.proceed <- struct{}{}
				select {
				case w := <-result:
					if w.Code != http.StatusUnprocessableEntity {
						t.Fatalf("retry hid the concurrent bad upload: %d %s", w.Code, w.Body.String())
					}
					id := w.Header().Get("X-Artifact-Gateway-Publish-Session")
					session, err := backend.store.GetConanPublishSession(context.Background(), id)
					if err != nil || session.State != "open" || id != bad.Header().Get("X-Artifact-Gateway-Publish-Session") {
						t.Fatalf("retry lost the concurrent session: %+v %v", session, err)
					}
					uploads, err := backend.store.ListConanPublishUploads(context.Background(), id)
					if err != nil {
						t.Fatal(err)
					}
					data, err := backend.objects.Get(context.Background(), uploads[name])
					if err != nil || string(data) != "conflicting concurrent bytes" {
						t.Fatalf("retry overwrote the bad pointer: %q %v", data, err)
					}
					f.assertNoPublicationAudit(t, id)
				case <-time.After(5 * time.Second):
					t.Fatal("manifest retry did not finish")
				}
				f.assertBytes(t)
			})
			t.Run("quarantine_still_blocks_replay", func(t *testing.T) {
				f := newConanNativeReplayFixture(t, backend, kind)
				recipe, err := backend.store.GetConanRecipeRevision(context.Background(), f.repo.ID, "pkg/1.0/_/_", f.recipeRevision)
				if err != nil {
					t.Fatal(err)
				}
				_, err = backend.store.ReplaceArtifactQuarantine(context.Background(), repository.ArtifactQuarantine{RepositoryID: f.repo.ID, Format: repository.FormatConan, Coordinate: recipe.Reference + "#" + recipe.Revision, Digest: recipe.Digest, State: repository.ArtifactQuarantineStateQuarantined, Reason: "do not replay governed bytes", UpdatedBy: "security"}, "0")
				if err != nil {
					t.Fatal(err)
				}
				w := f.request(t, http.MethodPut, "conanmanifest.txt", "build-agent", f.bodies["conanmanifest.txt"])
				if w.Code != http.StatusConflict {
					t.Fatalf("quarantined publication replay=%d %s", w.Code, w.Body.String())
				}
				f.assertNoPublicationAudit(t, w.Header().Get("X-Artifact-Gateway-Publish-Session"))
			})
			t.Run("promoted_target_can_replay_its_shared_tree", func(t *testing.T) {
				f := newConanNativeReplayFixture(t, backend, kind)
				ctx := context.Background()
				target, err := backend.store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "conan-retry-target-" + uuid.NewString(), Format: repository.FormatConan})
				if err != nil {
					t.Fatal(err)
				}
				f.grant(t, target.ID)
				recipe, err := backend.store.GetConanRecipeRevision(ctx, f.repo.ID, "pkg/1.0/_/_", f.recipeRevision)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = backend.store.PromoteConanRecipeRevision(ctx, repository.ConanPromotion{SourceRepositoryID: f.repo.ID, TargetRepositoryID: target.ID, Reference: recipe.Reference, Revision: recipe.Revision, Digest: recipe.Digest}); err != nil {
					t.Fatal(err)
				}
				f.base = strings.Replace(f.base, "/conan/"+f.repo.Name+"/", "/conan/"+target.Name+"/", 1)
				f.repo = target
				w := f.request(t, http.MethodPut, "conanmanifest.txt", "other-writer", f.bodies["conanmanifest.txt"])
				f.assertCommitted(t, w, "other-writer", true)
				f.assertBytes(t)
			})
		})
	}
}

type conanNativeReplayFixture struct {
	backend        conanClientBackend
	handler        http.Handler
	repo           repository.HostedRepository
	base           string
	recipeRevision string
	bodies         map[string][]byte
}

func newConanNativeReplayFixture(t *testing.T, backend conanClientBackend, kind string) conanNativeReplayFixture {
	t.Helper()
	f := conanNativeReplayFixture{backend: backend}
	var err error
	f.repo, err = backend.store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "conan-retry-" + uuid.NewString(), Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	f.grant(t, f.repo.ID)
	f.handler = f.gateway(t, backend.publications)
	entries := fmt.Sprintf("conanfile.py: %x\n", md5.Sum([]byte("recipe")))
	f.recipeRevision = fmt.Sprintf("%x", md5.Sum([]byte(entries)))
	f.base = "/conan/" + f.repo.Name + "/v2/conans/pkg/1.0/_/_/revisions/" + f.recipeRevision + "/files/"
	f.bodies = map[string][]byte{"conanfile.py": []byte("recipe"), "conanmanifest.txt": []byte("1700000000\n" + entries)}
	f.publish(t)
	if kind == "package" {
		entries = fmt.Sprintf("conaninfo.txt: %x\npackage.txt: %x\n", md5.Sum(nil), md5.Sum([]byte("package binary")))
		f.base = strings.TrimSuffix(f.base, "/files/") + "/packages/da39a3ee5e6b4b0d3255bfef95601890afd80709/revisions/" + fmt.Sprintf("%x", md5.Sum([]byte(entries))) + "/files/"
		f.bodies = map[string][]byte{"conaninfo.txt": {}, "conan_package.tgz": conanPackageArchive(), "conanmanifest.txt": []byte("1700000000\n" + entries)}
		f.publish(t)
	}
	return f
}

func (f conanNativeReplayFixture) grant(t *testing.T, id string) {
	t.Helper()
	if _, err := f.backend.store.ReplaceRepositoryGrants(context.Background(), id, []repository.RepositoryGrant{{Principal: "build-agent", Scopes: []string{"repositories:write"}}, {Principal: "other-writer", Scopes: []string{"repositories:write"}}, {Principal: "reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
}

func (f conanNativeReplayFixture) gateway(t *testing.T, publications repository.ConanPublicationStore) http.Handler {
	t.Helper()
	return newTestGateway(t, GatewayOptions{Store: f.backend.store, ConanPublications: publications, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatConan: f.backend.objects}})
}

func (f conanNativeReplayFixture) request(t *testing.T, method, name, actor string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r := httptest.NewRequest(method, f.base+name, bytes.NewReader(body)).WithContext(ctx)
	r.SetBasicAuth(actor, "resolver-secret")
	if method == http.MethodPut {
		r.Header.Set("X-Checksum-Sha1", fmt.Sprintf("%x", sha1.Sum(body)))
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

func (f conanNativeReplayFixture) publish(t *testing.T) {
	t.Helper()
	for name, body := range f.bodies {
		if name != "conanmanifest.txt" {
			if w := f.request(t, http.MethodPut, name, "build-agent", body); w.Code != http.StatusCreated {
				t.Fatalf("initial asset %s=%d %s", name, w.Code, w.Body.String())
			}
		}
	}
	w := f.request(t, http.MethodPut, "conanmanifest.txt", "build-agent", f.bodies["conanmanifest.txt"])
	f.assertCommitted(t, w, "build-agent", false)
}

func (f conanNativeReplayFixture) assertCommitted(t *testing.T, w *httptest.ResponseRecorder, actor string, replay bool) {
	t.Helper()
	if w.Code != http.StatusCreated || w.Header().Get("X-Artifact-Gateway-Publish-State") != "committed" {
		t.Fatalf("manifest PUT=%d state=%q %s", w.Code, w.Header().Get("X-Artifact-Gateway-Publish-State"), w.Body.String())
	}
	id := w.Header().Get("X-Artifact-Gateway-Publish-Session")
	session, err := f.backend.store.GetConanPublishSession(context.Background(), id)
	if err != nil || session.State != "committed" || session.Publisher != actor {
		t.Fatalf("durable committed session=%+v %v", session, err)
	}
	audits, err := f.backend.store.ListAudits(context.Background(), repository.AuditQuery{Repository: f.repo.Name, Operation: "publish", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, audit := range audits {
		if audit.Evidence["publicationSessionId"] == id {
			found++
			if audit.Actor != actor || audit.Outcome != repository.AuditResolved || audit.Status != http.StatusCreated || audit.Evidence["publicationState"] != "committed" || audit.Evidence["replay"] != fmt.Sprint(replay) {
				t.Errorf("successful publication audit=%+v", audit)
			}
		}
	}
	if found != 1 {
		t.Fatalf("committed request successful audits=%d", found)
	}
}

func (f conanNativeReplayFixture) assertNoPublicationAudit(t *testing.T, id string) {
	t.Helper()
	audits, err := f.backend.store.ListAudits(context.Background(), repository.AuditQuery{Repository: f.repo.Name, Operation: "publish", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, audit := range audits {
		if audit.Evidence["publicationSessionId"] == id {
			t.Fatalf("failed retry recorded publication: %+v", audit)
		}
	}
}

func (f conanNativeReplayFixture) assertBytes(t *testing.T) {
	t.Helper()
	for name, expected := range f.bodies {
		w := f.request(t, http.MethodGet, name, "reader", nil)
		if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), expected) {
			t.Errorf("published bytes %s=%d %q", name, w.Code, w.Body.String())
		}
	}
}

type conanReplayBeginBarrier struct {
	repository.ConanPublicationStore
	first   atomic.Bool
	began   chan struct{}
	proceed chan struct{}
}

func (b *conanReplayBeginBarrier) BeginConanNativeSession(ctx context.Context, incoming repository.ConanPublishSession) (repository.ConanPublishSession, error) {
	session, err := b.ConanPublicationStore.BeginConanNativeSession(ctx, incoming)
	if err == nil && b.first.CompareAndSwap(false, true) {
		close(b.began)
		select {
		case <-b.proceed:
		case <-ctx.Done():
			return repository.ConanPublishSession{}, ctx.Err()
		}
	}
	return session, err
}
