package app

import (
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type artifactIdentity struct {
	Coordinate  string
	BuildNumber int32
}

// searchHostedGroupArtifacts exposes the same format-aware projection as a
// repository search, but merges only members that are explicitly anonymous.
func (h generatedRepositoryAPIAdapter) searchHostedGroupArtifacts(w http.ResponseWriter, r *http.Request, group repository.HostedGroup, params adminopenapi.SearchRepositoryArtifactsParams) {
	query := ""
	if params.Q != nil {
		query = *params.Q
	}
	if !validArtifactSearchQuery(group.Format, query) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "q is not a valid artifact prefix for this group format")
		return
	}
	pageSize := 50
	if params.PageSize != nil {
		pageSize = int(*params.PageSize)
		if pageSize < 1 || pageSize > 200 {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "pageSize must be between 1 and 200")
			return
		}
	}
	pageToken := ""
	if params.PageToken != nil {
		pageToken = string(*params.PageToken)
	}
	after, err := h.decodeArtifactSearchCursor(pageToken, group.ID, group.Format, query)
	if err != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_page_token", "page token is invalid or expired")
		return
	}

	resolver := v2GroupResolver{groups: h.groups, repos: h.store}
	members, err := resolver.resolveMembers(r.Context(), group)
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "resolve group members failed")
		return
	}
	members = anonymousHostedGroupMembers(group, members)
	byIdentity := make(map[artifactIdentity]adminopenapi.ArtifactSummary)
	for _, member := range members {
		repo, err := h.store.GetHostedRepository(r.Context(), member.RepositoryID)
		if err != nil {
			continue
		}
		items, err := h.searchGroupMemberArtifacts(r, repo, query, pageSize+1, after)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "search group member artifacts failed")
			return
		}
		for _, item := range items {
			identity := artifactIdentity{Coordinate: item.Coordinate}
			if item.BuildNumber != nil {
				identity.BuildNumber = *item.BuildNumber
			}
			if _, exists := byIdentity[identity]; !exists {
				byIdentity[identity] = item
			}
		}
	}

	items := make([]adminopenapi.ArtifactSummary, 0, len(byIdentity))
	for _, item := range byIdentity {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Coordinate != items[j].Coordinate {
			return items[i].Coordinate < items[j].Coordinate
		}
		left, right := int32(0), int32(0)
		if items[i].BuildNumber != nil {
			left = *items[i].BuildNumber
		}
		if items[j].BuildNumber != nil {
			right = *items[j].BuildNumber
		}
		return left < right
	})
	var next *string
	if len(items) > pageSize {
		items = items[:pageSize]
		last := items[len(items)-1]
		buildNumber := 0
		if last.BuildNumber != nil {
			buildNumber = int(*last.BuildNumber)
		}
		token := h.encodeArtifactSearchCursor(group.ID, group.Format, query, last.Coordinate, buildNumber)
		next = &token
	}
	writeNativeMavenJSON(w, http.StatusOK, adminopenapi.ArtifactSummaryPage{Items: items, NextPageToken: next})
}

func (h generatedRepositoryAPIAdapter) searchGroupMemberArtifacts(r *http.Request, repo repository.HostedRepository, query string, limit int, after artifactSearchPosition) ([]adminopenapi.ArtifactSummary, error) {
	return h.searchGroupMemberArtifactsByQuery(r, repo, repository.ArtifactSearchQuery{Mode: repository.ArtifactSearchByCoordinate, Value: query}, limit, after)
}

func (h generatedRepositoryAPIAdapter) searchGroupMemberArtifactsByQuery(r *http.Request, repo repository.HostedRepository, query repository.ArtifactSearchQuery, limit int, after artifactSearchPosition) ([]adminopenapi.ArtifactSummary, error) {
	query = repositorySearchQueryForFormat(repo.Format, query)
	items := make([]adminopenapi.ArtifactSummary, 0, limit)
	if h.searchProjection != nil {
		projected, err := h.searchProjection.SearchArtifactProjection(r.Context(), repo.ID, repo.Format, query, limit, repository.ArtifactSearchPosition{Coordinate: after.Coordinate, BuildNumber: after.BuildNumber, Digest: after.Digest})
		if err != nil {
			return nil, err
		}
		for _, item := range projected {
			var digest *string
			if item.Digest != "" {
				value := item.Digest
				digest = &value
			}
			createdAt := item.CreatedAt
			size := item.Size
			var contentType *string
			if item.ContentType != "" {
				value := item.ContentType
				contentType = &value
			}
			var buildNumber *int32
			if item.BuildNumber > 0 {
				value := int32(item.BuildNumber)
				buildNumber = &value
			}
			var version *string
			if item.Version != "" {
				value := item.Version
				version = &value
			}
			items = append(items, adminopenapi.ArtifactSummary{Coordinate: item.Coordinate, Version: version, Digest: digest, CreatedAt: createdAt, Size: size, ContentType: contentType, BuildNumber: buildNumber, Publisher: optionalPublisher(item.Publisher), Intelligence: artifactIntelligenceSummaryResponse(item.Intelligence)})
		}
		return items, nil
	}
	if query.Mode != repository.ArtifactSearchByCoordinate {
		return nil, errors.New("digest search requires the artifact search projection")
	}
	switch repo.Format {
	case repository.FormatOCI:
		names, err := h.oci.SearchOCIManifestNames(r.Context(), repo.ID, query.Value, limit, after.Coordinate)
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			items = append(items, adminopenapi.ArtifactSummary{Coordinate: name})
		}
	case repository.FormatMaven:
		artifacts, err := h.sessions.store.SearchMavenArtifacts(r.Context(), repo.ID, query.Value, limit, repository.MavenArtifactCursor{Coordinate: after.Coordinate, BuildNumber: after.BuildNumber})
		if err != nil {
			return nil, err
		}
		for _, artifact := range artifacts {
			digest, createdAt := artifact.Digest, artifact.CreatedAt
			buildNumber := int32(artifact.BuildNumber)
			items = append(items, adminopenapi.ArtifactSummary{Coordinate: artifact.Coordinate, Digest: &digest, CreatedAt: &createdAt, BuildNumber: &buildNumber, Publisher: optionalPublisher(artifact.Publisher)})
		}
	case repository.FormatConan:
		references, err := h.conan.SearchConanReferences(r.Context(), repo.ID, query.Value, limit, after.Coordinate)
		if err != nil {
			return nil, err
		}
		for _, reference := range references {
			items = append(items, adminopenapi.ArtifactSummary{Coordinate: reference.Reference, Publisher: optionalPublisher(reference.Publisher)})
		}
	case repository.FormatRaw:
		assets, err := h.sessions.store.ListRawAssets(r.Context(), repo.ID, query.Value, limit, after.Coordinate)
		if err != nil {
			return nil, err
		}
		for _, asset := range assets {
			digest, contentType, size := asset.Digest, asset.ContentType, asset.Size
			updatedAt := asset.UpdatedAt
			items = append(items, adminopenapi.ArtifactSummary{Coordinate: asset.Path, Digest: &digest, ContentType: &contentType, Size: &size, CreatedAt: &updatedAt})
		}
	case repository.FormatNPM:
		packages, err := h.sessions.store.SearchNPMPackages(r.Context(), repo.ID, query.Value, limit, after.Coordinate)
		if err != nil {
			return nil, err
		}
		for _, pkg := range packages {
			digest, version, size, updatedAt := pkg.Latest.Digest, pkg.Latest.Version, pkg.Latest.Size, pkg.UpdatedAt
			versionCount := int32(pkg.VersionCount)
			items = append(items, adminopenapi.ArtifactSummary{Coordinate: pkg.Name, Version: &version, VersionCount: &versionCount, Digest: &digest, Size: &size, CreatedAt: &updatedAt, Publisher: optionalPublisher(pkg.Latest.Publisher)})
		}
	case repository.FormatPyPI:
		projects, err := h.sessions.store.ListPyPIProjects(r.Context(), repo.ID, normalizePyPIProject(query.Value), limit, after.Coordinate)
		if err != nil {
			return nil, err
		}
		for _, project := range projects {
			digest, version, size, updatedAt := project.Latest.Digest, project.Latest.Version, project.Latest.Size, project.UpdatedAt
			versionCount := int32(project.VersionCount)
			items = append(items, adminopenapi.ArtifactSummary{Coordinate: project.Project, Version: &version, VersionCount: &versionCount, Digest: &digest, Size: &size, CreatedAt: &updatedAt, Publisher: optionalPublisher(project.Latest.Publisher)})
		}
	}
	return items, nil
}

func repositorySearchQueryForFormat(format repository.Format, query repository.ArtifactSearchQuery) repository.ArtifactSearchQuery {
	if format != repository.FormatRaw || query.Mode != repository.ArtifactSearchByCoordinate || query.Value == "" {
		return query
	}
	segments := strings.Split(query.Value, "/")
	canonical := true
	for _, segment := range segments {
		decoded, err := url.PathUnescape(segment)
		if err != nil || url.PathEscape(decoded) != segment {
			canonical = false
			break
		}
	}
	if canonical {
		return query
	}
	for index, segment := range segments {
		segments[index] = url.PathEscape(segment)
	}
	query.Value = strings.Join(segments, "/")
	return query
}

func (h generatedRepositoryAPIAdapter) listHostedGroupConanRecipeRevisions(w http.ResponseWriter, r *http.Request, group repository.HostedGroup, params adminopenapi.ListConanRecipeRevisionsParams) {
	reference := strings.TrimSuffix(strings.TrimSpace(params.Reference), "/")
	if !validConanReferencePrefix(reference) || strings.Count(reference, "/") != 3 {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "reference must be a valid Conan recipe reference")
		return
	}
	query := ""
	if params.Q != nil {
		query = strings.TrimSpace(*params.Q)
	}
	if len(query) > 255 || strings.ContainsRune(query, '\x00') {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "q must be at most 255 characters")
		return
	}
	pageSize := 50
	if params.PageSize != nil {
		pageSize = int(*params.PageSize)
		if pageSize < 1 || pageSize > 200 {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "pageSize must be between 1 and 200")
			return
		}
	}
	pageToken := ""
	if params.PageToken != nil {
		pageToken = string(*params.PageToken)
	}
	after, err := h.decodeConanRevisionCursor(pageToken, group.ID, reference, query)
	if err != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_page_token", "page token is invalid or expired")
		return
	}
	resolver := v2GroupResolver{groups: h.groups, repos: h.store}
	members, err := resolver.resolveMembers(r.Context(), group)
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "resolve group members failed")
		return
	}
	members = anonymousHostedGroupMembers(group, members)
	byRevision := make(map[string]repository.ConanRecipeRevision)
	for _, member := range members {
		if member.Type != repository.MemberHosted {
			continue
		}
		repo, getErr := h.store.GetHostedRepository(r.Context(), member.RepositoryID)
		if getErr != nil || repo.Type == repository.RepositoryTypeProxy {
			continue
		}
		revisions, searchErr := h.conan.SearchConanRecipeRevisions(r.Context(), repo.ID, reference, query, pageSize+1, after)
		if searchErr != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list Conan recipe revisions failed")
			return
		}
		for _, revision := range revisions {
			if _, exists := byRevision[revision.Revision]; !exists {
				byRevision[revision.Revision] = revision
			}
		}
	}
	revisions := make([]repository.ConanRecipeRevision, 0, len(byRevision))
	for _, revision := range byRevision {
		revisions = append(revisions, revision)
	}
	sort.Slice(revisions, func(i, j int) bool { return revisions[i].Revision < revisions[j].Revision })
	var next *string
	if len(revisions) > pageSize {
		revisions = revisions[:pageSize]
		token := h.encodeConanRevisionCursor(group.ID, reference, query, revisions[len(revisions)-1].Revision)
		next = &token
	}
	items := make([]adminopenapi.ConanRecipeRevision, 0, len(revisions))
	for _, revision := range revisions {
		items = append(items, adminopenapi.ConanRecipeRevision{Reference: revision.Reference, Revision: revision.Revision, Digest: revision.Digest, State: adminopenapi.ConanRecipeRevisionState(revision.State), CreatedAt: revision.CreatedAt})
	}
	writeNativeMavenJSON(w, http.StatusOK, adminopenapi.ConanRecipeRevisionList{Items: items, NextPageToken: next})
}
