package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/capacityplan"
	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/preflight"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestMigrationCapacityPreflightConsumesGatewayReadOnlySnapshots(t *testing.T) {
	testMigrationCapacityPreflight(t, repository.NewMemoryStore())
}

func testMigrationCapacityPreflight(t *testing.T, store GatewayStore) {
	t.Helper()
	ctx := context.Background()
	create := func(quota int64) repository.HostedRepository {
		t.Helper()
		repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "capacity-fixture-" + uuid.NewString(), Format: repository.FormatOCI})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.ReplaceRepositoryCapacityQuota(ctx, repo.ID, quota); err != nil {
			t.Fatal(err)
		}
		return repo
	}
	a, b := create(20), create(9)
	body := []byte("0123456789")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	key := "native/oci/blobs/sha256/" + hex.EncodeToString(sum[:])
	upload, err := store.CreateOCIUpload(ctx, repository.OCIUpload{ID: uuid.NewString(), RepositoryID: a.ID, Name: "synthetic", ObjectKey: "synthetic-upload-" + uuid.NewString(), State: "open", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteOCIUpload(ctx, upload.ID, repository.OCIBlob{Digest: digest, ObjectKey: key, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	objects := objectstore.NewMemoryStore()
	if err := objects.Put(ctx, key, body); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeOCIObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		authorize(r, "admin-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s=%d %s", path, w.Code, w.Body)
		}
		return w
	}
	if got := get("/v2/" + a.Name + "/synthetic/blobs/" + digest).Body.Bytes(); !bytes.Equal(got, body) {
		t.Fatalf("fixture byte verification=%q", got)
	}
	readCapacity := func(id string) capacityplan.RepositoryCapacity {
		t.Helper()
		var capacity repository.RepositoryCapacity
		if err := json.Unmarshal(get("/api/v2/repositories/"+id+"/capacity").Body.Bytes(), &capacity); err != nil {
			t.Fatal(err)
		}
		return capacityplan.RepositoryCapacity{RepositoryID: capacity.RepositoryID, UsedBytes: &capacity.UsedBytes, QuotaBytes: &capacity.QuotaBytes}
	}
	beforeA, beforeB := readCapacity(a.ID), readCapacity(b.ID)
	zero, size, free := int64(0), int64(len(body)), int64(100)
	now := time.Now().UTC()
	plan := capacityplan.Plan{
		SchemaVersion: 1, InventoryID: "synthetic-gateway-readback", Complete: true, TargetID: "synthetic-gateway",
		References: []capacityplan.Reference{
			{RepositoryID: a.ID, LogicalKey: digest, ObjectKey: key, Digest: digest, Size: &size},
			{RepositoryID: b.ID, LogicalKey: digest, ObjectKey: key, Digest: digest, Size: &size},
		},
		Snapshot: capacityplan.Snapshot{
			TargetID: "synthetic-gateway", ObservedAt: now, ValidUntil: now.Add(time.Minute), StoragePoolID: "synthetic-pool", FreeBytes: &free,
			Repositories: []capacityplan.RepositoryCapacity{beforeA, beforeB},
			Objects:      []capacityplan.Presence{{Key: key, State: "verified", Digest: digest, Size: &size}},
			References: []capacityplan.Membership{
				{RepositoryID: a.ID, Key: digest, State: "verified", Digest: digest, Size: &size},
				{RepositoryID: b.ID, Key: digest, State: "absent"},
			},
		},
		Peak: capacityplan.PeakBudget{StoragePoolID: "synthetic-pool", DownloadBytes: &zero, UploadBytes: &zero, BackupBytes: &zero, RestoreBytes: &zero, HeadroomBytes: &zero},
	}
	input, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(path, input, 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := preflight.RunCLI(ctx, []string{"capacity", "--input", path}, &stdout, &stderr); code != 1 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var report capacityplan.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != capacityplan.Insufficient || report.Storage.Status != capacityplan.Sufficient || *report.Storage.AdditionalBytes != 0 {
		t.Fatalf("read-only plan report=%+v", report)
	}
	results := make(map[string]capacityplan.RepositoryResult)
	for _, result := range report.Repositories {
		results[result.RepositoryID] = result
	}
	if *results[a.ID].AdditionalBytes != 0 || *results[b.ID].AdditionalBytes != size || results[b.ID].Status != capacityplan.Insufficient {
		t.Fatalf("shared byte and quota accounting=%+v", report)
	}
	for _, before := range []capacityplan.RepositoryCapacity{beforeA, beforeB} {
		after := readCapacity(before.RepositoryID)
		if *before.UsedBytes != *after.UsedBytes || *before.QuotaBytes != *after.QuotaBytes {
			t.Fatalf("preflight changed capacity: before=%+v after=%+v", before, after)
		}
	}
}
