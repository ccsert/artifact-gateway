package nexusmigration

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/nexussource"
	"github.com/ccsert/artifact-gateway/internal/ocigraph"
	"github.com/ccsert/artifact-gateway/internal/testsupport/ocifixture"
)

// The public worker seals the actual inventory, then observes an operator pause
// before any publication. The test reads those ledger rows through Entries.
func TestOCIWorkerSealsSharedGraphDepthBoundary(t *testing.T) {
	for _, depth := range []int{8, 9, 11} {
		t.Run(fmt.Sprintf("depth=%d", depth), func(t *testing.T) {
			fixture := sharedDepthSource(t, depth)
			now := time.Now()
			zero, free := int64(0), int64(1<<30)
			batch := Batch{ID: "depth-batch", LeaseToken: "depth-owner", StartInput: StartInput{
				SourceID: "depth-source", SourceVersion: "1", PolicyVersion: PolicyVersion,
				Mappings: []Mapping{{SourceRepository: "images", TargetRepositoryID: "target-repo"}},
				CapacityEvidence: CapacityEvidence{TargetID: "target", PoolID: "pool", ObservedAt: now.Add(-time.Second), ValidUntil: now.Add(time.Minute),
					FreeBytes: &free, DownloadBytes: &free, UploadBytes: &zero, BackupBytes: &zero, RestoreBytes: &zero, HeadroomBytes: &zero},
			}}
			store := &ociDepthScanStore{ociPlanStore: &ociPlanStore{batch: batch}}
			source := ociDepthScanSource{ociPlanSource: ociPlanSource{fixture: fixture}}
			service := New(store, source, ociPlanPublication{}, Options{TargetID: "target", PoolID: "pool", HeartbeatInterval: time.Hour})
			if err := service.RunOnce(t.Context(), []string{"oci"}); err != nil {
				t.Fatal(err)
			}
			if !store.batch.ScanComplete || store.batch.ManifestSHA256 == "" || store.verified == 0 || store.finished != "paused" {
				t.Fatalf("public worker did not seal and pause a fully read inventory: batch=%+v finish=%s verified=%d", store.batch, store.finished, store.verified)
			}
			entries, next, err := service.Entries(t.Context(), batch.ID, 100, "")
			if err != nil || next != "" {
				t.Fatalf("sealed ledger read: next=%q err=%v", next, err)
			}
			var root *Entry
			for i := range entries {
				if entries[i].Identity == fixture.Name+":"+fixture.Reference {
					root = &entries[i]
				}
			}
			if root == nil {
				t.Fatal("source root is absent from the sealed ledger")
			}
			if depth == 8 {
				if root.State != "pending" || root.LastError != "" || len(root.Assets) != 9 {
					t.Fatalf("valid boundary closure must remain executable: %+v", root)
				}
				_, _, digest, err := OCIEntryRoot(*root)
				if err != nil || digest != fixture.Root {
					t.Fatalf("the complete root identity was changed: %q %v", digest, err)
				}
			} else if root.State != "unsupported" || root.LastError != "oci_graph_limit_exceeded" || len(root.Assets) != 0 {
				t.Fatalf("over-deep shared root must be non-executable: state=%s error=%s assets=%d", root.State, root.LastError, len(root.Assets))
			}
		})
	}
}

type ociDepthScanStore struct {
	*ociPlanStore
	entries  []Entry
	finished string
	scan     InventoryScan
	pages    []InventoryPage
}

func (s *ociDepthScanStore) Claim(context.Context, []string) (Batch, error) { return s.batch, nil }
func (s *ociDepthScanStore) PreviousEntries(context.Context, Batch) ([]Entry, error) {
	return nil, nil
}
func (s *ociDepthScanStore) Seal(_ context.Context, batch Batch, entries []Entry, hash string, metadata, total int64) error {
	s.entries = entries
	s.batch = batch
	s.batch.ScanComplete, s.batch.PauseRequested = true, true
	s.batch.ManifestSHA256, s.batch.MetadataDiscovered = hash, metadata
	s.batch.EntryCount, s.batch.TotalBytes = int64(len(entries)), &total
	return nil
}
func (s *ociDepthScanStore) Entries(context.Context, string, int, string) ([]Entry, string, error) {
	return s.entries, "", nil
}
func (s *ociDepthScanStore) Finish(_ context.Context, _, _, state, _ string) error {
	s.finished = state
	return nil
}

type ociDepthScanSource struct{ ociPlanSource }

func (s ociDepthScanSource) Get(context.Context, string) (nexussource.Source, error) {
	return nexussource.Source{Version: "1"}, nil
}
func (s ociDepthScanSource) InventoryRepositories(context.Context, string, string, []string) ([]nexussource.Repository, error) {
	return []nexussource.Repository{{Name: "images", Format: "docker", Type: "hosted", Status: "supported"}}, nil
}
func (s ociDepthScanSource) ReadInventoryPage(context.Context, string, string, nexussource.Repository, string) (nexussource.InventoryPage, error) {
	assets := []nexussource.Asset{}
	for path, body := range s.fixture.Bytes {
		assets = append(assets, nexussource.Asset{Repository: "images", Path: path, Size: int64(len(body)), Checksum: map[string]string{"sha256": s.fixture.Digests[path]}})
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].Path < assets[j].Path })
	return nexussource.InventoryPage{Assets: assets}, nil
}
func (s *ociDepthScanStore) InventoryScan(context.Context, string) (InventoryScan, error) {
	if s.scan.BindingSHA256 == "" {
		return InventoryScan{}, ErrNotFound
	}
	return s.scan, nil
}
func (s *ociDepthScanStore) BeginInventoryScan(_ context.Context, _ Batch, scan InventoryScan) error {
	s.scan = scan
	return nil
}
func (s *ociDepthScanStore) AppendInventoryPage(_ context.Context, _ Batch, _ InventoryScan, page nexussource.InventoryPage) error {
	s.pages = append(s.pages, InventoryPage{SourceRepository: "images", SHA256: InventoryPageDigest(page), Page: page})
	s.scan.PageCount++
	s.scan.AssetCount += int64(len(page.Assets))
	s.scan.RepositoryIndex++
	return nil
}
func (s *ociDepthScanStore) InventoryPage(_ context.Context, _ string, ordinal int64) (InventoryPage, error) {
	return s.pages[ordinal], nil
}
func (s *ociDepthScanStore) VerifyInventoryPage(context.Context, Batch, int64, string, int64) error {
	s.scan.VerifiedPages++
	return nil
}

func sharedDepthSource(t *testing.T, depth int) ocifixture.Fixture {
	t.Helper()
	f := ocifixture.Fixture{Name: "team/depth", Reference: "release", Bytes: map[string][]byte{}, Digests: map[string]string{}, MediaTypes: map[string]string{}}
	add := func(body []byte, mediaType string) ocigraph.Descriptor {
		digest := ocigraph.Digest(body)
		path := "v2/" + f.Name + "/manifests/" + digest
		if mediaType == "application/vnd.oci.image.config.v1+json" {
			path = "v2/-/blobs/" + digest
		}
		f.Bytes[path], f.Digests[path], f.MediaTypes[path] = body, strings.TrimPrefix(digest, "sha256:"), mediaType
		size := int64(len(body))
		return ocigraph.Descriptor{Digest: digest, Size: &size, MediaType: mediaType}
	}
	addDocument := func(document ocigraph.Document) ocigraph.Descriptor {
		body, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		return add(body, document.MediaType)
	}
	config := add([]byte(`{}`), "application/vnd.oci.image.config.v1+json")
	image := addDocument(ocigraph.Document{SchemaVersion: 2, MediaType: ocigraph.OCIManifest, Config: &config, Layers: []ocigraph.Descriptor{}})
	children := []ocigraph.Descriptor{image}
	for i := 0; i < depth-2; i++ {
		children = append(children, addDocument(ocigraph.Document{SchemaVersion: 2, MediaType: ocigraph.OCIIndex, Manifests: []ocigraph.Descriptor{children[len(children)-1]}}))
	}
	root := addDocument(ocigraph.Document{SchemaVersion: 2, MediaType: ocigraph.OCIIndex, Manifests: children})
	path, tagPath := "v2/"+f.Name+"/manifests/"+root.Digest, "v2/"+f.Name+"/manifests/"+f.Reference
	f.Bytes[tagPath], f.Digests[tagPath], f.MediaTypes[tagPath] = f.Bytes[path], f.Digests[path], f.MediaTypes[path]
	delete(f.Bytes, path)
	delete(f.Digests, path)
	delete(f.MediaTypes, path)
	f.Root = root.Digest
	return f
}
