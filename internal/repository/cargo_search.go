package repository

import (
	"sort"

	"github.com/artifact-gateway/artifact-gateway/internal/protocol/cargo"
	"golang.org/x/mod/semver"
)

func addCargoCrateVersion(summary CargoCrateSummary, version, description string) CargoCrateSummary {
	summary.Versions++
	if summary.MaxVersion == "" || semver.Compare("v"+version, "v"+summary.MaxVersion) > 0 {
		summary.MaxVersion = version
		summary.Description = description
	}
	return summary
}

func cargoSearchAfterKey(after string) (string, error) {
	if after == "" {
		return "", nil
	}
	identity, err := cargo.NormalizeIdentity(after, "0.0.0")
	if err != nil {
		return "", ErrInvalidCargoIdentity
	}
	return identity.CollisionKey, nil
}

func sortedCargoCrateSummaries(byName map[string]CargoCrateSummary, limit int, after string) ([]CargoCrateSummary, int) {
	keys := make([]string, 0, len(byName))
	for key := range byName {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	items := make([]CargoCrateSummary, 0, min(len(keys), limit))
	for _, key := range keys {
		if key <= after {
			continue
		}
		items = append(items, byName[key])
		if len(items) == limit {
			break
		}
	}
	return items, len(byName)
}
