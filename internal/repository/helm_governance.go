package repository

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Masterminds/semver/v3"
)

// HelmGovernance is an overlay over one retained, immutable publication closure.
// Hiding a chart never releases its byte references, quota or source authority.
type HelmGovernance struct {
	Version     string
	Tombstoned  bool
	Quarantined bool
	UpdatedAt   time.Time
	UpdatedBy   string
	Reason      string
}

type HelmGovernanceChange struct {
	RepositoryID, Name, Version     string
	ExpectedDigest, ExpectedVersion string
	Action, Actor, Reason           string
}

// HelmGovernanceStore is explicitly composed separately from publication and
// GatewayStore ports. Mutations and their audit record commit atomically.
type HelmGovernanceStore interface {
	TransitionHelmGovernance(context.Context, HelmGovernanceChange) (HelmPublication, error)
	GetRepositoryQuarantineReadPolicy(context.Context, string) (RepositoryQuarantineReadPolicy, error)
	ReplaceHelmQuarantineReadPolicy(context.Context, string, RepositoryQuarantineReadPolicy, string, string) (RepositoryQuarantineReadPolicy, error)
}

func validHelmGovernanceChange(in HelmGovernanceChange) bool {
	_, versionErr := semver.StrictNewVersion(in.Version)
	if in.RepositoryID == "" || !helmName.MatchString(in.Name) || versionErr != nil || len(in.Version) > 256 ||
		!validSHA256Digest(in.ExpectedDigest) || !sourceVersionValid(in.ExpectedVersion) || !validHelmGovernanceActor(in.Actor) ||
		strings.TrimSpace(in.Reason) == "" || utf8.RuneCountInString(in.Reason) > 2048 || strings.ContainsRune(in.Reason, '\x00') {
		return false
	}
	switch in.Action {
	case "delete", "restore", "quarantine", "release":
		return true
	}
	return false
}

func validHelmGovernanceActor(actor string) bool {
	return actor != "" && len(actor) <= 512 && !strings.ContainsAny(actor, "\x00\r\n")
}

func transitionHelmGovernance(p HelmPublication, in HelmGovernanceChange) (HelmPublication, bool, error) {
	if p.Digest != in.ExpectedDigest || p.Governance.Version != in.ExpectedVersion {
		return p, false, ErrVersionConflict
	}
	g := p.Governance
	switch in.Action {
	case "delete", "restore":
		g.Tombstoned = in.Action == "delete"
	case "quarantine", "release":
		g.Quarantined = in.Action == "quarantine"
	}
	if g == p.Governance {
		return p, false, nil
	}
	g.Version = nextHostedGroupVersion(g.Version)
	g.UpdatedAt, g.UpdatedBy, g.Reason = time.Now().UTC(), in.Actor, in.Reason
	p.Governance = g
	return p, true, nil
}

func helmGovernanceAudit(p HelmPublication, in HelmGovernanceChange, name string) AuditRecord {
	return AuditRecord{Repository: name, GroupName: name, Actor: in.Actor, Format: string(FormatHelm), Resource: p.Name + "@" + p.Version,
		Operation: "helm.governance." + in.Action, Outcome: AuditResolved, Status: 200, OccurredAt: p.Governance.UpdatedAt,
		Evidence: map[string]string{"digest": p.Digest, "provenanceDigest": p.ProvenanceDigest, "governanceVersion": p.Governance.Version, "reason": in.Reason}}
}

func helmReadPolicyAudit(repo HostedRepository, policy RepositoryQuarantineReadPolicy, actor string) AuditRecord {
	return AuditRecord{Repository: repo.Name, GroupName: repo.Name, Actor: actor, Format: string(FormatHelm), Resource: repo.ID,
		Operation: "helm.preview.quarantine_read_policy.configure", Outcome: AuditResolved, Status: 200, OccurredAt: time.Now().UTC(),
		Evidence: map[string]string{"version": policy.Version, "enabled": boolEvidence(policy.Enabled)}}
}

func boolEvidence(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
