package app

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/protocol/cargo"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type interruptedCargoCopyStore struct {
	OCIObjectStore
	interrupt bool
}

func (s *interruptedCargoCopyStore) PutReader(ctx context.Context, key string, reader io.Reader, size int64) error {
	if !s.interrupt {
		return s.OCIObjectStore.PutReader(ctx, key, reader, size)
	}
	s.interrupt = false
	partial, err := io.ReadAll(io.LimitReader(reader, size/2))
	if err != nil {
		return err
	}
	if err := s.Put(ctx, key, partial); err != nil {
		return err
	}
	return errors.New("interrupted Cargo archive copy")
}

type interruptedCargoAdmissionStore struct {
	OCIObjectStore
	stats int
}

func (s *interruptedCargoAdmissionStore) Stat(ctx context.Context, key string) (OCIObjectInfo, error) {
	s.stats++
	if s.stats == 2 {
		return OCIObjectInfo{}, errors.New("interrupted before Cargo metadata commit")
	}
	return s.OCIObjectStore.Stat(ctx, key)
}

func publishCargoDistributionFixture(t *testing.T, store *repository.MemoryStore, objects OCIObjectStore, repo repository.HostedRepository, name, version, marker string) repository.CargoPublication {
	t.Helper()
	payload, _ := cargoC0PublishFixture(t, name, version, name, marker)
	if marker == "other" {
		archiveStart := 8 + int(binary.LittleEndian.Uint32(payload[:4]))
		payload[archiveStart+4] ^= 1 // gzip mtime differs; the crate still extracts identically.
	}
	r := httptest.NewRequest(http.MethodPut, "/cargo/"+repo.Name+"/api/v1/crates/new", bytes.NewReader(payload))
	r.Host = "localhost:8080"
	authorize(r, "admin-secret")
	w := httptest.NewRecorder()
	NewGatewayHandler(Dependencies{NativeCargoObjectStore: objects}, store, TestAdapter{}, testAuthenticator()).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("publish Cargo fixture=%d %s", w.Code, w.Body.String())
	}
	publication, err := store.GetCargoPublication(context.Background(), repo.ID, name, version)
	if err != nil {
		t.Fatal(err)
	}
	return publication
}

func TestCargoPromotionPreservesImmutableIdentityAndYank(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	source, _ := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "cargo-promote-source", Format: repository.FormatCargo})
	target, _ := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "cargo-promote-target", Format: repository.FormatCargo})
	other, _ := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "cargo-promote-other", Format: repository.FormatCargo})
	publication := publishCargoDistributionFixture(t, store, objects, source, "demo", "1.0.0", "source")
	if _, _, err := store.SetCargoYanked(ctx, source.ID, "demo", "1.0.0", true); err != nil {
		t.Fatal(err)
	}
	worker := NativeCargoPromotion{Store: store, Objects: objects}
	if _, _, err := worker.Enqueue(ctx, target.ID, "cargo-promotion", CargoPromotionPayload{SourceRepositoryID: source.ID,
		Name: publication.Name, Version: publication.Version, Digest: publication.Digest}); err != nil {
		t.Fatal(err)
	}
	if err := worker.RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	promoted, err := store.GetCargoPublication(ctx, target.ID, publication.Name, publication.Version)
	if err != nil || !promoted.Yanked || !cargoDistributionEquivalent(promoted, cargoDistributionTarget(publication, target.ID)) {
		t.Fatalf("promoted Cargo publication=%+v err=%v", promoted, err)
	}
	// The target may manage its own mutable yank state after the immutable copy.
	if _, _, err := store.SetCargoYanked(ctx, target.ID, publication.Name, publication.Version, false); err != nil {
		t.Fatal(err)
	}
	if err := publishCargoDistribution(ctx, store, cargoDistributionTarget(publication, target.ID)); err != nil {
		t.Fatalf("exact promotion replay changed target: %v", err)
	}
	if current, err := store.GetCargoPublication(ctx, target.ID, publication.Name, publication.Version); err != nil || current.Yanked {
		t.Fatalf("replay overwrote target yank=%+v err=%v", current, err)
	}
	conflicting := publishCargoDistributionFixture(t, store, objects, other, "demo", "1.0.0", "other")
	if _, _, err := worker.Enqueue(ctx, target.ID, "cargo-promotion-conflict", CargoPromotionPayload{SourceRepositoryID: other.ID,
		Name: conflicting.Name, Version: conflicting.Version, Digest: conflicting.Digest}); err != nil {
		t.Fatal(err)
	}
	if err := worker.RunJobs(ctx, 10); err == nil {
		t.Fatal("conflicting Cargo promotion succeeded")
	}
	if current, err := store.GetCargoPublication(ctx, target.ID, publication.Name, publication.Version); err != nil || current.Digest != publication.Digest {
		t.Fatalf("conflict changed target=%+v err=%v", current, err)
	}
}

func TestCargoReplicationPublishesOnlyAfterVerifiedObject(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	sourceObjects := NewMemoryOCIObjectStore()
	destinationObjects := NewMemoryOCIObjectStore()
	source, _ := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "cargo-repl-source", Format: repository.FormatCargo})
	target, _ := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "cargo-repl-target", Format: repository.FormatCargo})
	publication := publishCargoDistributionFixture(t, store, sourceObjects, source, "demo", "2.0.0", "source")
	plan, _, err := store.CreateReplicationPlan(ctx, repository.ReplicationPlan{ID: uuid.NewString(), SourceRepositoryID: source.ID,
		TargetRepositoryID: target.ID, Format: repository.FormatCargo, Coordinate: publication.Name + "@" + publication.Version,
		Digest: publication.Digest, IdempotencyKey: "cargo-replication"}, []repository.ReplicationCheckpoint{{
		SourceObjectKey: publication.ObjectKey, ObjectKey: publication.ObjectKey, Digest: publication.Digest, Size: publication.Size,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetCargoPublication(ctx, target.ID, "demo", "2.0.0"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("replication plan made crate visible: %v", err)
	}
	archive, err := sourceObjects.Get(ctx, publication.ObjectKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := destinationObjects.Put(ctx, publication.ObjectKey, archive[:len(archive)/2]); err != nil {
		t.Fatal(err)
	}
	worker := CargoReplication{Store: store, Source: sourceObjects, Destination: destinationObjects, ChunkBytes: 8}
	if err := worker.RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if completed, err := store.GetReplicationPlan(ctx, target.ID, plan.ID); err != nil || completed.State != "completed" {
		t.Fatalf("Cargo replication state=%+v err=%v", completed, err)
	}
	replicated, err := store.GetCargoPublication(ctx, target.ID, "demo", "2.0.0")
	if err != nil || replicated.Digest != publication.Digest || !cargoDistributionEquivalent(replicated, cargoDistributionTarget(publication, target.ID)) {
		t.Fatalf("replicated Cargo publication=%+v err=%v", replicated, err)
	}
	stored, err := destinationObjects.Stat(ctx, publication.ObjectKey)
	if err != nil || stored.Digest != publication.Digest || stored.Size != publication.Size {
		t.Fatalf("replicated object=%+v err=%v", stored, err)
	}
}

func TestCargoReplicationRecoversCopyAndAdmissionInterruptions(t *testing.T) {
	for _, scenario := range []struct {
		name string
		wrap func(OCIObjectStore) OCIObjectStore
	}{
		{"copy", func(store OCIObjectStore) OCIObjectStore {
			return &interruptedCargoCopyStore{OCIObjectStore: store, interrupt: true}
		}},
		{"admission", func(store OCIObjectStore) OCIObjectStore {
			return &interruptedCargoAdmissionStore{OCIObjectStore: store}
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx := context.Background()
			store := repository.NewMemoryStore()
			sourceObjects := NewMemoryOCIObjectStore()
			targetObjects := NewMemoryOCIObjectStore()
			source, _ := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "cargo-fault-source", Format: repository.FormatCargo})
			target, _ := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "cargo-fault-target", Format: repository.FormatCargo})
			publication := publishCargoDistributionFixture(t, store, sourceObjects, source, "fault", "1.0.0", "source")
			plan, _, err := store.CreateReplicationPlan(ctx, repository.ReplicationPlan{ID: uuid.NewString(), SourceRepositoryID: source.ID,
				TargetRepositoryID: target.ID, Format: repository.FormatCargo, Coordinate: "fault@1.0.0",
				Digest: publication.Digest, IdempotencyKey: "fault-" + scenario.name}, []repository.ReplicationCheckpoint{{
				SourceObjectKey: publication.ObjectKey, ObjectKey: publication.ObjectKey, Digest: publication.Digest, Size: publication.Size,
			}})
			if err != nil {
				t.Fatal(err)
			}
			worker := CargoReplication{Store: store, Source: sourceObjects, Destination: scenario.wrap(targetObjects)}
			if err := worker.RunJobs(ctx, 1); err != nil {
				t.Fatal(err)
			}
			if _, err := store.GetCargoPublication(ctx, target.ID, "fault", "1.0.0"); !errors.Is(err, repository.ErrNotFound) {
				t.Fatalf("target index was visible after interrupted %s: %v", scenario.name, err)
			}
			if failed, err := store.GetReplicationPlan(ctx, target.ID, plan.ID); err != nil || failed.State != "failed" {
				t.Fatalf("interrupted replication=%+v err=%v", failed, err)
			}
			if err := worker.RunJobs(ctx, 1); err != nil {
				t.Fatal(err)
			}
			if completed, err := store.GetReplicationPlan(ctx, target.ID, plan.ID); err != nil || completed.State != "completed" {
				t.Fatalf("replayed replication=%+v err=%v", completed, err)
			}
			if published, err := store.GetCargoPublication(ctx, target.ID, "fault", "1.0.0"); err != nil || published.Digest != publication.Digest {
				t.Fatalf("replayed target=%+v err=%v", published, err)
			}
			if object, err := targetObjects.Stat(ctx, publication.ObjectKey); err != nil || object.Digest != publication.Digest {
				t.Fatalf("replayed object=%+v err=%v", object, err)
			}
		})
	}
}

func TestCargoDistributionManagementAPI(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	source, _ := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "cargo-dist-api-source", Format: repository.FormatCargo})
	promotionTarget, _ := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "cargo-dist-api-promo", Format: repository.FormatCargo})
	replicationTarget, _ := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "cargo-dist-api-repl", Format: repository.FormatCargo})
	publication := publishCargoDistributionFixture(t, store, objects, source, "demo", "3.0.0", "source")
	handler := NewGatewayHandler(Dependencies{NativeCargoObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	post := func(path, targetID, key, digest string) *httptest.ResponseRecorder {
		t.Helper()
		body := `{"targetRepositoryId":"` + targetID + `","coordinate":"demo@3.0.0","digest":"` + digest + `"}`
		r := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+source.ID+path, strings.NewReader(body))
		authorize(r, "admin-secret")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := post("/promotions", promotionTarget.ID, "cargo-api-promo", publication.Digest); w.Code != http.StatusAccepted {
		t.Fatalf("Cargo promotion API=%d %s", w.Code, w.Body.String())
	}
	if w := post("/replications", replicationTarget.ID, "cargo-api-repl", publication.Digest); w.Code != http.StatusAccepted {
		t.Fatalf("Cargo replication API=%d %s", w.Code, w.Body.String())
	}
	if w := post("/promotions", promotionTarget.ID, "cargo-api-wrong", "sha256:"+strings.Repeat("f", 64)); w.Code != http.StatusNotFound {
		t.Fatalf("wrong Cargo digest accepted=%d %s", w.Code, w.Body.String())
	}
	if err := (NativeCargoPromotion{Store: store, Objects: objects}).RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if err := (CargoReplication{Store: store, Source: objects, Destination: objects}).RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	for _, target := range []repository.HostedRepository{promotionTarget, replicationTarget} {
		if copy, err := store.GetCargoPublication(ctx, target.ID, "demo", "3.0.0"); err != nil || copy.Digest != publication.Digest {
			t.Fatalf("distribution target %s=%+v err=%v", target.Name, copy, err)
		}
	}
}

func TestCargoDistributionRequiresReadableSameRegistryDependencies(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	target, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "cargo-dependency-target", Format: repository.FormatCargo})
	if err != nil {
		t.Fatal(err)
	}
	row, err := json.Marshal(cargo.IndexEntry{Dependencies: []cargo.IndexDependency{{Name: "helper", Requirement: "^1.0"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := cargoTargetDependenciesReachable(ctx, store, target.ID, row); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("missing target dependency accepted: %v", err)
	}
	dependency := publishCargoDistributionFixture(t, store, objects, target, "helper", "1.2.0", "helper")
	if err := cargoTargetDependenciesReachable(ctx, store, target.ID, row); err != nil {
		t.Fatalf("compatible target dependency rejected: %v", err)
	}
	if _, _, err := store.SetCargoYanked(ctx, target.ID, "helper", "1.2.0", true); err != nil {
		t.Fatal(err)
	}
	if err := cargoTargetDependenciesReachable(ctx, store, target.ID, row); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("yanked target dependency accepted: %v", err)
	}
	if _, _, err := store.SetCargoYanked(ctx, target.ID, "helper", "1.2.0", false); err != nil {
		t.Fatal(err)
	}
	enableQuarantineReadPolicy(t, store, target.ID)
	if _, err := store.ReplaceArtifactQuarantine(ctx, repository.ArtifactQuarantine{RepositoryID: target.ID,
		Format: repository.FormatCargo, Coordinate: "helper@1.2.0", Digest: dependency.Digest,
		State: repository.ArtifactQuarantineStateQuarantined, Reason: "review", UpdatedBy: "operator"}, "0"); err != nil {
		t.Fatal(err)
	}
	if err := cargoTargetDependenciesReachable(ctx, store, target.ID, row); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("quarantined target dependency accepted: %v", err)
	}
	external := "https://registry.example/index"
	externalRow, _ := json.Marshal(cargo.IndexEntry{Dependencies: []cargo.IndexDependency{{Name: "elsewhere", Requirement: "^2.0", Registry: &external}}})
	if err := cargoTargetDependenciesReachable(ctx, store, target.ID, externalRow); err != nil {
		t.Fatalf("external registry dependency was treated as local: %v", err)
	}
}
