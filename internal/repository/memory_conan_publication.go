package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
)

func (s *MemoryStore) BeginConanNativeSession(ctx context.Context, incoming ConanPublishSession) (ConanPublishSession, error) {
	if err := ctx.Err(); err != nil {
		return ConanPublishSession{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	repo, ok := s.hostedRepositories[incoming.RepositoryID]
	if !ok || repo.Format != FormatConan || repo.Type != RepositoryTypeHosted || repo.State != RepositoryActive {
		return ConanPublishSession{}, ErrNotFound
	}
	now := time.Now().UTC()
	for id, session := range s.conanSessions {
		if !session.Native || !sameConanSessionIdentity(session, incoming) || session.State != "open" {
			continue
		}
		if session.ExpiresAt.After(now) {
			return cloneConanSession(session), nil
		}
		session.State = "expired"
		s.conanSessions[id] = session
	}
	incoming.ID, incoming.Native, incoming.Version = uuid.NewString(), true, 1
	incoming.State, incoming.ExpiresAt, incoming.Objects = "open", now.Add(time.Hour), nil
	s.conanSessions[incoming.ID] = incoming
	s.conanUploads[incoming.ID] = make(map[string]string)
	return incoming, nil
}

func (s *MemoryStore) StageConanNativeObject(ctx context.Context, id string, object MavenDeclaredObject, key string) (ConanPublishSession, error) {
	if err := ctx.Err(); err != nil {
		return ConanPublishSession{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.conanSessions[id]
	if !ok {
		return ConanPublishSession{}, ErrNotFound
	}
	if !session.Native || session.State != "open" || !session.ExpiresAt.After(time.Now()) {
		return ConanPublishSession{}, ErrDisabled
	}
	intent, ok := s.conanObjects[key]
	if !ok || intent.RepositoryID != session.RepositoryID || intent.Digest != object.Digest || intent.Size != object.Size || !intent.ClaimedAt.IsZero() || !intent.CollectedAt.IsZero() {
		return ConanPublishSession{}, ErrDisabled
	}
	found := false
	session = cloneConanSession(session)
	for i, existing := range session.Objects {
		if existing.Name == object.Name {
			session.Objects[i], found = object, true
			break
		}
	}
	if !found {
		session.Objects = append(session.Objects, object)
	}
	session.Version++
	s.conanSessions[id] = session
	s.conanUploads[id][object.Name] = key
	return cloneConanSession(session), nil
}

func (s *MemoryStore) CommitConanNativeSession(ctx context.Context, id string, version int64, digest string) (ConanPublishSession, bool, error) {
	if err := ctx.Err(); err != nil {
		return ConanPublishSession{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.conanSessions[id]
	if !ok {
		return ConanPublishSession{}, false, ErrNotFound
	}
	if !session.Native || session.Version != version || session.State != "open" && session.State != "committed" || !session.ExpiresAt.After(time.Now()) {
		return ConanPublishSession{}, false, ErrVersionConflict
	}
	repo := s.hostedRepositories[session.RepositoryID]
	if repo.State != RepositoryActive || repo.Type != RepositoryTypeHosted {
		return ConanPublishSession{}, false, ErrDisabled
	}
	assets, err := conanSessionAssets(session, s.conanUploads[id])
	if err != nil {
		return ConanPublishSession{}, false, err
	}
	for _, asset := range assets {
		intent, ok := s.conanObjects[asset.ObjectKey]
		if !ok || intent.Digest != asset.Digest || intent.Size != asset.Size || !intent.CollectedAt.IsZero() {
			return ConanPublishSession{}, false, ErrDisabled
		}
	}
	recipeKey := conanRecipeKey(session.RepositoryID, session.Reference, session.RecipeRevision)
	recipe, exists := s.conanRecipes[recipeKey]
	if _, tombstoned := s.artifactTombstones[session.RepositoryID+"\x00conan\x00"+session.Reference+"#"+session.RecipeRevision]; tombstoned || exists && recipe.State != "visible" {
		return ConanPublishSession{}, false, ErrArtifactTombstoned
	}
	if exists && s.artifactQuarantines[artifactQuarantineKey(session.RepositoryID, FormatConan, session.Reference+"#"+session.RecipeRevision, recipe.Digest)].State == ArtifactQuarantineStateQuarantined {
		return ConanPublishSession{}, false, ErrDisabled
	}
	replay := false
	if session.Kind == "recipe" {
		replay = exists
		_, err = s.putConanRecipeRevisionLocked(ConanRecipeRevision{RepositoryID: session.RepositoryID, Reference: session.Reference, Revision: session.RecipeRevision, Digest: digest}, assets)
	} else {
		_, replay = s.conanPackages[conanPackageKey(session.RepositoryID, session.Reference, session.RecipeRevision, session.PackageID, session.PackageRevision)]
		_, err = s.putConanPackageRevisionLocked(ConanPackageRevision{RepositoryID: session.RepositoryID, Reference: session.Reference, RecipeRevision: session.RecipeRevision, PackageID: session.PackageID, Revision: session.PackageRevision, Digest: digest}, assets)
	}
	if err != nil {
		return ConanPublishSession{}, false, err
	}
	session.State = "committed"
	s.conanSessions[id] = session
	s.appendAuditLocked(withRequestContextAudit(ctx, conanNativePublicationAudit(repo, session, digest, replay)))
	return cloneConanSession(session), replay, nil
}
