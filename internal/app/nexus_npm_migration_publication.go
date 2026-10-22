package app

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/ccsert/artifact-gateway/internal/nexusmigration"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	npmprotocol "github.com/ccsert/artifact-gateway/internal/protocol/npm"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

// NexusNPMMigrationStore retains only native npm publication and its readback.
type NexusNPMMigrationStore interface {
	repository.NPMImportStore
	GetHostedRepository(context.Context, string) (repository.HostedRepository, error)
	GetNPMPackage(context.Context, string, string) (repository.NPMPackage, error)
	GetNPMVersion(context.Context, string, string, string) (repository.NPMVersion, error)
	GetArtifactQuarantine(context.Context, string, repository.Format, string, string) (repository.ArtifactQuarantine, error)
	LockNPMObject(context.Context, string) (func(), error)
}

type NexusNPMMigrationPublication struct {
	store   NexusNPMMigrationStore
	objects objectstore.Store
	scanner nexusMigrationPublicationScanner
}

func NewNexusNPMMigrationPublication(store NexusNPMMigrationStore, objects objectstore.Store) *NexusNPMMigrationPublication {
	return &NexusNPMMigrationPublication{store: store, objects: objects}
}
func (p *NexusNPMMigrationPublication) WithPublicationScanner(scanner nexusMigrationPublicationScanner) *NexusNPMMigrationPublication {
	p.scanner = scanner
	return p
}

var _ NexusFormatMigrationPublication = (*NexusNPMMigrationPublication)(nil)

func (p *NexusNPMMigrationPublication) target(ctx context.Context, e nexusmigration.Entry) error {
	if p == nil || nilDependency(p.store) || objectstore.Validate(p.objects) != nil {
		return nexusmigration.Failed("target_unavailable")
	}
	repo, err := p.store.GetHostedRepository(ctx, e.TargetRepositoryID)
	if err != nil || repo.Format != repository.FormatNPM || repo.Type != repository.RepositoryTypeHosted || repo.State != repository.RepositoryActive {
		return nexusmigration.Failed("target_not_active_hosted")
	}
	return nil
}
func npmMigrationClosure(e nexusmigration.Entry) (repository.NPMImportClosure, error) {
	doc, assets, err := nexusmigration.NPMEntryMetadata(e)
	if err != nil {
		return repository.NPMImportClosure{}, err
	}
	c := repository.NPMImportClosure{RepositoryID: e.TargetRepositoryID, PackageName: e.Identity, DistTags: doc.DistTags, Audit: repository.AuditRecord{Actor: e.Actor, Evidence: map[string]string{"migrationEntryId": e.ID, "migrationBatchId": e.BatchID}}}
	for version, a := range assets {
		var identity npmprotocol.ImportVersion
		_ = json.Unmarshal(doc.Versions[version], &identity)
		manifest, err := npmprotocol.CanonicalImportManifest(doc.Versions[version])
		if err != nil || !repository.ValidNPMDeprecation(identity.Deprecated) {
			return c, nexusmigration.Failed("npm_packument_invalid")
		}
		sha512Bytes, err := hex.DecodeString(a.SHA512)
		if err != nil {
			return c, nexusmigration.Failed("npm_closure_invalid")
		}
		c.Versions = append(c.Versions, repository.NPMVersion{RepositoryID: e.TargetRepositoryID, PackageName: e.Identity, Version: version, Digest: "sha256:" + a.SHA256, Integrity: "sha512-" + base64.StdEncoding.EncodeToString(sha512Bytes), Shasum: a.SHA1, TarballName: path.Base(a.SourcePath), ObjectKey: "native/npm/sha256/" + a.SHA256, Size: a.Size, Manifest: manifest, Deprecated: identity.Deprecated, Publisher: "nexus-migration:" + e.ID})
	}
	sort.Slice(c.Versions, func(i, j int) bool { return c.Versions[i].Version < c.Versions[j].Version })
	return c, nil
}
func (p *NexusNPMMigrationPublication) NativeAssets(e nexusmigration.Entry) ([]MigrationPublicationAsset, error) {
	c, err := npmMigrationClosure(e)
	if err != nil {
		return nil, err
	}
	out := make([]MigrationPublicationAsset, 0, len(c.Versions))
	for _, v := range c.Versions {
		out = append(out, MigrationPublicationAsset{Path: e.Identity + "/" + v.Version, ObjectKey: v.ObjectKey, Digest: v.Digest, Size: v.Size})
	}
	return out, nil
}
func (p *NexusNPMMigrationPublication) NativeAsset(ctx context.Context, e nexusmigration.Entry, a MigrationPublicationAsset) (MigrationPublicationAsset, error) {
	version, valid := strings.CutPrefix(a.Path, e.Identity+"/")
	if !valid || !npmprotocol.ValidVersion(version) {
		return MigrationPublicationAsset{}, nexusmigration.Failed("invalid_publication")
	}
	v, err := p.store.GetNPMVersion(ctx, e.TargetRepositoryID, e.Identity, version)
	return MigrationPublicationAsset{Path: a.Path, ObjectKey: v.ObjectKey, Digest: v.Digest, Size: v.Size}, err
}

type npmMigrationLockKey struct{}
type npmMigrationLock struct {
	owner            *NexusNPMMigrationPublication
	identity         string
	active, verified atomic.Bool
}

func npmMigrationFingerprint(e nexusmigration.Entry) string {
	// The frozen packument hash and each byte receipt are included; mutable
	// checkpoints and private body JSON serialization are not identities.
	type receipt struct {
		Source, Target, SHA256, SHA1, SHA512, Validator, MediaType string
		Size                                                       int64
	}
	assets := make([]receipt, 0, len(e.Assets))
	for _, a := range e.Assets {
		assets = append(assets, receipt{a.SourcePath, a.TargetPath, a.SHA256, a.SHA1, a.SHA512, a.Validator, a.MediaType, a.Size})
	}
	raw, _ := json.Marshal(struct {
		ID, Batch, Target, Name string
		Assets                  []receipt
	}{e.ID, e.BatchID, e.TargetRepositoryID, e.Identity, assets})
	return string(raw)
}
func (p *NexusNPMMigrationPublication) Lock(ctx context.Context, e nexusmigration.Entry) (context.Context, func(), error) {
	if err := p.target(ctx, e); err != nil {
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
	ctx, release, err := repository.LockObjectKeys(ctx, keys, p.store, repository.FormatNPM, p.store.LockNPMObject)
	if err != nil {
		return ctx, nil, err
	}
	lock := &npmMigrationLock{owner: p, identity: npmMigrationFingerprint(e)}
	lock.active.Store(true)
	return context.WithValue(ctx, npmMigrationLockKey{}, lock), func() { lock.active.Store(false); release() }, nil
}
func (p *NexusNPMMigrationPublication) held(ctx context.Context, e nexusmigration.Entry) (*npmMigrationLock, error) {
	lock, _ := ctx.Value(npmMigrationLockKey{}).(*npmMigrationLock)
	if lock == nil || lock.owner != p || !lock.active.Load() || lock.identity != npmMigrationFingerprint(e) {
		return nil, nexusmigration.Failed("target_coordination_required")
	}
	return lock, nil
}
func (p *NexusNPMMigrationPublication) Begin(ctx context.Context, e nexusmigration.Entry) (string, error) {
	if _, err := p.held(ctx, e); err != nil {
		return "", err
	}
	return e.ID, nil
}
func (p *NexusNPMMigrationPublication) Stage(ctx context.Context, e nexusmigration.Entry, a nexusmigration.Asset, input io.ReadSeeker) error {
	lock, err := p.held(ctx, e)
	if err != nil {
		return err
	}
	lock.verified.Store(false)
	found := false
	for _, expected := range e.Assets {
		if expected.SourcePath == a.SourcePath && expected.TargetPath == a.TargetPath && expected.SHA256 == a.SHA256 && expected.Size == a.Size {
			found = true
		}
	}
	if !found || e.SessionID != e.ID {
		return nexusmigration.Failed("invalid_publication")
	}
	if err = nexusVerifyInput(ctx, input, a); err != nil {
		return err
	}
	// Exact packument bytes are already durable in the ledger, never exposed as
	// native package bytes. Only immutable tarballs enter the object store.
	if a.TargetPath == nexusmigration.NPMPackumentPath {
		return nil
	}
	key := "native/npm/sha256/" + a.SHA256
	if existing, readErr := p.objects.Stat(ctx, key); readErr == nil {
		if existing.Size != a.Size {
			return nexusmigration.Failed("target_bytes_mismatch")
		}
		return p.verifyTarball(ctx, e, a, key)
	} else if !errors.Is(readErr, objectstore.ErrNotFound) {
		return nexusmigration.Failed("target_upload_failed")
	}
	if err = p.objects.PutVerifiedReader(ctx, key, input, a.Size, "sha256:"+a.SHA256); err != nil {
		return nexusmigration.Failed("target_upload_failed")
	}
	return nil
}
func (p *NexusNPMMigrationPublication) verifyTarball(ctx context.Context, e nexusmigration.Entry, a nexusmigration.Asset, key string) error {
	doc, _, err := nexusmigration.NPMEntryMetadata(e)
	if err != nil {
		return err
	}
	reader, size, err := p.objects.Open(ctx, key)
	if err != nil {
		return nexusmigration.Failed("target_bytes_mismatch")
	}
	defer func() { _ = reader.Close() }()
	hashers := npmprotocol.ImportHashers()
	writers := []io.Writer{}
	for _, h := range hashers {
		writers = append(writers, h)
	}
	// Stream the archive through identity validation while hashing every byte,
	// then drain any gzip/tar trailer before accepting the digest.
	stream := io.TeeReader(&nexusContextReader{ctx: ctx, reader: io.LimitReader(reader, a.Size+1)}, io.MultiWriter(writers...))
	identityErr := npmprotocol.ValidateTarballReader(stream, e.Identity, a.TargetPath)
	_, readErr := io.Copy(io.Discard, stream)
	sums := map[string][]byte{}
	for alg, h := range hashers {
		sums[alg] = h.Sum(nil)
	}
	var v npmprotocol.ImportVersion
	_ = json.Unmarshal(doc.Versions[a.TargetPath], &v)
	if identityErr != nil || readErr != nil || size != a.Size || hex.EncodeToString(sums["sha256"]) != a.SHA256 || hex.EncodeToString(sums["sha1"]) != a.SHA1 || hex.EncodeToString(sums["sha512"]) != a.SHA512 || !npmprotocol.ImportIntegrity(v.Dist.Integrity, v.Dist.Shasum, sums) {
		return nexusmigration.Failed("target_bytes_mismatch")
	}
	return nil
}
func (p *NexusNPMMigrationPublication) Verify(ctx context.Context, e nexusmigration.Entry) error {
	lock, err := p.held(ctx, e)
	if err != nil {
		return err
	}
	lock.verified.Store(false)
	if err = p.target(ctx, e); err != nil {
		return err
	}
	_, assets, err := nexusmigration.NPMEntryMetadata(e)
	if err != nil {
		return err
	}
	for _, a := range assets {
		if err = p.verifyTarball(ctx, e, a, "native/npm/sha256/"+a.SHA256); err != nil {
			return err
		}
	}
	lock.verified.Store(true)
	return nil
}
func (p *NexusNPMMigrationPublication) Readback(ctx context.Context, e nexusmigration.Entry) (nexusmigration.Readback, error) {
	if err := p.target(ctx, e); err != nil {
		return "", err
	}
	c, err := npmMigrationClosure(e)
	if err != nil {
		return "", err
	}
	pkg, err := p.store.GetNPMPackage(ctx, e.TargetRepositoryID, e.Identity)
	if errors.Is(err, repository.ErrNotFound) {
		return nexusmigration.Absent, nil
	}
	if err != nil {
		return "", nexusmigration.Failed("target_read_failed")
	}
	if pkg.SourceEndpoint != "" || pkg.Negative {
		return nexusmigration.Conflict, nil
	}
	missing := false
	for tag, want := range c.DistTags {
		if old, exists := pkg.DistTags[tag]; exists {
			if old != want {
				return nexusmigration.Conflict, nil
			}
		} else {
			missing = true
		}
	}
	_, assets, _ := nexusmigration.NPMEntryMetadata(e)
	byVersion := map[string]repository.NPMVersion{}
	for _, v := range pkg.Versions {
		byVersion[v.Version] = v
	}
	for _, v := range pkg.TombstonedVersions {
		byVersion[v.Version] = v
	}
	for _, want := range c.Versions {
		got, exists := byVersion[want.Version]
		if !exists {
			missing = true
			continue
		}
		if !repository.NPMImportVersionMatches(got, want) {
			return nexusmigration.Conflict, nil
		}
		quarantine, qerr := p.store.GetArtifactQuarantine(ctx, e.TargetRepositoryID, repository.FormatNPM, e.Identity+"@"+want.Version, want.Digest)
		if qerr != nil && !errors.Is(qerr, repository.ErrNotFound) {
			return "", nexusmigration.Failed("target_read_failed")
		}
		if qerr == nil && quarantine.State == repository.ArtifactQuarantineStateQuarantined {
			return "", nexusmigration.Failed("artifact_quarantined")
		}
		if err = p.verifyTarball(ctx, e, assets[want.Version], got.ObjectKey); err != nil {
			return nexusmigration.Conflict, nil
		}
	}
	if missing {
		return nexusmigration.Absent, nil
	}
	return nexusmigration.Identical, nil
}
func (p *NexusNPMMigrationPublication) Publish(ctx context.Context, e nexusmigration.Entry) error {
	lock, err := p.held(ctx, e)
	if err != nil {
		return err
	}
	if !lock.verified.Load() {
		return nexusmigration.Failed("target_verification_required")
	}
	c, err := npmMigrationClosure(e)
	if err != nil {
		return err
	}
	if err = p.store.PublishNPMImport(ctx, c); err != nil {
		if errors.Is(err, repository.ErrNameExists) {
			return nexusmigration.Failed("target_conflict")
		}
		if errors.Is(err, repository.ErrArtifactQuarantined) {
			return nexusmigration.Failed("artifact_quarantined")
		}
		if repository.IsQuotaExceeded(err) {
			return nexusmigration.Failed("target_quota_exceeded")
		}
		return err
	}
	if !nilDependency(p.scanner) {
		for _, v := range c.Versions {
			_ = p.scanner.ScheduleRepository(ctx, e.TargetRepositoryID, repository.FormatNPM, e.Identity+"@"+v.Version, v.Digest, e.Actor)
		}
	}
	return nil
}
