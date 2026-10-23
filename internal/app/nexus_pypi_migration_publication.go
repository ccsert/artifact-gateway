package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync/atomic"

	"github.com/ccsert/artifact-gateway/internal/nexusmigration"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	pypi "github.com/ccsert/artifact-gateway/internal/protocol/pypi"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

type NexusPyPIMigrationStore interface {
	repository.PyPIImportStore
	GetHostedRepository(context.Context, string) (repository.HostedRepository, error)
	GetPyPIFile(context.Context, string, string) (repository.PyPIFile, error)
	GetArtifactQuarantine(context.Context, string, repository.Format, string, string) (repository.ArtifactQuarantine, error)
	GetArtifactTombstone(context.Context, string, repository.Format, string) (repository.ArtifactTombstone, error)
	LockPyPIObject(context.Context, string) (func(), error)
}
type NexusPyPIMigrationPublication struct {
	store   NexusPyPIMigrationStore
	objects objectstore.Store
	scanner nexusMigrationPublicationScanner
}

func NewNexusPyPIMigrationPublication(store NexusPyPIMigrationStore, objects objectstore.Store) *NexusPyPIMigrationPublication {
	return &NexusPyPIMigrationPublication{store: store, objects: objects}
}
func (p *NexusPyPIMigrationPublication) WithPublicationScanner(scanner nexusMigrationPublicationScanner) *NexusPyPIMigrationPublication {
	p.scanner = scanner
	return p
}

var _ NexusFormatMigrationPublication = (*NexusPyPIMigrationPublication)(nil)

func (p *NexusPyPIMigrationPublication) CheckActor(ctx context.Context, actor string) error {
	if p == nil || nilDependency(p.store) || p.store.CheckPyPIImportActor(ctx, actor) != nil {
		return nexusmigration.Failed("pypi_actor_not_current_admin")
	}
	return nil
}
func (p *NexusPyPIMigrationPublication) target(ctx context.Context, e nexusmigration.Entry) error {
	if p == nil || nilDependency(p.store) || objectstore.Validate(p.objects) != nil {
		return nexusmigration.Failed("target_unavailable")
	}
	repo, err := p.store.GetHostedRepository(ctx, e.TargetRepositoryID)
	if err != nil || repo.Format != repository.FormatPyPI || repo.Type != repository.RepositoryTypeHosted || repo.State != repository.RepositoryActive {
		return nexusmigration.Failed("target_not_active_hosted")
	}
	return nil
}
func pypiMigrationClosure(e nexusmigration.Entry) (repository.PyPIImportClosure, error) {
	_, files, err := nexusmigration.PyPIEntryMetadata(e)
	if err != nil {
		return repository.PyPIImportClosure{}, err
	}
	project, version, _ := strings.Cut(e.Identity, "@")
	c := repository.PyPIImportClosure{RepositoryID: e.TargetRepositoryID, Project: project, Version: version, Audit: repository.AuditRecord{Actor: e.Actor, Evidence: map[string]string{"migrationEntryId": e.ID, "migrationBatchId": e.BatchID}}}
	assets := map[string]nexusmigration.Asset{}
	for _, a := range e.Assets {
		assets[a.TargetPath] = a
	}
	for _, f := range files {
		d, _ := pypi.FilenameIdentity(f.Filename)
		a := assets[f.Filename]
		c.Files = append(c.Files, repository.PyPIFile{RepositoryID: c.RepositoryID, Project: project, Version: version, Filename: f.Filename, FileType: d.FileType, PythonVersion: d.PythonVersion, RequiresPython: f.RequiresPython, Yanked: f.Yanked, YankReason: f.YankReason, Digest: "sha256:" + a.SHA256, ObjectKey: "native/pypi/sha256/" + a.SHA256, Size: a.Size, Publisher: "nexus-migration:" + e.ID})
	}
	return c, nil
}
func (p *NexusPyPIMigrationPublication) NativeAssets(e nexusmigration.Entry) ([]MigrationPublicationAsset, error) {
	c, err := pypiMigrationClosure(e)
	if err != nil {
		return nil, err
	}
	out := make([]MigrationPublicationAsset, 0, len(c.Files))
	for _, f := range c.Files {
		out = append(out, MigrationPublicationAsset{Path: f.Filename, ObjectKey: f.ObjectKey, Digest: f.Digest, Size: f.Size})
	}
	return out, nil
}
func (p *NexusPyPIMigrationPublication) NativeAsset(ctx context.Context, e nexusmigration.Entry, a MigrationPublicationAsset) (MigrationPublicationAsset, error) {
	f, err := p.store.GetPyPIFile(ctx, e.TargetRepositoryID, a.Path)
	return MigrationPublicationAsset{Path: a.Path, ObjectKey: f.ObjectKey, Digest: f.Digest, Size: f.Size}, err
}

type pypiMigrationLockKey struct{}
type pypiMigrationLock struct {
	owner            *NexusPyPIMigrationPublication
	fingerprint      string
	active, verified atomic.Bool
}

func pypiMigrationFingerprint(e nexusmigration.Entry) string {
	e.Assets = append([]nexusmigration.Asset(nil), e.Assets...)
	for i := range e.Assets {
		e.Assets[i].Uploaded = false
		e.Assets[i].Verified = false
	}
	raw, _ := json.Marshal(struct {
		ID, Batch, Source, Target, Identity, Actor string
		Assets                                     []nexusmigration.Asset
	}{e.ID, e.BatchID, e.SourceRepository, e.TargetRepositoryID, e.Identity, e.Actor, e.Assets})
	return string(raw)
}
func (p *NexusPyPIMigrationPublication) Lock(ctx context.Context, e nexusmigration.Entry) (context.Context, func(), error) {
	if err := p.target(ctx, e); err != nil {
		return ctx, nil, err
	}
	if err := p.CheckActor(ctx, e.Actor); err != nil {
		return ctx, nil, err
	}
	assets, err := p.NativeAssets(e)
	if err != nil {
		return ctx, nil, err
	}
	keys := make([]string, 0, len(assets))
	for _, a := range assets {
		keys = append(keys, a.ObjectKey)
	}
	ctx, release, err := repository.LockObjectKeys(ctx, keys, p.store, repository.FormatPyPI, p.store.LockPyPIObject)
	if err != nil {
		return ctx, nil, err
	}
	lock := &pypiMigrationLock{owner: p, fingerprint: pypiMigrationFingerprint(e)}
	lock.active.Store(true)
	return context.WithValue(ctx, pypiMigrationLockKey{}, lock), func() { lock.active.Store(false); release() }, nil
}
func (p *NexusPyPIMigrationPublication) held(ctx context.Context, e nexusmigration.Entry) (*pypiMigrationLock, error) {
	lock, _ := ctx.Value(pypiMigrationLockKey{}).(*pypiMigrationLock)
	if lock == nil || lock.owner != p || !lock.active.Load() || lock.fingerprint != pypiMigrationFingerprint(e) {
		return nil, nexusmigration.Failed("target_coordination_required")
	}
	return lock, nil
}
func (p *NexusPyPIMigrationPublication) Begin(ctx context.Context, e nexusmigration.Entry) (string, error) {
	if _, err := p.held(ctx, e); err != nil {
		return "", err
	}
	return e.ID, nil
}
func (p *NexusPyPIMigrationPublication) Stage(ctx context.Context, e nexusmigration.Entry, a nexusmigration.Asset, input io.ReadSeeker) error {
	lock, err := p.held(ctx, e)
	if err != nil {
		return err
	}
	lock.verified.Store(false)
	found := false
	for _, v := range e.Assets {
		if v.SourcePath == a.SourcePath && v.TargetPath == a.TargetPath && v.SHA256 == a.SHA256 && v.Size == a.Size {
			found = true
		}
	}
	if !found || e.SessionID != e.ID {
		return nexusmigration.Failed("invalid_publication")
	}
	if err = nexusVerifyInput(ctx, input, a); err != nil {
		return err
	}
	if a.TargetPath == nexusmigration.PyPISimplePath {
		return nil
	}
	key := "native/pypi/sha256/" + a.SHA256
	if stat, err := p.objects.Stat(ctx, key); err == nil {
		if stat.Size != a.Size {
			return nexusmigration.Failed("target_bytes_mismatch")
		}
		return p.verifyFile(ctx, e, a, key)
	} else if !errors.Is(err, objectstore.ErrNotFound) {
		return nexusmigration.Failed("target_upload_failed")
	}
	if err = p.objects.PutVerifiedReader(ctx, key, input, a.Size, "sha256:"+a.SHA256); err != nil {
		return nexusmigration.Failed("target_upload_failed")
	}
	return nil
}
func (p *NexusPyPIMigrationPublication) verifyFile(ctx context.Context, e nexusmigration.Entry, a nexusmigration.Asset, key string) error {
	_, files, err := nexusmigration.PyPIEntryMetadata(e)
	if err != nil {
		return err
	}
	var expected *pypi.File
	for i := range files {
		if files[i].Filename == a.TargetPath {
			expected = &files[i]
		}
	}
	if expected == nil {
		return nexusmigration.Failed("pypi_closure_invalid")
	}
	input, size, err := p.objects.Open(ctx, key)
	if err != nil {
		return nexusmigration.Failed("target_bytes_mismatch")
	}
	defer func() { _ = input.Close() }()
	spool, err := os.CreateTemp("", "artifact-gateway-pypi-verify-*")
	if err != nil {
		return nexusmigration.Failed("temporary_storage_unavailable")
	}
	name := spool.Name()
	_ = os.Remove(name)
	defer func() { _ = spool.Close(); _ = os.Remove(name) }()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(spool, hash), &nexusContextReader{ctx: ctx, reader: io.LimitReader(input, a.Size+1)})
	if err != nil || size != a.Size || n != a.Size || hex.EncodeToString(hash.Sum(nil)) != a.SHA256 {
		return nexusmigration.Failed("target_bytes_mismatch")
	}
	d, err := pypi.Inspect(spool, a.Size, a.TargetPath)
	if err != nil || d.Project+"@"+d.Version != e.Identity || d.RequiresPython != expected.RequiresPython {
		return nexusmigration.Failed("target_bytes_mismatch")
	}
	return nil
}
func (p *NexusPyPIMigrationPublication) Verify(ctx context.Context, e nexusmigration.Entry) error {
	lock, err := p.held(ctx, e)
	if err != nil {
		return err
	}
	lock.verified.Store(false)
	if err = p.target(ctx, e); err != nil {
		return err
	}
	for _, a := range e.Assets {
		if a.TargetPath != nexusmigration.PyPISimplePath {
			if err = p.verifyFile(ctx, e, a, "native/pypi/sha256/"+a.SHA256); err != nil {
				return err
			}
		}
	}
	lock.verified.Store(true)
	return nil
}
func (p *NexusPyPIMigrationPublication) Readback(ctx context.Context, e nexusmigration.Entry) (nexusmigration.Readback, error) {
	if err := p.target(ctx, e); err != nil {
		return "", err
	}
	c, err := pypiMigrationClosure(e)
	if err != nil {
		return "", err
	}
	if _, err = p.store.GetArtifactTombstone(ctx, c.RepositoryID, repository.FormatPyPI, e.Identity); err == nil {
		return nexusmigration.Conflict, nil
	} else if !errors.Is(err, repository.ErrNotFound) {
		return "", err
	}
	missing := false
	for _, want := range c.Files {
		quarantine, qerr := p.store.GetArtifactQuarantine(ctx, c.RepositoryID, repository.FormatPyPI, e.Identity, want.Digest)
		if qerr != nil && !errors.Is(qerr, repository.ErrNotFound) {
			return "", qerr
		}
		if qerr == nil && quarantine.State == repository.ArtifactQuarantineStateQuarantined {
			return "", nexusmigration.Failed("artifact_quarantined")
		}
		got, err := p.store.GetPyPIFile(ctx, c.RepositoryID, want.Filename)
		if errors.Is(err, repository.ErrNotFound) {
			missing = true
			continue
		}
		if err != nil {
			return "", err
		}
		if !repository.PyPIImportFileMatches(got, want) {
			return nexusmigration.Conflict, nil
		}
		for _, a := range e.Assets {
			if a.TargetPath == want.Filename {
				if err = p.verifyFile(ctx, e, a, got.ObjectKey); err != nil {
					return nexusmigration.Conflict, nil
				}
			}
		}
	}
	if missing {
		return nexusmigration.Absent, nil
	}
	return nexusmigration.Identical, nil
}
func (p *NexusPyPIMigrationPublication) Publish(ctx context.Context, e nexusmigration.Entry) error {
	lock, err := p.held(ctx, e)
	if err != nil {
		return err
	}
	if !lock.verified.Load() {
		return nexusmigration.Failed("target_verification_required")
	}
	c, err := pypiMigrationClosure(e)
	if err != nil {
		return err
	}
	if err = p.store.PublishPyPIImport(ctx, c); err != nil {
		switch {
		case errors.Is(err, repository.ErrNameExists):
			return nexusmigration.Failed("target_conflict")
		case errors.Is(err, repository.ErrArtifactQuarantined):
			return nexusmigration.Failed("artifact_quarantined")
		case errors.Is(err, repository.ErrDisabled):
			return nexusmigration.Failed("pypi_actor_or_target_unavailable")
		case repository.IsQuotaExceeded(err):
			return nexusmigration.Failed("target_quota_exceeded")
		}
		return err
	}
	if !nilDependency(p.scanner) {
		for _, f := range c.Files {
			_ = p.scanner.ScheduleRepository(ctx, c.RepositoryID, repository.FormatPyPI, e.Identity, f.Digest, e.Actor)
		}
	}
	return nil
}
