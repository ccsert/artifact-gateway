package helm

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	dist "github.com/ccsert/artifact-gateway/internal/helmdistribution"

	"github.com/ccsert/artifact-gateway/internal/repository"
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
		if a.Actor != job.Actor || a.Kind != job.ActorKind || a.Source.ID != job.SourceRepositoryID || a.Target.ID != job.TargetRepositoryID {
			return dist.ErrAccessDenied
		}
		principal, ok := m.auth.DurablePrincipal(a.Actor, authorization.AuthenticationKind(a.Kind), a.Role, a.Active, a.MustChangePassword)
		if !ok {
			return dist.ErrAccessDenied
		}
		grants := replicationGrants{sourceID: a.Source.ID, targetID: a.Target.ID, source: replicationGrantSet(a.Source), target: replicationGrantSet(a.Target)}
		access := authorization.RepositoryAuthorizer{Grants: grants, Legacy: m.auth}
		source := repository.HostedRepository{ID: a.Source.ID, Name: a.Source.Name, Format: repository.FormatHelm, Type: repository.RepositoryTypeHosted, State: repository.RepositoryActive}
		target := repository.HostedRepository{ID: a.Target.ID, Name: a.Target.Name, Format: repository.FormatHelm, Type: repository.RepositoryTypeHosted, State: repository.RepositoryActive}
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
		m.problem(w, 409, "helm_replication_conflict")
	default:
		m.failure(w, err)
	}
}
func (m *Module) previewReplication(w http.ResponseWriter, r *http.Request, p authorization.Principal) {
	if m.distribution == nil {
		m.problem(w, 503, "helm_replication_unavailable")
		return
	}
	const prefix = "/api/v2/previews/helm/replication-jobs"
	tail := strings.TrimPrefix(r.URL.Path, prefix)
	if tail == "" {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		var in dist.Plan
		key, valid := replicationRequestKey(r)
		if !valid || !decodeBody(w, r, &in) {
			m.replicationFailure(w, dist.ErrInvalid)
			return
		}
		job, err := dist.NewJob(in, p.Actor, string(p.AuthenticationKind), p.CredentialID, key)
		if err != nil {
			m.replicationFailure(w, err)
			return
		}
		job, _, err = m.distribution.CreateHelmReplication(r.Context(), job, m.replicationCheck(job))
		if err != nil {
			m.replicationFailure(w, err)
			return
		}
		w.Header().Set("Location", prefix+"/"+job.ID)
		writeJSON(w, r, 202, job)
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
	job, err := m.distribution.GetHelmReplication(r.Context(), parts[0])
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
		if !valid || !decodeBody(w, r, &in) {
			m.replicationFailure(w, dist.ErrInvalid)
			return
		}
		job, _, err = m.distribution.RetryHelmReplication(r.Context(), job.ID, in.ExpectedVersion, in.ItemIndexes, key, m.replicationCheck(job))
		if err != nil {
			m.replicationFailure(w, err)
			return
		}
		writeJSON(w, r, 202, job)
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
	writeJSON(w, r, 200, job)
}

func (m *Module) ReplicationWorker(owner string) (dist.Worker, error) {
	if m == nil || nilPort(m.distribution) || owner == "" {
		return dist.Worker{}, dist.ErrInvalid
	}
	return dist.Worker{Store: m.distribution, Transfer: m, Owner: owner}, nil
}
func replicationRequestKey(r *http.Request) (string, bool) {
	key := r.Header.Get("Idempotency-Key")
	return key, key != "" && len(key) <= 200 && !strings.ContainsAny(key, "\x00\r\n")
}
func (m *Module) ReplicateHelmItem(ctx context.Context, job dist.Job, item dist.Item) error {
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
	check := m.replicationCheck(job)
	if err = m.distribution.AuthorizeHelmReplicationItem(ctx, job.Lease, item.Index, check); err != nil {
		return err
	}
	source, err := m.store.GetHelmPublication(ctx, job.SourceRepositoryID, item.Name, item.Version)
	if err != nil || source.Digest != item.Digest || source.Size != item.Size || source.ProvenanceDigest != item.ProvenanceDigest || source.ProvenanceSize != item.ProvenanceSize || source.Governance.Version != item.SourceGovernanceVersion || source.Governance.Tombstoned || source.Governance.Quarantined {
		return dist.ErrSourceChanged
	}
	chart, prov, err := m.verify(ctx, publicationSession(source))
	if err != nil {
		return err
	}
	defer func() {
		_ = chart.Close()
		if prov != nil {
			_ = prov.Close()
		}
	}()
	// The same publication lock is held by ordinary uploads and maintenance.
	// It covers reservation, both target writes, readback and fenced commit.
	ctx, release, err := m.store.LockHelmPublication(ctx, job.TargetRepositoryID, item.Name, item.Version)
	if err != nil {
		return err
	}
	defer release()
	if err = m.distribution.AuthorizeHelmReplicationItem(ctx, job.Lease, item.Index, check); err != nil {
		return err
	}
	session := publicationSession(source)
	session.ID = uuid.NewString()
	session.RepositoryID = job.TargetRepositoryID
	session.Actor = job.Actor
	session.ExpiresAt = time.Now().UTC().Add(time.Hour)
	session.ObjectKey, session.ProvenanceKey = "", ""
	session, _, err = m.store.ReserveHelmPublication(ctx, session)
	if err != nil {
		return replicationStorageError(err)
	}
	if _, err = m.distribution.BindHelmReplicationSession(ctx, job.Lease, item.Index, session.ID, check); err != nil {
		return err
	}
	if session.State != "committed" {
		if err = m.objects.PutVerifiedReader(ctx, session.ObjectKey, chart.Reader(), session.Size, session.Digest); err != nil {
			return err
		}
		if prov != nil {
			if err = m.objects.PutVerifiedReader(ctx, session.ProvenanceKey, prov.Reader(), session.ProvenanceSize, session.ProvenanceDigest); err != nil {
				return err
			}
		}
	}
	targetChart, targetProv, err := m.verify(ctx, session)
	if err != nil {
		return err
	}
	_ = targetChart.Close()
	if targetProv != nil {
		_ = targetProv.Close()
	}
	_, err = m.distribution.CompleteHelmReplicationItem(ctx, job.Lease, item.Index, dist.Commit{SessionID: session.ID}, check)
	return replicationStorageError(err)
}
func replicationStorageError(err error) error {
	if errors.Is(err, repository.ErrQuotaExceeded) || repository.IsQuotaExceeded(err) {
		return dist.ErrCapacity
	}
	if errors.Is(err, repository.ErrHelmPublicationConflict) || errors.Is(err, repository.ErrIdempotencyConflict) {
		return dist.ErrTargetBlocked
	}
	return err
}
