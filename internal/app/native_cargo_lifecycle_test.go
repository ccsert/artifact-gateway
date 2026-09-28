package app

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestCargoTombstoneRestoreAndGroupOwner(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	first, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "cargo-life-a", Format: repository.FormatCargo})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "cargo-life-b", Format: repository.FormatCargo})
	if err != nil {
		t.Fatal(err)
	}
	group := createV2Group(t, store, "cargo-life-group", repository.FormatCargo,
		repository.GroupMember{RepositoryID: first.ID}, repository.GroupMember{RepositoryID: second.ID})
	handler := NewGatewayHandler(Dependencies{NativeCargoObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Host = "localhost:8080"
		authorize(r, "admin-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	payload, archive := cargoC0PublishFixture(t, "demo", "1.0.0", "demo")
	for _, repo := range []repository.HostedRepository{first, second} {
		r := httptest.NewRequest(http.MethodPut, "/cargo/"+repo.Name+"/api/v1/crates/new", bytes.NewReader(payload))
		r.Host = "localhost:8080"
		authorize(r, "admin-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("publish %s=%d %s", repo.Name, w.Code, w.Body.String())
		}
	}
	groupIndex := "/cargo/" + group.Name + "/de/mo/demo"
	groupDownload := "/cargo/" + group.Name + "/api/v1/crates/demo/1.0.0/download"
	if w := request(http.MethodGet, groupIndex, ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"vers":"1.0.0"`) {
		t.Fatalf("group baseline index=%d %s", w.Code, w.Body.String())
	}
	owner, err := store.GetCargoGroupVersion(ctx, group.ID, "demo", "1.0.0")
	if err != nil || owner.SourceRepositoryID != first.ID {
		t.Fatalf("group owner=%+v err=%v", owner, err)
	}
	management := "/api/v2/repositories/" + first.ID
	if w := request(http.MethodPost, management+"/tombstones", `{"coordinate":"demo@1.0.0"}`); w.Code != http.StatusNoContent {
		t.Fatalf("tombstone API=%d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{"/cargo/" + first.Name + "/de/mo/demo", groupIndex} {
		w := request(http.MethodGet, path, "")
		if w.Code == http.StatusOK && strings.Contains(w.Body.String(), `"vers":"1.0.0"`) {
			t.Fatalf("tombstoned version in %s: %s", path, w.Body.String())
		}
	}
	for _, path := range []string{"/cargo/" + first.Name + "/api/v1/crates/demo/1.0.0/download", groupDownload} {
		if w := request(http.MethodGet, path, ""); w.Code != http.StatusNotFound {
			t.Fatalf("tombstoned download %s=%d %s", path, w.Code, w.Body.String())
		}
	}
	if w := request(http.MethodGet, "/cargo/"+second.Name+"/api/v1/crates/demo/1.0.0/download", ""); w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), archive) {
		t.Fatalf("other Hosted member changed=%d", w.Code)
	}
	if w := request(http.MethodPost, management+"/restore", `{"coordinate":"demo@1.0.0"}`); w.Code != http.StatusNoContent {
		t.Fatalf("restore API=%d %s", w.Code, w.Body.String())
	}
	if w := request(http.MethodGet, groupDownload, ""); w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), archive) {
		t.Fatalf("restored group download=%d %s", w.Code, w.Body.String())
	}
	if w := request(http.MethodPost, management+"/tombstones", `{"coordinate":"demo@1.0.0"}`); w.Code != http.StatusNoContent {
		t.Fatalf("second tombstone=%d %s", w.Code, w.Body.String())
	}
	maintenance := NativeCargoMaintenance{Store: store, Objects: objects, RecoveryWindow: time.Hour,
		Now: func() time.Time { return time.Now().UTC().Add(2 * time.Hour) }}
	ready, err := store.ListReclaimableCargoObjects(ctx, time.Now().UTC().Add(time.Hour), 10, "")
	if err != nil || len(ready) != 1 {
		t.Fatalf("expected one reclaimable Cargo object: %+v err=%v", ready, err)
	}
	if err := maintenance.Collect(ctx); err != nil {
		t.Fatal(err)
	}
	if err := maintenance.RunReclaimJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	publication, err := store.GetCargoPublication(ctx, second.ID, "demo", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := objects.Stat(ctx, publication.ObjectKey); err != nil {
		t.Fatalf("shared object was removed: %v", err)
	}
	if w := request(http.MethodPost, management+"/restore", `{"coordinate":"demo@1.0.0"}`); w.Code != http.StatusConflict {
		t.Fatalf("collected restore=%d %s", w.Code, w.Body.String())
	}
	if _, err := store.GetCargoPublication(ctx, first.ID, "demo", "1.0.0"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("collected owner reappeared: %v", err)
	}
	if _, err := objects.Stat(ctx, publication.ObjectKey); errors.Is(err, objectstore.ErrNotFound) {
		t.Fatal("reclaim removed an object with a live reference")
	}
}

func TestCargoRetentionRequiresExplicitPolicyAndPreservesYank(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "cargo-retain", Format: repository.FormatCargo})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := store.GetRepositoryRetentionPolicy(ctx, repo.ID)
	if err != nil || policy.Enabled {
		t.Fatalf("Cargo retention enabled by default: %+v err=%v", policy, err)
	}
	retention := NativeRepositoryRetention{Store: store, Now: func() time.Time { return time.Now().UTC() }}
	if err := retention.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	if jobs, err := store.ListLifecycleJobs(ctx, repo.ID, 10); err != nil || len(jobs) != 0 {
		t.Fatalf("disabled Cargo retention scheduled jobs=%+v err=%v", jobs, err)
	}
	handler := NewGatewayHandler(Dependencies{NativeCargoObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator())
	for _, version := range []string{"1.0.0", "1.1.0", "1.2.0"} {
		payload, _ := cargoC0PublishFixture(t, "demo", version, "demo")
		r := httptest.NewRequest(http.MethodPut, "/cargo/"+repo.Name+"/api/v1/crates/new", bytes.NewReader(payload))
		r.Host = "localhost:8080"
		authorize(r, "admin-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("publish %s=%d %s", version, w.Code, w.Body.String())
		}
	}
	if _, _, err := store.SetCargoYanked(ctx, repo.ID, "demo", "1.2.0", true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetCargoPublication(ctx, repo.ID, "demo", "1.2.0"); err != nil {
		t.Fatalf("yank deleted archive: %v", err)
	}
	retentionPolicyForTest(t, store, repo.ID, repository.RepositoryRetentionPolicy{KeepDays: 365, MinimumVersions: 1, MaximumVersions: 2})
	candidates, err := retention.PlanRepositoryDetailed(ctx, repo.ID, repository.FormatCargo)
	if err != nil || len(candidates) != 1 || candidates[0].Coordinate != "demo@1.0.0" || !containsRetentionReason(candidates[0].Reasons, "maximum_versions") {
		t.Fatalf("Cargo retention preview=%+v err=%v", candidates, err)
	}
	if _, err := store.GetCargoPublication(ctx, repo.ID, "demo", "1.0.0"); err != nil {
		t.Fatalf("preview changed visible version: %v", err)
	}
	if _, _, err := retention.EnqueueRepository(ctx, repo.ID, "cargo-retain-explicit"); err != nil {
		t.Fatal(err)
	}
	if err := retention.RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetCargoPublication(ctx, repo.ID, "demo", "1.0.0"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("retained oldest version remains visible: %v", err)
	}
	if _, err := store.GetCargoPublication(ctx, repo.ID, "demo", "1.2.0"); err != nil {
		t.Fatalf("yanked newest version was deleted: %v", err)
	}
}
