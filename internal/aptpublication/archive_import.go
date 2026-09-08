package aptpublication

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

var ErrArchiveRestoreBusy = errors.New("APT archive restore concurrency limit reached")

type SnapshotArchiveImporter struct {
	store           managerStore
	objects         objectstore.Store
	trust           SnapshotArchiveTrustVerifier
	slots           chan struct{}
	MaxArchiveBytes int64
}

func NewSnapshotArchiveImporter(store managerStore, objects objectstore.Store, trust SnapshotArchiveTrustVerifier) *SnapshotArchiveImporter {
	return &SnapshotArchiveImporter{store: store, objects: objects, trust: trust, slots: make(chan struct{}, 2), MaxArchiveBytes: MaxSnapshotArchiveImportBytes}
}

// Import requires an independently saved digest from the original backup.
// Signature trust authenticates Release content; the digest also pins the
// otherwise unsigned archive identity and provenance fields.
func (i *SnapshotArchiveImporter) Import(ctx context.Context, repositoryID, expectedArchiveDigest string, input io.Reader, actor string) (_ repository.APTRepositorySnapshot, err error) {
	if i == nil || i.store == nil || i.objects == nil || i.trust == nil || actor == "" || len(actor) > 512 || strings.ContainsAny(actor, "\x00\r\n") {
		return repository.APTRepositorySnapshot{}, ErrInvalidSnapshotArchiveInput
	}
	select {
	case i.slots <- struct{}{}:
		defer func() { <-i.slots }()
	default:
		return repository.APTRepositorySnapshot{}, ErrArchiveRestoreBusy
	}
	repo, err := i.store.GetHostedRepository(ctx, repositoryID)
	if err != nil {
		return repository.APTRepositorySnapshot{}, err
	}
	if repo.Format != repository.FormatAPT || repo.Type != repository.RepositoryTypeHosted || repo.State != repository.RepositoryActive {
		return repository.APTRepositorySnapshot{}, repository.ErrNotFound
	}
	archive, err := prepareTrustedArchive(ctx, input, i.MaxArchiveBytes, expectedArchiveDigest, i.trust)
	if err != nil {
		return repository.APTRepositorySnapshot{}, err
	}
	defer archive.close()
	if archive.manifest.Snapshot.RepositoryID != repositoryID {
		return repository.APTRepositorySnapshot{}, repository.ErrNotFound
	}
	snapshot, assets := snapshotArchiveRepositoryView(archive.manifest)
	plan := repository.APTArchiveRestorePlan{ID: uuid.NewString(), ArchiveDigest: archive.digest, Snapshot: snapshot, Assets: assets, Packages: make([]repository.APTArchivePackage, 0, len(archive.manifest.Packages))}
	revisionIDs := make(map[string]string)
	for _, p := range archive.manifest.Packages {
		id := revisionIDs[p.CanonicalIdentity]
		if id == "" {
			id = uuid.NewString()
			revisionIDs[p.CanonicalIdentity] = id
		}
		createdAt, _ := time.Parse(time.RFC3339Nano, p.CreatedAt)
		plan.Packages = append(plan.Packages, repository.APTArchivePackage{Component: p.Component, Revision: repository.APTPackageRevision{ID: id, RepositoryID: repositoryID, Package: p.Package, Version: p.Version, Architecture: p.Architecture, CanonicalIdentity: p.CanonicalIdentity, Digest: p.Digest, ObjectKey: "native/apt/sha256/" + strings.TrimPrefix(p.Digest, "sha256:"), Size: p.Size, ObjectName: p.ObjectName, Publisher: p.Publisher, CreatedAt: createdAt}})
	}
	snapshotCtx, releaseSnapshot, err := repository.LockObjectKeys(ctx, []string{"snapshot-lock/" + snapshot.ID}, i.store, repository.FormatAPT, i.store.LockAPTObject)
	if err != nil {
		return repository.APTRepositorySnapshot{}, err
	}
	defer releaseSnapshot()
	if err = i.store.BeginAPTArchiveRestore(snapshotCtx, plan); err != nil {
		return repository.APTRepositorySnapshot{}, err
	}
	defer func() {
		if err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(snapshotCtx), 5*time.Second)
			defer cancel()
			if failErr := i.store.FailAPTArchiveRestore(cleanupCtx, plan.ID); failErr != nil {
				err = errors.Join(err, fmt.Errorf("mark APT archive restore failed: %w", failErr))
			}
		}
	}()
	expected, err := validateSnapshotArchiveManifest(archive.manifest)
	if err != nil {
		return repository.APTRepositorySnapshot{}, err
	}
	names := make([]string, 0, len(expected))
	for name := range expected {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		object := expected[name]
		if err = i.writeObject(snapshotCtx, archive, name, object); err != nil {
			return repository.APTRepositorySnapshot{}, err
		}
	}
	return i.store.CommitAPTArchiveRestore(snapshotCtx, plan, archive.release, repository.AuditRecord{
		Repository: repo.Name, GroupName: repo.Name, Actor: actor, Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(), Format: string(repository.FormatAPT), Resource: "snapshots/" + snapshot.ID, Representation: snapshot.ReleaseDigest,
		Operation: "apt.repository_snapshot.restore", Status: 200, CacheDisposition: "bypass", AuthorizationSource: "repository_admin", AuthorizationReason: "trusted_archive_restored",
		Evidence: map[string]string{"archiveDigest": archive.digest, "restoreId": plan.ID, "keyFingerprint": snapshot.KeyFingerprint, "signerIdentity": snapshot.SignerIdentity, "signatureAlgorithm": snapshot.SignatureAlgorithm},
	})
}

func (i *SnapshotArchiveImporter) writeObject(ctx context.Context, archive *verifiedArchive, name string, object archiveObject) error {
	key := "native/apt/sha256/" + strings.TrimPrefix(object.digest, "sha256:")
	lockedCtx, release, err := repository.LockObjectKeys(ctx, []string{key}, i.store, repository.FormatAPT, i.store.LockAPTObject)
	if err != nil {
		return err
	}
	defer release()
	reader, err := archive.object(name)
	if err != nil {
		return err
	}
	if err = i.objects.PutVerifiedReader(lockedCtx, key, aptArchiveContextReader{ctx: lockedCtx, reader: reader}, object.size, object.digest); err != nil {
		return fmt.Errorf("write APT recovery object: %w", err)
	}
	return nil
}
