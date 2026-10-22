package app

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"path"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ccsert/artifact-gateway/internal/capacityplan"
	formatmodule "github.com/ccsert/artifact-gateway/internal/format"
	rawformat "github.com/ccsert/artifact-gateway/internal/format/raw"
	"github.com/ccsert/artifact-gateway/internal/nexusmigration"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

// NexusMigrationPublicationStore contains only native publication, readback and
// capacity operations. The migration ledger and lease fence remain domain-owned.
type NexusMigrationPublicationStore interface {
	LockRawObject(context.Context, string) (func(), error)
	GetHostedRepository(context.Context, string) (repository.HostedRepository, error)
	GetRepositoryCapacity(context.Context, string) (repository.RepositoryCapacity, error)
	LockMavenObject(context.Context, string) (context.Context, func(), error)
	CreateMavenPublishSession(context.Context, repository.MavenPublishSession) (repository.MavenPublishSession, error)
	FindMavenPublishSession(context.Context, string, string, string) (repository.MavenPublishSession, error)
	GetMavenPublishSession(context.Context, string) (repository.MavenPublishSession, error)
	MarkMavenPublishObject(context.Context, string, string, string) error
	CommitMavenPublishSessionIdempotently(context.Context, string, string, string, []repository.MavenAsset) (repository.MavenArtifact, bool, error)
	GetMavenArtifactByCoordinate(context.Context, string, string) (repository.MavenArtifact, error)
	GetMavenAsset(context.Context, string, string) (repository.MavenAsset, error)
	ListMavenAssets(context.Context, string, string) ([]repository.MavenAsset, error)
	StageRawObject(context.Context, repository.RawObject) error
	PutRawAssetIf(context.Context, repository.RawAsset, repository.RawWriteCondition) (repository.RawAsset, error)
	GetRawAsset(context.Context, string, string) (repository.RawAsset, error)
	RecordAudit(context.Context, repository.AuditRecord) error
}

type nexusMigrationPublicationScanner interface {
	ScheduleRepository(context.Context, string, repository.Format, string, string, string) error
}

// NexusMigrationPublication uses existing native metadata transactions. It does
// not create repositories, grant authority or implement a second migration worker.
type NexusMigrationPublication struct {
	store            NexusMigrationPublicationStore
	objects          ObjectStores
	targetID, poolID string
	scanner          nexusMigrationPublicationScanner
	oci              NexusFormatMigrationPublication
	conan            NexusFormatMigrationPublication
	npm              NexusFormatMigrationPublication
}

var _ nexusmigration.Publication = (*NexusMigrationPublication)(nil)

func NewNexusMigrationPublication(store NexusMigrationPublicationStore, objects ObjectStores, targetID, poolID string) *NexusMigrationPublication {
	stores := make(ObjectStores, len(objects))
	for format, store := range objects {
		stores[format] = store
	}
	return &NexusMigrationPublication{store: store, objects: stores, targetID: targetID, poolID: poolID}
}

func (p *NexusMigrationPublication) WithPublicationScanner(scanner nexusMigrationPublicationScanner) *NexusMigrationPublication {
	p.scanner = scanner
	return p
}

func (p *NexusMigrationPublication) ValidateTargets(ctx context.Context, mappings []nexusmigration.Mapping) error {
	if p == nil || nilDependency(p.store) || len(mappings) == 0 {
		return nexusmigration.Failed("target_unavailable")
	}
	for _, mapping := range mappings {
		if _, err := p.target(ctx, mapping.TargetRepositoryID, ""); err != nil {
			return err
		}
	}
	return nil
}

func (p *NexusMigrationPublication) target(ctx context.Context, id, format string) (repository.HostedRepository, error) {
	if p == nil || nilDependency(p.store) {
		return repository.HostedRepository{}, nexusmigration.Failed("target_unavailable")
	}
	repo, err := p.store.GetHostedRepository(ctx, id)
	if err != nil || repo.Type != repository.RepositoryTypeHosted || repo.State != repository.RepositoryActive ||
		!p.supportsFormat(repo.Format) || (format != "" && string(repo.Format) != format) {
		return repository.HostedRepository{}, nexusmigration.Failed("target_not_active_hosted")
	}
	if objectstore.Validate(p.objects[repo.Format]) != nil {
		return repository.HostedRepository{}, nexusmigration.Failed("target_unavailable")
	}
	return repo, nil
}

type nexusNativeAsset struct {
	repository.MavenAsset
	body string // Only generated checksum sidecars have a body here.
}

// nativeAssets binds the source manifest to exactly the native paths that will
// be published. Checksum files are generated from verified primary bytes rather
// than copying Nexus sidecar formatting into a virtual/native checksum route.
func nexusNativeAssets(entry nexusmigration.Entry) ([]nexusNativeAsset, error) {
	if entry.ID == "" || entry.TargetRepositoryID == "" || len(entry.Assets) == 0 {
		return nil, nexusmigration.Failed("invalid_publication")
	}
	format := repository.Format(entry.Format)
	capability, migrated := formatmodule.Capability[formatmodule.ImmutablePublication](rawFormatRegistry(rawformat.Options{}), format)
	if !migrated && format != repository.FormatMaven {
		return nil, nexusmigration.Failed("unsupported_format")
	}
	base, pom := "", ""
	if format == repository.FormatMaven {
		parts := strings.Split(entry.Identity, ":")
		if len(parts) != 3 || !validMavenCoordinate(entry.Identity) || strings.ContainsAny(entry.Identity, "/\\\x00") || strings.HasSuffix(parts[2], "-SNAPSHOT") {
			return nil, nexusmigration.Failed("unsupported_maven_coordinate")
		}
		base = mavenCoordinatePath(entry.Identity)
		pom = parts[1] + "-" + parts[2] + ".pom"
	} else {
		native := make([]formatmodule.NativeAsset, 0, len(entry.Assets))
		for _, asset := range entry.Assets {
			native = append(native, formatmodule.NativeAsset{RepositoryID: entry.TargetRepositoryID, Path: asset.TargetPath, Digest: "sha256:" + asset.SHA256, Size: asset.Size})
		}
		if err := capability.ValidatePublication(entry.Identity, native); err != nil {
			if errors.Is(err, formatmodule.ErrPublicationPath) {
				return nil, nexusmigration.Failed("raw_path_unsupported")
			}
			return nil, nexusmigration.Failed("invalid_publication")
		}
	}
	seen, hasPOM := map[string]bool{}, false
	out := make([]nexusNativeAsset, 0, len(entry.Assets)*4)
	for _, asset := range entry.Assets {
		if asset.Size < 0 || asset.Size == math.MaxInt64 || !nexusDigest(asset.SHA256, 64) || seen[asset.TargetPath] {
			return nil, nexusmigration.Failed("invalid_publication")
		}
		if !migrated && (asset.TargetPath == "" || strings.HasPrefix(asset.TargetPath, "/") || path.Clean(asset.TargetPath) != asset.TargetPath || strings.ContainsAny(asset.TargetPath, "\\\r\n\x00%?#")) {
			return nil, nexusmigration.Failed("maven_path_unsupported")
		}
		if nexusChecksumSuffix(asset.TargetPath) != "" {
			return nil, nexusmigration.Failed("source_checksum_path_unsupported")
		}
		seen[asset.TargetPath] = true
		item := nexusNativeAsset{MavenAsset: repository.MavenAsset{RepositoryID: entry.TargetRepositoryID, Path: asset.TargetPath,
			ObjectKey: "native/" + entry.Format + "/sha256/" + asset.SHA256, Digest: "sha256:" + asset.SHA256, Size: asset.Size}}
		out = append(out, item)
		if format == repository.FormatMaven {
			parts := strings.Split(entry.Identity, ":")
			name := path.Base(asset.TargetPath)
			prefix := parts[1] + "-" + parts[2]
			if path.Dir(asset.TargetPath) != base || (!strings.HasPrefix(name, prefix+".") && !strings.HasPrefix(name, prefix+"-")) || !nexusDigest(asset.SHA1, 40) || !nexusDigest(asset.MD5, 32) {
				return nil, nexusmigration.Failed("invalid_maven_manifest")
			}
			hasPOM = hasPOM || name == pom
			for _, checksum := range nexusMavenChecksums(asset) {
				out = append(out, nexusNativeAsset{MavenAsset: repository.MavenAsset{RepositoryID: entry.TargetRepositoryID,
					Path: asset.TargetPath + checksum.extension, ObjectKey: "native/maven/sha256/" + checksum.digest,
					Digest: "sha256:" + checksum.digest, Size: int64(len(checksum.body))}, body: checksum.body})
			}
		}
	}
	if format == repository.FormatMaven && !hasPOM {
		return nil, nexusmigration.Failed("maven_pom_missing")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func nexusDigest(value string, size int) bool {
	return len(value) == size && strings.Trim(value, "0123456789abcdef") == ""
}

func nexusChecksumSuffix(value string) string {
	for _, suffix := range []string{".sha256", ".sha512", ".sha1", ".md5"} {
		if strings.HasSuffix(value, suffix) {
			return suffix
		}
	}
	return ""
}

func nexusMavenChecksums(asset nexusmigration.Asset) []mavenChecksum {
	out := make([]mavenChecksum, 0, 3)
	for _, checksum := range []struct{ extension, value string }{{".sha256", asset.SHA256}, {".sha1", asset.SHA1}, {".md5", asset.MD5}} {
		body := checksum.value + "\n"
		sum := sha256.Sum256([]byte(body))
		out = append(out, mavenChecksum{extension: checksum.extension, body: body, digest: hex.EncodeToString(sum[:])})
	}
	return out
}

// Lock keeps the same native object locks used by reclamation through Stage,
// Verify, Publish and the caller's durable entry checkpoint. It carries one
// PostgreSQL advisory session when a Maven GAV contains many objects.
func (p *NexusMigrationPublication) Lock(ctx context.Context, entry nexusmigration.Entry) (context.Context, func(), error) {
	if publication := p.formatPublication(entry.Format); publication != nil {
		return publication.Lock(ctx, entry)
	}
	assets, err := nexusNativeAssets(entry)
	if err != nil {
		return ctx, nil, err
	}
	if _, err = p.target(ctx, entry.TargetRepositoryID, entry.Format); err != nil {
		return ctx, nil, err
	}
	releases := []func(){}
	releaseAll := func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}
	if capability, ok := p.publicationModule(entry.Format); ok {
		ctx, release, lockErr := capability.LockPublication(ctx, []formatmodule.NativeAsset{nativePublicationAsset(assets[0].MavenAsset)})
		if lockErr != nil {
			return ctx, nil, nexusmigration.Failed("target_coordination_unavailable")
		}
		return p.lockContext(ctx, entry, release)
	}
	keys := make([]string, 0, len(assets))
	seen := map[string]bool{}
	for _, asset := range assets {
		if !seen[asset.ObjectKey] {
			keys, seen[asset.ObjectKey] = append(keys, asset.ObjectKey), true
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		var release func()
		ctx, release, err = p.store.LockMavenObject(ctx, key)
		if err != nil {
			releaseAll()
			return ctx, nil, nexusmigration.Failed("target_coordination_unavailable")
		}
		releases = append(releases, release)
	}
	return p.lockContext(ctx, entry, releaseAll)
}

type nexusPublicationLockKey struct{}
type nexusPublicationLock struct {
	owner            *NexusMigrationPublication
	entry            string
	active, verified atomic.Bool
}

func nexusPublicationIdentity(entry nexusmigration.Entry) string {
	assets, _ := nexusNativeAssets(entry)
	data, _ := json.Marshal(struct {
		ID, BatchID, Format, Repository, Identity string
		Assets                                    []nexusNativeAsset
	}{entry.ID, entry.BatchID, entry.Format, entry.TargetRepositoryID, entry.Identity, assets})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (p *NexusMigrationPublication) lockContext(ctx context.Context, entry nexusmigration.Entry, release func()) (context.Context, func(), error) {
	lock := &nexusPublicationLock{owner: p, entry: nexusPublicationIdentity(entry)}
	lock.active.Store(true)
	return context.WithValue(ctx, nexusPublicationLockKey{}, lock), func() {
		if lock.active.Swap(false) {
			release()
		}
	}, nil
}

func (p *NexusMigrationPublication) held(ctx context.Context, entry nexusmigration.Entry) (*nexusPublicationLock, error) {
	lock, _ := ctx.Value(nexusPublicationLockKey{}).(*nexusPublicationLock)
	if lock == nil || lock.owner != p || !lock.active.Load() || lock.entry != nexusPublicationIdentity(entry) {
		return nil, nexusmigration.Failed("target_coordination_required")
	}
	return lock, nil
}

func (p *NexusMigrationPublication) Begin(ctx context.Context, entry nexusmigration.Entry) (string, error) {
	if publication := p.formatPublication(entry.Format); publication != nil {
		return publication.Begin(ctx, entry)
	}
	if _, err := p.held(ctx, entry); err != nil {
		return "", err
	}
	if _, ok := p.publicationModule(entry.Format); ok {
		return "", nil
	}
	assets, err := nexusNativeAssets(entry)
	if err != nil {
		return "", err
	}
	expected := nexusMavenSession(entry, assets)
	session, err := p.store.FindMavenPublishSession(ctx, entry.TargetRepositoryID, entry.Identity, expected.Publisher)
	if err == nil {
		if !sameNexusMavenSession(session, expected) {
			return "", nexusmigration.Failed("native_session_conflict")
		}
		if session.State == "committed" || (session.State == "open" && session.ExpiresAt.After(time.Now())) {
			return session.ID, nil
		}
	} else if !errors.Is(err, repository.ErrNotFound) {
		return "", nexusmigration.Failed("native_session_unavailable")
	}
	expected.ID = uuid.NewString()
	session, err = p.store.CreateMavenPublishSession(ctx, expected)
	if errors.Is(err, repository.ErrNameExists) {
		// Native creation can commit before a response/ledger update is lost.
		session, err = p.store.FindMavenPublishSession(ctx, entry.TargetRepositoryID, entry.Identity, expected.Publisher)
	}
	if err != nil || !sameNexusMavenSession(session, expected) || session.State != "open" || !session.ExpiresAt.After(time.Now()) {
		return "", nexusmigration.Failed("native_session_unavailable")
	}
	return session.ID, nil
}

func nexusMavenSession(entry nexusmigration.Entry, assets []nexusNativeAsset) repository.MavenPublishSession {
	parts := strings.Split(entry.Identity, ":")
	session := repository.MavenPublishSession{RepositoryID: entry.TargetRepositoryID, Coordinate: entry.Identity,
		Publisher: "nexus-migration:" + entry.ID, PomObject: parts[1] + "-" + parts[2] + ".pom", State: "open", ExpiresAt: time.Now().UTC().Add(24 * time.Hour)}
	for _, asset := range assets {
		session.Objects = append(session.Objects, repository.MavenDeclaredObject{Name: path.Base(asset.Path), Digest: asset.Digest, Size: asset.Size})
	}
	return session
}

func sameNexusMavenSession(actual, expected repository.MavenPublishSession) bool {
	if actual.RepositoryID != expected.RepositoryID || actual.Coordinate != expected.Coordinate || actual.Publisher != expected.Publisher || actual.PomObject != expected.PomObject || len(actual.Objects) != len(expected.Objects) {
		return false
	}
	objects := make(map[string]repository.MavenDeclaredObject, len(actual.Objects))
	for _, object := range actual.Objects {
		objects[object.Name] = object
	}
	for _, object := range expected.Objects {
		if objects[object.Name] != object {
			return false
		}
	}
	return true
}

func (p *NexusMigrationPublication) session(ctx context.Context, entry nexusmigration.Entry, assets []nexusNativeAsset) (repository.MavenPublishSession, error) {
	session, err := p.store.GetMavenPublishSession(ctx, entry.SessionID)
	if err != nil || !sameNexusMavenSession(session, nexusMavenSession(entry, assets)) {
		return repository.MavenPublishSession{}, nexusmigration.Failed("native_session_conflict")
	}
	return session, nil
}

func (p *NexusMigrationPublication) Stage(ctx context.Context, entry nexusmigration.Entry, asset nexusmigration.Asset, input io.ReadSeeker) error {
	if publication := p.formatPublication(entry.Format); publication != nil {
		return publication.Stage(ctx, entry, asset, input)
	}
	lock, err := p.held(ctx, entry)
	if err != nil {
		return err
	}
	lock.verified.Store(false)
	if _, err = p.target(ctx, entry.TargetRepositoryID, entry.Format); err != nil {
		return err
	}
	planned, err := nexusNativeAssets(entry)
	if err != nil {
		return err
	}
	found := false
	for _, expected := range entry.Assets {
		found = found || (expected.SourcePath == asset.SourcePath && expected.TargetPath == asset.TargetPath && expected.Size == asset.Size && expected.SHA256 == asset.SHA256 && expected.SHA1 == asset.SHA1 && expected.MD5 == asset.MD5)
	}
	if !found || nilDependency(input) {
		return nexusmigration.Failed("invalid_publication")
	}
	if err = nexusVerifyInput(ctx, input, asset); err != nil {
		return err
	}
	objects := p.objects[repository.Format(entry.Format)]
	key := "native/" + entry.Format + "/sha256/" + asset.SHA256
	if capability, ok := p.publicationModule(entry.Format); ok {
		err = capability.StageVerifiedPublication(ctx, formatmodule.NativeAsset{RepositoryID: entry.TargetRepositoryID, Path: asset.TargetPath, ObjectKey: key, Digest: "sha256:" + asset.SHA256, Size: asset.Size}, input)
		if errors.Is(err, formatmodule.ErrPublicationStage) {
			return nexusmigration.Failed("native_stage_failed")
		}
		if err != nil {
			return nexusmigration.Failed("target_upload_failed")
		}
		return nil
	}
	session, err := p.session(ctx, entry, planned)
	if err != nil || session.State != "open" || !session.ExpiresAt.After(time.Now()) {
		return nexusmigration.Failed("native_session_unavailable")
	}
	for _, item := range planned {
		if item.Path != asset.TargetPath && !strings.HasPrefix(item.Path, asset.TargetPath+".") {
			continue
		}
		// Other primary files may share the prefix (e.g. .jar.asc); only the
		// three generated sidecars of this exact primary belong to this Stage.
		if item.Path != asset.TargetPath && item.Path != asset.TargetPath+".sha256" && item.Path != asset.TargetPath+".sha1" && item.Path != asset.TargetPath+".md5" {
			continue
		}
		if err = p.store.MarkMavenPublishObject(ctx, entry.SessionID, path.Base(item.Path), item.ObjectKey); err != nil {
			return nexusmigration.Failed("native_stage_failed")
		}
		var reader io.Reader = strings.NewReader(item.body)
		if item.Path == asset.TargetPath {
			reader = input
		}
		if err = objects.PutVerifiedReader(ctx, item.ObjectKey, reader, item.Size, item.Digest); err != nil {
			return nexusmigration.Failed("target_upload_failed")
		}
	}
	return nil
}

func nexusVerifyInput(ctx context.Context, input io.ReadSeeker, asset nexusmigration.Asset) error {
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		return nexusmigration.Failed("source_bytes_mismatch")
	}
	h256, h1, hmd5 := sha256.New(), sha1.New(), md5.New()
	n, err := io.Copy(io.MultiWriter(h256, h1, hmd5), &nexusContextReader{ctx: ctx, reader: io.LimitReader(input, asset.Size+1)})
	if err != nil || n != asset.Size || hex.EncodeToString(h256.Sum(nil)) != asset.SHA256 ||
		(asset.SHA1 != "" && hex.EncodeToString(h1.Sum(nil)) != asset.SHA1) || (asset.MD5 != "" && hex.EncodeToString(hmd5.Sum(nil)) != asset.MD5) {
		return nexusmigration.Failed("source_bytes_mismatch")
	}
	if _, err = input.Seek(0, io.SeekStart); err != nil {
		return nexusmigration.Failed("source_bytes_mismatch")
	}
	return nil
}

type nexusContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *nexusContextReader) Read(out []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(out)
}

func nexusVerifyObject(ctx context.Context, objects objectstore.Store, asset repository.MavenAsset) error {
	reader, size, err := objects.Open(ctx, asset.ObjectKey)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, &nexusContextReader{ctx: ctx, reader: io.LimitReader(reader, asset.Size+1)})
	if err != nil {
		return err
	}
	if size != asset.Size || n != asset.Size || "sha256:"+hex.EncodeToString(h.Sum(nil)) != asset.Digest {
		return nexusmigration.Failed("target_bytes_mismatch")
	}
	return nil
}

func (p *NexusMigrationPublication) Verify(ctx context.Context, entry nexusmigration.Entry) error {
	if publication := p.formatPublication(entry.Format); publication != nil {
		return publication.Verify(ctx, entry)
	}
	lock, err := p.held(ctx, entry)
	if err != nil {
		return err
	}
	lock.verified.Store(false)
	assets, err := nexusNativeAssets(entry)
	if err != nil {
		return err
	}
	objects := p.objects[repository.Format(entry.Format)]
	for _, asset := range assets {
		if err = nexusVerifyObject(ctx, objects, asset.MavenAsset); err != nil {
			return nexusmigration.Failed("target_bytes_mismatch")
		}
	}
	if entry.Format == "maven" {
		session, err := p.session(ctx, entry, assets)
		if err != nil {
			return err
		}
		pom, err := readNativeMavenPOM(ctx, objects, session)
		if err != nil {
			return nexusmigration.Failed("invalid_maven_pom")
		}
		if err = nexusMavenReleaseClosure(entry, pom.Packaging); err != nil {
			return err
		}
	}
	lock.verified.Store(true)
	return nil
}

// A complete source listing is not proof of a usable Maven release when its
// POM requires a main artifact that is absent. Custom lifecycle extensions are
// deliberately unsupported instead of guessing their required output files.
func nexusMavenReleaseClosure(entry nexusmigration.Entry, packaging string) error {
	packaging = strings.TrimSpace(packaging)
	extension := ""
	switch packaging {
	case "pom":
		return nil
	case "", "jar", "maven-plugin", "ejb", "bundle":
		extension = "jar"
	case "war", "ear", "rar":
		extension = packaging
	default:
		return nexusmigration.Failed("maven_release_closure_unsupported")
	}
	parts := strings.Split(entry.Identity, ":")
	required := mavenCoordinatePath(entry.Identity) + "/" + parts[1] + "-" + parts[2] + "." + extension
	for _, asset := range entry.Assets {
		if asset.TargetPath == required {
			return nil
		}
	}
	return nexusmigration.Failed("maven_required_asset_missing")
}

func (p *NexusMigrationPublication) Readback(ctx context.Context, entry nexusmigration.Entry) (nexusmigration.Readback, error) {
	if publication := p.formatPublication(entry.Format); publication != nil {
		return publication.Readback(ctx, entry)
	}
	assets, err := nexusNativeAssets(entry)
	if err != nil {
		return "", err
	}
	if _, err = p.target(ctx, entry.TargetRepositoryID, entry.Format); err != nil {
		return "", err
	}
	if entry.Format == "maven" {
		artifact, err := p.store.GetMavenArtifactByCoordinate(ctx, entry.TargetRepositoryID, entry.Identity)
		if errors.Is(err, repository.ErrNotFound) {
			return nexusmigration.Absent, nil
		}
		if err != nil {
			return "", nexusmigration.Failed("target_readback_failed")
		}
		if artifact.State != "visible" {
			return nexusmigration.Conflict, nil
		}
		visible, err := p.store.ListMavenAssets(ctx, entry.TargetRepositoryID, entry.Identity)
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return "", nexusmigration.Failed("target_readback_failed")
		}
		if err != nil || !sameNexusNativeAssets(visible, assets) {
			return nexusmigration.Conflict, nil
		}
	}
	for _, expected := range assets {
		actual, err := p.nativeAsset(ctx, entry.Format, entry.TargetRepositoryID, expected.Path)
		if errors.Is(err, repository.ErrNotFound) {
			if _, ok := p.publicationModule(entry.Format); ok {
				return nexusmigration.Absent, nil
			}
			return nexusmigration.Conflict, nil
		}
		if err != nil {
			return "", nexusmigration.Failed("target_readback_failed")
		}
		if actual != expected.MavenAsset {
			return nexusmigration.Conflict, nil
		}
		if err = nexusVerifyObject(ctx, p.objects[repository.Format(entry.Format)], actual); err != nil {
			var failure *nexusmigration.Failure
			if !errors.Is(err, objectstore.ErrNotFound) && !errors.As(err, &failure) {
				return "", nexusmigration.Failed("target_readback_failed")
			}
			return nexusmigration.Conflict, nil
		}
		current, err := p.nativeAsset(ctx, entry.Format, entry.TargetRepositoryID, expected.Path)
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return "", nexusmigration.Failed("target_readback_failed")
		}
		if err != nil || current != actual {
			return nexusmigration.Conflict, nil
		}
	}
	if entry.Format == "maven" {
		visible, err := p.store.ListMavenAssets(ctx, entry.TargetRepositoryID, entry.Identity)
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return "", nexusmigration.Failed("target_readback_failed")
		}
		if err != nil || !sameNexusNativeAssets(visible, assets) {
			return nexusmigration.Conflict, nil
		}
		pom, err := readNativeMavenPOM(ctx, p.objects[repository.FormatMaven], nexusMavenSession(entry, assets))
		if err != nil {
			return "", nexusmigration.Failed("invalid_maven_pom")
		}
		if err = nexusMavenReleaseClosure(entry, pom.Packaging); err != nil {
			return "", err
		}
	}
	return nexusmigration.Identical, nil
}

func sameNexusNativeAssets(actual []repository.MavenAsset, expected []nexusNativeAsset) bool {
	if len(actual) != len(expected) {
		return false
	}
	byPath := make(map[string]repository.MavenAsset, len(actual))
	for _, asset := range actual {
		byPath[asset.Path] = asset
	}
	for _, asset := range expected {
		if byPath[asset.Path] != asset.MavenAsset {
			return false
		}
	}
	return true
}

func (p *NexusMigrationPublication) nativeAsset(ctx context.Context, format, repo, assetPath string) (repository.MavenAsset, error) {
	if format == "maven" {
		return p.store.GetMavenAsset(ctx, repo, assetPath)
	}
	if capability, ok := p.publicationModule(format); ok {
		asset, err := capability.ReadPublication(ctx, repo, assetPath)
		return repository.MavenAsset{RepositoryID: asset.RepositoryID, Path: asset.Path, ObjectKey: asset.ObjectKey, Digest: asset.Digest, Size: asset.Size}, err
	}
	return repository.MavenAsset{}, repository.ErrDisabled
}

func (p *NexusMigrationPublication) Publish(ctx context.Context, entry nexusmigration.Entry) error {
	if publication := p.formatPublication(entry.Format); publication != nil {
		return publication.Publish(ctx, entry)
	}
	lock, err := p.held(ctx, entry)
	if err != nil {
		return err
	}
	if !lock.verified.Load() {
		return nexusmigration.Failed("target_verification_required")
	}
	repo, err := p.target(ctx, entry.TargetRepositoryID, entry.Format)
	if err != nil {
		return err
	}
	assets, err := nexusNativeAssets(entry)
	if err != nil {
		return err
	}
	digest := assets[0].Digest
	if capability, ok := p.publicationModule(entry.Format); ok {
		err = capability.PublishPublication(ctx, nativePublicationAsset(assets[0].MavenAsset))
		if errors.Is(err, repository.ErrPreconditionFailed) {
			return nexusmigration.Failed("target_conflict")
		}
	} else {
		if _, err = p.session(ctx, entry, assets); err != nil {
			return err
		}
		native := make([]repository.MavenAsset, 0, len(assets))
		for _, asset := range assets {
			native = append(native, asset.MavenAsset)
		}
		artifact, _, commitErr := p.store.CommitMavenPublishSessionIdempotently(ctx, entry.SessionID, "nexus-migration:"+entry.ID, nexusPublicationIdentity(entry), native)
		err, digest = commitErr, artifact.Digest
		if errors.Is(err, repository.ErrNameExists) {
			return nexusmigration.Failed("target_conflict")
		}
	}
	if repository.IsQuotaExceeded(err) {
		return nexusmigration.Failed("target_quota_exceeded")
	}
	if err != nil {
		return nexusmigration.Failed("native_publish_failed")
	}
	_ = p.store.RecordAudit(ctx, repository.AuditRecord{Repository: repo.Name, GroupName: repo.Name, Actor: entry.Actor,
		Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(), Format: entry.Format, Resource: entry.Identity,
		Representation: digest, Operation: "nexus.migration.publish", Status: 201})
	if !nilDependency(p.scanner) {
		_ = p.scanner.ScheduleRepository(ctx, entry.TargetRepositoryID, repo.Format, entry.Identity, digest, entry.Actor)
	}
	return nil
}

func (p *NexusMigrationPublication) Capacity(ctx context.Context, batch nexusmigration.Batch, entries []nexusmigration.Entry) (capacityplan.Report, error) {
	evidence := batch.CapacityEvidence
	if p == nil || p.targetID == "" || p.poolID == "" || evidence.TargetID != p.targetID || evidence.PoolID != p.poolID {
		return capacityplan.Report{}, nexusmigration.Failed("capacity_target_mismatch")
	}
	if int64(len(entries)) != batch.EntryCount {
		return capacityplan.Report{}, nexusmigration.Failed("capacity_inventory_mismatch")
	}
	mappings := make(map[string]bool, len(batch.Mappings))
	for _, mapping := range batch.Mappings {
		mappings[mapping.SourceRepository+"\x00"+mapping.TargetRepositoryID] = true
	}
	plan := capacityplan.Plan{SchemaVersion: 1, InventoryID: batch.ManifestSHA256, Complete: batch.ScanComplete,
		TargetID: p.targetID, References: []capacityplan.Reference{},
		Snapshot: capacityplan.Snapshot{TargetID: evidence.TargetID, ObservedAt: evidence.ObservedAt, ValidUntil: evidence.ValidUntil,
			StoragePoolID: evidence.PoolID, FreeBytes: evidence.FreeBytes, Repositories: []capacityplan.RepositoryCapacity{}, Objects: []capacityplan.Presence{}, References: []capacityplan.Membership{}},
		Peak: capacityplan.PeakBudget{StoragePoolID: p.poolID, DownloadBytes: evidence.DownloadBytes, UploadBytes: evidence.UploadBytes,
			BackupBytes: evidence.BackupBytes, RestoreBytes: evidence.RestoreBytes, HeadroomBytes: evidence.HeadroomBytes}}
	seenRepositories, seenObjects := map[string]bool{}, map[string]bool{}
	seenReferences := map[string]MigrationPublicationAsset{}
	var downloadPeak int64
	for _, entry := range entries {
		switch entry.State {
		case "unsupported", "conflict", "source_missing", "source_deleted":
			continue
		}
		if entry.BatchID != batch.ID || !mappings[entry.SourceRepository+"\x00"+entry.TargetRepositoryID] {
			return capacityplan.Report{}, nexusmigration.Failed("capacity_inventory_mismatch")
		}
		if _, err := p.target(ctx, entry.TargetRepositoryID, entry.Format); err != nil {
			return capacityplan.Report{}, err
		}
		assets, err := p.capacityAssets(entry)
		if err != nil {
			return capacityplan.Report{}, err
		}
		for _, asset := range entry.Assets {
			if asset.Size > downloadPeak {
				downloadPeak = asset.Size
			}
		}
		if !seenRepositories[entry.TargetRepositoryID] {
			capacity, err := p.store.GetRepositoryCapacity(ctx, entry.TargetRepositoryID)
			if err != nil {
				return capacityplan.Report{}, nexusmigration.Failed("capacity_unavailable")
			}
			plan.Snapshot.Repositories = append(plan.Snapshot.Repositories, capacityplan.RepositoryCapacity{RepositoryID: entry.TargetRepositoryID, UsedBytes: &capacity.UsedBytes, QuotaBytes: &capacity.QuotaBytes})
			seenRepositories[entry.TargetRepositoryID] = true
		}
		for _, asset := range assets {
			logical := entry.TargetRepositoryID + "\x00" + asset.Path
			actual, referenceErr := p.capacityAsset(ctx, entry, asset)
			membership := capacityplan.Membership{RepositoryID: entry.TargetRepositoryID, Key: asset.Path, State: "unknown"}
			if errors.Is(referenceErr, repository.ErrNotFound) {
				membership.State = "absent"
			} else if referenceErr == nil && actual.Path == asset.Path && actual.Digest == asset.Digest && actual.Size == asset.Size && actual.ObjectKey != "" {
				// Replayed native publications may own a different immutable key.
				// Account for the key actually referenced by the target.
				asset.ObjectKey = actual.ObjectKey
			}
			if prior, duplicate := seenReferences[logical]; duplicate {
				if prior != asset {
					return capacityplan.Report{}, nexusmigration.Failed("capacity_reference_conflict")
				}
				continue
			}
			seenReferences[logical] = asset
			size := asset.Size
			plan.References = append(plan.References, capacityplan.Reference{RepositoryID: entry.TargetRepositoryID, LogicalKey: asset.Path, ObjectKey: asset.ObjectKey, Digest: asset.Digest, Size: &size})
			presence := capacityplan.Presence{Key: asset.ObjectKey, State: "verified", Digest: asset.Digest, Size: &size}
			verifyErr := nexusVerifyObject(ctx, p.objects[repository.Format(entry.Format)], repository.MavenAsset{RepositoryID: entry.TargetRepositoryID, Path: asset.Path, ObjectKey: asset.ObjectKey, Digest: asset.Digest, Size: asset.Size})
			if errors.Is(verifyErr, objectstore.ErrNotFound) {
				presence = capacityplan.Presence{Key: asset.ObjectKey, State: "absent"}
			} else if verifyErr != nil {
				presence.State = "unknown"
			}
			if !seenObjects[asset.ObjectKey] {
				plan.Snapshot.Objects = append(plan.Snapshot.Objects, presence)
				seenObjects[asset.ObjectKey] = true
			}
			if referenceErr == nil && actual.Path == asset.Path && actual.Digest == asset.Digest && actual.Size == asset.Size && actual.ObjectKey != "" && presence.State == "verified" {
				membership.State, membership.Digest, membership.Size = "verified", asset.Digest, &size
			}
			plan.Snapshot.References = append(plan.Snapshot.References, membership)
		}
	}
	// The worker downloads one complete primary into its seekable spool. This
	// mandatory peak cannot be removed by supplying an explicit operator zero.
	if evidence.DownloadBytes == nil || *evidence.DownloadBytes < downloadPeak {
		plan.Peak.DownloadBytes = nil
	}
	return capacityplan.Evaluate(plan, time.Now().UTC()), nil
}

func supportsMigratedPublication(format repository.Format) bool {
	_, ok := formatmodule.Capability[formatmodule.ImmutablePublication](rawFormatRegistry(rawformat.Options{}), format)
	return ok
}
func (p *NexusMigrationPublication) publicationModule(format string) (formatmodule.ImmutablePublication, bool) {
	return formatmodule.Capability[formatmodule.ImmutablePublication](rawFormatRegistry(rawformat.Options{Publication: p.store, Objects: p.objects[repository.Format(format)]}), repository.Format(format))
}
func nativePublicationAsset(asset repository.MavenAsset) formatmodule.NativeAsset {
	return formatmodule.NativeAsset{RepositoryID: asset.RepositoryID, Path: asset.Path, ObjectKey: asset.ObjectKey, Digest: asset.Digest, Size: asset.Size}
}
