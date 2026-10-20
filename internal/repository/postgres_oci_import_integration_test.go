//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresOCIImportClosureLocksShareOneConnection(t *testing.T) {
	store, primary, locks := newOCILockTestStore(t, 1, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	closure := OCIImportClosure{RepositoryID: uuid.NewString(), Name: "team/widget", Reference: "release", Manifests: []OCIImportManifest{{OCIManifest: OCIManifest{Digest: "sha256:root", ObjectKey: "owned-oci-import-" + uuid.NewString()}}}, Blobs: []OCIBlob{{Digest: "sha256:blob", ObjectKey: "owned-oci-import-" + uuid.NewString()}}}
	_, release, err := store.LockOCIImport(ctx, closure)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if locks.Stats().InUse != 1 {
		t.Fatalf("closure objects borrowed %d lock sessions", locks.Stats().InUse)
	}
	if err = primary.PingContext(ctx); err != nil {
		t.Fatalf("metadata pool exhausted inside full closure: %v", err)
	}
	release()
	if locks.Stats().InUse != 0 {
		t.Fatal("closure lock session leaked")
	}
}
