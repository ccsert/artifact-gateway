package app

import (
	"context"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func artifactSearchMatch[T ~string](value *T) (repository.ArtifactSearchMatch, bool) {
	if value == nil {
		return "", true
	}
	match := repository.ArtifactSearchMatch(*value)
	return match, match == repository.ArtifactSearchPrefix || match == repository.ArtifactSearchContains
}

func pypiSearchKeyword(value string, match repository.ArtifactSearchMatch) string {
	if match == repository.ArtifactSearchContains {
		return value
	}
	return normalizePyPIProject(value)
}

func validArtifactKeyword(format repository.Format, value string, match repository.ArtifactSearchMatch) bool {
	if match != repository.ArtifactSearchContains {
		return validArtifactSearchQuery(format, value)
	}
	limit := 255
	if format == repository.FormatRaw {
		limit = 4096
	}
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > limit {
		return false
	}
	for _, character := range value {
		if invalidRawSearchRune(character) {
			return false
		}
	}
	return repository.IsSupportedFormat(format)
}

// Bound even short/low-selectivity contains queries across all repositories.
// The incoming cancellation still stops both SQL and later repository probes.
func artifactSearchRequest(r *http.Request, match repository.ArtifactSearchMatch) (*http.Request, context.CancelFunc) {
	if match != repository.ArtifactSearchContains {
		return r, func() {}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	return r.WithContext(ctx), cancel
}
