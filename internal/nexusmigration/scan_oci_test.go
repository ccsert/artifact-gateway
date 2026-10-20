package nexusmigration

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/nexussource"
	"github.com/ccsert/artifact-gateway/internal/testsupport/ocifixture"
)

type ociPlanStore struct {
	Store
	batch    Batch
	verified int64
}

func (s *ociPlanStore) Get(context.Context, string) (Batch, error) { return s.batch, nil }
func (s *ociPlanStore) RecordScanProgress(_ context.Context, _, _ string, _ int64, bytes int64) error {
	s.verified += bytes
	return nil
}

type ociPlanSource struct {
	Source
	fixture ocifixture.Fixture
	mutate  bool
}

func (s ociPlanSource) OpenAsset(_ context.Context, _, _, _ string, in AssetRequest) (*Content, error) {
	body := bytes.Clone(s.fixture.Bytes[in.Path])
	if s.mutate {
		body = append(body, ' ')
	}
	if in.Format != "oci" {
		return nil, Failed("format_missing")
	}
	return &Content{Body: io.NopCloser(bytes.NewReader(body)), MediaType: s.fixture.MediaTypes[in.Path]}, nil
}

func TestOCIPlanningFreezesCompleteAllPlatformClosure(t *testing.T) {
	f := ocifixture.New(t.Name())
	zero, free := int64(0), int64(1<<30)
	now := time.Now()
	b := Batch{ID: "batch", StartInput: StartInput{SourceID: "source", SourceVersion: "1", CapacityEvidence: CapacityEvidence{TargetID: "target", PoolID: "pool", ObservedAt: now.Add(-time.Second), ValidUntil: now.Add(time.Minute), FreeBytes: &free, DownloadBytes: &free, UploadBytes: &zero, BackupBytes: &zero, RestoreBytes: &zero, HeadroomBytes: &zero}}}
	store := &ociPlanStore{batch: b}
	source := ociPlanSource{fixture: f}
	service := New(store, source, nil, Options{TargetID: "target", PoolID: "pool"})
	service.publication = ociPlanPublication{}
	inventory := []nexussource.Asset{}
	for path, body := range f.Bytes {
		inventory = append(inventory, nexussource.Asset{Repository: "images", Path: path, Size: int64(len(body)), Checksum: map[string]string{"sha256": f.Digests[path]}})
	}
	entries, err := service.planOCIEntries(context.Background(), b, "images", "target-repo", inventory, &ScanFailure{})
	if err != nil {
		t.Fatal(err)
	}
	var root *Entry
	for i := range entries {
		if entries[i].Identity == f.Name+":"+f.Reference {
			root = &entries[i]
		}
	}
	if root == nil || root.State != "pending" || len(root.Assets) != 6 {
		t.Fatalf("root=%+v entries=%+v", root, entries)
	}
	name, reference, digest, err := OCIEntryRoot(*root)
	if err != nil || name != f.Name || reference != f.Reference || digest != f.Root {
		t.Fatalf("root identity=%q %q %q %v", name, reference, digest, err)
	}
	if store.verified == 0 {
		t.Fatal("manifest pre-read omitted source verification accounting")
	}
	for _, asset := range root.Assets {
		if asset.MediaType == "" || asset.Validator == "" || asset.Size != int64(len(f.Bytes[asset.SourcePath])) {
			t.Fatalf("unfrozen asset=%+v", asset)
		}
	}
	t.Run("missing layer is unsupported", func(t *testing.T) {
		missing := []nexussource.Asset{}
		for _, a := range inventory {
			if !strings.HasPrefix(a.Path, "v2/-/blobs/") || f.MediaTypes[a.Path] != "application/vnd.oci.image.layer.v1.tar+gzip" {
				missing = append(missing, a)
			}
		}
		entries, err := service.planOCIEntries(context.Background(), b, "images", "target-repo", missing, &ScanFailure{})
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.Identity == f.Name+":"+f.Reference && (entry.State != "unsupported" || entry.LastError != "oci_dependency_missing") {
				t.Fatalf("missing graph admitted %+v", entry)
			}
		}
	})
	t.Run("source tag drift fails scan", func(t *testing.T) {
		service.source = ociPlanSource{fixture: f, mutate: true}
		_, err := service.planOCIEntries(context.Background(), b, "images", "target-repo", inventory, &ScanFailure{})
		if err == nil || err.Error() != "source_changed" {
			t.Fatalf("drift=%v", err)
		}
	})
}

type ociPlanPublication struct{ Publication }
