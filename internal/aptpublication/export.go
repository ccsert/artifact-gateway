package aptpublication

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

const SnapshotArchiveSchemaVersion = "artifact-gateway.dev/apt-snapshot-export/v1"

var (
	ErrInvalidSnapshotArchiveInput = errors.New("APT snapshot archive input is invalid")
	ErrSnapshotArchiveCorrupt      = errors.New("APT snapshot archive object is corrupt")
)

// SnapshotArchiveExporter emits a portable, deterministic tar archive for one
// visible signed APT snapshot. Internal object-store keys never leave this
// boundary; archive objects are addressed only by their verified SHA-256.
type SnapshotArchiveExporter struct {
	Store   NativeSnapshotArchiveStore
	Objects objectstore.Store
}

// PreparedSnapshotArchive is an immutable, digest-verified export plan. The
// object bytes are opened again only while WriteTo streams the archive, so an
// HTTP caller can fail before writing response headers without buffering the
// complete snapshot in memory.
type PreparedSnapshotArchive struct {
	Manifest     SnapshotArchiveManifest
	manifestBody []byte
	objects      []archiveObject
	objectStore  objectstore.Store
}

// NativeSnapshotArchiveStore is the read boundary required to export a
// complete snapshot without reaching into a storage implementation.
type NativeSnapshotArchiveStore interface {
	GetAPTRepositorySnapshot(context.Context, string) (repository.APTRepositorySnapshot, []repository.APTSnapshotPackage, error)
	GetAPTPackageRevisionForSession(context.Context, string) (repository.APTPackageRevision, error)
	ListAPTSnapshotAssets(context.Context, string) ([]repository.APTSnapshotAsset, error)
}

type SnapshotArchiveManifest struct {
	SchemaVersion string                   `json:"schemaVersion"`
	Snapshot      SnapshotArchiveSnapshot  `json:"snapshot"`
	Packages      []SnapshotArchivePackage `json:"packages"`
	Assets        []SnapshotArchiveAsset   `json:"assets"`
}

type SnapshotArchiveSnapshot struct {
	ID                 string                                `json:"id"`
	RepositoryID       string                                `json:"repositoryId"`
	Suite              string                                `json:"suite"`
	Sequence           int64                                 `json:"sequence"`
	State              repository.APTRepositorySnapshotState `json:"state"`
	ReleaseDigest      string                                `json:"releaseDigest"`
	InReleaseDigest    string                                `json:"inReleaseDigest"`
	SignerIdentity     string                                `json:"signerIdentity"`
	KeyFingerprint     string                                `json:"keyFingerprint"`
	SignatureAlgorithm string                                `json:"signatureAlgorithm"`
	CreatedAt          string                                `json:"createdAt"`
	PublishedAt        string                                `json:"publishedAt"`
}

type SnapshotArchivePackage struct {
	CanonicalIdentity string `json:"canonicalIdentity"`
	Package           string `json:"package"`
	Version           string `json:"version"`
	Architecture      string `json:"architecture"`
	Component         string `json:"component"`
	ObjectName        string `json:"objectName"`
	Publisher         string `json:"publisher"`
	CreatedAt         string `json:"createdAt"`
	Digest            string `json:"digest"`
	Size              int64  `json:"size"`
	Object            string `json:"object"`
}

type SnapshotArchiveAsset struct {
	Path        string `json:"path"`
	Digest      string `json:"digest"`
	Size        int64  `json:"size"`
	ContentType string `json:"contentType"`
	Object      string `json:"object"`
}

type archiveObject struct {
	name      string
	objectKey string
	digest    string
	size      int64
}

func (e SnapshotArchiveExporter) Export(ctx context.Context, snapshotID string, output io.Writer) (SnapshotArchiveManifest, error) {
	if output == nil {
		return SnapshotArchiveManifest{}, ErrInvalidSnapshotArchiveInput
	}
	prepared, err := e.Prepare(ctx, snapshotID)
	if err != nil {
		return SnapshotArchiveManifest{}, err
	}
	if err = prepared.WriteTo(ctx, output); err != nil {
		return SnapshotArchiveManifest{}, err
	}
	return prepared.Manifest, nil
}

// Prepare resolves the exact snapshot membership and verifies every referenced
// object before any archive bytes are emitted.
func (e SnapshotArchiveExporter) Prepare(ctx context.Context, snapshotID string) (*PreparedSnapshotArchive, error) {
	if e.Store == nil || e.Objects == nil || strings.TrimSpace(snapshotID) == "" {
		return nil, ErrInvalidSnapshotArchiveInput
	}
	snapshot, membership, err := e.Store.GetAPTRepositorySnapshot(ctx, snapshotID)
	if err != nil {
		return nil, err
	}
	if snapshot.State != repository.APTRepositorySnapshotVisible && snapshot.State != repository.APTRepositorySnapshotRetired {
		return nil, repository.ErrDisabled
	}
	assets, err := e.Store.ListAPTSnapshotAssets(ctx, snapshot.ID)
	if err != nil {
		return nil, err
	}
	manifest := SnapshotArchiveManifest{
		SchemaVersion: SnapshotArchiveSchemaVersion,
		Snapshot: SnapshotArchiveSnapshot{
			ID: snapshot.ID, RepositoryID: snapshot.RepositoryID, Suite: snapshot.Suite, Sequence: snapshot.Sequence,
			State: snapshot.State, ReleaseDigest: snapshot.ReleaseDigest, InReleaseDigest: snapshot.InReleaseDigest,
			SignerIdentity: snapshot.SignerIdentity, KeyFingerprint: snapshot.KeyFingerprint,
			SignatureAlgorithm: snapshot.SignatureAlgorithm,
			CreatedAt:          snapshot.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z"),
			PublishedAt:        snapshot.PublishedAt.UTC().Format("2006-01-02T15:04:05.999999999Z"),
		},
		Packages: make([]SnapshotArchivePackage, 0, len(membership)),
		Assets:   make([]SnapshotArchiveAsset, 0, len(assets)),
	}
	if len(membership) > maxSnapshotArchivePackages || len(assets) > maxSnapshotArchiveAssets {
		return nil, ErrSnapshotArchiveCorrupt
	}
	objects := make(map[string]archiveObject)
	for _, item := range membership {
		revision, revisionErr := e.Store.GetAPTPackageRevisionForSession(ctx, item.PublicationSessionID)
		if revisionErr != nil {
			return nil, revisionErr
		}
		if revision.ID != item.PackageRevisionID || revision.RepositoryID != snapshot.RepositoryID || revision.Architecture != item.Architecture || item.SnapshotID != snapshot.ID {
			return nil, ErrSnapshotArchiveCorrupt
		}
		objectName, objectErr := portableArchiveObject(revision.Digest)
		if objectErr != nil {
			return nil, objectErr
		}
		manifest.Packages = append(manifest.Packages, SnapshotArchivePackage{
			CanonicalIdentity: revision.CanonicalIdentity, Package: revision.Package, Version: revision.Version,
			Architecture: revision.Architecture, Component: item.Component, ObjectName: revision.ObjectName,
			Publisher: revision.Publisher, CreatedAt: revision.CreatedAt.UTC().Format(time.RFC3339Nano),
			Digest: revision.Digest, Size: revision.Size, Object: objectName,
		})
		if err = addArchiveObject(objects, archiveObject{name: objectName, objectKey: revision.ObjectKey, digest: revision.Digest, size: revision.Size}); err != nil {
			return nil, err
		}
	}
	for _, asset := range assets {
		if asset.SnapshotID != snapshot.ID || asset.RepositoryID != snapshot.RepositoryID {
			return nil, ErrSnapshotArchiveCorrupt
		}
		objectName, objectErr := portableArchiveObject(asset.Digest)
		if objectErr != nil {
			return nil, objectErr
		}
		manifest.Assets = append(manifest.Assets, SnapshotArchiveAsset{
			Path: asset.Path, Digest: asset.Digest, Size: asset.Size, ContentType: asset.ContentType, Object: objectName,
		})
		if err = addArchiveObject(objects, archiveObject{name: objectName, objectKey: asset.ObjectKey, digest: asset.Digest, size: asset.Size}); err != nil {
			return nil, err
		}
	}
	slices.SortFunc(manifest.Packages, func(left, right SnapshotArchivePackage) int {
		return strings.Compare(left.CanonicalIdentity+"\x00"+left.Component, right.CanonicalIdentity+"\x00"+right.Component)
	})
	slices.SortFunc(manifest.Assets, func(left, right SnapshotArchiveAsset) int { return strings.Compare(left.Path, right.Path) })
	if _, err = validateSnapshotArchiveManifest(manifest); err != nil {
		return nil, err
	}
	objectList := make([]archiveObject, 0, len(objects))
	for _, object := range objects {
		objectList = append(objectList, object)
	}
	slices.SortFunc(objectList, func(left, right archiveObject) int { return strings.Compare(left.name, right.name) })
	var release bytes.Buffer
	for _, object := range objectList {
		capture := io.Discard
		if object.digest == snapshot.ReleaseDigest {
			capture = &release
		}
		if err = e.verifyObject(ctx, object, capture); err != nil {
			return nil, err
		}
	}
	if !repository.ValidateAPTSnapshotArchiveClosure(snapshot, assets, release.Bytes()) {
		return nil, ErrSnapshotArchiveCorrupt
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	if len(body) > maxSnapshotArchiveManifestBytes {
		return nil, ErrSnapshotArchiveCorrupt
	}
	return &PreparedSnapshotArchive{Manifest: manifest, manifestBody: body, objects: objectList, objectStore: e.Objects}, nil
}

// WriteTo streams one previously verified archive. Callers must treat a write
// failure as a truncated response and must not append a JSON error body.
func (p *PreparedSnapshotArchive) WriteTo(ctx context.Context, output io.Writer) error {
	if p == nil || output == nil || p.objectStore == nil || len(p.manifestBody) == 0 {
		return ErrInvalidSnapshotArchiveInput
	}
	w := tar.NewWriter(output)
	if err := writeArchiveBytes(w, "manifest.json", p.manifestBody); err != nil {
		return err
	}
	exporter := SnapshotArchiveExporter{Objects: p.objectStore}
	for _, object := range p.objects {
		if err := exporter.writeObject(ctx, w, object); err != nil {
			return err
		}
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close APT snapshot archive: %w", err)
	}
	return nil
}

func portableArchiveObject(digest string) (string, error) {
	if !repository.ValidAPTSHA256Digest(digest) {
		return "", ErrSnapshotArchiveCorrupt
	}
	return "objects/sha256/" + strings.TrimPrefix(digest, "sha256:"), nil
}

func addArchiveObject(objects map[string]archiveObject, object archiveObject) error {
	if existing, ok := objects[object.name]; ok {
		if existing.digest != object.digest || existing.size != object.size || existing.objectKey != object.objectKey {
			return ErrSnapshotArchiveCorrupt
		}
		return nil
	}
	objects[object.name] = object
	return nil
}

func (e SnapshotArchiveExporter) verifyObject(ctx context.Context, object archiveObject, capture io.Writer) error {
	reader, size, err := e.Objects.Open(ctx, object.objectKey)
	if err != nil {
		return fmt.Errorf("%w: open %s: %v", ErrSnapshotArchiveCorrupt, object.name, err)
	}
	defer func() { _ = reader.Close() }()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(hash, capture), io.LimitReader(aptArchiveContextReader{ctx: ctx, reader: reader}, object.size+1))
	if err != nil {
		return fmt.Errorf("%w: read %s: %v", ErrSnapshotArchiveCorrupt, object.name, err)
	}
	actual := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	if size != object.size || written != object.size || actual != object.digest {
		return fmt.Errorf("%w: %s digest or size mismatch", ErrSnapshotArchiveCorrupt, object.name)
	}
	return nil
}

func writeArchiveBytes(w *tar.Writer, name string, body []byte) error {
	if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err := w.Write(body)
	return err
}

func (e SnapshotArchiveExporter) writeObject(ctx context.Context, w *tar.Writer, object archiveObject) error {
	reader, size, err := e.Objects.Open(ctx, object.objectKey)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	if size != object.size {
		return ErrSnapshotArchiveCorrupt
	}
	if err = w.WriteHeader(&tar.Header{Name: object.name, Mode: 0o644, Size: object.size, Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(w, hash), io.LimitReader(aptArchiveContextReader{ctx: ctx, reader: reader}, object.size+1))
	if err != nil {
		return err
	}
	actual := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	if written != object.size || actual != object.digest {
		return ErrSnapshotArchiveCorrupt
	}
	return nil
}

// Size is the exact length of the canonical tar stream, including its terminator.
func (p *PreparedSnapshotArchive) Size() int64 {
	size := int64(512 + ((len(p.manifestBody)+511)/512)*512 + 1024)
	for _, object := range p.objects {
		size += 512 + ((object.size+511)/512)*512
	}
	return size
}
