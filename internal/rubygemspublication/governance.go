package rubygemspublication

import (
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ccsert/artifact-gateway/internal/protocol/identity"
	protocol "github.com/ccsert/artifact-gateway/internal/protocol/rubygems"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

type Governance struct {
	Version           string
	Yanked            bool
	UpdatedAt         time.Time
	UpdatedBy, Reason string
}

type GovernanceChange struct {
	RepositoryID, Name, Version, Platform                  string
	ExpectedDigest, ExpectedVersion, Action, Actor, Reason string
}

func ValidGovernanceChange(c GovernanceChange) bool {
	v, err := strconv.ParseInt(c.ExpectedVersion, 10, 64)
	return err == nil && v > 0 && v < math.MaxInt64 && strconv.FormatInt(v, 10) == c.ExpectedVersion &&
		protocol.ValidName(c.Name) && protocol.ValidVersion(c.Version) && protocol.ValidPlatform(c.Platform) &&
		identity.IsSHA256Digest(c.ExpectedDigest) && (c.Action == "yank" || c.Action == "restore") &&
		c.Actor != "" && len(c.Actor) <= 256 && strings.TrimSpace(c.Reason) != "" &&
		utf8.ValidString(c.Reason) && utf8.RuneCountInString(c.Reason) <= 2048 && !strings.ContainsRune(c.Reason, '\x00')
}

func ApplyGovernance(p Publication, c GovernanceChange) (Governance, bool, error) {
	if !ValidGovernanceChange(c) {
		return Governance{}, false, ErrInvalid
	}
	if p.Digest != c.ExpectedDigest || p.Governance.Version != c.ExpectedVersion {
		return Governance{}, false, repository.ErrVersionConflict
	}
	yanked := c.Action == "yank"
	if p.Governance.Yanked == yanked {
		return p.Governance, false, nil
	}
	v, _ := strconv.ParseInt(c.ExpectedVersion, 10, 64)
	return Governance{Version: strconv.FormatInt(v+1, 10), Yanked: yanked, UpdatedAt: time.Now().UTC(), UpdatedBy: c.Actor, Reason: c.Reason}, true, nil
}

func GovernanceAudit(p Publication, repo, action string) repository.AuditRecord {
	g := p.Governance
	return repository.AuditRecord{Actor: g.UpdatedBy, Repository: repo, GroupName: repo, Format: "rubygems", Resource: p.FullName(), Operation: "rubygems." + action, Outcome: repository.AuditResolved, Status: 200, OccurredAt: g.UpdatedAt, Evidence: map[string]string{"digest": p.Digest, "size": strconv.FormatInt(p.Size, 10), "name": p.Name, "version": p.Version, "platform": p.Platform, "sessionId": p.SessionID, "governanceVersion": g.Version, "yanked": strconv.FormatBool(g.Yanked), "reason": g.Reason}}
}
