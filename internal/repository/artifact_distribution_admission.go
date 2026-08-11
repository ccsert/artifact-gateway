package repository

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
)

const ArtifactQuarantinedReason = "artifact_quarantined"
const ReplicationSnapshotChangedReason = "replication_snapshot_changed"
const artifactDistributionUnitDigest = "\x00artifact-distribution-unit"

type artifactDistributionLocksContextKey struct{}

type artifactDistributionLockLease struct {
	keys   map[string]struct{}
	active atomic.Bool
}

// ArtifactDistributionCoordinate identifies one aggregate format publication
// unit. PyPI uses project@version because all files in that version cross the
// governance boundary atomically.
type ArtifactDistributionCoordinate struct {
	RepositoryID string
	Format       Format
	Coordinate   string
}

var ErrArtifactQuarantined = errors.New(ArtifactQuarantinedReason)

// ArtifactDistributionAllowed evaluates the repository-local quarantine
// invariant for one immutable artifact identity. Stores that predate the
// quarantine capability remain compatible and allow distribution.
func ArtifactDistributionAllowed(ctx context.Context, store any, repositoryID string, format Format, coordinate, digest string) (bool, error) {
	return ArtifactDistributionAllowedForDigests(ctx, store, repositoryID, format, coordinate, []string{digest})
}

// ArtifactDistributionAllowedForDigests evaluates every immutable file in one
// publication unit. This is required by formats such as PyPI where one version
// can contain several independently addressed files.
func ArtifactDistributionAllowedForDigests(ctx context.Context, store any, repositoryID string, format Format, coordinate string, digests []string) (bool, error) {
	quarantines, ok := store.(ArtifactQuarantineStore)
	if !ok || quarantines == nil {
		return true, nil
	}
	for _, digest := range sortedUniqueDigests(digests) {
		value, err := quarantines.GetArtifactQuarantine(ctx, repositoryID, format, coordinate, digest)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return false, err
		}
		if value.State == ArtifactQuarantineStateQuarantined {
			return false, nil
		}
	}
	return true, nil
}

// LockArtifactDistributionAdmission linearizes a quarantine transition with
// the final metadata publication performed by a promotion or replication
// worker. Single-file formats reuse the scanner's immutable-identity key;
// aggregate formats use a coordinate-level distribution-unit key that is also
// taken by every mutation capable of changing the unit's visible membership.
func LockArtifactDistributionAdmission(ctx context.Context, store any, repositoryID string, format Format, coordinate, digest string) (func(), error) {
	return LockArtifactDistributionAdmissionForDigests(ctx, store, repositoryID, format, coordinate, []string{digest})
}

// LockArtifactDistributionAdmissionForDigests serializes a complete
// multi-file publication unit with quarantine transitions. Locks are acquired
// in digest order and released in reverse order so concurrent workers cannot
// deadlock while publishing the same set in a different enumeration order.
func LockArtifactDistributionAdmissionForDigests(ctx context.Context, store any, repositoryID string, format Format, coordinate string, digests []string) (func(), error) {
	digests = sortedUniqueDigests(digests)
	releaseAll, err := LockArtifactDistributionUnit(ctx, store, repositoryID, format, coordinate, digests)
	if err != nil {
		return nil, err
	}
	allowed, err := ArtifactDistributionAllowedForDigests(ctx, store, repositoryID, format, coordinate, digests)
	if err != nil {
		releaseAll()
		return nil, err
	}
	if !allowed {
		releaseAll()
		return nil, ErrArtifactQuarantined
	}
	return releaseAll, nil
}

// LockArtifactDistributionUnit serializes a final publication or governance
// transition without evaluating current quarantine state. PyPI uses one
// coordinate-level lock because every file of project@version is one
// distribution unit; other formats retain exact digest locks.
func LockArtifactDistributionUnit(ctx context.Context, store any, repositoryID string, format Format, coordinate string, digests []string) (func(), error) {
	identities := make([]ArtifactDistributionLockIdentity, 0, len(digests)+1)
	if format == FormatPyPI {
		identities = append(identities, ArtifactDistributionLockIdentity{RepositoryID: repositoryID, Format: format, Coordinate: coordinate, Digest: artifactDistributionUnitDigest})
	} else {
		for _, digest := range sortedUniqueDigests(digests) {
			identities = append(identities, ArtifactDistributionLockIdentity{RepositoryID: repositoryID, Format: format, Coordinate: coordinate, Digest: digest})
		}
	}
	_, release, err := lockArtifactDistributionIdentities(ctx, store, identities)
	return release, err
}

// LockArtifactQuarantineTransition uses the same publication-unit lock as the
// workers. It intentionally does not inspect current state so both quarantine
// and release transitions can proceed after optimistic-version validation.
func LockArtifactQuarantineTransition(ctx context.Context, store any, repositoryID string, format Format, coordinate, digest string) (func(), error) {
	return LockArtifactDistributionUnit(ctx, store, repositoryID, format, coordinate, []string{digest})
}

// LockArtifactDistributionCoordinates acquires several aggregate coordinate
// locks in one globally sorted operation and returns a derived context that
// lets nested repository publication reuse them. This prevents source/target
// lock inversion and avoids consuming one PostgreSQL connection per unit.
func LockArtifactDistributionCoordinates(ctx context.Context, store any, coordinates []ArtifactDistributionCoordinate) (context.Context, func(), error) {
	identities := make([]ArtifactDistributionLockIdentity, 0, len(coordinates))
	for _, coordinate := range coordinates {
		if coordinate.RepositoryID != "" && coordinate.Format != "" && coordinate.Coordinate != "" {
			identities = append(identities, ArtifactDistributionLockIdentity{RepositoryID: coordinate.RepositoryID, Format: coordinate.Format, Coordinate: coordinate.Coordinate, Digest: artifactDistributionUnitDigest})
		}
	}
	acquired, release, err := lockArtifactDistributionIdentities(ctx, store, identities)
	if err != nil {
		return ctx, nil, err
	}
	lease := &artifactDistributionLockLease{keys: make(map[string]struct{}, len(acquired))}
	for _, identity := range acquired {
		lease.keys[artifactDistributionIdentityKey(identity)] = struct{}{}
	}
	lease.active.Store(true)
	leases, _ := ctx.Value(artifactDistributionLocksContextKey{}).([]*artifactDistributionLockLease)
	derivedLeases := append(append([]*artifactDistributionLockLease(nil), leases...), lease)
	var once sync.Once
	releaseLease := func() {
		once.Do(func() {
			lease.active.Store(false)
			release()
		})
	}
	return context.WithValue(ctx, artifactDistributionLocksContextKey{}, derivedLeases), releaseLease, nil
}

func lockArtifactDistributionCoordinates(ctx context.Context, store any, repositoryID string, format Format, coordinates []string) (func(), error) {
	scopes := make([]ArtifactDistributionCoordinate, 0, len(coordinates))
	for _, coordinate := range coordinates {
		scopes = append(scopes, ArtifactDistributionCoordinate{RepositoryID: repositoryID, Format: format, Coordinate: coordinate})
	}
	_, release, err := LockArtifactDistributionCoordinates(ctx, store, scopes)
	return release, err
}

func lockArtifactDistributionIdentities(ctx context.Context, store any, identities []ArtifactDistributionLockIdentity) ([]ArtifactDistributionLockIdentity, func(), error) {
	sort.Slice(identities, func(left, right int) bool {
		return artifactDistributionIdentityKey(identities[left]) < artifactDistributionIdentityKey(identities[right])
	})
	unique := identities[:0]
	for _, identity := range identities {
		if identity.RepositoryID == "" || identity.Format == "" || identity.Coordinate == "" || identity.Digest == "" {
			continue
		}
		key := artifactDistributionIdentityKey(identity)
		if artifactDistributionIdentityHeld(ctx, key) {
			continue
		}
		if len(unique) > 0 && unique[len(unique)-1] == identity {
			continue
		}
		unique = append(unique, identity)
	}
	identities = unique
	if locker, ok := store.(ArtifactDistributionIdentityLockStore); ok {
		release, err := locker.LockArtifactDistributionIdentities(ctx, identities)
		if err != nil {
			return nil, nil, err
		}
		var once sync.Once
		return identities, func() { once.Do(release) }, nil
	}
	releases := make([]func(), 0, len(identities))
	releaseAll := func() {
		for index := len(releases) - 1; index >= 0; index-- {
			releases[index]()
		}
	}
	if locker, ok := store.(ArtifactScanIdentityLockStore); ok {
		for _, identity := range identities {
			release, err := locker.LockArtifactScanIdentity(ctx, identity.RepositoryID, identity.Format, identity.Coordinate, identity.Digest)
			if err != nil {
				releaseAll()
				return nil, nil, err
			}
			releases = append(releases, release)
		}
	}
	var once sync.Once
	return identities, func() { once.Do(releaseAll) }, nil
}

func artifactDistributionIdentityKey(identity ArtifactDistributionLockIdentity) string {
	return artifactScanLockKey(identity.RepositoryID, identity.Format, identity.Coordinate, identity.Digest)
}

func artifactDistributionIdentityHeld(ctx context.Context, key string) bool {
	leases, _ := ctx.Value(artifactDistributionLocksContextKey{}).([]*artifactDistributionLockLease)
	for _, lease := range leases {
		if !lease.active.Load() {
			continue
		}
		if _, exists := lease.keys[key]; exists {
			return true
		}
	}
	return false
}

func sortedUniqueDigests(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
