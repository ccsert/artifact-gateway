// Package aptpublication owns APT Hosted staging and signed repository-snapshot
// assembly. Session staging cannot publish protocol metadata; Publisher is the
// only boundary allowed to make a complete signed snapshot visible.
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
	ErrInvalidPackage      = errors.New("debian binary package is invalid")
	ErrIdentityMismatch    = errors.New("debian package identity does not match expected identity")
	ErrDigestMismatch      = errors.New("debian package size or digest does not match declaration")
)

type managerStore interface {
	repository.HostedRepositoryStore
	repository.NativeAPTStore
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
	return m.store.CreateAPTPublicationSessionWithAuditIdempotently(ctx, repository.APTPublicationSession{
		ID: input.ID, RepositoryID: input.RepositoryID, Suite: input.Suite, Component: input.Component,
		Publisher: input.Publisher, ObjectName: input.ObjectName, DeclaredDigest: input.DeclaredDigest,
		DeclaredSize: input.DeclaredSize, ExpectedIdentity: input.ExpectedIdentity,
		State: repository.APTPublicationSessionOpen, ExpiresAt: input.ExpiresAt,
	}, input.Publisher, "repositories/"+input.RepositoryID+"/apt-publication-sessions", input.IdempotencyKey, hex.EncodeToString(payloadDigest[:]), repository.AuditRecord{
		GroupName: repo.Name, Repository: repo.Name, Actor: input.Publisher, Outcome: repository.AuditResolved,
		OccurredAt: time.Now().UTC(), Format: string(repository.FormatAPT), Resource: input.ID,
		Representation: input.DeclaredDigest, Operation: "apt.publication_session.create", Status: 201,
		CacheDisposition: "bypass", AuthorizationSource: "repository_write", AuthorizationReason: "created",
	})
}

func validCreateSessionInput(input CreateSessionInput) bool {
	return input.RepositoryID != "" && repository.ValidAPTPublicationScope(input.Suite) && repository.ValidAPTPublicationScope(input.Component) &&
		input.Publisher != "" && len(input.Publisher) <= 512 && repository.ValidAPTObjectName(input.ObjectName) &&
		repository.ValidAPTSHA256Digest(input.DeclaredDigest) && input.DeclaredSize > 0 && input.DeclaredSize <= MaxDebianPackageBytes &&
		len(input.ExpectedIdentity) <= 1024 && !strings.ContainsRune(input.ExpectedIdentity, '\x00') &&
		input.IdempotencyKey != "" && len(input.IdempotencyKey) <= 128
}

func (m *Manager) UploadPackage(ctx context.Context, sessionID, objectName string, body io.Reader, contentLength int64) (repository.APTPackageRevision, error) {
	return m.uploadPackage(ctx, sessionID, objectName, body, contentLength, "")
}

// UploadPackageAs records the authenticated management actor in the same
// transaction that makes the immutable revision management-visible.
func (m *Manager) UploadPackageAs(ctx context.Context, sessionID, objectName string, body io.Reader, contentLength int64, actor string) (repository.APTPackageRevision, error) {
	if actor == "" || len(actor) > 512 {
		return repository.APTPackageRevision{}, ErrInvalidSessionInput
	}
	return m.uploadPackage(ctx, sessionID, objectName, body, contentLength, actor)
}

func (m *Manager) uploadPackage(ctx context.Context, sessionID, objectName string, body io.Reader, contentLength int64, actor string) (repository.APTPackageRevision, error) {
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
	if actor == "" {
		actor = session.Publisher
	}
	if session.State != repository.APTPublicationSessionOpen && session.State != repository.APTPublicationSessionUploading {
		return repository.APTPackageRevision{}, repository.ErrDisabled
	}
	if contentLength < -1 || contentLength == 0 || contentLength > MaxDebianPackageBytes || contentLength > 0 && contentLength != session.DeclaredSize {
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
		return repository.APTPackageRevision{}, fmt.Errorf("%w: %v", ErrInvalidPackage, err)
	}
	if session.ExpectedIdentity != "" && session.ExpectedIdentity != metadata.CanonicalIdentity {
		return repository.APTPackageRevision{}, ErrIdentityMismatch
	}
	objectKey := "native/apt/sha256/" + strings.TrimPrefix(session.DeclaredDigest, "sha256:")
	objectCtx, release, err := repository.LockObjectKeys(ctx, []string{objectKey}, m.store, repository.FormatAPT, m.store.LockAPTObject)
	if err != nil {
		return repository.APTPackageRevision{}, fmt.Errorf("coordinate APT package object: %w", err)
	}
	defer release()
	if err = m.store.BeginAPTPackageUpload(objectCtx, session.ID, objectKey); err != nil {
		return repository.APTPackageRevision{}, err
	}
	if _, err = spool.Seek(0, io.SeekStart); err != nil {
		return repository.APTPackageRevision{}, fmt.Errorf("rewind Debian package: %w", err)
	}
	if err = m.objects.PutVerifiedReader(objectCtx, objectKey, spool, written, session.DeclaredDigest); err != nil {
		return repository.APTPackageRevision{}, fmt.Errorf("store Debian package: %w", err)
	}
	revision := repository.APTPackageRevision{
		ID: uuid.NewString(), RepositoryID: session.RepositoryID,
		Package: metadata.Package, Version: metadata.Version, Architecture: metadata.Architecture,
		CanonicalIdentity: metadata.CanonicalIdentity, Digest: session.DeclaredDigest,
		ObjectKey: objectKey, Size: written, ObjectName: session.ObjectName, Publisher: session.Publisher,
	}
	repo, err := m.store.GetHostedRepository(objectCtx, session.RepositoryID)
	if err != nil {
		return repository.APTPackageRevision{}, err
	}
	return m.store.CompleteAPTPackageUploadWithAudit(objectCtx, session.ID, revision, repository.AuditRecord{
		GroupName: repo.Name, Repository: repo.Name, Actor: actor, Outcome: repository.AuditResolved,
		OccurredAt: time.Now().UTC(), Format: string(repository.FormatAPT), Resource: revision.CanonicalIdentity,
		Representation: revision.Digest, Operation: "apt.publication_package.stage", Status: 200, Bytes: revision.Size,
		CacheDisposition: "bypass", AuthorizationSource: "repository_write", AuthorizationReason: "staged",
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
