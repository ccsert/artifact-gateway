package quotaalert

import (
	"errors"
	"github.com/google/uuid"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"
)

func (e Event) Validate() error {
	invalid := errors.New("invalid quota event")
	for _, id := range []string{e.ID, e.RuleID, e.RepositoryID, e.EpisodeID} {
		v, err := uuid.Parse(id)
		if err != nil || v == uuid.Nil {
			return invalid
		}
	}
	if e.PreviousEventID != "" {
		v, err := uuid.Parse(e.PreviousEventID)
		if err != nil || v == uuid.Nil {
			return invalid
		}
	}
	version, err := strconv.ParseInt(e.RuleVersion, 10, 64)
	if err != nil || version < 1 || e.Sequence < 1 || e.Policy.Validate() != nil {
		return invalid
	}
	if !utf8.ValidString(e.RepositoryName) || len(e.RepositoryName) < 1 || len(e.RepositoryName) > 512 || utf8.RuneCountInString(e.RepositoryName) > 128 {
		return invalid
	}
	for _, r := range e.RepositoryName {
		if unicode.IsControl(r) {
			return invalid
		}
	}
	if e.UsedBytes < 0 || e.QuotaBytes <= 0 || e.EvidenceSince.IsZero() || e.SampleAt.IsZero() || e.OccurredAt.IsZero() || e.EvidenceSince.After(e.SampleAt) || e.SampleAt.After(e.OccurredAt) || e.OccurredAt.Sub(e.SampleAt) > time.Duration(e.Policy.MaxSampleAgeSeconds)*time.Second {
		return invalid
	}
	seconds := 0
	switch e.Scenario {
	case "warning":
		seconds = e.Policy.WarningForSeconds
		if ratioCompare(e.UsedBytes, e.QuotaBytes, e.Policy.WarningBasisPoints) < 0 {
			return invalid
		}
	case "critical":
		seconds = e.Policy.CriticalForSeconds
		if ratioCompare(e.UsedBytes, e.QuotaBytes, e.Policy.CriticalBasisPoints) < 0 {
			return invalid
		}
	case "resolved":
		seconds = e.Policy.RecoveryForSeconds
		if ratioCompare(e.UsedBytes, e.QuotaBytes, e.Policy.RecoveryBelowBasisPoints) >= 0 {
			return invalid
		}
	default:
		return invalid
	}
	if e.SampleAt.Sub(e.EvidenceSince) < time.Duration(seconds)*time.Second {
		return invalid
	}
	return nil
}
