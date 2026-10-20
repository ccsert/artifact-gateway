package app

import (
	"context"
	"errors"
	"io"
	"sort"
	"sync/atomic"
	"time"

	"github.com/ccsert/artifact-gateway/internal/nexusmigration"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

// NexusConanMigrationStore is the Conan-only inventory and staging port. The
// explicit publication port retains its concrete transaction/lease semantics.
type NexusConanMigrationStore interface {
	GetHostedRepository(context.Context, string) (repository.HostedRepository, error)
	CreateConanPublishSession(context.Context, repository.ConanPublishSession) (repository.ConanPublishSession, error)
	GetConanPublishSession(context.Context, string) (repository.ConanPublishSession, error)
	ListConanPublishUploads(context.Context, string) (map[string]string, error)
	StageConanObject(context.Context, repository.ConanObjectIntent) error
	GetConanRecipeRevision(context.Context, string, string, string) (repository.ConanRecipeRevision, error)
	GetConanPackageRevision(context.Context, string, string, string, string, string) (repository.ConanPackageRevision, error)
	ListConanRecipeAssets(context.Context, string, string, string) ([]repository.ConanAsset, error)
	ListConanPackageAssets(context.Context, string, string, string, string, string) ([]repository.ConanAsset, error)
}

// NexusConanMigrationPublication imports complete verified source revisions
// through the same manifest validation and atomic commit as ordinary publication.
// Capacity and whole-batch admission belong to the migration coordinator.
type NexusConanMigrationPublication struct {
	store        NexusConanMigrationStore
	publications repository.ConanPublicationStore
	objects      objectstore.Store
	scanner      nexusMigrationPublicationScanner
}

func NewNexusConanMigrationPublication(store NexusConanMigrationStore, publications repository.ConanPublicationStore, objects objectstore.Store) *NexusConanMigrationPublication {
	return &NexusConanMigrationPublication{store: store, publications: publications, objects: objects}
}

func (p *NexusConanMigrationPublication) WithPublicationScanner(scanner nexusMigrationPublicationScanner) *NexusConanMigrationPublication {
	p.scanner = scanner
	return p
}

func (p *NexusConanMigrationPublication) target(ctx context.Context, entry nexusmigration.Entry) error {
	if p == nil || nilDependency(p.store) || nilDependency(p.publications) || objectstore.Validate(p.objects) != nil {
		return nexusmigration.Failed("target_unavailable")
	}
	repo, err := p.store.GetHostedRepository(ctx, entry.TargetRepositoryID)
	if err != nil || repo.Format != repository.FormatConan || repo.Type != repository.RepositoryTypeHosted || repo.State != repository.RepositoryActive {
		return nexusmigration.Failed("target_not_active_hosted")
	}
	return nil
}

func nexusConanSession(entry nexusmigration.Entry) (repository.ConanPublishSession, error) {
	if entry.ID == "" || entry.TargetRepositoryID == "" {
		return repository.ConanPublishSession{}, nexusmigration.Failed("invalid_publication")
	}
	if _, err := uuid.Parse(entry.ID); err != nil {
		return repository.ConanPublishSession{}, nexusmigration.Failed("invalid_publication")
	}
	if err := nexusmigration.ValidateConanEntry(entry); err != nil {
		return repository.ConanPublishSession{}, err
	}
	c, _ := nexusmigration.ParseConanIdentity(entry.Identity)
	session := repository.ConanPublishSession{RepositoryID: entry.TargetRepositoryID, Publisher: "nexus-migration:" + entry.ID,
		Kind: c.Kind, Reference: c.Reference, RecipeRevision: c.RecipeRevision, PackageID: c.PackageID, PackageRevision: c.PackageRevision,
		State: "open", ExpiresAt: time.Now().UTC().Add(time.Hour)}
	for _, asset := range entry.Assets {
		session.Objects = append(session.Objects, repository.MavenDeclaredObject{Name: asset.TargetPath, Digest: "sha256:" + asset.SHA256, Size: asset.Size})
	}
	sort.Slice(session.Objects, func(i, j int) bool { return session.Objects[i].Name < session.Objects[j].Name })
	return session, nil
}

func sameNexusConanSession(a, b repository.ConanPublishSession) bool {
	if a.Native || a.RepositoryID != b.RepositoryID || a.Publisher != b.Publisher || a.Kind != b.Kind || a.Reference != b.Reference ||
		a.RecipeRevision != b.RecipeRevision || a.PackageID != b.PackageID || a.PackageRevision != b.PackageRevision || len(a.Objects) != len(b.Objects) {
		return false
	}
	objects := make(map[string]repository.MavenDeclaredObject, len(a.Objects))
	for _, object := range a.Objects {
		if _, duplicate := objects[object.Name]; duplicate {
			return false
		}
		objects[object.Name] = object
	}
	for _, object := range b.Objects {
		if objects[object.Name] != object {
			return false
		}
	}
	return true
}

type nexusConanLockKey struct{}
type nexusConanLock struct {
	owner            *NexusConanMigrationPublication
	identity         string
	active, verified atomic.Bool
	verifiedVersion  atomic.Int64
}

func (p *NexusConanMigrationPublication) Lock(ctx context.Context, entry nexusmigration.Entry) (context.Context, func(), error) {
	if _, err := nexusConanSession(entry); err != nil {
		return ctx, nil, err
	}
	if err := p.target(ctx, entry); err != nil {
		return ctx, nil, err
	}
	lock := &nexusConanLock{owner: p, identity: nexusConanInventoryIdentity(entry)}
	lock.active.Store(true)
	return context.WithValue(ctx, nexusConanLockKey{}, lock), func() { lock.active.Store(false) }, nil
}

func nexusConanInventoryIdentity(entry nexusmigration.Entry) string {
	session, _ := nexusConanSession(entry)
	return entry.ID + "\x00" + entry.BatchID + "\x00" + entry.TargetRepositoryID + "\x00" + entry.Identity + "\x00" + conanPublishDigest(session.Objects)
}

func (p *NexusConanMigrationPublication) held(ctx context.Context, entry nexusmigration.Entry) (*nexusConanLock, error) {
	lock, _ := ctx.Value(nexusConanLockKey{}).(*nexusConanLock)
	if lock == nil || lock.owner != p || !lock.active.Load() || lock.identity != nexusConanInventoryIdentity(entry) {
		return nil, nexusmigration.Failed("target_coordination_required")
	}
	return lock, nil
}

func (p *NexusConanMigrationPublication) Begin(ctx context.Context, entry nexusmigration.Entry) (string, error) {
	if _, err := p.held(ctx, entry); err != nil {
		return "", err
	}
	expected, err := nexusConanSession(entry)
	if err != nil {
		return "", err
	}
	if entry.SessionID != "" {
		session, getErr := p.store.GetConanPublishSession(ctx, entry.SessionID)
		if getErr == nil && !sameNexusConanSession(session, expected) {
			return "", nexusmigration.Failed("native_session_conflict")
		}
		if getErr == nil && session.State == "open" && session.ExpiresAt.After(time.Now()) {
			return session.ID, nil
		}
		if getErr != nil && !errors.Is(getErr, repository.ErrNotFound) {
			return "", nexusmigration.Failed("native_session_unavailable")
		}
	}
	expected.ID = uuid.NewString()
	session, err := p.store.CreateConanPublishSession(ctx, expected)
	if err != nil || !sameNexusConanSession(session, expected) {
		return "", nexusmigration.Failed("native_session_unavailable")
	}
	return session.ID, nil
}

func (p *NexusConanMigrationPublication) session(ctx context.Context, entry nexusmigration.Entry) (repository.ConanPublishSession, error) {
	expected, err := nexusConanSession(entry)
	if err != nil {
		return repository.ConanPublishSession{}, err
	}
	session, err := p.store.GetConanPublishSession(ctx, entry.SessionID)
	if err != nil || !sameNexusConanSession(session, expected) || session.State != "open" || !session.ExpiresAt.After(time.Now()) {
		return repository.ConanPublishSession{}, nexusmigration.Failed("native_session_conflict")
	}
	return session, nil
}

func nexusConanObjectKey(entry nexusmigration.Entry, asset nexusmigration.Asset) string {
	session := entry.SessionID
	if session == "" {
		session = "unallocated"
	}
	return "native/conan/migration/" + entry.ID + "/" + session + "/" + asset.SHA256 + "/" + asset.TargetPath
}

func (p *NexusConanMigrationPublication) Stage(ctx context.Context, entry nexusmigration.Entry, asset nexusmigration.Asset, input io.ReadSeeker) error {
	lock, err := p.held(ctx, entry)
	if err != nil {
		return err
	}
	lock.verified.Store(false)
	session, err := p.session(ctx, entry)
	if err != nil {
		return err
	}
	found := false
	for _, declared := range entry.Assets {
		if declared.SourcePath == asset.SourcePath && declared.TargetPath == asset.TargetPath && declared.Size == asset.Size && declared.SHA256 == asset.SHA256 {
			found = true
			break
		}
	}
	if !found {
		return nexusmigration.Failed("invalid_publication")
	}
	if err = nexusVerifyInput(ctx, input, asset); err != nil {
		return err
	}
	key := nexusConanObjectKey(entry, asset)
	if err = p.store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: entry.TargetRepositoryID, ObjectKey: key, Digest: "sha256:" + asset.SHA256, Size: asset.Size}); err != nil {
		return nexusmigration.Failed("target_conflict")
	}
	if err = p.objects.PutVerifiedReader(ctx, key, input, asset.Size, "sha256:"+asset.SHA256); err != nil {
		return nexusmigration.Failed("target_upload_failed")
	}
	_, err = p.publications.StageConanPublicationObject(ctx, session.ID, repository.MavenDeclaredObject{Name: asset.TargetPath, Digest: "sha256:" + asset.SHA256, Size: asset.Size}, key)
	if err != nil {
		return nexusmigration.Failed("native_session_conflict")
	}
	return nil
}

func (p *NexusConanMigrationPublication) Verify(ctx context.Context, entry nexusmigration.Entry) error {
	lock, err := p.held(ctx, entry)
	if err != nil {
		return err
	}
	lock.verified.Store(false)
	session, err := p.session(ctx, entry)
	if err != nil {
		return err
	}
	uploads, err := p.store.ListConanPublishUploads(ctx, session.ID)
	if err != nil {
		return nexusmigration.Failed("native_session_unavailable")
	}
	if err = validateConanPublication(ctx, p.objects, session, uploads); err != nil {
		return nexusmigration.Failed("conan_manifest_invalid")
	}
	lock.verifiedVersion.Store(session.Version)
	lock.verified.Store(true)
	return nil
}

func (p *NexusConanMigrationPublication) Publish(ctx context.Context, entry nexusmigration.Entry) error {
	lock, err := p.held(ctx, entry)
	if err != nil {
		return err
	}
	if !lock.verified.Load() {
		return nexusmigration.Failed("target_verification_required")
	}
	if err = p.target(ctx, entry); err != nil {
		return err
	}
	session, err := p.session(ctx, entry)
	if err != nil {
		return err
	}
	digest := conanPublishDigest(session.Objects)
	_, _, err = p.publications.CommitConanPublicationSession(ctx, session.ID, lock.verifiedVersion.Load(), digest, entry.Actor)
	if errors.Is(err, nexusmigration.ErrLeaseLost) || errors.Is(err, nexusmigration.ErrStopped) || ctx.Err() != nil {
		return err
	}
	if repository.IsQuotaExceeded(err) {
		return nexusmigration.Failed("target_quota_exceeded")
	}
	if err != nil {
		return nexusmigration.Failed("target_conflict")
	}
	if !nilDependency(p.scanner) {
		_ = p.scanner.ScheduleRepository(ctx, entry.TargetRepositoryID, repository.FormatConan, entry.Identity, digest, entry.Actor)
	}
	return nil
}

func (p *NexusConanMigrationPublication) revision(ctx context.Context, entry nexusmigration.Entry, c nexusmigration.ConanCoordinate) (string, string, error) {
	recipe, err := p.store.GetConanRecipeRevision(ctx, entry.TargetRepositoryID, c.Reference, c.RecipeRevision)
	if err != nil {
		return "", "", err
	}
	if c.Kind == "recipe" || recipe.State != "visible" {
		return recipe.Digest, recipe.State, nil
	}
	pkg, err := p.store.GetConanPackageRevision(ctx, entry.TargetRepositoryID, c.Reference, c.RecipeRevision, c.PackageID, c.PackageRevision)
	return pkg.Digest, pkg.State, err
}

func (p *NexusConanMigrationPublication) assets(ctx context.Context, entry nexusmigration.Entry, c nexusmigration.ConanCoordinate) ([]repository.ConanAsset, error) {
	if c.Kind == "recipe" {
		return p.store.ListConanRecipeAssets(ctx, entry.TargetRepositoryID, c.Reference, c.RecipeRevision)
	}
	return p.store.ListConanPackageAssets(ctx, entry.TargetRepositoryID, c.Reference, c.RecipeRevision, c.PackageID, c.PackageRevision)
}

func (p *NexusConanMigrationPublication) Readback(ctx context.Context, entry nexusmigration.Entry) (nexusmigration.Readback, error) {
	expected, err := nexusConanSession(entry)
	if err != nil {
		return "", err
	}
	if err = p.target(ctx, entry); err != nil {
		return "", err
	}
	c, _ := nexusmigration.ParseConanIdentity(entry.Identity)
	digest, state, err := p.revision(ctx, entry, c)
	if errors.Is(err, repository.ErrNotFound) {
		return nexusmigration.Absent, nil
	}
	if err != nil {
		return "", nexusmigration.Failed("target_readback_failed")
	}
	if state != "visible" || digest != conanPublishDigest(expected.Objects) {
		return nexusmigration.Conflict, nil
	}
	assets, err := p.assets(ctx, entry, c)
	if err != nil {
		return "", nexusmigration.Failed("target_readback_failed")
	}
	if len(assets) != len(expected.Objects) {
		return nexusmigration.Conflict, nil
	}
	byName := make(map[string]repository.ConanAsset, len(assets))
	uploads := make(map[string]string, len(assets))
	for _, asset := range assets {
		byName[asset.Path] = asset
		uploads[asset.Path] = asset.ObjectKey
	}
	for _, object := range expected.Objects {
		actual := byName[object.Name]
		if actual.RepositoryID != entry.TargetRepositoryID || actual.Reference != c.Reference || actual.RecipeRevision != c.RecipeRevision ||
			actual.PackageID != c.PackageID || actual.PackageRevision != c.PackageRevision || actual.Digest != object.Digest || actual.Size != object.Size {
			return nexusmigration.Conflict, nil
		}
		if err = nexusVerifyObject(ctx, p.objects, repository.MavenAsset{ObjectKey: actual.ObjectKey, Digest: actual.Digest, Size: actual.Size}); err != nil {
			var mismatch *nexusmigration.Failure
			if errors.Is(err, objectstore.ErrNotFound) || errors.As(err, &mismatch) {
				return nexusmigration.Conflict, nil
			}
			return "", nexusmigration.Failed("target_readback_failed")
		}
	}
	if err = validateConanPublication(ctx, p.objects, expected, uploads); err != nil {
		return nexusmigration.Conflict, nil
	}
	current, err := p.assets(ctx, entry, c)
	if err != nil {
		return "", nexusmigration.Failed("target_readback_failed")
	}
	if len(current) != len(assets) {
		return nexusmigration.Conflict, nil
	}
	for _, asset := range current {
		if byName[asset.Path] != asset {
			return nexusmigration.Conflict, nil
		}
	}
	digest, state, err = p.revision(ctx, entry, c)
	if err != nil || state != "visible" || digest != conanPublishDigest(expected.Objects) {
		return nexusmigration.Conflict, nil
	}
	return nexusmigration.Identical, nil
}

func (p *NexusConanMigrationPublication) NativeAssets(entry nexusmigration.Entry) ([]MigrationPublicationAsset, error) {
	if _, err := nexusConanSession(entry); err != nil {
		return nil, err
	}
	assets := make([]MigrationPublicationAsset, 0, len(entry.Assets))
	for _, asset := range entry.Assets {
		assets = append(assets, MigrationPublicationAsset{Path: entry.Identity + "/" + asset.TargetPath,
			ObjectKey: nexusConanObjectKey(entry, asset), Digest: "sha256:" + asset.SHA256, Size: asset.Size})
	}
	return assets, nil
}

func (p *NexusConanMigrationPublication) NativeAsset(ctx context.Context, entry nexusmigration.Entry, planned MigrationPublicationAsset) (MigrationPublicationAsset, error) {
	if err := p.target(ctx, entry); err != nil {
		return MigrationPublicationAsset{}, err
	}
	expected, err := p.NativeAssets(entry)
	if err != nil {
		return MigrationPublicationAsset{}, err
	}
	found := false
	for _, asset := range expected {
		found = found || asset.Path == planned.Path && asset.Digest == planned.Digest && asset.Size == planned.Size
	}
	if !found {
		return MigrationPublicationAsset{}, nexusmigration.Failed("invalid_publication")
	}
	c, _ := nexusmigration.ParseConanIdentity(entry.Identity)
	assets, err := p.assets(ctx, entry, c)
	if err != nil {
		return MigrationPublicationAsset{}, err
	}
	for _, asset := range assets {
		if entry.Identity+"/"+asset.Path == planned.Path {
			return MigrationPublicationAsset{Path: planned.Path, ObjectKey: asset.ObjectKey, Digest: asset.Digest, Size: asset.Size}, nil
		}
	}
	return MigrationPublicationAsset{}, repository.ErrNotFound
}
