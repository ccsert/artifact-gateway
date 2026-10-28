package rubygems

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	dist "github.com/ccsert/artifact-gateway/internal/rubygemsdistribution"

	protocol "github.com/ccsert/artifact-gateway/internal/protocol/rubygems"
	"github.com/ccsert/artifact-gateway/internal/repository"
	domain "github.com/ccsert/artifact-gateway/internal/rubygemspublication"
	"github.com/google/uuid"
)

// The read-only adapter feeds transaction-locked grant snapshots through the
// same authorizer as HTTP. No grant, role or administrator decision is cached
// in the durable job and no second database connection is used by the check.
type replicationGrants struct {
	source, target     repository.RepositoryGrantSet
	sourceID, targetID string
}

func (g replicationGrants) GetRepositoryGrants(_ context.Context, id string) (repository.RepositoryGrantSet, error) {
	if id == g.sourceID {
		return g.source, nil
	}
	if id == g.targetID {
		return g.target, nil
	}
	return repository.RepositoryGrantSet{}, repository.ErrNotFound
}
func (replicationGrants) ReplaceRepositoryGrants(context.Context, string, []repository.RepositoryGrant, string) (repository.RepositoryGrantSet, error) {
	return repository.RepositoryGrantSet{}, dist.ErrAccessDenied
}
func (replicationGrants) UpsertRepositoryGrant(context.Context, string, repository.RepositoryGrant) (repository.RepositoryGrantSet, error) {
	return repository.RepositoryGrantSet{}, dist.ErrAccessDenied
}
func (replicationGrants) DeleteRepositoryGrant(context.Context, string, string, string) (repository.RepositoryGrantSet, error) {
	return repository.RepositoryGrantSet{}, dist.ErrAccessDenied
}
func replicationGrantSet(a dist.RepositoryAuthority) repository.RepositoryGrantSet {
	out := repository.RepositoryGrantSet{Version: a.Version, Grants: []repository.RepositoryGrant{}}
	for _, g := range a.Grants {
		out.Grants = append(out.Grants, repository.RepositoryGrant{Principal: g.Principal, Scopes: g.Scopes, ResourcePrefix: g.ResourcePrefix})
	}
	return out
}
func (m *Module) replicationCheck(job dist.Job) dist.Check {
	return func(ctx context.Context, a dist.Authority) error {
		if !dist.SameSubject(a.Subject, job.Subject()) || a.Source.ID != job.SourceRepositoryID || a.Target.ID != job.TargetRepositoryID {
			return dist.ErrAccessDenied
		}
		principal, ok := m.auth.DurablePrincipal(a.Subject.Actor, authorization.AuthenticationKind(a.Subject.Kind), a.Role, a.Active, a.MustChangePassword)
		if !ok {
			return dist.ErrAccessDenied
		}
		grants := replicationGrants{sourceID: a.Source.ID, targetID: a.Target.ID, source: replicationGrantSet(a.Source), target: replicationGrantSet(a.Target)}
		access := authorization.RepositoryAuthorizer{Grants: grants, Legacy: m.auth}
		source := repository.HostedRepository{ID: a.Source.ID, Name: a.Source.Name, Format: repository.FormatRubyGems, Type: repository.RepositoryTypeHosted, State: repository.RepositoryActive}
		target := repository.HostedRepository{ID: a.Target.ID, Name: a.Target.Name, Format: repository.FormatRubyGems, Type: repository.RepositoryTypeHosted, State: repository.RepositoryActive}
		for _, item := range job.Items {
			if !access.AuthorizeResource(ctx, principal, source, authorization.RepositoryRead, item.Name).Allowed || !access.AuthorizeResource(ctx, principal, target, authorization.RepositoryWrite, item.Name).Allowed {
				return dist.ErrAccessDenied
			}
		}
		return nil
	}
}
func (m *Module) replicationFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, dist.ErrInvalid):
		m.problem(w, 400, "invalid_replication_plan")
	case errors.Is(err, dist.ErrAccessDenied):
		m.problem(w, 403, "access_denied")
	case errors.Is(err, dist.ErrNotFound):
		m.problem(w, 404, "not_found")
	case errors.Is(err, dist.ErrCapacity):
		m.problem(w, 409, "quota_exceeded")
	case errors.Is(err, dist.ErrConflict), errors.Is(err, dist.ErrSourceChanged), errors.Is(err, dist.ErrTargetBlocked), errors.Is(err, dist.ErrLeaseLost):
		m.problem(w, 409, "rubygems_replication_conflict")
	default:
		m.failure(w, err)
	}
}
func (m *Module) previewReplication(w http.ResponseWriter, r *http.Request, p authorization.Principal) {
	if m.distribution == nil {
		m.problem(w, 503, "rubygems_replication_unavailable")
		return
	}
	const prefix = "/api/v2/previews/rubygems/replication-jobs"
	tail := strings.TrimPrefix(r.URL.Path, prefix)
	if tail == "" {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		var in dist.Plan
		key, valid := replicationRequestKey(r)
		if !valid || !decode(w, r, &in) {
			m.replicationFailure(w, dist.ErrInvalid)
			return
		}
		identity, identityErr := m.governanceIdentity(r, p)
		if identityErr != nil {
			m.failure(w, identityErr)
			return
		}
		subject := dist.Subject{Actor: identity.Actor, Kind: identity.Kind, CredentialID: identity.CredentialID, UserID: identity.UserID, SessionID: identity.SessionID, SessionVersion: identity.SessionVersion, ValidUntil: identity.ValidUntil}
		job, err := dist.NewJob(in, subject, key)
		if err != nil {
			m.replicationFailure(w, err)
			return
		}
		job, _, err = m.distribution.CreateRubyGemsReplication(r.Context(), job, m.replicationCheck(job))
		if err != nil {
			m.replicationFailure(w, err)
			return
		}
		w.Header().Set("Location", prefix+"/"+job.ID)
		jsonResponse(w, 202, job)
		return
	}
	if !strings.HasPrefix(tail, "/") {
		m.problem(w, 404, "not_found")
		return
	}
	parts := strings.Split(strings.TrimPrefix(tail, "/"), "/")
	if _, err := uuid.Parse(parts[0]); err != nil {
		m.problem(w, 404, "not_found")
		return
	}
	if len(parts) > 2 || len(parts) == 2 && parts[1] != "retry" {
		m.problem(w, 404, "not_found")
		return
	}
	job, err := m.distribution.GetRubyGemsReplication(r.Context(), parts[0])
	if err != nil {
		m.replicationFailure(w, err)
		return
	}
	if len(parts) == 2 {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		if p.Actor != job.Actor || string(p.AuthenticationKind) != job.ActorKind || p.CredentialID != job.CredentialID {
			m.problem(w, 403, "access_denied")
			return
		}
		var in struct {
			ExpectedVersion string `json:"expectedVersion"`
			ItemIndexes     []int  `json:"itemIndexes"`
		}
		key, valid := replicationRequestKey(r)
		if !valid || !decode(w, r, &in) {
			m.replicationFailure(w, dist.ErrInvalid)
			return
		}
		job, _, err = m.distribution.RetryRubyGemsReplication(r.Context(), job.ID, in.ExpectedVersion, in.ItemIndexes, key, m.replicationCheck(job))
		if err != nil {
			m.replicationFailure(w, err)
			return
		}
		jsonResponse(w, 202, job)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	source, err := m.repository(r.Context(), job.SourceRepositoryID, true)
	if err != nil {
		m.failure(w, err)
		return
	}
	target, err := m.repository(r.Context(), job.TargetRepositoryID, true)
	if err != nil {
		m.failure(w, err)
		return
	}
	for _, item := range job.Items {
		if !m.access.AuthorizeResource(r.Context(), p, source, authorization.RepositoryRead, item.Name).Allowed || !m.access.AuthorizeResource(r.Context(), p, target, authorization.RepositoryRead, item.Name).Allowed {
			m.problem(w, 403, "access_denied")
			return
		}
	}
	jsonResponse(w, 200, job)
}

func (m *Module) ReplicationWorker(owner string) (dist.Worker, error) {
	if m == nil || nilPort(m.distribution) || owner == "" {
		return dist.Worker{}, dist.ErrInvalid
	}
	return dist.Worker{Store: m.distribution, Transfer: m, Owner: owner}, nil
}
func replicationRequestKey(r *http.Request) (string, bool) {
	key := r.Header.Get("Idempotency-Key")
	return key, len(r.Header.Values("Idempotency-Key")) == 1 && key != "" && len(key) <= 200 && !strings.ContainsAny(key, "\x00\r\n")
}
func (m *Module) ReplicateRubyGemsItem(ctx context.Context, job dist.Job, item dist.Item) error {
	plan := dist.Plan{SourceRepositoryID: job.SourceRepositoryID, TargetRepositoryID: job.TargetRepositoryID}
	for i, v := range job.Items {
		if v.Index != i {
			return dist.ErrInvalid
		}
		plan.Items = append(plan.Items, v.Identity)
	}
	_, digest, err := dist.CanonicalPlan(plan)
	if err != nil || digest != job.PlanDigest || item.Index < 0 || item.Index >= len(job.Items) || job.Items[item.Index].Identity != item.Identity {
		return dist.ErrInvalid
	}
	// Bind the exported transfer request to the server-owned immutable plan.
	// Recomputing a caller-supplied digest alone would allow a narrowed grant check.
	if job.ID != job.Lease.JobID {
		return dist.ErrInvalid
	}
	persisted, err := m.distribution.GetRubyGemsReplication(ctx, job.ID)
	if err != nil {
		return err
	}
	if persisted.PlanDigest != job.PlanDigest || !dist.SameSubject(persisted.Subject(), job.Subject()) || persisted.SourceRepositoryID != job.SourceRepositoryID || persisted.TargetRepositoryID != job.TargetRepositoryID || len(persisted.Items) != len(job.Items) {
		return dist.ErrInvalid
	}
	for i, v := range persisted.Items {
		if v.Index != job.Items[i].Index || v.Identity != job.Items[i].Identity {
			return dist.ErrInvalid
		}
	}
	check := m.replicationCheck(job)
	if err = m.distribution.AuthorizeRubyGemsReplicationItem(ctx, job.Lease, item.Index, check); err != nil {
		return err
	}
	source, err := m.store.GetRetained(ctx, job.SourceRepositoryID, item.Name, item.Version, item.Platform)
	if err != nil || domain.ReplicationSource(source, item) != nil {
		return dist.ErrSourceChanged
	}
	bytes, err := m.openVerified(ctx, source.ObjectKey, source.Digest, source.Size)
	if err != nil {
		return err
	}
	defer func() { _ = bytes.Close() }()
	actual, err := protocol.Inspect(bytes.Reader())
	if err != nil || !reflect.DeepEqual(actual.MetadataJSON, source.MetadataJSON) || !reflect.DeepEqual(actual.MetadataYAML, source.MetadataYAML) {
		return errIntegrity
	}
	ctx, release, err := m.store.Lock(ctx, job.TargetRepositoryID, domain.Identity(item.Name, item.Version, item.Platform))
	if err != nil {
		return err
	}
	defer release()
	if err = m.distribution.AuthorizeRubyGemsReplicationItem(ctx, job.Lease, item.Index, check); err != nil {
		return err
	}
	session := domain.Session{ID: uuid.NewString(), RepositoryID: job.TargetRepositoryID, Name: source.Name, Version: source.Version, Platform: source.Platform, Digest: source.Digest, Size: source.Size, Actor: job.Actor, MetadataJSON: source.MetadataJSON, MetadataYAML: source.MetadataYAML, ExpiresAt: time.Now().UTC().Add(time.Hour)}
	session, _, err = m.store.Reserve(ctx, session)
	if err != nil {
		return replicationStorageError(err)
	}
	if _, err = m.distribution.BindRubyGemsReplicationSession(ctx, job.Lease, item.Index, session.ID, check); err != nil {
		return err
	}
	if session.State != "committed" {
		if err = bytes.Rewind(); err != nil {
			return err
		}
		if err = m.objects.PutVerifiedReader(ctx, session.ObjectKey, bytes.Reader(), session.Size, session.Digest); err != nil {
			return err
		}
	}
	if err = m.verifyRetained(ctx, domain.Visible(session)); err != nil {
		return err
	}
	_, err = m.distribution.CompleteRubyGemsReplicationItem(ctx, job.Lease, item.Index, dist.Commit{SessionID: session.ID}, check)
	return replicationStorageError(err)
}
func replicationStorageError(err error) error {
	if errors.Is(err, repository.ErrQuotaExceeded) || repository.IsQuotaExceeded(err) {
		return dist.ErrCapacity
	}
	if errors.Is(err, domain.ErrConflict) || errors.Is(err, repository.ErrIdempotencyConflict) {
		return dist.ErrTargetBlocked
	}
	return err
}

func (m *Module) previewPackageIdentity(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, name, version, platform string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		w.WriteHeader(405)
		return
	}
	if !protocol.ValidName(name) || !protocol.ValidVersion(version) || !protocol.ValidPlatform(platform) {
		m.problem(w, 404, "not_found")
		return
	}
	if _, ok := m.authorize(w, r, repo, authorization.RepositoryRead, name, false); !ok {
		return
	}
	ctx, release, err := m.store.Lock(r.Context(), repo.ID, domain.Identity(name, version, platform))
	if err != nil {
		m.failure(w, err)
		return
	}
	defer release()
	p, err := m.store.GetRetained(ctx, repo.ID, name, version, platform)
	if err != nil {
		m.failure(w, err)
		return
	}
	if p.Governance.Yanked || p.Governance.Quarantined {
		m.problem(w, 404, "not_found")
		return
	}
	if err = m.verifyRetained(ctx, p); err != nil {
		m.failure(w, err)
		return
	}
	if _, ok := m.authorize(w, r, repo, authorization.RepositoryRead, name, false); !ok {
		return
	}
	jsonResponse(w, 200, dist.Identity{Name: p.Name, Version: p.Version, Platform: p.Platform, Digest: p.Digest, Size: p.Size, SourceGovernanceVersion: p.Governance.Version})
}
