package nuget

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/uploadspool"
)

type failNuGetCollectionMarker struct {
	repository.NativeNuGetStore
	fail bool
}

func (s *failNuGetCollectionMarker) MarkNuGetSessionCollected(ctx context.Context, id, key string) error {
	if s.fail {
		s.fail = false
		return errors.New("injected lost collection marker")
	}
	return s.NativeNuGetStore.MarkNuGetSessionCollected(ctx, id, key)
}

func TestNuGetExpiredCollectionRetriesWithoutDeletingReplacement(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	body := packageFixture(t, "Expired", "1.0.0", "")
	spool, err := uploadspool.Read(bytes.NewReader(body), 256<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = spool.Close() }()
	old, err := f.module.reserve(ctx, f.repo, "operator", "old-session", publicationRequest{PackageID: "Expired", Version: "1.0.0", Digest: spool.Digest(), Size: spool.Size()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.module.stage(ctx, old, spool); err != nil {
		t.Fatal(err)
	}
	failure := &failNuGetCollectionMarker{NativeNuGetStore: f.store, fail: true}
	maintenance, err := NewMaintenance(failure, f.objects)
	if err != nil {
		t.Fatal(err)
	}
	if err = maintenance.Collect(ctx, time.Now().Add(2*time.Hour)); err == nil {
		t.Fatal("lost collection marker was hidden")
	}
	if w := push(t, f, body); w.Code != 201 {
		t.Fatalf("replacement publication=%d %s", w.Code, w.Body.String())
	}
	if err = maintenance.Collect(ctx, time.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	pub, err := f.store.GetNuGetPublication(ctx, f.repo.ID, "expired", "1.0.0")
	if err != nil || pub.ObjectKey == old.ObjectKey {
		t.Fatalf("replacement key=%+v err=%v", pub, err)
	}
	if _, err = f.objects.Stat(ctx, pub.ObjectKey); err != nil {
		t.Fatal("retry deleted visible replacement", err)
	}
	if _, err = f.objects.Stat(ctx, old.ObjectKey); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("old upload bytes remain: %v", err)
	}
	if candidates, err := f.store.ExpireNuGetSessions(ctx, time.Now().Add(2*time.Hour), 100); err != nil || len(candidates) != 0 {
		t.Fatalf("collection did not converge: %+v %v", candidates, err)
	}
}
