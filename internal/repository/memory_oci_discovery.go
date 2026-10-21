package repository

import (
	"context"
	"time"

	discovery "github.com/ccsert/artifact-gateway/internal/ocidiscovery"
)

var _ discovery.Store = (*MemoryStore)(nil)

func (s *MemoryStore) checkOCIGuardsLocked(guards []discovery.Guard) error {
	for _, guard := range guards {
		version := ""
		switch guard.Kind {
		case "group":
			version = s.hostedGroups[guard.ID].Version
		case "repository":
			repo := s.hostedRepositories[guard.ID]
			if repo.State == RepositoryActive && repo.Format == FormatOCI {
				version = repo.Version
			}
		case "grants":
			version = s.repositoryGrants[guard.ID].Version
			if version == "" {
				version = "1"
			}
		}
		if version != guard.Version {
			return discovery.ErrChanged
		}
	}
	return nil
}

func (s *MemoryStore) SaveOCIDiscoverySnapshot(ctx context.Context, snapshot discovery.Snapshot) error {
	if err := discovery.ValidateSnapshot(snapshot, time.Now().UTC()); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.checkOCIGuardsLocked(snapshot.Guards); err != nil {
		return err
	}
	if prior, exists := s.ociDiscoverySnapshots[snapshot.ID]; exists {
		if sameOCISnapshot(prior, snapshot) {
			return nil
		}
		return discovery.ErrChanged
	}
	if s.ociDiscoverySnapshots == nil {
		s.ociDiscoverySnapshots = map[string]discovery.Snapshot{}
	}
	active := 0
	for id, prior := range s.ociDiscoverySnapshots {
		if !prior.ExpiresAt.After(time.Now().UTC()) {
			delete(s.ociDiscoverySnapshots, id)
		} else if prior.Owner == snapshot.Owner {
			active++
		}
	}
	if active >= 32 {
		return discovery.ErrLimit
	}
	s.ociDiscoverySnapshots[snapshot.ID] = discovery.CloneSnapshot(snapshot)
	s.Audits = append(s.Audits, ociSnapshotAudit(snapshot))
	return nil
}

func (s *MemoryStore) GetOCIDiscoverySnapshot(ctx context.Context, id, owner string) (discovery.Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return discovery.Snapshot{}, err
	}
	snapshot, exists := s.ociDiscoverySnapshots[id]
	if !exists || snapshot.Owner != owner || !snapshot.ExpiresAt.After(time.Now().UTC()) {
		return discovery.Snapshot{}, discovery.ErrNotFound
	}
	if err := s.checkOCIGuardsLocked(snapshot.Guards); err != nil {
		return discovery.Snapshot{}, err
	}
	return discovery.CloneSnapshot(snapshot), nil
}

func (s *MemoryStore) PublishOCISubjectBinding(ctx context.Context, binding discovery.Binding) error {
	if err := discovery.ValidateBinding(binding); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.checkOCIGuardsLocked(binding.Guards); err != nil {
		return err
	}
	if s.ociSubjectBindings == nil {
		s.ociSubjectBindings = map[string]discovery.Binding{}
	}
	key := ociBindingKey(binding.Scope, binding.Owner, binding.Image, binding.Subject)
	if prior, exists := s.ociSubjectBindings[key]; exists {
		var err error
		binding, err = mergeOCIBinding(prior, binding)
		if err != nil {
			return err
		}
	}
	for _, prior := range s.ociSubjectBindings {
		if prior.Scope != binding.Scope || prior.Owner != binding.Owner || prior.Image != binding.Image {
			continue
		}
		for _, a := range prior.Objects {
			for _, b := range binding.Objects {
				if a.Digest == b.Digest && (prior.SourceID != binding.SourceID || prior.SourceVersion != binding.SourceVersion || a != b) {
					return discovery.ErrChanged
				}
			}
		}
	}
	s.ociSubjectBindings[key] = discovery.CloneBinding(binding)
	s.Audits = append(s.Audits, ociDiscoveryAudit(binding.Actor, binding.Scope, binding.Image, binding.Subject, "oci.referrers.bind"))
	return nil
}

func (s *MemoryStore) GetOCISubjectBinding(ctx context.Context, scope, owner, image, subject string) (discovery.Binding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return discovery.Binding{}, err
	}
	binding, exists := s.ociSubjectBindings[ociBindingKey(scope, owner, image, subject)]
	if !exists {
		return discovery.Binding{}, discovery.ErrNotFound
	}
	return discovery.CloneBinding(binding), nil
}

func (s *MemoryStore) GetOCIBoundObject(ctx context.Context, scope, owner, image, digest string) (discovery.Binding, discovery.Object, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return discovery.Binding{}, discovery.Object{}, err
	}
	for _, binding := range s.ociSubjectBindings {
		if binding.Scope != scope || binding.Owner != owner || binding.Image != image {
			continue
		}
		for _, object := range binding.Objects {
			if object.Digest == digest {
				return discovery.CloneBinding(binding), object, nil
			}
		}
	}
	return discovery.Binding{}, discovery.Object{}, discovery.ErrNotFound
}
