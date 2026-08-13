package aptpublication

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	aptprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/apt"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

var (
	ErrInvalidSnapshotInput = errors.New("APT repository snapshot input is invalid")
	ErrInvalidSignature     = errors.New("APT Release signature result is invalid")
	ErrSignerUnavailable    = errors.New("APT Release signer is unavailable")
)

type publisherStore interface {
	repository.HostedRepositoryStore
	repository.NativeAPTStore
	repository.NativeAPTPublicationStore
}

// Publisher builds a complete immutable suite view and performs exactly one
// database visibility transition after all bytes and signatures are durable.
type Publisher struct {
	store   publisherStore
	objects objectstore.Store
	signer  Signer
}

func NewPublisher(store publisherStore, objects objectstore.Store, signer Signer) *Publisher {
	return &Publisher{store: store, objects: objects, signer: signer}
}

type PublishSnapshotInput struct {
	ID           string
	RepositoryID string
	Suite        string
	Sequence     int64
	SessionIDs   []string
	Actor        string
	CreatedAt    time.Time
}

type snapshotPackage struct {
	session   repository.APTPublicationSession
	revision  repository.APTPackageRevision
	metadata  aptprotocol.DebianBinaryMetadata
	poolPath  string
	component string
}

type snapshotBundle struct {
	assets    []repository.APTSnapshotAsset
	generated map[string][]byte
	release   []byte
}

const maxAPTGeneratedIndexBytes = 128 << 20

func (p *Publisher) Publish(ctx context.Context, input PublishSnapshotInput) (published repository.APTRepositorySnapshot, err error) {
	if p == nil || p.store == nil || p.objects == nil || p.signer == nil || !validPublishSnapshotInput(input) {
		return repository.APTRepositorySnapshot{}, ErrInvalidSnapshotInput
	}
	repo, err := p.store.GetHostedRepository(ctx, input.RepositoryID)
	if err != nil {
		return repository.APTRepositorySnapshot{}, err
	}
	if repo.Format != repository.FormatAPT || repo.Type != repository.RepositoryTypeHosted || repo.State != repository.RepositoryActive {
		return repository.APTRepositorySnapshot{}, repository.ErrNotFound
	}
	snapshotCtx, releaseSnapshot, err := repository.LockObjectKeys(ctx, []string{"snapshot-lock/" + input.ID}, p.store, repository.FormatAPT, p.store.LockAPTObject)
	if err != nil {
		return repository.APTRepositorySnapshot{}, err
	}
	defer releaseSnapshot()
	existing, existingMembership, existingErr := p.store.GetAPTRepositorySnapshot(snapshotCtx, input.ID)
	if existingErr == nil {
		if !sameSnapshotRequest(existing, existingMembership, input) {
			return repository.APTRepositorySnapshot{}, repository.ErrIdempotencyConflict
		}
		if existing.State == repository.APTRepositorySnapshotVisible || existing.State == repository.APTRepositorySnapshotRetired {
			return existing, nil
		}
		if existing.State != repository.APTRepositorySnapshotBuilding {
			return repository.APTRepositorySnapshot{}, repository.ErrVersionConflict
		}
	} else if !errors.Is(existingErr, repository.ErrNotFound) {
		return repository.APTRepositorySnapshot{}, existingErr
	}
	packages, memberships, err := p.loadPackages(snapshotCtx, input)
	if err != nil {
		return repository.APTRepositorySnapshot{}, err
	}
	snapshot := repository.APTRepositorySnapshot{
		ID: input.ID, RepositoryID: input.RepositoryID, Suite: input.Suite, Sequence: input.Sequence,
		State: repository.APTRepositorySnapshotBuilding, CreatedAt: input.CreatedAt.UTC(),
	}
	snapshot, err = p.store.CreateAPTRepositorySnapshot(snapshotCtx, snapshot, memberships)
	if err != nil {
		return repository.APTRepositorySnapshot{}, err
	}
	if snapshot.State == repository.APTRepositorySnapshotVisible || snapshot.State == repository.APTRepositorySnapshotRetired {
		return snapshot, nil
	}
	if snapshot.State != repository.APTRepositorySnapshotBuilding {
		return repository.APTRepositorySnapshot{}, repository.ErrVersionConflict
	}
	releaseObjects := func() {}
	defer func() { releaseObjects() }()

	bundle, err := buildSnapshotBundle(snapshot, packages)
	if err != nil {
		return repository.APTRepositorySnapshot{}, err
	}
	releaseDigest := digestBytes(bundle.release)
	signature, err := p.signer.SignRelease(snapshotCtx, SignReleaseRequest{
		RepositoryID: snapshot.RepositoryID, SnapshotID: snapshot.ID, ReleaseDigest: releaseDigest,
		Release: bytes.NewReader(bundle.release),
	})
	if err != nil {
		if errors.Is(err, ErrUntrustedSigner) {
			return repository.APTRepositorySnapshot{}, ErrInvalidSignature
		}
		return repository.APTRepositorySnapshot{}, fmt.Errorf("%w: %v", ErrSignerUnavailable, err)
	}
	if !validSignatureResult(signature) {
		return repository.APTRepositorySnapshot{}, ErrInvalidSignature
	}
	bundle.addGenerated(snapshot, "dists/"+snapshot.Suite+"/Release", bundle.release, "text/plain; charset=utf-8")
	bundle.addGenerated(snapshot, "dists/"+snapshot.Suite+"/InRelease", signature.InRelease, "application/pgp-signature")
	bundle.addGenerated(snapshot, "dists/"+snapshot.Suite+"/Release.gpg", signature.Detached, "application/pgp-signature")

	intents := make([]repository.APTSnapshotObjectIntent, 0, len(bundle.generated))
	objectKeys := make([]string, 0, len(bundle.generated))
	for objectKey, body := range bundle.generated {
		intents = append(intents, repository.APTSnapshotObjectIntent{
			SnapshotID: snapshot.ID, RepositoryID: snapshot.RepositoryID, ObjectKey: objectKey,
			Digest: digestBytes(body), Size: int64(len(body)),
		})
		objectKeys = append(objectKeys, objectKey)
	}
	if err = p.store.CreateAPTSnapshotObjectIntents(snapshotCtx, snapshot.ID, intents); err != nil {
		return repository.APTRepositorySnapshot{}, err
	}
	objectCtx, release, err := repository.LockObjectKeys(snapshotCtx, objectKeys, p.store, repository.FormatAPT, p.store.LockAPTObject)
	if err != nil {
		return repository.APTRepositorySnapshot{}, err
	}
	releaseObjects = release
	if err = p.persistGeneratedObjects(objectCtx, bundle.generated); err != nil {
		return repository.APTRepositorySnapshot{}, err
	}
	snapshot.State = repository.APTRepositorySnapshotVisible
	snapshot.ReleaseDigest = releaseDigest
	snapshot.InReleaseDigest = digestBytes(signature.InRelease)
	snapshot.SignerIdentity = signature.SignerIdentity
	snapshot.KeyFingerprint = signature.KeyFingerprint
	snapshot.SignatureAlgorithm = signature.Algorithm
	published, err = p.store.PublishAPTRepositorySnapshotWithAudit(objectCtx, snapshot, bundle.assets, bundle.release, repository.AuditRecord{
		GroupName: repo.Name, Repository: repo.Name, Actor: input.Actor, Outcome: repository.AuditResolved,
		OccurredAt: time.Now().UTC(), Format: string(repository.FormatAPT), Resource: input.Suite,
		Representation: releaseDigest, Operation: "apt.repository_snapshot.publish", Status: 200,
		CacheDisposition: "bypass", AuthorizationSource: "repository_write", AuthorizationReason: "signed_snapshot_visible",
		Evidence: signedSnapshotAuditEvidence(signature),
	})
	if err != nil {
		return repository.APTRepositorySnapshot{}, err
	}
	return published, nil
}

func signedSnapshotAuditEvidence(signature SignReleaseResult) map[string]string {
	return map[string]string{
		"signerIdentity": signature.SignerIdentity, "keyFingerprint": signature.KeyFingerprint,
		"signatureAlgorithm": signature.Algorithm,
	}
}

func sameSnapshotRequest(snapshot repository.APTRepositorySnapshot, membership []repository.APTSnapshotPackage, input PublishSnapshotInput) bool {
	if snapshot.RepositoryID != input.RepositoryID || snapshot.Suite != input.Suite || snapshot.Sequence != input.Sequence || len(membership) != len(input.SessionIDs) {
		return false
	}
	stored := make([]string, 0, len(membership))
	for _, item := range membership {
		stored = append(stored, item.PublicationSessionID)
	}
	requested := append([]string(nil), input.SessionIDs...)
	slices.Sort(stored)
	slices.Sort(requested)
	return slices.Equal(stored, requested)
}

func validPublishSnapshotInput(input PublishSnapshotInput) bool {
	if _, err := uuid.Parse(input.ID); err != nil {
		return false
	}
	if input.RepositoryID == "" || !repository.ValidAPTPublicationScope(input.Suite) || input.Sequence <= 0 ||
		len(input.SessionIDs) == 0 || len(input.SessionIDs) > 10000 || input.Actor == "" || len(input.Actor) > 512 || input.CreatedAt.IsZero() {
		return false
	}
	seen := make(map[string]struct{}, len(input.SessionIDs))
	for _, id := range input.SessionIDs {
		if id == "" {
			return false
		}
		if _, duplicate := seen[id]; duplicate {
			return false
		}
		seen[id] = struct{}{}
	}
	return true
}

func (p *Publisher) loadPackages(ctx context.Context, input PublishSnapshotInput) ([]snapshotPackage, []repository.APTSnapshotPackage, error) {
	packages := make([]snapshotPackage, 0, len(input.SessionIDs))
	memberships := make([]repository.APTSnapshotPackage, 0, len(input.SessionIDs))
	poolPaths := make(map[string]struct{}, len(input.SessionIDs))
	for _, sessionID := range input.SessionIDs {
		session, err := p.store.GetAPTPublicationSession(ctx, sessionID)
		if err != nil {
			return nil, nil, err
		}
		if session.RepositoryID != input.RepositoryID || session.Suite != input.Suite || session.State != repository.APTPublicationSessionStaged {
			return nil, nil, repository.ErrDisabled
		}
		revision, err := p.store.GetAPTPackageRevisionForSession(ctx, session.ID)
		if err != nil {
			return nil, nil, err
		}
		info, err := p.objects.Stat(ctx, revision.ObjectKey)
		if err != nil || info.Size != revision.Size || info.Digest != revision.Digest {
			if err == nil {
				err = ErrDigestMismatch
			}
			return nil, nil, err
		}
		reader, size, err := p.objects.Open(ctx, revision.ObjectKey)
		if err != nil {
			return nil, nil, err
		}
		metadata, parseErr := aptprotocol.ParseDebianBinary(reader, size)
		closeErr := reader.Close()
		if parseErr != nil {
			return nil, nil, fmt.Errorf("parse staged Debian package: %w", parseErr)
		}
		if closeErr != nil {
			return nil, nil, closeErr
		}
		if metadata.CanonicalIdentity != revision.CanonicalIdentity {
			return nil, nil, ErrIdentityMismatch
		}
		poolPath := repository.APTPoolPath(session.Component, revision.Package, revision.ObjectName)
		if _, duplicate := poolPaths[poolPath]; duplicate {
			return nil, nil, repository.ErrNameExists
		}
		poolPaths[poolPath] = struct{}{}
		packages = append(packages, snapshotPackage{session: session, revision: revision, metadata: metadata, poolPath: poolPath, component: session.Component})
		memberships = append(memberships, repository.APTSnapshotPackage{
			PublicationSessionID: session.ID, PackageRevisionID: revision.ID,
			Component: session.Component, Architecture: revision.Architecture,
		})
	}
	sort.Slice(packages, func(i, j int) bool {
		left, right := packages[i], packages[j]
		return strings.Join([]string{left.component, left.revision.Architecture, left.revision.Package, left.revision.Version, left.revision.ObjectName}, "\x00") <
			strings.Join([]string{right.component, right.revision.Architecture, right.revision.Package, right.revision.Version, right.revision.ObjectName}, "\x00")
	})
	return packages, memberships, nil
}

func buildSnapshotBundle(snapshot repository.APTRepositorySnapshot, packages []snapshotPackage) (snapshotBundle, error) {
	bundle := snapshotBundle{generated: make(map[string][]byte)}
	groups := make(map[string][]snapshotPackage)
	components, architectures := make(map[string]struct{}), make(map[string]struct{})
	for _, pkg := range packages {
		key := pkg.component + "\x00" + pkg.revision.Architecture
		groups[key] = append(groups[key], pkg)
		components[pkg.component] = struct{}{}
		architectures[pkg.revision.Architecture] = struct{}{}
		bundle.assets = append(bundle.assets, repository.APTSnapshotAsset{
			SnapshotID: snapshot.ID, RepositoryID: snapshot.RepositoryID, Path: pkg.poolPath,
			Digest: pkg.revision.Digest, ObjectKey: pkg.revision.ObjectKey, Size: pkg.revision.Size,
			ContentType: "application/vnd.debian.binary-package",
		})
	}
	groupKeys := make([]string, 0, len(groups))
	for key := range groups {
		groupKeys = append(groupKeys, key)
	}
	slices.Sort(groupKeys)
	type releaseIndex struct {
		path   string
		body   []byte
		digest string
	}
	indices := make([]releaseIndex, 0, len(groupKeys)*2)
	totalIndexBytes := 0
	for _, key := range groupKeys {
		component, architecture, _ := strings.Cut(key, "\x00")
		var packagesBody bytes.Buffer
		for _, pkg := range groups[key] {
			stanza, err := aptprotocol.PackageIndexStanza(pkg.metadata, pkg.poolPath, pkg.revision.Size, pkg.revision.Digest)
			if err != nil {
				return snapshotBundle{}, err
			}
			if len(stanza) > maxAPTGeneratedIndexBytes-totalIndexBytes {
				return snapshotBundle{}, errors.New("APT Packages indices exceed publication limit")
			}
			totalIndexBytes += len(stanza)
			packagesBody.Write(stanza)
		}
		plain := packagesBody.Bytes()
		compressed, err := deterministicGzip(plain)
		if err != nil {
			return snapshotBundle{}, err
		}
		base := "dists/" + snapshot.Suite + "/" + component + "/binary-" + architecture + "/"
		for _, item := range []struct {
			name, contentType string
			body              []byte
		}{{"Packages", "text/plain; charset=utf-8", plain}, {"Packages.gz", "application/gzip", compressed}} {
			path := base + item.name
			digest := digestBytes(item.body)
			bundle.addGenerated(snapshot, path, item.body, item.contentType)
			bundle.addGenerated(snapshot, base+"by-hash/SHA256/"+strings.TrimPrefix(digest, "sha256:"), item.body, item.contentType)
			indices = append(indices, releaseIndex{path: strings.TrimPrefix(path, "dists/"+snapshot.Suite+"/"), body: item.body, digest: digest})
		}
	}
	componentValues, architectureValues := mapKeys(components), mapKeys(architectures)
	slices.Sort(componentValues)
	slices.Sort(architectureValues)
	var release bytes.Buffer
	fmt.Fprintf(&release, "Origin: Artifact Gateway\nLabel: Artifact Gateway\nSuite: %s\nCodename: %s\nDate: %s\nArchitectures: %s\nComponents: %s\nAcquire-By-Hash: yes\nDescription: Artifact Gateway immutable APT snapshot %d\nSHA256:\n",
		snapshot.Suite, snapshot.Suite, snapshot.CreatedAt.UTC().Format("Mon, 02 Jan 2006 15:04:05 -0700"),
		strings.Join(architectureValues, " "), strings.Join(componentValues, " "), snapshot.Sequence)
	for _, index := range indices {
		fmt.Fprintf(&release, " %s %16d %s\n", strings.TrimPrefix(index.digest, "sha256:"), len(index.body), index.path)
	}
	bundle.release = release.Bytes()
	return bundle, nil
}

func deterministicGzip(body []byte) ([]byte, error) {
	var output bytes.Buffer
	writer, err := gzip.NewWriterLevel(&output, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	writer.ModTime = time.Time{}
	writer.OS = 255
	if _, err = writer.Write(body); err != nil {
		_ = writer.Close()
		return nil, err
	}
	if err = writer.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func (b *snapshotBundle) addGenerated(snapshot repository.APTRepositorySnapshot, path string, body []byte, contentType string) {
	digest := digestBytes(body)
	objectKey := "native/apt/sha256/" + strings.TrimPrefix(digest, "sha256:")
	b.generated[objectKey] = append([]byte(nil), body...)
	b.assets = append(b.assets, repository.APTSnapshotAsset{
		SnapshotID: snapshot.ID, RepositoryID: snapshot.RepositoryID, Path: path,
		Digest: digest, ObjectKey: objectKey, Size: int64(len(body)), ContentType: contentType,
	})
}

func (p *Publisher) persistGeneratedObjects(ctx context.Context, generated map[string][]byte) error {
	keys := make([]string, 0, len(generated))
	for key := range generated {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		body := generated[key]
		digest := digestBytes(body)
		if info, err := p.objects.Stat(ctx, key); err == nil {
			if info.Size != int64(len(body)) || info.Digest != digest {
				return fmt.Errorf("APT snapshot object %q conflicts with its content digest", key)
			}
			continue
		} else if !errors.Is(err, objectstore.ErrNotFound) {
			return fmt.Errorf("inspect APT snapshot object %q: %w", key, err)
		}
		if err := p.objects.PutVerifiedReader(ctx, key, bytes.NewReader(body), int64(len(body)), digest); err != nil {
			return fmt.Errorf("persist APT snapshot object %q: %w", key, err)
		}
	}
	return nil
}

func validSignatureResult(result SignReleaseResult) bool {
	return len(result.InRelease) > 0 && len(result.InRelease) <= 16<<20 && len(result.Detached) > 0 && len(result.Detached) <= 1<<20 &&
		len(result.SignerIdentity) <= 512 && result.SignerIdentity != "" &&
		len(result.KeyFingerprint) <= 512 && result.KeyFingerprint != "" && len(result.Algorithm) <= 128 && result.Algorithm != "" &&
		!strings.ContainsAny(result.SignerIdentity+result.KeyFingerprint+result.Algorithm, "\x00\r\n")
}

func digestBytes(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func mapKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	return result
}
