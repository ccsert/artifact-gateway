package app

import (
	"context"
	"errors"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// artifactDistributionDigests returns every immutable file digest published by
// one management distribution request. Most formats have one admission anchor;
// a PyPI version is an aggregate of all its visible distribution files.
func (h generatedRepositoryAPIAdapter) artifactDistributionDigests(ctx context.Context, source repository.HostedRepository, coordinate, digest string) ([]string, error) {
	digests := []string{digest}
	if source.Format != repository.FormatPyPI {
		return digests, nil
	}
	project, version, valid := parsePyPIVersionCoordinate(coordinate)
	if !valid {
		return digests, nil
	}
	files, err := h.sessions.store.ListPyPIProjectFiles(ctx, source.ID, project)
	if errors.Is(err, repository.ErrNotFound) {
		return digests, nil
	}
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		if file.Version == version {
			digests = append(digests, file.Digest)
		}
	}
	return digests, nil
}
