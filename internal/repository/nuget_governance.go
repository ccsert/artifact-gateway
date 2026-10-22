package repository

import (
	"strings"
	"time"
	"unicode/utf8"
)

// NuGetGovernance is a preview-local overlay over retained immutable bytes.
// Unlisting affects discovery; tombstones and the separate quarantine read
// policy affect resolution. None of these operations reclaims storage.
type NuGetGovernance struct {
	Version     string
	Listed      bool
	Tombstoned  bool
	Quarantined bool
	UpdatedAt   time.Time
	UpdatedBy   string
	Reason      string
}

type NuGetGovernanceChange struct {
	RepositoryID, PackageID, Version string
	ExpectedDigest, ExpectedVersion  string
	Action, Actor, Reason            string
}

func validNuGetGovernanceChange(in NuGetGovernanceChange) bool {
	if in.RepositoryID == "" || !nuGetID.MatchString(in.PackageID) || len(in.PackageID) > 100 || !nuGetVersion.MatchString(in.Version) || len(in.Version) > 256 ||
		!validSHA256Digest(in.ExpectedDigest) || !sourceVersionValid(in.ExpectedVersion) || in.Actor == "" || len(in.Actor) > 512 || strings.ContainsAny(in.Actor, "\x00\r\n") ||
		strings.TrimSpace(in.Reason) == "" || utf8.RuneCountInString(in.Reason) > 2048 || strings.ContainsRune(in.Reason, '\x00') {
		return false
	}
	switch in.Action {
	case "unlist", "relist", "delete", "restore", "quarantine", "release":
		return true
	}
	return false
}

func transitionNuGetGovernance(p NuGetPublication, in NuGetGovernanceChange) (NuGetPublication, bool, error) {
	if p.Digest != in.ExpectedDigest || p.Governance.Version != in.ExpectedVersion {
		return p, false, ErrVersionConflict
	}
	g := p.Governance
	switch in.Action {
	case "unlist", "relist":
		if g.Tombstoned {
			return p, false, ErrNotFound
		}
		g.Listed = in.Action == "relist"
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

func nuGetGovernanceAudit(p NuGetPublication, in NuGetGovernanceChange, name string) AuditRecord {
	return AuditRecord{Repository: name, GroupName: name, Actor: in.Actor, Format: string(FormatNuGet), Resource: p.PackageID + "@" + p.Version,
		Operation: "nuget.governance." + in.Action, Outcome: AuditResolved, Status: 200, OccurredAt: p.Governance.UpdatedAt,
		Evidence: map[string]string{"digest": p.Digest, "governanceVersion": p.Governance.Version, "reason": in.Reason}}
}
