package rubygemspublication

import (
	"bytes"
	"context"
	"strconv"
	"time"

	"github.com/ccsert/artifact-gateway/internal/repository"
	dist "github.com/ccsert/artifact-gateway/internal/rubygemsdistribution"
)

func ReplicationSource(p Publication, item dist.Item) error {
	if p.Name != item.Name || p.Version != item.Version || p.Platform != item.Platform || p.Digest != item.Digest || p.Size != item.Size || p.Governance.Version != item.SourceGovernanceVersion || p.Governance.Yanked || p.Governance.Quarantined {
		return dist.ErrSourceChanged
	}
	return nil
}
func ReplicationSession(source Publication, s Session, target string) error {
	if s.RepositoryID != target || s.Name != source.Name || s.Version != source.Version || s.Platform != source.Platform || s.Digest != source.Digest || s.Size != source.Size || s.Governance.Yanked || s.Governance.Quarantined || !bytes.Equal(s.MetadataJSON, source.MetadataJSON) || !bytes.Equal(s.MetadataYAML, source.MetadataYAML) {
		return dist.ErrTargetBlocked
	}
	return nil
}
func ReplicationAudit(j dist.Job, name, operation string, status int, item *dist.Item) repository.AuditRecord {
	evidence := map[string]string{"jobId": j.ID, "planDigest": j.PlanDigest, "sourceRepositoryId": j.SourceRepositoryID, "targetRepositoryId": j.TargetRepositoryID, "jobVersion": j.Version}
	resource := j.ID
	if item != nil {
		resource = item.Name + "@" + item.Version + "/" + item.Platform
		evidence["digest"], evidence["size"], evidence["platform"] = item.Digest, strconv.FormatInt(item.Size, 10), item.Platform
		evidence["sourceGovernanceVersion"], evidence["result"], evidence["code"] = item.SourceGovernanceVersion, item.Result, item.Code
	}
	outcome := repository.AuditResolved
	if status >= 400 {
		outcome = repository.AuditStorageError
	}
	if status == 403 {
		outcome = repository.AuditAccessDenied
	}
	return repository.AuditRecord{Actor: j.Actor, Repository: name, GroupName: name, Format: "rubygems", Resource: resource, Operation: "rubygems.replication." + operation, Outcome: outcome, Status: status, OccurredAt: time.Now().UTC(), Evidence: evidence}
}
func ReplicationIdentity(j dist.Job) repository.RubyGemsGovernanceIdentity {
	return repository.RubyGemsGovernanceIdentity{Actor: j.Actor, Kind: j.ActorKind, CredentialID: j.CredentialID, UserID: j.UserID, SessionID: j.SessionID, SessionVersion: j.SessionVersion, ValidUntil: j.ValidUntil}
}
func ReplicationAuthority(j dist.Job, source, target repository.RubyGemsGovernanceAuthority) dist.Authority {
	convert := func(a repository.RubyGemsGovernanceAuthority) dist.RepositoryAuthority {
		r := dist.RepositoryAuthority{ID: a.Repository.ID, Name: a.Repository.Name, Version: a.Grants.Version}
		for _, g := range a.Grants.Grants {
			r.Grants = append(r.Grants, dist.Grant{Principal: g.Principal, Scopes: append([]string(nil), g.Scopes...), ResourcePrefix: g.ResourcePrefix})
		}
		return r
	}
	return dist.Authority{Subject: j.Subject(), Role: source.Role, Active: source.Active && target.Active, MustChangePassword: source.MustChangePassword || target.MustChangePassword, ValidUntil: repository.RubyGemsEarlierAuthorityExpiry(source.ValidUntil, target.ValidUntil), Source: convert(source), Target: convert(target)}
}
func replicationMemoryLock(ctx context.Context, m *Memory) error {
	timer := time.NewTicker(time.Millisecond)
	defer timer.Stop()
	for !m.mu.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		return err
	}
	return nil
}
