package app

import (
	"context"
	"net/http"

	"github.com/ccsert/artifact-gateway/internal/repository"
)

type artifactRepositoryCatalog interface {
	managementRepositoryReader
	ListHostedRepositories(context.Context, int, string) ([]repository.HostedRepository, string, error)
}

type artifactGroupReader interface {
	GetHostedGroup(context.Context, string) (repository.HostedGroup, error)
}

type artifactGrantReader interface {
	GetRepositoryGrants(context.Context, string) (repository.RepositoryGrantSet, error)
}

type artifactUsageReader interface {
	QueryArtifactUsage(context.Context, string, repository.ArtifactUsageQuery) (repository.ArtifactUsagePage, error)
}

// The native publishers retain transaction and object-stream ownership. The
// management domain can invoke these operations without seeing their stores.
type mavenManagementOperations interface {
	createWithIdempotencyKey(http.ResponseWriter, *http.Request, Principal, string, string)
	listArtifacts(http.ResponseWriter, *http.Request, string)
	getArtifact(http.ResponseWriter, *http.Request, string, string)
	deleteArtifact(http.ResponseWriter, *http.Request, string, string)
	getSession(http.ResponseWriter, *http.Request, string)
	upload(http.ResponseWriter, *http.Request, string, string)
	commit(http.ResponseWriter, *http.Request, string)
}

type artifactMavenReader interface {
	GetMavenPublishSession(context.Context, string) (repository.MavenPublishSession, error)
	SearchMavenArtifacts(context.Context, string, string, int, repository.MavenArtifactCursor, ...repository.ArtifactSearchMatch) ([]repository.MavenArtifact, error)
}

type artifactRawReader interface {
	ListRawAssets(context.Context, string, string, int, string, ...repository.ArtifactSearchMatch) ([]repository.RawAsset, error)
}

type artifactNPMReader interface {
	SearchNPMPackages(context.Context, string, string, int, string, ...repository.ArtifactSearchMatch) ([]repository.NPMPackageSummary, error)
}

type artifactPyPIReader interface {
	ListPyPIProjects(context.Context, string, string, int, string, ...repository.ArtifactSearchMatch) ([]repository.PyPIProjectSummary, error)
}

type artifactAPTReader interface {
	ListAPTAssets(context.Context, string, string, int, string, ...repository.ArtifactSearchMatch) ([]repository.APTAsset, error)
}

type artifactOCIReader interface {
	ListOCIManifests(context.Context, string, string, int, string) ([]repository.OCIManifest, error)
	SearchOCIManifestNames(context.Context, string, string, int, string, ...repository.ArtifactSearchMatch) ([]string, error)
}

type artifactConanStore interface {
	SearchConanReferences(context.Context, string, string, int, string, ...repository.ArtifactSearchMatch) ([]repository.ConanReference, error)
	SearchConanRecipeRevisions(context.Context, string, string, string, int, string) ([]repository.ConanRecipeRevision, error)
	ListConanPackageRevisions(context.Context, string, string, string, string) ([]repository.ConanPackageRevision, error)
	ListConanPackageIDs(context.Context, string, string, string) ([]string, error)
	TombstoneConanPackageRevision(context.Context, string, string, string, string, string) (repository.ConanPackageRevision, error)
}

type artifactCargoReader interface {
	SearchCargoCrates(context.Context, string, string, int, string, bool, ...repository.ArtifactSearchMatch) ([]repository.CargoCrateSummary, int, error)
}

type artifactCargoProxyReader interface {
	SearchCargoProxyCrates(context.Context, string, string, int, string, ...repository.ArtifactSearchMatch) ([]repository.CargoCrateSummary, error)
}

type artifactProxyOperations interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
	Invalidate(http.ResponseWriter, *http.Request)
	ClearNegative(http.ResponseWriter, *http.Request)
}

type artifactMavenProxyOperations interface {
	Refresh(http.ResponseWriter, *http.Request)
	Health(http.ResponseWriter, *http.Request)
}
