package repository

import "strings"

// ArtifactSearchMatch selects literal coordinate matching independently of
// coordinate/digest identity. The empty value retains the caller's legacy mode.
type ArtifactSearchMatch string

const (
	ArtifactSearchPrefix   ArtifactSearchMatch = "prefix"
	ArtifactSearchContains ArtifactSearchMatch = "contains"
)

func searchMatch(matches []ArtifactSearchMatch, legacy ArtifactSearchMatch) ArtifactSearchMatch {
	if len(matches) > 0 && matches[0] != "" {
		return matches[0]
	}
	return legacy
}

func searchTextMatches(value, keyword string, matches []ArtifactSearchMatch) bool {
	if searchMatch(matches, ArtifactSearchPrefix) == ArtifactSearchContains {
		return strings.Contains(strings.ToLower(value), strings.ToLower(keyword))
	}
	return strings.HasPrefix(value, keyword)
}

// Columns and parameter placeholders are fixed by the store, never user input.
func searchTextSQL(column, parameter string, matches []ArtifactSearchMatch) string {
	operator := " LIKE "
	if searchMatch(matches, ArtifactSearchPrefix) == ArtifactSearchContains {
		operator = " ILIKE "
	}
	return column + operator + parameter + ` ESCAPE '\'`
}

func searchTextPattern(keyword string, matches []ArtifactSearchMatch) string {
	pattern := escapeLikePrefix(keyword) + "%"
	if searchMatch(matches, ArtifactSearchPrefix) == ArtifactSearchContains {
		pattern = "%" + pattern
	}
	return pattern
}
