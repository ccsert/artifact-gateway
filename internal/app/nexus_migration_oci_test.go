package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/nexusmigration"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/ocigraph"
	ociprotocol "github.com/ccsert/artifact-gateway/internal/protocol/oci"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/testsupport/ocifixture"
	"github.com/google/uuid"
)

type nexusOCIContractStore interface {
	GatewayStore
	NexusOCIMigrationStore
	repository.NativeOCIStore
	repository.HostedRepositoryStore
	repository.RepositoryCapacityStore
	repository.LifecycleJobStore
	repository.ArtifactQuarantineStore
}

func nexusOCIInventory(repo repository.HostedRepository, fixture ocifixture.Fixture) nexusmigration.Entry {
	e := nexusmigration.Entry{ID: uuid.NewString(), BatchID: uuid.NewString(), SourceRepository: "source", TargetRepositoryID: repo.ID, Format: "oci", Identity: fixture.Name + ":" + fixture.Reference, Actor: "synthetic-migration-admin", State: "pending"}
	for source, body := range fixture.Bytes {
		path := fixture.Name + "/manifests/sha256:" + fixture.Digests[source]
		if strings.Contains(source, "/blobs/") {
			path = "blobs/sha256:" + fixture.Digests[source]
		}
		e.Assets = append(e.Assets, nexusmigration.Asset{SourcePath: source, TargetPath: path, Size: int64(len(body)), SHA256: fixture.Digests[source], Validator: "sha256:" + fixture.Digests[source], MediaType: fixture.MediaTypes[source]})
	}
	return e
}

func nexusOCIStage(t *testing.T, ctx context.Context, p *NexusOCIMigrationPublication, e nexusmigration.Entry, f ocifixture.Fixture) {
	t.Helper()
	if _, err := p.Begin(ctx, e); err != nil {
		t.Fatal(err)
	}
	for _, a := range e.Assets {
		if err := p.Stage(ctx, e, a, bytes.NewReader(f.Bytes[a.SourcePath])); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNexusOCICompletePublicationAndNativeReadback(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := objectstore.NewMemoryStore()
	exerciseNexusOCICompletePublication(t, store, objects)
}

func exerciseNexusOCICompletePublication(t *testing.T, store nexusOCIContractStore, objects objectstore.Store) {
	t.Helper()
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "nexus-oci-" + uuid.NewString()[:8], Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	f := ocifixture.New(t.Name() + uuid.NewString())
	e := nexusOCIInventory(repo, f)
	p := NewNexusOCIMigrationPublication(store, objects)
	locked, release, err := p.Lock(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	nexusOCIStage(t, locked, p, e, f)
	if err = p.Publish(locked, e); err == nil || err.Error() != "target_verification_required" {
		t.Fatalf("publish before verification=%v", err)
	}
	if state, err := p.Readback(ctx, e); err != nil || state != nexusmigration.Absent {
		t.Fatalf("staging became visible: %s %v", state, err)
	}
	if err = p.Verify(locked, e); err != nil {
		t.Fatal(err)
	}
	if err = p.Publish(locked, e); err != nil {
		t.Fatal(err)
	}
	if state, err := p.Readback(ctx, e); err != nil || state != nexusmigration.Identical {
		t.Fatalf("published=%s %v", state, err)
	}
	capacity, err := store.GetRepositoryCapacity(ctx, repo.ID)
	if err != nil || capacity.ObjectCount != 6 {
		t.Fatalf("closure capacity=%+v %v", capacity, err)
	}
	handler := newTestGateway(t, GatewayOptions{Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatOCI: objects, repository.FormatRaw: objects}})
	for _, asset := range e.Assets {
		path := "/v2/" + repo.Name + "/" + asset.TargetPath
		if strings.HasPrefix(asset.TargetPath, "blobs/") {
			path = "/v2/" + repo.Name + "/" + f.Name + "/" + asset.TargetPath
		}
		r := httptest.NewRequest(http.MethodGet, path, nil)
		authorize(r, "admin-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), f.Bytes[asset.SourcePath]) || w.Header().Get("Docker-Content-Digest") != "sha256:"+asset.SHA256 {
			t.Fatalf("native asset=%s code=%d bodydigest=%s", path, w.Code, ocigraph.Digest(w.Body.Bytes()))
		}
	}
	if err = p.Publish(locked, e); err != nil {
		t.Fatalf("idempotent commit replay=%v", err)
	}
	for _, asset := range e.Assets {
		if !strings.HasPrefix(asset.TargetPath, "blobs/") {
			continue
		}
		planned, _ := p.NativeAssets(e)
		var key string
		for _, a := range planned {
			if a.Path == asset.TargetPath {
				key = a.ObjectKey
			}
		}
		want := f.Bytes[asset.SourcePath]
		bad := bytes.Clone(want)
		bad[0] ^= 0xff
		if err = objects.Put(ctx, key, bad); err != nil {
			t.Fatal(err)
		}
		if state, err := p.Readback(ctx, e); err != nil || state != nexusmigration.Conflict {
			t.Fatalf("same-size corrupted target was verified: %s %v", state, err)
		}
		if err = objects.PutVerifiedReader(ctx, key, bytes.NewReader(want), asset.Size, "sha256:"+asset.SHA256); err != nil {
			t.Fatal(err)
		}
		break
	}
	partial := e
	partial.Assets = append([]nexusmigration.Asset(nil), e.Assets...)
	for i, a := range partial.Assets {
		if strings.HasPrefix(a.TargetPath, "blobs/") {
			partial.Assets = append(partial.Assets[:i], partial.Assets[i+1:]...)
			break
		}
	}
	if state, err := p.Readback(ctx, partial); err != nil || state != nexusmigration.Conflict {
		t.Fatalf("partial closure marked identical: %s %v", state, err)
	}
	release()
	if err = p.Verify(locked, e); err == nil {
		t.Fatal("released lock used for later verification")
	}
}

func TestNexusOCIRejectsWrongBytesIncompleteGraphAndNilPorts(t *testing.T) {
	for _, mode := range []string{"wrong_bytes", "missing_member", "unverified", "wrong_entry"} {
		t.Run(mode, func(t *testing.T) {
			store := repository.NewMemoryStore()
			objects := objectstore.NewMemoryStore()
			repo, _ := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "oci-" + uuid.NewString()[:8], Format: repository.FormatOCI})
			f := ocifixture.New(t.Name())
			e := nexusOCIInventory(repo, f)
			p := NewNexusOCIMigrationPublication(store, objects)
			if mode == "missing_member" {
				for i, a := range e.Assets {
					if strings.HasPrefix(a.TargetPath, "blobs/") {
						e.Assets = append(e.Assets[:i], e.Assets[i+1:]...)
						break
					}
				}
			}
			ctx, release, err := p.Lock(context.Background(), e)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			if mode == "wrong_bytes" {
				a := e.Assets[0]
				err := p.Stage(ctx, e, a, bytes.NewReader(bytes.Repeat([]byte("x"), int(a.Size))))
				if err == nil || err.Error() != "source_bytes_mismatch" {
					t.Fatalf("wrong bytes=%v", err)
				}
				return
			}
			nexusOCIStage(t, ctx, p, e, f)
			if mode == "wrong_entry" {
				changed := e
				changed.Identity = f.Name + ":different"
				if err := p.Verify(ctx, changed); err == nil {
					t.Fatal("lock identity drift admitted")
				}
				return
			}
			if mode == "missing_member" {
				if err := p.Verify(ctx, e); err == nil {
					t.Fatal("incomplete graph verified")
				}
			}
			if err := p.Publish(ctx, e); err == nil {
				t.Fatal("incomplete or unverified graph published")
			}
			if _, err := store.GetOCIManifest(ctx, repo.ID, f.Name, f.Reference); !errors.Is(err, repository.ErrNotFound) {
				t.Fatalf("failed graph visible=%v", err)
			}
		})
	}
	var nilStore *repository.MemoryStore
	if _, _, err := NewNexusOCIMigrationPublication(nilStore, objectstore.NewMemoryStore()).Lock(context.Background(), nexusmigration.Entry{}); err == nil {
		t.Fatal("typed nil store admitted")
	}
	var nilObjects *objectstore.MemoryStore
	if _, _, err := NewNexusOCIMigrationPublication(repository.NewMemoryStore(), nilObjects).Lock(context.Background(), nexusmigration.Entry{}); err == nil {
		t.Fatal("typed nil objects admitted")
	}
}

func TestNexusOCITagConflictQuotaAndGovernanceAreAtomic(t *testing.T) {
	for _, mode := range []string{"tag_conflict", "quota", "tombstone", "quarantine"} {
		t.Run(mode, func(t *testing.T) {
			store := repository.NewMemoryStore()
			objects := objectstore.NewMemoryStore()
			ctx := context.Background()
			repo, _ := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "oci-" + uuid.NewString()[:8], Format: repository.FormatOCI})
			f := ocifixture.New(t.Name())
			e := nexusOCIInventory(repo, f)
			p := NewNexusOCIMigrationPublication(store, objects)
			if mode == "tag_conflict" {
				_, err := store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: repo.ID, Name: f.Name, Digest: "sha256:" + strings.Repeat("a", 64), ObjectKey: "existing", MediaType: ocigraph.OCIManifest, Size: 1}, f.Reference)
				if err != nil {
					t.Fatal(err)
				}
			}
			locked, release, err := p.Lock(ctx, e)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			nexusOCIStage(t, locked, p, e, f)
			if err = p.Verify(locked, e); err != nil {
				t.Fatal(err)
			}
			if mode == "quota" {
				if _, err = store.ReplaceRepositoryCapacityQuota(ctx, repo.ID, 1); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "tombstone" {
				_, _ = store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: repo.ID, Name: f.Name, Digest: f.Root, ObjectKey: "old-root", MediaType: ocigraph.OCIIndex, Size: 1}, f.Root)
				if err = store.DeleteOCIManifest(ctx, repo.ID, f.Name, f.Root); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "quarantine" {
				_, err = store.ReplaceArtifactQuarantine(ctx, repository.ArtifactQuarantine{RepositoryID: repo.ID, Format: repository.FormatOCI, Coordinate: f.Name, Digest: f.Root, State: repository.ArtifactQuarantineStateQuarantined, Reason: "fixture", UpdatedBy: "security"}, "0")
				if err != nil {
					t.Fatal(err)
				}
			}
			if err = p.Publish(locked, e); err == nil {
				t.Fatalf("%s admitted", mode)
			}
			if _, err = store.GetOCIManifest(ctx, repo.ID, f.Name, f.Root); !errors.Is(err, repository.ErrNotFound) {
				t.Fatalf("failed closure partially visible %v", err)
			}
			for _, a := range e.Assets {
				if strings.HasPrefix(a.TargetPath, "blobs/") {
					if _, err := store.GetOCIBlob(ctx, repo.ID, "sha256:"+a.SHA256); !errors.Is(err, repository.ErrNotFound) {
						t.Fatalf("failed closure mounted blob: %v", err)
					}
				}
			}
		})
	}
}

func TestNexusOCIGarbageCollectionAndImportSerialize(t *testing.T) {
	for _, mode := range []string{"collector_wins", "publication_wins", "published_stage_failure"} {
		t.Run(mode, func(t *testing.T) {
			exerciseNexusOCIGarbageCollection(t, repository.NewMemoryStore(), objectstore.NewMemoryStore(), mode)
		})
	}
}

func exerciseNexusOCIGarbageCollection(t *testing.T, store nexusOCIContractStore, objects objectstore.Store, mode string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "oci-gc-" + uuid.NewString()[:8], Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	f := ocifixture.New(t.Name() + uuid.NewString())
	e := nexusOCIInventory(repo, f)
	p := NewNexusOCIMigrationPublication(store, objects)
	assets, _ := p.NativeAssets(e)
	target := assets[0]
	for _, a := range assets {
		if strings.HasPrefix(a.Path, "blobs/") {
			target = a
			break
		}
	}
	var source []byte
	for _, a := range e.Assets {
		if a.TargetPath == target.Path {
			source = f.Bytes[a.SourcePath]
		}
	}
	if err = store.StageOCIObjectIntent(ctx, repository.OCIObjectIntent{RepositoryID: repo.ID, ObjectKey: target.ObjectKey, Digest: target.Digest, Size: target.Size}); err != nil {
		t.Fatal(err)
	}
	if err = objects.PutVerifiedReader(ctx, target.ObjectKey, bytes.NewReader(source), target.Size, target.Digest); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"format": "oci", "objectKey": target.ObjectKey})
	if _, _, err = store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: repo.ID, Kind: repository.LifecycleJobReclaim, IdempotencyKey: "owned-oci-reclaim:" + uuid.NewString(), Payload: payload}); err != nil {
		t.Fatal(err)
	}
	gate := &conanCollectionDeleteBarrier{ObjectStore: objects, target: target.ObjectKey, entered: make(chan struct{}), release: make(chan struct{})}
	defer func() {
		select {
		case <-gate.release:
		default:
			close(gate.release)
		}
	}()
	var locked context.Context
	var release func()
	if mode != "collector_wins" {
		locked, release, err = p.Lock(ctx, e)
		if err != nil {
			t.Fatal(err)
		}
		nexusOCIStage(t, locked, p, e, f)
		if err = p.Verify(locked, e); err != nil {
			t.Fatal(err)
		}
		if err = p.Publish(locked, e); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan error, 1)
	go func() { done <- (ociprotocol.NativeMaintenance{Store: store, Objects: gate}).RunReclaimJobs(ctx, 1) }()
	if mode == "collector_wins" {
		select {
		case <-gate.entered:
		case <-ctx.Done():
			t.Fatal("collector did not enter physical delete")
		}
		lockDone := make(chan error, 1)
		go func() { var err error; locked, release, err = p.Lock(ctx, e); lockDone <- err }()
		select {
		case err := <-lockDone:
			if err == nil {
				release()
			}
			t.Fatalf("import crossed active collector lock %v", err)
		case <-time.After(40 * time.Millisecond):
		}
		close(gate.release)
		if err = <-done; err != nil {
			t.Fatal(err)
		}
		if err = <-lockDone; err != nil {
			t.Fatal(err)
		}
		nexusOCIStage(t, locked, p, e, f)
		if err = p.Verify(locked, e); err != nil {
			t.Fatal(err)
		}
		if err = p.Publish(locked, e); err != nil {
			t.Fatal(err)
		}
		release()
	} else {
		if mode == "published_stage_failure" {
			var a nexusmigration.Asset
			for _, candidate := range e.Assets {
				if candidate.TargetPath == target.Path {
					a = candidate
					break
				}
			}
			if err = p.Stage(locked, e, a, bytes.NewReader(f.Bytes[a.SourcePath])); err != nil {
				t.Fatal(err)
			}
		}
		release()
		select {
		case err = <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-gate.entered:
			t.Fatal("old collector attempted deleting published closure")
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if state, err := p.Readback(ctx, e); err != nil || state != nexusmigration.Identical {
		t.Fatalf("winner lost complete bytes: %s %v", state, err)
	}
}
