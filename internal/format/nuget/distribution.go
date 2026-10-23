package nuget

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	dist "github.com/ccsert/artifact-gateway/internal/nugetdistribution"
	nugetprotocol "github.com/ccsert/artifact-gateway/internal/protocol/nuget"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/uploadspool"
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
		source := repository.HostedRepository{ID: a.Source.ID, Name: a.Source.Name, Format: repository.FormatNuGet, Type: repository.RepositoryTypeHosted, State: repository.RepositoryActive}
		target := repository.HostedRepository{ID: a.Target.ID, Name: a.Target.Name, Format: repository.FormatNuGet, Type: repository.RepositoryTypeHosted, State: repository.RepositoryActive}
		for _, item := range job.Items {
			if !access.AuthorizeResource(ctx, principal, source, authorization.RepositoryRead, item.PackageID).Allowed || !access.AuthorizeResource(ctx, principal, target, authorization.RepositoryWrite, item.PackageID).Allowed {
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
		m.problem(w, 409, "nuget_replication_conflict")
	default:
		m.failure(w, err)
	}
}
func (m *Module) previewReplication(w http.ResponseWriter, r *http.Request, p authorization.Principal) {
	if m.distribution == nil {
		m.problem(w, 503, "nuget_replication_unavailable")
		return
	}
	const prefix = "/api/v2/previews/nuget/replication-jobs"
	tail := strings.TrimPrefix(r.URL.Path, prefix)
	if tail == "" {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		var in dist.Plan
		key, valid := requestKey(r)
		if !valid || !decodeBody(w, r, &in) {
			m.replicationFailure(w, dist.ErrInvalid)
			return
		}
		for i := range in.Items {
			var err error
			in.Items[i].PackageID, err = nugetprotocol.NormalizeID(in.Items[i].PackageID)
			if err != nil {
				m.replicationFailure(w, dist.ErrInvalid)
				return
			}
			in.Items[i].Version, err = nugetprotocol.NormalizeVersion(in.Items[i].Version)
			in.Items[i].Version = strings.ToLower(in.Items[i].Version)
			if err != nil {
				m.replicationFailure(w, dist.ErrInvalid)
				return
			}
		}
		job, err := dist.NewJob(in, p.Actor, string(p.AuthenticationKind), p.CredentialID, key)
		if err != nil {
			m.replicationFailure(w, err)
			return
		}
		job, _, err = m.distribution.CreateNuGetReplication(r.Context(), job, m.replicationCheck(job))
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
	job, err := m.distribution.GetNuGetReplication(r.Context(), parts[0])
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
		key, valid := requestKey(r)
		if !valid || !decodeBody(w, r, &in) {
			m.replicationFailure(w, dist.ErrInvalid)
			return
		}
		job, _, err = m.distribution.RetryNuGetReplication(r.Context(), job.ID, in.ExpectedVersion, in.ItemIndexes, key, m.replicationCheck(job))
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
		if !m.access.AuthorizeResource(r.Context(), p, source, authorization.RepositoryRead, item.PackageID).Allowed || !m.access.AuthorizeResource(r.Context(), p, target, authorization.RepositoryRead, item.PackageID).Allowed {
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
func (m *Module) replicationSpool(ctx context.Context, objectKey string, expected dist.Identity) (*uploadspool.Spool, packageMetadata, []byte, error) {
	reader, size, err := m.objects.Open(ctx, objectKey)
	if err != nil {
		return nil, packageMetadata{}, nil, err
	}
	if size != expected.Size {
		_ = reader.Close()
		return nil, packageMetadata{}, nil, dist.ErrConflict
	}
	spool, err := uploadspool.Read(reader, m.maxBytes)
	closeErr := reader.Close()
	if err != nil {
		return nil, packageMetadata{}, nil, err
	}
	if closeErr != nil {
		_ = spool.Close()
		return nil, packageMetadata{}, nil, closeErr
	}
	fail := func(err error) (*uploadspool.Spool, packageMetadata, []byte, error) {
		_ = spool.Close()
		return nil, packageMetadata{}, nil, err
	}
	if spool.Size() != expected.Size || spool.Digest() != expected.Digest {
		return fail(dist.ErrConflict)
	}
	meta, nuspec, err := inspectSpool(spool)
	if err != nil || !strings.EqualFold(meta.ID, expected.PackageID) || meta.Version != expected.Version {
		return fail(dist.ErrConflict)
	}
	return spool, meta, nuspec, nil
}
func replicationMetadataMatches(meta packageMetadata, nuspec []byte, stored []byte, original []byte) bool {
	var listing struct {
		Listed *bool `json:"listed"`
	}
	if json.Unmarshal(stored, &listing) != nil {
		return false
	}
	meta.Listed = listing.Listed
	encoded, err := json.Marshal(meta)
	return err == nil && bytes.Equal(encoded, stored) && bytes.Equal(nuspec, original)
}
func (m *Module) ReplicateNuGetItem(ctx context.Context, job dist.Job, item dist.Item) error {
	// The exported transfer seam cannot turn a caller-mutated job snapshot
	// into a narrower authorization check or a different source coordinate.
	plan := dist.Plan{SourceRepositoryID: job.SourceRepositoryID, TargetRepositoryID: job.TargetRepositoryID}
	for i, v := range job.Items {
		if v.Index != i {
			return dist.ErrInvalid
		}
		plan.Items = append(plan.Items, v.Identity)
	}
	_, digest, planErr := dist.CanonicalPlan(plan)
	if planErr != nil || digest != job.PlanDigest || item.Index < 0 || item.Index >= len(job.Items) || job.Items[item.Index].Identity != item.Identity {
		return dist.ErrInvalid
	}
	check := m.replicationCheck(job)
	if err := m.distribution.AuthorizeNuGetReplicationItem(ctx, job.Lease, item.Index, check); err != nil {
		return err
	}
	source, err := m.store.GetNuGetPublication(ctx, job.SourceRepositoryID, item.PackageID, item.Version)
	if err != nil {
		return dist.ErrSourceChanged
	}
	if source.Governance.Version != item.SourceGovernanceVersion || source.Governance.Tombstoned || source.Governance.Quarantined || source.Digest != item.Digest || source.Size != item.Size {
		return dist.ErrSourceChanged
	}
	spool, meta, nuspec, err := m.replicationSpool(ctx, source.ObjectKey, item.Identity)
	if err != nil {
		return err
	}
	defer func() { _ = spool.Close() }()
	if !replicationMetadataMatches(meta, nuspec, source.MetadataJSON, source.Nuspec) {
		return dist.ErrConflict
	}
	target, err := m.repository(ctx, job.TargetRepositoryID, true)
	if err != nil {
		return err
	}
	if target.Type != repository.RepositoryTypeHosted {
		return dist.ErrTargetBlocked
	}
	session, err := m.reserve(ctx, target, job.Actor, "replication:"+job.ID+":"+strconv.Itoa(item.Index)+":"+job.Lease.Token, publicationRequest{PackageID: item.PackageID, Version: item.Version, Digest: item.Digest, Size: item.Size})
	if err != nil {
		if errors.Is(err, repository.ErrNuGetPublicationConflict) || errors.Is(err, repository.ErrIdempotencyConflict) {
			return dist.ErrTargetBlocked
		}
		if errors.Is(err, repository.ErrQuotaExceeded) || repository.IsQuotaExceeded(err) {
			return dist.ErrCapacity
		}
		return err
	}
	if _, err = m.distribution.BindNuGetReplicationSession(ctx, job.Lease, item.Index, session.ID, check); err != nil {
		return err
	}
	if session.State != "committed" {
		session, err = m.stageWithListing(ctx, session, spool, &source.Governance.Listed)
		if err != nil {
			return err
		}
	}
	// Re-open the actual target-owned object. A metadata-only copy or an object
	// store that acknowledged a truncated/corrupt write cannot reach visibility.
	targetSpool, targetMeta, targetNuspec, err := m.replicationSpool(ctx, session.ObjectKey, item.Identity)
	if err != nil {
		return err
	}
	defer func() { _ = targetSpool.Close() }()
	if !replicationMetadataMatches(targetMeta, targetNuspec, session.MetadataJSON, session.Nuspec) {
		return dist.ErrConflict
	}
	_, err = m.distribution.CompleteNuGetReplicationItem(ctx, job.Lease, item.Index, dist.Commit{SessionID: session.ID}, check)
	if errors.Is(err, repository.ErrQuotaExceeded) || repository.IsQuotaExceeded(err) {
		return dist.ErrCapacity
	}
	return err
}
