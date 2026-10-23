package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"

	dist "github.com/ccsert/artifact-gateway/internal/nugetdistribution"
)

func nuGetReplicationSource(p NuGetPublication, item dist.Item) error {
	if p.SourceVersion != "" || p.PackageID != item.PackageID || p.Version != item.Version || p.Digest != item.Digest || p.Size != item.Size || p.Governance.Version != item.SourceGovernanceVersion || p.Governance.Tombstoned || p.Governance.Quarantined {
		return dist.ErrSourceChanged
	}
	return nil
}
func nuGetReplicationMetadata(a, b []byte) bool {
	var left, right map[string]json.RawMessage
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	delete(left, "listed")
	delete(right, "listed")
	x, _ := json.Marshal(left)
	y, _ := json.Marshal(right)
	return bytes.Equal(x, y)
}
func nuGetReplicationSession(source NuGetPublication, session NuGetSession, target string) error {
	var metadata struct {
		Listed *bool `json:"listed"`
	}
	if session.RepositoryID != target || session.SourceVersion != "" || session.PackageID != source.PackageID || session.Version != source.Version || session.Digest != source.Digest || session.Size != source.Size ||
		!nuGetReplicationMetadata(source.MetadataJSON, session.MetadataJSON) || !bytes.Equal(source.Nuspec, session.Nuspec) || json.Unmarshal(session.MetadataJSON, &metadata) != nil || metadata.Listed == nil || *metadata.Listed != source.Governance.Listed {
		return dist.ErrTargetBlocked
	}
	return nil
}
func nuGetReplicationExisting(source, target NuGetPublication) error {
	if target.Digest != source.Digest || target.Size != source.Size || target.SourceVersion != "" || target.Governance.Tombstoned || target.Governance.Quarantined || target.Governance.Listed != source.Governance.Listed ||
		!nuGetReplicationMetadata(source.MetadataJSON, target.MetadataJSON) || !bytes.Equal(source.Nuspec, target.Nuspec) {
		return dist.ErrTargetBlocked
	}
	return nil
}
func nuGetReplicationAudit(j dist.Job, name, operation string, status int, item *dist.Item) AuditRecord {
	evidence := map[string]string{"jobId": j.ID, "planDigest": j.PlanDigest, "sourceRepositoryId": j.SourceRepositoryID, "targetRepositoryId": j.TargetRepositoryID, "jobVersion": j.Version}
	resource := j.ID
	if item != nil {
		resource = item.PackageID + "@" + item.Version
		evidence["digest"] = item.Digest
		evidence["sourceGovernanceVersion"] = item.SourceGovernanceVersion
		evidence["result"] = item.Result
		evidence["code"] = item.Code
	}
	outcome := AuditResolved
	if status >= 400 {
		outcome = AuditStorageError
	}
	if status == 403 {
		outcome = AuditAccessDenied
	}
	return AuditRecord{Repository: name, GroupName: name, Actor: j.Actor, Format: string(FormatNuGet), Resource: resource, Operation: "nuget.replication." + operation, Outcome: outcome, Status: status, OccurredAt: time.Now().UTC(), Evidence: evidence}
}
func nuGetReplicationGrantSet(r HostedRepository, set RepositoryGrantSet) dist.RepositoryAuthority {
	out := dist.RepositoryAuthority{ID: r.ID, Name: r.Name, Version: set.Version}
	if out.Version == "" {
		out.Version = "1"
	}
	for _, v := range set.Grants {
		out.Grants = append(out.Grants, dist.Grant{Principal: v.Principal, Scopes: append([]string(nil), v.Scopes...), ResourcePrefix: v.ResourcePrefix})
	}
	return out
}
func nuGetReplicationKeyRole(roles []string) string {
	result := ""
	for _, v := range roles {
		if v == "admin" {
			return "admin"
		}
		if v == "member" {
			result = "member"
		}
	}
	return result
}
func nuGetReplicationCheck(ctx context.Context, check dist.Check, a dist.Authority) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if check == nil || !a.ValidUntil.IsZero() && !a.ValidUntil.After(time.Now()) {
		return dist.ErrAccessDenied
	}
	return check(ctx, a)
}
func nuGetReplicationRetry(j dist.Job, expected string, indexes []int) (dist.Job, error) {
	if j.Version != expected || j.State != "failed" {
		return j, dist.ErrConflict
	}
	for _, index := range indexes {
		if index < 0 || index >= len(j.Items) || j.Items[index].State != "failed" {
			return j, dist.ErrConflict
		}
	}
	j = dist.Clone(j)
	for _, index := range indexes {
		j.Items[index].State = "pending"
		j.Items[index].Code = ""
		j.Items[index].Result = ""
		j.Items[index].Listed = nil
		j.Items[index].CompletedAt = nil
	}
	j.State = "queued"
	j.Version = dist.NextVersion(j.Version)
	j.UpdatedAt = time.Now().UTC()
	j.Lease.Owner = ""
	j.Lease.Token = ""
	j.Lease.Until = time.Time{}
	return j, nil
}
func nuGetReplicationLease(j dist.Job, lease dist.Lease) bool {
	return j.State == "running" && lease.JobID == j.ID && j.Lease.Owner == lease.Owner && j.Lease.Token == lease.Token && j.Lease.Fence == lease.Fence && j.Lease.Until.After(time.Now())
}
func nuGetReplicationActorValid(j dist.Job) bool {
	switch j.ActorKind {
	case "static_admin", "static_resolver":
		return !strings.Contains(j.Actor, "\x00")
	case "local_session", "oidc":
		return strings.HasPrefix(j.Actor, "user:")
	case "api_key":
		return strings.HasPrefix(j.Actor, "api-key:")
	case "service_account_credential":
		return strings.HasPrefix(j.Actor, "service-account:") && j.CredentialID != ""
	}
	return false
}
