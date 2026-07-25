package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type Resolver struct {
	Store   repository.Store
	Adapter Adapter
	Metrics *Metrics
}

func (r Resolver) Resolve(ctx context.Context, groupName, repositoryName, actor string) (repository.Member, error) {
	return r.resolve(ctx, groupName, repositoryName, actor, func(member repository.Member) bool {
		return r.Adapter.Available(ctx, member, repositoryName)
	})
}

func (r Resolver) ResolveOCIMembers(ctx context.Context, groupName, repositoryName, actor string) ([]repository.Member, error) {
	resolved := false
	defer func() {
		if !resolved {
			r.Metrics.failed.Add(1)
		}
	}()
	group, err := r.loadActiveGroup(ctx, groupName, repositoryName, actor)
	if err != nil {
		return nil, err
	}
	var hosted, proxy []repository.Member
	for _, member := range group.Members {
		if actor == "anonymous" && !member.Anonymous {
			continue
		}
		if !r.Adapter.Available(ctx, member, repositoryName) {
			continue
		}
		switch member.Type {
		case repository.MemberHosted:
			hosted = append(hosted, member)
		case repository.MemberProxy:
			proxy = append(proxy, member)
		}
	}
	if len(hosted)+len(proxy) == 0 {
		if err := r.audit(ctx, groupName, repositoryName, "", repository.AuditNotFound, actor); err != nil {
			return nil, err
		}
		return nil, repository.ErrNotFound
	}
	resolved = true
	return append(hosted, proxy...), nil
}

func (r Resolver) RecordOCIResolution(ctx context.Context, groupName, repositoryName, memberName, actor string) error {
	if err := r.audit(ctx, groupName, repositoryName, memberName, repository.AuditResolved, actor); err != nil {
		r.Metrics.failed.Add(1)
		return err
	}
	r.Metrics.resolved.Add(1)
	return nil
}

func (r Resolver) RecordOCIFailure(ctx context.Context, groupName, repositoryName, memberName, actor string, outcome repository.AuditOutcome) error {
	return r.audit(ctx, groupName, repositoryName, memberName, outcome, actor)
}

func (r Resolver) RecordOCIGrantDenied(ctx context.Context, groupName, repositoryName, resource, method, memberName, actor string, decision AuthorizationDecision) error {
	if err := r.Store.RecordAudit(ctx, repository.AuditRecord{
		GroupName: groupName, Repository: repositoryName, MemberName: memberName, Actor: actor, Outcome: repository.AuditAccessDenied, OccurredAt: time.Now().UTC(),
		Format: "oci", Resource: resource, Representation: resource, Operation: strings.ToLower(method), Status: http.StatusForbidden, CacheDisposition: "bypass",
		AuthorizationSource: decision.Source, AuthorizationReason: decision.Reason,
	}); err != nil {
		return fmt.Errorf("record OCI grant denial: %w", err)
	}
	r.Metrics.recordAudit(repositoryName, repository.AuditAccessDenied)
	r.Metrics.recordRepositoryAuthorizationDenied("oci", decision.Source, decision.Reason)
	return nil
}

func (r Resolver) RecordOCIRequestFailure() {
	r.Metrics.failed.Add(1)
}

func (r Resolver) RecordOCIAnonymousDenied(ctx context.Context, groupName, repositoryName, resource, method string, status int) error {
	if err := r.Store.RecordAudit(ctx, repository.AuditRecord{
		GroupName: groupName, Repository: repositoryName, Actor: "anonymous", Outcome: repository.AuditAccessDenied, OccurredAt: time.Now().UTC(),
		Format: "oci", Resource: resource, Representation: resource, Operation: strings.ToLower(method), Status: status, CacheDisposition: "bypass",
	}); err != nil {
		return fmt.Errorf("record OCI anonymous denial: %w", err)
	}
	r.Metrics.recordAudit(repositoryName, repository.AuditAccessDenied)
	r.Metrics.failed.Add(1)
	return nil
}

func (r Resolver) resolve(ctx context.Context, groupName, repositoryName, actor string, eligible func(repository.Member) bool) (repository.Member, error) {
	resolved := false
	defer func() {
		if !resolved {
			r.Metrics.failed.Add(1)
		}
	}()
	group, err := r.loadActiveGroup(ctx, groupName, repositoryName, actor)
	if err != nil {
		return repository.Member{}, err
	}
	for _, member := range group.Members {
		if eligible(member) {
			if err := r.audit(ctx, groupName, repositoryName, member.Name, repository.AuditResolved, actor); err != nil {
				return repository.Member{}, err
			}
			r.Metrics.resolved.Add(1)
			resolved = true
			return member, nil
		}
	}
	if err := r.audit(ctx, groupName, repositoryName, "", repository.AuditNotFound, actor); err != nil {
		return repository.Member{}, err
	}
	return repository.Member{}, repository.ErrNotFound
}

func (r Resolver) loadActiveGroup(ctx context.Context, groupName, repositoryName, actor string) (repository.Group, error) {
	group, err := r.Store.GetGroup(ctx, groupName)
	if err != nil {
		outcome := repository.AuditStorageError
		if errors.Is(err, repository.ErrNotFound) {
			outcome = repository.AuditNotFound
		}
		if auditErr := r.audit(ctx, groupName, repositoryName, "", outcome, actor); auditErr != nil {
			return repository.Group{}, auditErr
		}
		return repository.Group{}, err
	}
	if !group.Enabled {
		if auditErr := r.audit(ctx, groupName, repositoryName, "", repository.AuditGroupDisabled, actor); auditErr != nil {
			return repository.Group{}, auditErr
		}
		return repository.Group{}, repository.ErrDisabled
	}
	return group, nil
}

func (r Resolver) audit(ctx context.Context, groupName, repositoryName, memberName string, outcome repository.AuditOutcome, actor string) error {
	if err := r.Store.RecordAudit(ctx, repository.AuditRecord{GroupName: groupName, Repository: repositoryName, MemberName: memberName, Outcome: outcome, Actor: actor, OccurredAt: time.Now().UTC()}); err != nil {
		return fmt.Errorf("record resolver audit: %w", err)
	}
	r.Metrics.recordAudit(repositoryName, outcome)
	return nil
}
