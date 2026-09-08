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
	"strings"
	"time"

	aptprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/apt"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

const (
	maxSnapshotArchiveManifestBytes = 128 << 20
	maxSnapshotArchivePackages      = 10000
	maxSnapshotArchiveAssets        = 50016
	maxSnapshotArchiveObjects       = 50000
)

// VerifySnapshotArchive validates a portable archive without trusting its tar
// names, manifest sizes, or object digests. It consumes the complete stream and
// rejects missing, duplicate, unexpected, unsorted, or non-regular entries.
func VerifySnapshotArchive(ctx context.Context, input io.Reader) (SnapshotArchiveManifest, error) {
	if input == nil {
		return SnapshotArchiveManifest{}, ErrInvalidSnapshotArchiveInput
	}
	stream := &archiveCountingReader{reader: aptArchiveContextReader{ctx: ctx, reader: input}}
	reader := tar.NewReader(stream)
	header, err := reader.Next()
	if err != nil || header.Name != "manifest.json" || header.Typeflag != tar.TypeReg || header.Size <= 0 || header.Size > maxSnapshotArchiveManifestBytes {
		return SnapshotArchiveManifest{}, ErrSnapshotArchiveCorrupt
	}
	manifestBody, err := io.ReadAll(io.LimitReader(reader, maxSnapshotArchiveManifestBytes+1))
	if err != nil || int64(len(manifestBody)) != header.Size {
		return SnapshotArchiveManifest{}, ErrSnapshotArchiveCorrupt
	}
	var manifest SnapshotArchiveManifest
	decoder := json.NewDecoder(bytes.NewReader(manifestBody))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&manifest); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return SnapshotArchiveManifest{}, ErrSnapshotArchiveCorrupt
	}
	expected, err := validateSnapshotArchiveManifest(manifest)
	if err != nil {
		return SnapshotArchiveManifest{}, err
	}
	seen := make(map[string]struct{}, len(expected))
	previous := ""
	releaseObject := ""
	for _, asset := range manifest.Assets {
		if asset.Path == "dists/"+manifest.Snapshot.Suite+"/Release" {
			releaseObject = asset.Object
			break
		}
	}
	packagesByObject := make(map[string][]SnapshotArchivePackage)
	for _, item := range manifest.Packages {
		packagesByObject[item.Object] = append(packagesByObject[item.Object], item)
	}
	var release bytes.Buffer
	for {
		padding := (512 - header.Size%512) % 512
		before := stream.count
		header, err = reader.Next()
		if errors.Is(err, io.EOF) {
			// archive/tar also accepts a bare EOF. A portable archive must include
			// both end blocks and must not hide a second archive or trailing payload.
			if stream.count-before != padding+1024 {
				return SnapshotArchiveManifest{}, ErrSnapshotArchiveCorrupt
			}
			var tail [1]byte
			if n, tailErr := io.ReadFull(stream, tail[:]); n != 0 || tailErr != io.EOF {
				return SnapshotArchiveManifest{}, ErrSnapshotArchiveCorrupt
			}
			break
		}
		if err != nil || header.Typeflag != tar.TypeReg || header.Name <= previous {
			return SnapshotArchiveManifest{}, ErrSnapshotArchiveCorrupt
		}
		previous = header.Name
		object, ok := expected[header.Name]
		if !ok || header.Size != object.size {
			return SnapshotArchiveManifest{}, ErrSnapshotArchiveCorrupt
		}
		if _, duplicate := seen[header.Name]; duplicate {
			return SnapshotArchiveManifest{}, ErrSnapshotArchiveCorrupt
		}
		hash := sha256.New()
		destination := io.Writer(hash)
		if header.Name == releaseObject {
			destination = io.MultiWriter(hash, &release)
		}

		var written int64
		var copyErr error
		if packages := packagesByObject[header.Name]; len(packages) > 0 {
			counted := &archiveCountingReader{reader: io.TeeReader(reader, destination)}
			metadata, parseErr := aptprotocol.ParseDebianBinary(counted, object.size)
			if parseErr != nil {
				return SnapshotArchiveManifest{}, ErrSnapshotArchiveCorrupt
			}
			for _, item := range packages {
				if item.CanonicalIdentity != metadata.CanonicalIdentity || item.Package != metadata.Package || item.Version != metadata.Version || item.Architecture != metadata.Architecture {
					return SnapshotArchiveManifest{}, ErrSnapshotArchiveCorrupt
				}
			}
			written = counted.count
		} else {
			written, copyErr = io.Copy(destination, io.LimitReader(reader, object.size+1))
		}
		if copyErr != nil || written != object.size || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != object.digest {
			return SnapshotArchiveManifest{}, ErrSnapshotArchiveCorrupt
		}
		seen[header.Name] = struct{}{}
	}
	if len(seen) != len(expected) {
		return SnapshotArchiveManifest{}, ErrSnapshotArchiveCorrupt
	}
	snapshot, assets := snapshotArchiveRepositoryView(manifest)
	if releaseObject == "" || !repository.ValidateAPTSnapshotArchiveClosure(snapshot, assets, release.Bytes()) {
		return SnapshotArchiveManifest{}, ErrSnapshotArchiveCorrupt
	}
	return manifest, nil
}

func snapshotArchiveRepositoryView(manifest SnapshotArchiveManifest) (repository.APTRepositorySnapshot, []repository.APTSnapshotAsset) {
	createdAt, _ := time.Parse(time.RFC3339Nano, manifest.Snapshot.CreatedAt)
	publishedAt, _ := time.Parse(time.RFC3339Nano, manifest.Snapshot.PublishedAt)
	snapshot := repository.APTRepositorySnapshot{
		ID: manifest.Snapshot.ID, RepositoryID: manifest.Snapshot.RepositoryID, Suite: manifest.Snapshot.Suite,
		Sequence: manifest.Snapshot.Sequence, State: manifest.Snapshot.State,
		ReleaseDigest: manifest.Snapshot.ReleaseDigest, InReleaseDigest: manifest.Snapshot.InReleaseDigest,
		SignerIdentity: manifest.Snapshot.SignerIdentity, KeyFingerprint: manifest.Snapshot.KeyFingerprint,
		SignatureAlgorithm: manifest.Snapshot.SignatureAlgorithm, CreatedAt: createdAt, PublishedAt: publishedAt,
	}
	assets := make([]repository.APTSnapshotAsset, 0, len(manifest.Assets))
	for _, asset := range manifest.Assets {
		assets = append(assets, repository.APTSnapshotAsset{
			SnapshotID: snapshot.ID, RepositoryID: snapshot.RepositoryID, Path: asset.Path,
			Digest: asset.Digest, ObjectKey: "native/apt/sha256/" + strings.TrimPrefix(asset.Digest, "sha256:"),
			Size: asset.Size, ContentType: asset.ContentType,
		})
	}
	return snapshot, assets
}

func validateSnapshotArchiveManifest(manifest SnapshotArchiveManifest) (map[string]archiveObject, error) {
	snapshot := manifest.Snapshot
	if manifest.SchemaVersion != SnapshotArchiveSchemaVersion || snapshot.ID == "" || snapshot.RepositoryID == "" ||
		!repository.ValidAPTPublicationScope(snapshot.Suite) || snapshot.Sequence <= 0 ||
		(snapshot.State != repository.APTRepositorySnapshotVisible && snapshot.State != repository.APTRepositorySnapshotRetired) ||
		!repository.ValidAPTSHA256Digest(snapshot.ReleaseDigest) || !repository.ValidAPTSHA256Digest(snapshot.InReleaseDigest) ||
		snapshot.SignerIdentity == "" || snapshot.KeyFingerprint == "" || snapshot.SignatureAlgorithm == "" ||
		len(snapshot.SignerIdentity) > 512 || len(snapshot.KeyFingerprint) > 512 || len(snapshot.SignatureAlgorithm) > 128 ||
		strings.ContainsAny(snapshot.SignerIdentity+snapshot.KeyFingerprint+snapshot.SignatureAlgorithm, "\x00\r\n") ||
		!validSnapshotArchiveTime(snapshot.CreatedAt) || !validSnapshotArchiveTime(snapshot.PublishedAt) ||
		len(manifest.Packages) == 0 || len(manifest.Packages) > maxSnapshotArchivePackages || len(manifest.Assets) == 0 || len(manifest.Assets) > maxSnapshotArchiveAssets {
		return nil, ErrSnapshotArchiveCorrupt
	}
	expected := make(map[string]archiveObject)
	packageIdentities := make(map[string]struct{}, len(manifest.Packages))
	for _, item := range manifest.Packages {
		if item.CanonicalIdentity == "" || item.CanonicalIdentity != item.Package+"@"+item.Version+"#"+item.Architecture ||
			!repository.ValidAPTPublicationScope(item.Component) || !repository.ValidAPTObjectName(item.ObjectName) ||
			item.Size <= 0 ||
			item.Publisher == "" || len(item.Publisher) > 512 || strings.ContainsAny(item.Publisher, "\x00\r\n") ||
			!validSnapshotArchiveTime(item.CreatedAt) {
			return nil, ErrSnapshotArchiveCorrupt
		}
		if _, duplicate := packageIdentities[item.CanonicalIdentity+"\x00"+item.Component]; duplicate {
			return nil, ErrSnapshotArchiveCorrupt
		}
		packageIdentities[item.CanonicalIdentity+"\x00"+item.Component] = struct{}{}
		if err := addExpectedSnapshotArchiveObject(expected, item.Object, item.Digest, item.Size); err != nil {
			return nil, err
		}
	}
	assetPaths := make(map[string]SnapshotArchiveAsset, len(manifest.Assets))
	for _, asset := range manifest.Assets {
		if !repository.ValidAPTRepositoryPath(asset.Path) || asset.ContentType == "" || len(asset.ContentType) > 255 ||
			strings.ContainsAny(asset.ContentType, "\x00\r\n") {
			return nil, ErrSnapshotArchiveCorrupt
		}
		if _, duplicate := assetPaths[asset.Path]; duplicate {
			return nil, ErrSnapshotArchiveCorrupt
		}
		assetPaths[asset.Path] = asset
		if err := addExpectedSnapshotArchiveObject(expected, asset.Object, asset.Digest, asset.Size); err != nil {
			return nil, err
		}
	}

	for _, item := range manifest.Packages {
		asset, ok := assetPaths[repository.APTPoolPath(item.Component, item.Package, item.ObjectName)]
		if !ok || asset.Digest != item.Digest || asset.Size != item.Size {
			return nil, ErrSnapshotArchiveCorrupt
		}
	}
	for _, path := range []string{"Release", "InRelease", "Release.gpg"} {
		asset, ok := assetPaths["dists/"+snapshot.Suite+"/"+path]
		limit := int64(16 << 20)
		if path == "Release.gpg" {
			limit = 1 << 20
		}
		if !ok || asset.Size <= 0 || asset.Size > limit {
			return nil, ErrSnapshotArchiveCorrupt
		}
	}
	if len(expected) == 0 || len(expected) > maxSnapshotArchiveObjects {
		return nil, ErrSnapshotArchiveCorrupt
	}
	return expected, nil
}

func addExpectedSnapshotArchiveObject(expected map[string]archiveObject, name, digest string, size int64) error {
	wantName, err := portableArchiveObject(digest)
	if err != nil || name != wantName || size < 0 || size > 1<<30 {
		return ErrSnapshotArchiveCorrupt
	}
	if current, ok := expected[name]; ok {
		if current.digest != digest || current.size != size {
			return ErrSnapshotArchiveCorrupt
		}
		return nil
	}
	expected[name] = archiveObject{name: name, digest: digest, size: size}
	return nil
}

func validSnapshotArchiveTime(value string) bool {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && parsed.Location() == time.UTC
}

type aptArchiveContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r aptArchiveContextReader) Read(body []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, fmt.Errorf("read APT snapshot archive: %w", r.ctx.Err())
	default:
		return r.reader.Read(body)
	}
}

type archiveCountingReader struct {
	reader io.Reader
	count  int64
}

func (r *archiveCountingReader) Read(body []byte) (int, error) {
	n, err := r.reader.Read(body)
	r.count += int64(n)
	return n, err
}
