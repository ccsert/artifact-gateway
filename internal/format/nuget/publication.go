package nuget

import (
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	nugetprotocol "github.com/ccsert/artifact-gateway/internal/protocol/nuget"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/uploadspool"
	"github.com/google/uuid"
)

type packageMetadata struct {
	ID               string                          `json:"id"`
	Version          string                          `json:"version"`
	Authors          string                          `json:"authors,omitempty"`
	Description      string                          `json:"description,omitempty"`
	DependencyGroups []nugetprotocol.DependencyGroup `json:"dependencyGroups"`
	PackageHash      string                          `json:"packageHash"`
	Listed           *bool                           `json:"listed,omitempty"`
}
type publicationRequest struct {
	PackageID string `json:"packageId"`
	Version   string `json:"version"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

func (m *Module) reserve(ctx context.Context, repo repository.HostedRepository, actor, key string, in publicationRequest) (repository.NuGetSession, error) {
	id, err := nugetprotocol.NormalizeID(in.PackageID)
	if err != nil {
		return repository.NuGetSession{}, repository.ErrInvalidNuGetPublication
	}
	version, err := nugetprotocol.NormalizeVersion(in.Version)
	if err != nil || in.Size > m.maxBytes {
		return repository.NuGetSession{}, repository.ErrInvalidNuGetPublication
	}
	sessionID := uuid.NewString()
	if key != "" {
		sessionID = uuid.NewSHA1(uuid.NameSpaceURL, []byte(repo.ID+"\x00nuget\x00"+actor+"\x00"+key)).String()
	}
	inSession := repository.NuGetSession{ID: sessionID, RepositoryID: repo.ID, PackageID: id, Version: strings.ToLower(version), Digest: in.Digest, Size: in.Size, Actor: actor, ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if repo.Type == repository.RepositoryTypeProxy {
		inSession.SourceVersion = repo.Version
	}
	session, _, err := m.store.ReserveNuGetPublication(ctx, inSession)
	return session, err
}

func inspectSpool(spool *uploadspool.Spool) (packageMetadata, []byte, error) {
	inspection, err := nugetprotocol.InspectPackage(spool.File(), spool.Size())
	if err != nil {
		return packageMetadata{}, nil, repository.ErrInvalidNuGetPublication
	}
	if err = spool.Rewind(); err != nil {
		return packageMetadata{}, nil, err
	}
	hash := sha512.New()
	if _, err = io.Copy(hash, spool.Reader()); err != nil {
		return packageMetadata{}, nil, err
	}
	meta := packageMetadata{ID: inspection.Identity.ID, Version: strings.ToLower(inspection.Identity.NormalizedVersion), Authors: inspection.Authors, Description: inspection.Description, DependencyGroups: inspection.DependencyGroups, PackageHash: base64.StdEncoding.EncodeToString(hash.Sum(nil))}
	return meta, inspection.Nuspec, spool.Rewind()
}

func (m *Module) stage(ctx context.Context, session repository.NuGetSession, spool *uploadspool.Spool) (repository.NuGetSession, error) {
	return m.stageWithListing(ctx, session, spool, nil)
}
func (m *Module) stageWithListing(ctx context.Context, session repository.NuGetSession, spool *uploadspool.Spool, listed *bool) (repository.NuGetSession, error) {
	if session.State != "committed" && (session.State == "expired" || !session.ExpiresAt.After(time.Now())) {
		return repository.NuGetSession{}, repository.ErrNuGetSessionExpired
	}
	meta, nuspec, err := inspectSpool(spool)
	if err != nil {
		return repository.NuGetSession{}, err
	}
	if !strings.EqualFold(meta.ID, session.PackageID) || meta.Version != session.Version || spool.Digest() != session.Digest || spool.Size() != session.Size {
		return repository.NuGetSession{}, repository.ErrNuGetPublicationConflict
	}
	if session.State == "committed" {
		_, err := m.commit(ctx, session, session.Actor)
		return session, err
	}
	meta.Listed = listed
	encoded, err := json.Marshal(meta)
	if err != nil {
		return repository.NuGetSession{}, err
	}
	if err = m.objects.PutVerifiedReader(ctx, session.ObjectKey, spool.Reader(), session.Size, session.Digest); err != nil {
		return repository.NuGetSession{}, err
	}
	session.Nuspec = nuspec
	session.MetadataJSON = encoded
	return m.store.StageNuGetPublication(ctx, session)
}

func (m *Module) commit(ctx context.Context, session repository.NuGetSession, actor string) (repository.NuGetPublication, error) {
	committed := session.State == "committed"
	if !committed && session.Actor != actor {
		return repository.NuGetPublication{}, repository.ErrNuGetPublicationConflict
	}
	if !committed && (session.State == "expired" || !session.ExpiresAt.After(time.Now())) {
		return repository.NuGetPublication{}, repository.ErrNuGetSessionExpired
	}
	if !committed && session.State != "staged" {
		return repository.NuGetPublication{}, repository.ErrNuGetSessionState
	}
	reader, size, err := m.objects.Open(ctx, session.ObjectKey)
	if err != nil {
		return repository.NuGetPublication{}, err
	}
	if size != session.Size {
		_ = reader.Close()
		return repository.NuGetPublication{}, repository.ErrNuGetPublicationConflict
	}
	spool, err := uploadspool.Read(reader, session.Size)
	closeErr := reader.Close()
	if err != nil {
		return repository.NuGetPublication{}, err
	}
	defer func() { _ = spool.Close() }()
	if closeErr != nil {
		return repository.NuGetPublication{}, closeErr
	}
	if spool.Digest() != session.Digest || spool.Size() != session.Size {
		return repository.NuGetPublication{}, repository.ErrNuGetPublicationConflict
	}
	// Re-read original package bytes before visibility, including after restart.
	meta, nuspec, err := inspectSpool(spool)
	if err != nil {
		return repository.NuGetPublication{}, err
	}
	var initialListing struct {
		Listed *bool `json:"listed"`
	}
	if json.Unmarshal(session.MetadataJSON, &initialListing) != nil {
		return repository.NuGetPublication{}, repository.ErrNuGetPublicationConflict
	}
	meta.Listed = initialListing.Listed
	encoded, err := json.Marshal(meta)
	if err != nil {
		return repository.NuGetPublication{}, err
	}
	if string(encoded) != string(session.MetadataJSON) || string(nuspec) != string(session.Nuspec) {
		return repository.NuGetPublication{}, repository.ErrNuGetPublicationConflict
	}
	if committed {
		publication, err := m.store.GetNuGetPublication(ctx, session.RepositoryID, session.PackageID, session.Version)
		if err != nil {
			return repository.NuGetPublication{}, err
		}
		if publication.Digest != session.Digest || publication.Size != session.Size || publication.ObjectKey != session.ObjectKey || string(publication.MetadataJSON) != string(encoded) || string(publication.Nuspec) != string(nuspec) {
			return repository.NuGetPublication{}, repository.ErrNuGetPublicationConflict
		}
		return publication, nil
	}
	publication, _, err := m.store.CommitNuGetPublication(ctx, session.ID, actor)
	return publication, err
}

func (m *Module) push(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository) {
	if r.Method != http.MethodPut {
		w.WriteHeader(405)
		return
	}
	p, ok := m.authorizeCollection(w, r, repo, authorization.RepositoryWrite)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, m.maxBytes+(1<<20))
	reader, err := r.MultipartReader()
	if err != nil {
		m.problem(w, 400, "multipart_package_required")
		return
	}
	part, err := reader.NextPart()
	if err != nil {
		m.problem(w, 400, "package_required")
		return
	}
	defer func() { _ = part.Close() }()
	spool, err := uploadspool.Read(part, m.maxBytes)
	if err != nil {
		if errors.Is(err, uploadspool.ErrTooLarge) {
			m.problem(w, 413, "package_too_large")
		} else {
			m.problem(w, 400, "invalid_package")
		}
		return
	}
	defer func() { _ = spool.Close() }()
	meta, _, err := inspectSpool(spool)
	if err != nil {
		m.failure(w, err)
		return
	}
	if _, ok = m.authorize(w, r, repo, authorization.RepositoryWrite, strings.ToLower(meta.ID)); !ok {
		return
	}
	ctx, unlock, err := m.store.LockNuGetPublication(r.Context(), repo.ID, strings.ToLower(meta.ID), meta.Version)
	if err != nil {
		m.failure(w, err)
		return
	}
	defer unlock()
	session, err := m.reserve(ctx, repo, p.Actor, "", publicationRequest{PackageID: meta.ID, Version: meta.Version, Digest: spool.Digest(), Size: spool.Size()})
	if err != nil {
		m.failure(w, err)
		return
	}
	if session.State != "committed" {
		session, err = m.stage(ctx, session, spool)
		if err != nil {
			m.failure(w, err)
			return
		}
	}
	publication, err := m.commit(ctx, session, p.Actor)
	if err != nil {
		m.failure(w, err)
		return
	}
	w.Header().Set("Location", packageURL(r, repo, publication.PackageID, publication.Version))
	w.Header().Set("ETag", `"`+publication.Digest+`"`)
	w.WriteHeader(http.StatusCreated)
}
