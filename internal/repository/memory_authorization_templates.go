package repository

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

func cloneAuthorizationTemplate(template AuthorizationTemplate) AuthorizationTemplate {
	template.Grants = cloneRepositoryGrantSet(RepositoryGrantSet{Grants: template.Grants}).Grants
	return template
}

func (s *MemoryStore) CreateAuthorizationTemplate(_ context.Context, template AuthorizationTemplate) (AuthorizationTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := strings.TrimSpace(template.Name)
	if name == "" {
		return AuthorizationTemplate{}, ErrNameExists
	}
	for _, existing := range s.authorizationTemplates {
		if strings.EqualFold(existing.Name, name) {
			return AuthorizationTemplate{}, ErrTemplateNameExists
		}
	}
	now := time.Now().UTC()
	if template.ID == "" {
		template.ID = uuid.NewString()
	}
	template.Name = name
	template.Version = "1"
	template.CreatedAt = now
	template.UpdatedAt = now
	template = cloneAuthorizationTemplate(template)
	s.authorizationTemplates[template.ID] = template
	return cloneAuthorizationTemplate(template), nil
}

func (s *MemoryStore) ListAuthorizationTemplates(_ context.Context) ([]AuthorizationTemplate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]AuthorizationTemplate, 0, len(s.authorizationTemplates))
	for _, template := range s.authorizationTemplates {
		items = append(items, cloneAuthorizationTemplate(template))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}

func (s *MemoryStore) GetAuthorizationTemplate(_ context.Context, id string) (AuthorizationTemplate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	template, ok := s.authorizationTemplates[id]
	if !ok {
		return AuthorizationTemplate{}, ErrNotFound
	}
	return cloneAuthorizationTemplate(template), nil
}

func (s *MemoryStore) UpdateAuthorizationTemplate(_ context.Context, template AuthorizationTemplate, expectedVersion string) (AuthorizationTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.authorizationTemplates[template.ID]
	if !ok {
		return AuthorizationTemplate{}, ErrNotFound
	}
	if current.Version != expectedVersion {
		return AuthorizationTemplate{}, ErrVersionConflict
	}
	name := strings.TrimSpace(template.Name)
	if name == "" {
		return AuthorizationTemplate{}, ErrNameExists
	}
	for id, existing := range s.authorizationTemplates {
		if id != template.ID && strings.EqualFold(existing.Name, name) {
			return AuthorizationTemplate{}, ErrTemplateNameExists
		}
	}
	template.Name = name
	template.Version = nextHostedGroupVersion(current.Version)
	template.CreatedAt = current.CreatedAt
	template.UpdatedAt = time.Now().UTC()
	template = cloneAuthorizationTemplate(template)
	s.authorizationTemplates[template.ID] = template
	return cloneAuthorizationTemplate(template), nil
}

func (s *MemoryStore) DeleteAuthorizationTemplate(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.authorizationTemplates[id]; !ok {
		return ErrNotFound
	}
	delete(s.authorizationTemplates, id)
	return nil
}

func (s *MemoryStore) ApplyAuthorizationTemplate(_ context.Context, templateID, repositoryID, expectedVersion string) (RepositoryGrantSet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	template, ok := s.authorizationTemplates[templateID]
	if !ok {
		return RepositoryGrantSet{}, ErrNotFound
	}
	if _, ok := s.hostedRepositories[repositoryID]; !ok {
		return RepositoryGrantSet{}, ErrNotFound
	}
	set, ok := s.repositoryGrants[repositoryID]
	if !ok {
		set.Version = "1"
	}
	if set.Version != expectedVersion {
		return RepositoryGrantSet{}, ErrVersionConflict
	}
	set.Version = nextHostedGroupVersion(set.Version)
	set.Grants = cloneAuthorizationTemplate(template).Grants
	s.repositoryGrants[repositoryID] = set
	return cloneRepositoryGrantSet(set), nil
}
