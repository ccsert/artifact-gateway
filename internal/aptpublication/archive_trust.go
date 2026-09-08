package aptpublication

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
)

var ErrArchiveReceiptMismatch = errors.New("APT snapshot archive differs from the operator-pinned backup digest")

var ErrSnapshotArchiveUntrusted = errors.New("APT snapshot archive signature is untrusted")

// TrustedSnapshotArchiveVerifier applies the same bounded public-key and RSA
// policy as the production HTTPS signer client to both archived signatures.
type TrustedSnapshotArchiveVerifier struct {
	keyring openpgp.EntityList
	trusted map[string]struct{}
}

func NewTrustedSnapshotArchiveVerifier(fingerprints []string, publicKeys []byte) (*TrustedSnapshotArchiveVerifier, error) {
	canonical, err := NormalizeTrustedSignerFingerprints(fingerprints)
	if err != nil || len(canonical) == 0 {
		return nil, ErrSnapshotArchiveUntrusted
	}
	keyring, keyFingerprints, err := parseTrustedSignerPublicKeys(publicKeys)
	if err != nil || !sameFingerprints(canonical, keyFingerprints) {
		return nil, ErrSnapshotArchiveUntrusted
	}
	trusted := make(map[string]struct{}, len(canonical))
	for _, fingerprint := range canonical {
		trusted[fingerprint] = struct{}{}
	}
	return &TrustedSnapshotArchiveVerifier{keyring: keyring, trusted: trusted}, nil
}

func (v *TrustedSnapshotArchiveVerifier) Verify(ctx context.Context, manifest SnapshotArchiveManifest, release, inRelease, detached []byte) error {
	if v == nil || len(v.keyring) == 0 || len(release) == 0 || len(release) > 16<<20 || len(inRelease) == 0 || len(inRelease) > 16<<20 || len(detached) == 0 || len(detached) > 1<<20 {
		return ErrSnapshotArchiveUntrusted
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("%w: %v", ErrSnapshotArchiveUntrusted, ctx.Err())
	default:
	}
	snapshot := manifest.Snapshot
	if digestBytes(release) != snapshot.ReleaseDigest || digestBytes(inRelease) != snapshot.InReleaseDigest {
		return ErrSnapshotArchiveUntrusted
	}
	claimedFingerprint := strings.ToLower(snapshot.KeyFingerprint)
	if _, ok := v.trusted[claimedFingerprint]; !ok {
		return ErrSnapshotArchiveUntrusted
	}
	envelope := SignReleaseResult{
		InRelease: inRelease, Detached: detached, SignerIdentity: snapshot.SignerIdentity,
		KeyFingerprint: claimedFingerprint, Algorithm: snapshot.SignatureAlgorithm,
	}
	if !signatureEnvelopeMatchesRelease(release, envelope) {
		return ErrSnapshotArchiveUntrusted
	}
	evidence, ok := signatureEnvelopeVerifiedByTrustedKey(v.keyring, release, envelope)
	if !ok || evidence.fingerprint != claimedFingerprint || !v.matchesSignerIdentity(evidence, snapshot.SignerIdentity) || evidence.algorithm != snapshot.SignatureAlgorithm {
		return ErrSnapshotArchiveUntrusted
	}
	return nil
}

// SnapshotArchiveTrustVerifier is the explicit trust boundary for recovery.
// A digest-valid tar is not sufficient authority to restore signed metadata.
type SnapshotArchiveTrustVerifier interface {
	Verify(context.Context, SnapshotArchiveManifest, []byte, []byte, []byte) error
}

// The reference signer historically stored "Name <email>" without the OpenPGP
// UID comment. Accept that exact key-derived label as well as the full UID;
// an archive-supplied arbitrary identity never becomes signature authority.
func (v *TrustedSnapshotArchiveVerifier) matchesSignerIdentity(evidence verifiedSignerEvidence, claimed string) bool {
	if claimed == evidence.identity {
		return true
	}
	for _, entity := range v.keyring {
		identity := entity.PrimaryIdentity()
		if identity != nil && identity.Name == evidence.identity && identity.UserId != nil &&
			claimed == identity.UserId.Name+" <"+identity.UserId.Email+">" {
			return true
		}
	}
	return false
}
