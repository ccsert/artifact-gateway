package app

import (
	"context"
	"net/http"
	"time"

	"github.com/ccsert/artifact-gateway/internal/artifactquery"
	formatmodule "github.com/ccsert/artifact-gateway/internal/format"
	rawformat "github.com/ccsert/artifact-gateway/internal/format/raw"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

func artifactSearchMatch[T ~string](value *T) (repository.ArtifactSearchMatch, bool) {
	if value == nil {
		return "", true
	}
	match := repository.ArtifactSearchMatch(*value)
	return match, match == repository.ArtifactSearchPrefix || match == repository.ArtifactSearchContains
}

func pypiSearchKeyword(value string, match repository.ArtifactSearchMatch) string {
	if match != "" {
		return value
	}
	return normalizePyPIProject(value)
}

func validArtifactKeyword(format repository.Format, value string, match repository.ArtifactSearchMatch) bool {
	if capability, ok := formatmodule.Capability[formatmodule.Browse](rawFormatRegistry(rawformat.Options{}), format); ok {
		return capability.ValidKeyword(value, match)
	}
	if match != repository.ArtifactSearchContains {
		return validArtifactSearchQuery(format, value)
	}
	return repository.IsSupportedFormat(format) && artifactquery.ValidKeyword(value, 255)
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
