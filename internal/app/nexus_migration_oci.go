package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/ccsert/artifact-gateway/internal/nexusmigration"
	"github.com/ccsert/artifact-gateway/internal/nexussource"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/ocigraph"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

// NexusOCIMigrationStore is explicit and never erases its concrete native owner.
type NexusOCIMigrationStore interface {
	repository.OCIImportStore
	GetOCIManifest(context.Context, string, string, string) (repository.OCIManifest, error)
	GetOCIBlob(context.Context, string, string) (repository.OCIBlob, error)
}

type NexusOCIMigrationPublication struct {
	store   NexusOCIMigrationStore
	objects objectstore.Store
}

var _ NexusFormatMigrationPublication = (*NexusOCIMigrationPublication)(nil)

func NewNexusOCIMigrationPublication(store NexusOCIMigrationStore, objects objectstore.Store) *NexusOCIMigrationPublication {
	return &NexusOCIMigrationPublication{store: store, objects: objects}
}

func (p *NexusOCIMigrationPublication) available() error {
	if p == nil || nilDependency(p.store) || objectstore.Validate(p.objects) != nil {
		return nexusmigration.Failed("target_unavailable")
	}
	return nil
}

func (p *NexusOCIMigrationPublication) NativeAssets(entry nexusmigration.Entry) ([]MigrationPublicationAsset, error) {
	name, _, _, err := nexusmigration.OCIEntryRoot(entry)
	if err != nil {
		return nil, err
	}
	if entry.TargetRepositoryID == "" || entry.ID == "" || len(entry.Assets) > ocigraph.MaxObjects {
		return nil, nexusmigration.Failed("invalid_publication")
	}
	out := make([]MigrationPublicationAsset, 0, len(entry.Assets))
	seen := map[string]bool{}
	for _, asset := range entry.Assets {
		digest := "sha256:" + asset.SHA256
		if !ocigraph.ValidDigest(digest) || asset.Size < 0 || asset.Size > 1<<30 || seen[asset.TargetPath] || asset.MediaType == "" {
			return nil, nexusmigration.Failed("invalid_publication")
		}
		seen[asset.TargetPath] = true
		source, ok := nexussource.ParseOCIAssetPath(asset.SourcePath)
		if !ok || source.Kind == "blobs" && source.Reference != digest || source.Kind == "manifests" && (source.Name != name || ocigraph.ValidDigest(source.Reference) && source.Reference != digest) {
			return nil, nexusmigration.Failed("invalid_publication")
		}
		key := "native/oci/blobs/sha256/" + asset.SHA256
		if asset.TargetPath == name+"/manifests/"+digest {
			if source.Kind != "manifests" || !ocigraph.IsManifest(asset.MediaType) && !ocigraph.IsIndex(asset.MediaType) {
				return nil, nexusmigration.Failed("invalid_publication")
			}
			key = "native/oci/manifests/" + entry.TargetRepositoryID + "/" + url.PathEscape(name) + "/" + asset.SHA256
		} else if source.Kind != "blobs" || asset.TargetPath != "blobs/"+digest || ocigraph.IsManifest(asset.MediaType) || ocigraph.IsIndex(asset.MediaType) {
			return nil, nexusmigration.Failed("invalid_publication")
		}
		out = append(out, MigrationPublicationAsset{Path: asset.TargetPath, ObjectKey: key, Digest: digest, Size: asset.Size})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (p *NexusOCIMigrationPublication) NativeAsset(ctx context.Context, entry nexusmigration.Entry, planned MigrationPublicationAsset) (MigrationPublicationAsset, error) {
	if err := p.available(); err != nil {
		return MigrationPublicationAsset{}, err
	}
	name, _, _, err := nexusmigration.OCIEntryRoot(entry)
	if err != nil {
		return MigrationPublicationAsset{}, err
	}
	if planned.Path == "blobs/"+planned.Digest {
		blob, err := p.store.GetOCIBlob(ctx, entry.TargetRepositoryID, planned.Digest)
		return MigrationPublicationAsset{Path: planned.Path, ObjectKey: blob.ObjectKey, Digest: blob.Digest, Size: blob.Size}, err
	}
	if planned.Path != name+"/manifests/"+planned.Digest {
		return MigrationPublicationAsset{}, nexusmigration.Failed("invalid_publication")
	}
	manifest, err := p.store.GetOCIManifest(ctx, entry.TargetRepositoryID, name, planned.Digest)
	return MigrationPublicationAsset{Path: planned.Path, ObjectKey: manifest.ObjectKey, Digest: manifest.Digest, Size: manifest.Size}, err
}

type nexusOCILockKey struct{}
type nexusOCILock struct {
	owner       *NexusOCIMigrationPublication
	fingerprint string
	active      atomic.Bool
	verified    atomic.Bool
	closure     repository.OCIImportClosure
}

func ociEntryFingerprint(entry nexusmigration.Entry) string {
	assets := make([]nexusmigration.Asset, len(entry.Assets))
	for i, asset := range entry.Assets {
		asset.Uploaded, asset.Verified = false, false
		assets[i] = asset
	}
	encoded, _ := json.Marshal(struct {
		ID, Batch, Target, Identity string
		Assets                      []nexusmigration.Asset
	}{entry.ID, entry.BatchID, entry.TargetRepositoryID, entry.Identity, assets})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func (p *NexusOCIMigrationPublication) plannedClosure(entry nexusmigration.Entry) (repository.OCIImportClosure, error) {
	name, reference, root, err := nexusmigration.OCIEntryRoot(entry)
	if err != nil {
		return repository.OCIImportClosure{}, err
	}
	assets, err := p.NativeAssets(entry)
	if err != nil {
		return repository.OCIImportClosure{}, err
	}
	closure := repository.OCIImportClosure{RepositoryID: entry.TargetRepositoryID, Name: name, Reference: reference, RootDigest: root, Actor: entry.Actor}
	for _, asset := range assets {
		if strings.HasPrefix(asset.Path, "blobs/") {
			closure.Blobs = append(closure.Blobs, repository.OCIBlob{Digest: asset.Digest, ObjectKey: asset.ObjectKey, Size: asset.Size})
			continue
		}
		mediaType := ""
		for _, source := range entry.Assets {
			if source.TargetPath == asset.Path {
				mediaType = source.MediaType
				break
			}
		}
		closure.Manifests = append(closure.Manifests, repository.OCIImportManifest{OCIManifest: repository.OCIManifest{RepositoryID: entry.TargetRepositoryID, Name: name, Digest: asset.Digest, ObjectKey: asset.ObjectKey, Size: asset.Size, MediaType: mediaType}})
	}
	return closure, nil
}

func (p *NexusOCIMigrationPublication) Lock(ctx context.Context, entry nexusmigration.Entry) (context.Context, func(), error) {
	if err := p.available(); err != nil {
		return ctx, nil, err
	}
	closure, err := p.plannedClosure(entry)
	if err != nil {
		return ctx, nil, err
	}
	ctx, release, err := p.store.LockOCIImport(ctx, closure)
	if err != nil {
		return ctx, nil, nexusmigration.Failed("target_coordination_failed")
	}
	lock := &nexusOCILock{owner: p, fingerprint: ociEntryFingerprint(entry), closure: closure}
	lock.active.Store(true)
	return context.WithValue(ctx, nexusOCILockKey{}, lock), func() {
		if lock.active.Swap(false) {
			release()
		}
	}, nil
}

func (p *NexusOCIMigrationPublication) held(ctx context.Context, entry nexusmigration.Entry) (*nexusOCILock, error) {
	lock, _ := ctx.Value(nexusOCILockKey{}).(*nexusOCILock)
	if lock == nil || lock.owner != p || !lock.active.Load() || lock.fingerprint != ociEntryFingerprint(entry) {
		return nil, nexusmigration.Failed("target_coordination_required")
	}
	return lock, nil
}

func (p *NexusOCIMigrationPublication) Begin(ctx context.Context, entry nexusmigration.Entry) (string, error) {
	if _, err := p.held(ctx, entry); err != nil {
		return "", err
	}
	return "oci-import:" + entry.ID, nil
}

func (p *NexusOCIMigrationPublication) Stage(ctx context.Context, entry nexusmigration.Entry, asset nexusmigration.Asset, input io.ReadSeeker) error {
	lock, err := p.held(ctx, entry)
	if err != nil {
		return err
	}
	lock.verified.Store(false)
	assets, err := p.NativeAssets(entry)
	if err != nil {
		return err
	}
	var planned *MigrationPublicationAsset
	for _, source := range entry.Assets {
		if source.SourcePath == asset.SourcePath && source.TargetPath == asset.TargetPath && source.SHA256 == asset.SHA256 && source.Size == asset.Size && source.MediaType == asset.MediaType {
			for j := range assets {
				if assets[j].Path == source.TargetPath {
					planned = &assets[j]
					break
				}
			}
		}
	}
	if planned == nil || nilDependency(input) {
		return nexusmigration.Failed("invalid_publication")
	}
	if err = nexusVerifyInput(ctx, input, asset); err != nil {
		return err
	}
	if err = p.store.StageOCIImportObject(ctx, repository.OCIObjectIntent{RepositoryID: entry.TargetRepositoryID, ObjectKey: planned.ObjectKey, Digest: planned.Digest, Size: planned.Size}); err != nil {
		return nexusmigration.Failed("native_stage_failed")
	}
	if err = p.objects.PutVerifiedReader(ctx, planned.ObjectKey, input, planned.Size, planned.Digest); err != nil {
		return nexusmigration.Failed("target_upload_failed")
	}
	return nil
}

func (p *NexusOCIMigrationPublication) readBytes(ctx context.Context, asset MigrationPublicationAsset, body bool) ([]byte, error) {
	reader, size, err := p.objects.Open(ctx, asset.ObjectKey)
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()
	if size != asset.Size {
		return nil, nexusmigration.Failed("target_bytes_mismatch")
	}
	h := sha256.New()
	var data []byte
	var count int64
	if body {
		if asset.Size > ocigraph.MaxManifestBytes {
			return nil, nexusmigration.Failed("oci_graph_limit_exceeded")
		}
		data, err = io.ReadAll(io.TeeReader(io.LimitReader(reader, asset.Size+1), h))
		count = int64(len(data))
	} else {
		count, err = io.Copy(h, &nexusContextReader{ctx: ctx, reader: io.LimitReader(reader, asset.Size+1)})
	}
	if err != nil {
		return nil, err
	}
	if count != asset.Size || "sha256:"+hex.EncodeToString(h.Sum(nil)) != asset.Digest {
		return nil, nexusmigration.Failed("target_bytes_mismatch")
	}
	return data, nil
}

func (p *NexusOCIMigrationPublication) Verify(ctx context.Context, entry nexusmigration.Entry) error {
	lock, err := p.held(ctx, entry)
	if err != nil {
		return err
	}
	lock.verified.Store(false)
	closure, err := p.plannedClosure(entry)
	if err != nil {
		return err
	}
	assets, err := p.NativeAssets(entry)
	if err != nil {
		return err
	}
	for _, asset := range assets {
		isManifest := !strings.HasPrefix(asset.Path, "blobs/")
		body, err := p.readBytes(ctx, asset, isManifest)
		if err != nil {
			return nexusmigration.Failed("target_bytes_mismatch")
		}
		if !isManifest {
			continue
		}
		for i := range closure.Manifests {
			if closure.Manifests[i].Digest == asset.Digest {
				doc, err := ocigraph.Decode(body, closure.Manifests[i].MediaType)
				if err != nil {
					return nexusmigration.Failed("oci_graph_invalid")
				}
				closure.Manifests[i].Document = doc
				break
			}
		}
	}
	objects := make([]ocigraph.Object, 0, len(assets))
	for _, m := range closure.Manifests {
		objects = append(objects, ocigraph.Object{Digest: m.Digest, Size: m.Size, MediaType: m.MediaType, Document: &m.Document})
	}
	for _, source := range entry.Assets {
		if strings.HasPrefix(source.TargetPath, "blobs/") {
			objects = append(objects, ocigraph.Object{Digest: "sha256:" + source.SHA256, Size: source.Size, MediaType: source.MediaType})
		}
	}
	if err = ocigraph.Validate(closure.RootDigest, objects); err != nil {
		return nexusmigration.Failed("oci_graph_invalid")
	}
	lock.closure = closure
	lock.verified.Store(true)
	return nil
}

func (p *NexusOCIMigrationPublication) Readback(ctx context.Context, entry nexusmigration.Entry) (nexusmigration.Readback, error) {
	if err := p.available(); err != nil {
		return "", err
	}
	name, reference, root, err := nexusmigration.OCIEntryRoot(entry)
	if err != nil {
		return "", err
	}
	visible, err := p.store.GetOCIManifest(ctx, entry.TargetRepositoryID, name, reference)
	if errors.Is(err, repository.ErrNotFound) {
		return nexusmigration.Absent, nil
	}
	if err != nil {
		return "", nexusmigration.Failed("target_readback_failed")
	}
	if visible.Digest != root {
		return nexusmigration.Conflict, nil
	}
	assets, err := p.NativeAssets(entry)
	if err != nil {
		return "", err
	}
	objects := make([]ocigraph.Object, 0, len(assets))
	for _, asset := range assets {
		actual, err := p.NativeAsset(ctx, entry, asset)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return nexusmigration.Conflict, nil
			}
			return "", nexusmigration.Failed("target_readback_failed")
		}
		if actual.Digest != asset.Digest || actual.Size != asset.Size {
			return nexusmigration.Conflict, nil
		}
		isManifest := !strings.HasPrefix(asset.Path, "blobs/")
		body, readErr := p.readBytes(ctx, actual, isManifest)
		if readErr != nil {
			return nexusmigration.Conflict, nil
		}
		mediaType := ""
		for _, source := range entry.Assets {
			if source.TargetPath == asset.Path {
				mediaType = source.MediaType
				break
			}
		}
		object := ocigraph.Object{Digest: asset.Digest, Size: asset.Size, MediaType: mediaType}
		if isManifest {
			manifest, err := p.store.GetOCIManifest(ctx, entry.TargetRepositoryID, name, asset.Digest)
			if err != nil || manifest.MediaType != mediaType {
				return nexusmigration.Conflict, nil
			}
			document, err := ocigraph.Decode(body, mediaType)
			if err != nil {
				return nexusmigration.Conflict, nil
			}
			object.Document = &document
		}
		objects = append(objects, object)
		current, err := p.NativeAsset(ctx, entry, asset)
		if err != nil || current != actual {
			return nexusmigration.Conflict, nil
		}
	}
	if err = ocigraph.Validate(root, objects); err != nil {
		return nexusmigration.Conflict, nil
	}
	current, err := p.store.GetOCIManifest(ctx, entry.TargetRepositoryID, name, reference)
	if err != nil || current.Digest != root {
		return nexusmigration.Conflict, nil
	}
	return nexusmigration.Identical, nil
}

func (p *NexusOCIMigrationPublication) Publish(ctx context.Context, entry nexusmigration.Entry) error {
	lock, err := p.held(ctx, entry)
	if err != nil {
		return err
	}
	if !lock.verified.Load() {
		return nexusmigration.Failed("target_verification_required")
	}
	err = p.store.PublishOCIImportClosure(ctx, lock.closure)
	if errors.Is(err, repository.ErrNameExists) {
		return nexusmigration.Failed("target_conflict")
	}
	if repository.IsQuotaExceeded(err) {
		return nexusmigration.Failed("target_quota_exceeded")
	}
	if errors.Is(err, repository.ErrArtifactQuarantined) {
		return nexusmigration.Failed("artifact_quarantined")
	}
	if err != nil {
		return nexusmigration.Failed("native_publish_failed")
	}
	return nil
}
