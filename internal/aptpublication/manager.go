// Package aptpublication owns the pre-visibility APT Hosted upload workflow.
// It deliberately cannot publish protocol metadata; signed snapshot assembly
// is a separate boundary.
package aptpublication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	aptprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/apt"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

const MaxDebianPackageBytes = int64(1 << 30)

var (
	ErrInvalidSessionInput = errors.New("APT publication session input is invalid")
	ErrIdentityMismatch    = errors.New("debian package identity does not match expected identity")
	ErrDigestMismatch      = errors.New("debian package size or digest does not match declaration")
)

type managerStore interface {
	repository.HostedRepositoryStore
	repository.NativeAPTPublicationStore
}

type Manager struct {
	store   managerStore
	objects objectstore.Store
}

func NewManager(store managerStore, objects objectstore.Store) *Manager {
	return &Manager{store: store, objects: objects}
}

type CreateSessionInput struct {
	ID               string
	RepositoryID     string
	Suite            string
	Component        string
	Publisher        string
	ObjectName       string
	DeclaredDigest   string
	DeclaredSize     int64
	ExpectedIdentity string
	IdempotencyKey   string
	ExpiresAt        time.Time
}

func (m *Manager) CreateSession(ctx context.Context, input CreateSessionInput) (repository.APTPublicationSession, bool, error) {
	if m == nil || m.store == nil || m.objects == nil || !validCreateSessionInput(input) {
		return repository.APTPublicationSession{}, false, ErrInvalidSessionInput
	}
	repo, err := m.store.GetHostedRepository(ctx, input.RepositoryID)
	if err != nil {
		return repository.APTPublicationSession{}, false, err
	}
	if repo.Format != repository.FormatAPT || repo.Type != repository.RepositoryTypeHosted || repo.State != repository.RepositoryActive {
		return repository.APTPublicationSession{}, false, repository.ErrNotFound
	}
	if input.ID == "" {
		input.ID = uuid.NewString()
	}
	if input.ExpiresAt.IsZero() {
		input.ExpiresAt = time.Now().UTC().Add(time.Hour)
	}
	payload, _ := json.Marshal(struct {
		RepositoryID, Suite, Component, Publisher, ObjectName, DeclaredDigest, ExpectedIdentity string
		DeclaredSize                                                                            int64
	}{
		RepositoryID: input.RepositoryID, Suite: input.Suite, Component: input.Component,
		Publisher: input.Publisher, ObjectName: input.ObjectName, DeclaredDigest: input.DeclaredDigest,
		ExpectedIdentity: input.ExpectedIdentity, DeclaredSize: input.DeclaredSize,
	})
	payloadDigest := sha256.Sum256(payload)
	return m.store.CreateAPTPublicationSessionIdempotently(ctx, repository.APTPublicationSession{
		ID: input.ID, RepositoryID: input.RepositoryID, Suite: input.Suite, Component: input.Component,
		Publisher: input.Publisher, ObjectName: input.ObjectName, DeclaredDigest: input.DeclaredDigest,
		DeclaredSize: input.DeclaredSize, ExpectedIdentity: input.ExpectedIdentity,
		State: repository.APTPublicationSessionOpen, ExpiresAt: input.ExpiresAt,
	}, input.Publisher, "repositories/"+input.RepositoryID+"/apt-publication-sessions", input.IdempotencyKey, hex.EncodeToString(payloadDigest[:]))
}

func validCreateSessionInput(input CreateSessionInput) bool {
	return input.RepositoryID != "" && validScopeSegment(input.Suite) && validScopeSegment(input.Component) &&
		input.Publisher != "" && len(input.Publisher) <= 512 && input.ObjectName != "" &&
		!strings.Contains(input.ObjectName, "/") && strings.HasSuffix(input.ObjectName, ".deb") &&
		validDigest(input.DeclaredDigest) && input.DeclaredSize > 0 && input.DeclaredSize <= MaxDebianPackageBytes &&
		len(input.ExpectedIdentity) <= 1024 && !strings.ContainsRune(input.ExpectedIdentity, '\x00') &&
		input.IdempotencyKey != "" && len(input.IdempotencyKey) <= 128
}

func validScopeSegment(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '+' && r != '.' {
			return false
		}
	}
	return true
}

func validDigest(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, r := range value[7:] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func (m *Manager) UploadPackage(ctx context.Context, sessionID, objectName string, body io.Reader, contentLength int64) (repository.APTPackageRevision, error) {
	if m == nil || m.store == nil || m.objects == nil || body == nil {
		return repository.APTPackageRevision{}, ErrInvalidSessionInput
	}
	session, err := m.store.GetAPTPublicationSession(ctx, sessionID)
	if err != nil {
		return repository.APTPackageRevision{}, err
	}
	if objectName != session.ObjectName {
		return repository.APTPackageRevision{}, repository.ErrNotFound
	}
	if session.State == repository.APTPublicationSessionStaged {
		return m.store.GetAPTPackageRevisionForSession(ctx, sessionID)
	}
	if session.State != repository.APTPublicationSessionOpen && session.State != repository.APTPublicationSessionUploading {
		return repository.APTPackageRevision{}, repository.ErrDisabled
	}
	if contentLength != session.DeclaredSize || contentLength <= 0 || contentLength > MaxDebianPackageBytes {
		return repository.APTPackageRevision{}, ErrDigestMismatch
	}

	spool, err := os.CreateTemp("", "artifact-gateway-apt-*.deb")
	if err != nil {
		return repository.APTPackageRevision{}, fmt.Errorf("create Debian package spool: %w", err)
	}
	spoolName := spool.Name()
	defer func() {
		_ = spool.Close()
		_ = os.Remove(spoolName)
	}()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(spool, hash), io.LimitReader(body, session.DeclaredSize+1))
	if err != nil {
		return repository.APTPackageRevision{}, fmt.Errorf("spool Debian package: %w", err)
	}
	if written != session.DeclaredSize || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != session.DeclaredDigest {
		return repository.APTPackageRevision{}, ErrDigestMismatch
	}
	if _, err = spool.Seek(0, io.SeekStart); err != nil {
		return repository.APTPackageRevision{}, fmt.Errorf("rewind Debian package: %w", err)
	}
	metadata, err := aptprotocol.ParseDebianBinary(spool, written)
	if err != nil {
		return repository.APTPackageRevision{}, fmt.Errorf("invalid Debian package: %w", err)
	}
	if session.ExpectedIdentity != "" && session.ExpectedIdentity != metadata.CanonicalIdentity {
		return repository.APTPackageRevision{}, ErrIdentityMismatch
	}
	objectKey := "native/apt/sha256/" + strings.TrimPrefix(session.DeclaredDigest, "sha256:")
	if err = m.store.BeginAPTPackageUpload(ctx, session.ID, objectKey); err != nil {
		return repository.APTPackageRevision{}, err
	}
	if _, err = spool.Seek(0, io.SeekStart); err != nil {
		return repository.APTPackageRevision{}, fmt.Errorf("rewind Debian package: %w", err)
	}
	if err = m.objects.PutVerifiedReader(ctx, objectKey, spool, written, session.DeclaredDigest); err != nil {
		return repository.APTPackageRevision{}, fmt.Errorf("store Debian package: %w", err)
	}
	return m.store.CompleteAPTPackageUpload(ctx, session.ID, repository.APTPackageRevision{
		ID: uuid.NewString(), RepositoryID: session.RepositoryID,
		Package: metadata.Package, Version: metadata.Version, Architecture: metadata.Architecture,
		CanonicalIdentity: metadata.CanonicalIdentity, Digest: session.DeclaredDigest,
		ObjectKey: objectKey, Size: written, ObjectName: session.ObjectName, Publisher: session.Publisher,
	})
}

// Signer is intentionally narrow: application nodes pass immutable Release
// bytes and receive public signature evidence. Private key material is never a
// parameter or result of this interface.
type Signer interface {
	SignRelease(context.Context, SignReleaseRequest) (SignReleaseResult, error)
}

type SignReleaseRequest struct {
	RepositoryID  string
	SnapshotID    string
	ReleaseDigest string
	Release       io.Reader
}

type SignReleaseResult struct {
	InRelease      []byte
	Detached       []byte
	SignerIdentity string
	KeyFingerprint string
	Algorithm      string
}
