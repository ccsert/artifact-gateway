package aptpublication

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	aptprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/apt"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

var ErrSnapshotArchiveTooLarge = errors.New("APT archive exceeds restore size limit")

const MaxSnapshotArchiveImportBytes = int64(64 << 30)

type archiveEntry struct{ offset, size int64 }

type verifiedArchive struct {
	file     *os.File
	manifest SnapshotArchiveManifest
	entries  map[string]archiveEntry
	release  []byte
	digest   string
}

func prepareTrustedArchive(ctx context.Context, input io.Reader, maxBytes int64, expectedDigest string, trust SnapshotArchiveTrustVerifier) (_ *verifiedArchive, err error) {
	if input == nil || trust == nil || !repository.ValidAPTSHA256Digest(expectedDigest) {
		return nil, ErrInvalidSnapshotArchiveInput
	}
	if maxBytes <= 0 || maxBytes > MaxSnapshotArchiveImportBytes {
		maxBytes = MaxSnapshotArchiveImportBytes
	}
	file, err := os.CreateTemp("", "artifact-gateway-apt-restore-*.tar")
	if err != nil {
		return nil, fmt.Errorf("create APT recovery spool: %w", err)
	}
	archive := &verifiedArchive{file: file, entries: make(map[string]archiveEntry)}
	defer func() {
		if err != nil {
			archive.close()
		}
	}()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(aptArchiveContextReader{ctx: ctx, reader: input}, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("spool APT recovery archive: %w", err)
	}
	if written > maxBytes {
		return nil, ErrSnapshotArchiveTooLarge
	}
	if written == 0 {
		return nil, ErrInvalidSnapshotArchiveInput
	}
	archive.digest = "sha256:" + hex.EncodeToString(hash.Sum(nil))
	if archive.digest != expectedDigest {
		return nil, ErrArchiveReceiptMismatch
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	archive.manifest, err = VerifySnapshotArchive(ctx, file)
	if err != nil {
		return nil, err
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	reader := tar.NewReader(file)
	for {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		header, nextErr := reader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return nil, nextErr
		}
		offset, seekErr := file.Seek(0, io.SeekCurrent)
		if seekErr != nil {
			return nil, seekErr
		}
		archive.entries[header.Name] = archiveEntry{offset: offset, size: header.Size}
	}
	assets := make(map[string]SnapshotArchiveAsset, len(archive.manifest.Assets))
	for _, asset := range archive.manifest.Assets {
		assets[asset.Path] = asset
	}
	prefix := "dists/" + archive.manifest.Snapshot.Suite + "/"
	archive.release, err = archive.readObject(assets[prefix+"Release"].Object, 16<<20)
	if err != nil {
		return nil, err
	}
	inRelease, err := archive.readObject(assets[prefix+"InRelease"].Object, 16<<20)
	if err != nil {
		return nil, err
	}
	detached, err := archive.readObject(assets[prefix+"Release.gpg"].Object, 1<<20)
	if err != nil {
		return nil, err
	}
	if err = trust.Verify(ctx, archive.manifest, archive.release, inRelease, detached); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSnapshotArchiveUntrusted, err)
	}
	if err = archive.verifyCanonicalPublication(ctx, assets); err != nil {
		return nil, err
	}
	return archive, nil
}

func (a *verifiedArchive) close() {
	_ = a.file.Close()
	_ = os.Remove(a.file.Name())
}

func (a *verifiedArchive) object(name string) (*io.SectionReader, error) {
	entry, ok := a.entries[name]
	if !ok {
		return nil, ErrSnapshotArchiveCorrupt
	}
	return io.NewSectionReader(a.file, entry.offset, entry.size), nil
}

func (a *verifiedArchive) readObject(name string, limit int64) ([]byte, error) {
	entry, ok := a.entries[name]
	if !ok || entry.size > limit {
		return nil, ErrSnapshotArchiveCorrupt
	}
	reader, err := a.object(name)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(reader)
	if err != nil || int64(len(body)) != entry.size {
		return nil, ErrSnapshotArchiveCorrupt
	}
	return body, nil
}

// Archive v1 is an exact export of our deterministic publisher. Reconstructing
// its repository-owned fields binds every restored member, pool path, index,
// gzip representation, suite, sequence and creation time to the signed Release.
// A self-consistent manifest is not itself a signed authorization to add a package.
func (a *verifiedArchive) verifyCanonicalPublication(ctx context.Context, assets map[string]SnapshotArchiveAsset) error {
	snapshot, _ := snapshotArchiveRepositoryView(a.manifest)
	packages := make([]snapshotPackage, 0, len(a.manifest.Packages))
	total := 0
	for _, item := range a.manifest.Packages {
		reader, err := a.object(item.Object)
		if err != nil {
			return err
		}
		metadata, err := aptprotocol.ParseDebianBinary(aptArchiveContextReader{ctx: ctx, reader: reader}, item.Size)
		if err != nil {
			return ErrSnapshotArchiveCorrupt
		}
		poolPath := repository.APTPoolPath(item.Component, item.Package, item.ObjectName)
		stanza, err := aptprotocol.PackageIndexStanza(metadata, poolPath, item.Size, item.Digest)
		if err != nil || len(stanza) > maxAPTGeneratedIndexBytes-total {
			return ErrSnapshotArchiveCorrupt
		}
		total += len(stanza)
		// Keep only bounded, normalized index fields; the builder replaces the three
		// repository-owned fields again. Remove one stanza separator newline.
		metadata.Control = stanza[:len(stanza)-1]
		packages = append(packages, snapshotPackage{metadata: metadata, component: item.Component, poolPath: poolPath, revision: repository.APTPackageRevision{
			Package: item.Package, Version: item.Version, Architecture: item.Architecture, ObjectName: item.ObjectName,
			Digest: item.Digest, ObjectKey: "native/apt/sha256/" + strings.TrimPrefix(item.Digest, "sha256:"), Size: item.Size,
		}})
	}
	sort.Slice(packages, func(i, j int) bool {
		l, r := packages[i], packages[j]
		return strings.Join([]string{l.component, l.revision.Architecture, l.revision.Package, l.revision.Version, l.revision.ObjectName}, "\x00") < strings.Join([]string{r.component, r.revision.Architecture, r.revision.Package, r.revision.Version, r.revision.ObjectName}, "\x00")
	})
	bundle, err := buildSnapshotBundle(snapshot, packages)
	if err != nil || digestBytes(bundle.release) != snapshot.ReleaseDigest || len(assets) != len(bundle.assets)+3 {
		return ErrSnapshotArchiveCorrupt
	}
	for _, expected := range bundle.assets {
		actual, ok := assets[expected.Path]
		if !ok || expected.Digest != actual.Digest || expected.Size != actual.Size || expected.ContentType != actual.ContentType {
			return ErrSnapshotArchiveCorrupt
		}
	}
	return nil
}
