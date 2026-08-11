package app

import (
	"errors"
	"net/http"
	"strings"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	npmprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/npm"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func (h generatedRepositoryAPIAdapter) SearchRepositoryArtifacts(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.SearchRepositoryArtifactsParams) {
	if _, err := h.store.GetHostedRepository(r.Context(), repositoryID.String()); errors.Is(err, repository.ErrNotFound) {
		group, groupErr := h.groups.GetHostedGroup(r.Context(), repositoryID.String())
		if groupErr == nil {
			if !anonymousHostedGroupReadAllowed(r.Context(), h.store, h.store, group, r.Method) {
				writeHostedProblem(w, http.StatusForbidden, "access_denied", "group anonymous read is not enabled")
				return
			}
			h.searchHostedGroupArtifacts(w, r, group, params)
			return
		}
	}
	h.withRepositoryBrowseScope(w, r, repositoryID.String(), func(_ Principal, repo repository.HostedRepository) {
		query := ""
		if params.Q != nil {
			query = *params.Q
		}
		if !validArtifactSearchQuery(repo.Format, query) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "q is not a valid artifact prefix for this repository format")
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
		after, err := h.decodeArtifactSearchCursor(pageToken, repo.ID, repo.Format, query)
		if err != nil {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_page_token", "page token is invalid or expired")
			return
		}
		items := make([]adminopenapi.ArtifactSummary, 0, pageSize)
		var lastCoordinate string
		hasMore := false
		switch repo.Format {
		case repository.FormatOCI:
			names, err := h.oci.SearchOCIManifestNames(r.Context(), repo.ID, query, pageSize+1, after.Coordinate)
			if err != nil {
				writeHostedProblem(w, 500, "internal_error", "search OCI artifacts failed")
				return
			}
			hasMore = len(names) > pageSize
			if hasMore {
				names = names[:pageSize]
			}
			for _, name := range names {
				items = append(items, adminopenapi.ArtifactSummary{Coordinate: name})
				lastCoordinate = name
			}
		case repository.FormatMaven:
			artifacts, err := h.sessions.store.SearchMavenArtifacts(r.Context(), repo.ID, query, pageSize+1, repository.MavenArtifactCursor{Coordinate: after.Coordinate, BuildNumber: after.BuildNumber})
			if err != nil {
				writeHostedProblem(w, 500, "internal_error", "search Maven artifacts failed")
				return
			}
			hasMore = len(artifacts) > pageSize
			if hasMore {
				artifacts = artifacts[:pageSize]
			}
			for _, a := range artifacts {
				d := a.Digest
				created := a.CreatedAt
				buildNumber := int32(a.BuildNumber)
				items = append(items, adminopenapi.ArtifactSummary{Coordinate: a.Coordinate, Digest: &d, CreatedAt: &created, BuildNumber: &buildNumber, Publisher: optionalPublisher(a.Publisher)})
				lastCoordinate = a.Coordinate
			}
		case repository.FormatConan:
			references, err := h.conan.SearchConanReferences(r.Context(), repo.ID, query, pageSize+1, after.Coordinate)
			if err != nil {
				writeHostedProblem(w, 500, "internal_error", "search Conan artifacts failed")
				return
			}
			hasMore = len(references) > pageSize
			if hasMore {
				references = references[:pageSize]
			}
			for _, reference := range references {
				items = append(items, adminopenapi.ArtifactSummary{Coordinate: reference.Reference, Publisher: optionalPublisher(reference.Publisher)})
				lastCoordinate = reference.Reference
			}
		case repository.FormatRaw:
			assets, err := h.sessions.store.ListRawAssets(r.Context(), repo.ID, query, pageSize+1, after.Coordinate)
			if err != nil {
				writeHostedProblem(w, 500, "internal_error", "search Raw artifacts failed")
				return
			}
			hasMore = len(assets) > pageSize
			if hasMore {
				assets = assets[:pageSize]
			}
			for _, a := range assets {
				d, ct := a.Digest, a.ContentType
				size := a.Size
				updatedAt := a.UpdatedAt
				items = append(items, adminopenapi.ArtifactSummary{Coordinate: a.Path, Digest: &d, ContentType: &ct, Size: &size, CreatedAt: &updatedAt})
				lastCoordinate = a.Path
			}
		case repository.FormatNPM:
			packages, err := h.sessions.store.SearchNPMPackages(r.Context(), repo.ID, query, pageSize+1, after.Coordinate)
			if err != nil {
				writeHostedProblem(w, 500, "internal_error", "search npm packages failed")
				return
			}
			hasMore = len(packages) > pageSize
			if hasMore {
				packages = packages[:pageSize]
			}
			for _, pkg := range packages {
				digest, version, createdAt, size := pkg.Latest.Digest, pkg.Latest.Version, pkg.UpdatedAt, pkg.Latest.Size
				versionCount := int32(pkg.VersionCount)
				items = append(items, adminopenapi.ArtifactSummary{
					Coordinate: pkg.Name, Digest: &digest, Version: &version, VersionCount: &versionCount,
					CreatedAt: &createdAt, Size: &size, Publisher: optionalPublisher(pkg.Latest.Publisher),
				})
				lastCoordinate = pkg.Name
			}
		case repository.FormatPyPI:
			projects, err := h.sessions.store.ListPyPIProjects(r.Context(), repo.ID, normalizePyPIProject(query), pageSize+1, after.Coordinate)
			if err != nil {
				writeHostedProblem(w, 500, "internal_error", "search PyPI projects failed")
				return
			}
			hasMore = len(projects) > pageSize
			if hasMore {
				projects = projects[:pageSize]
			}
			for _, project := range projects {
				digest, version, createdAt, size := project.Latest.Digest, project.Latest.Version, project.UpdatedAt, project.Latest.Size
				versionCount := int32(project.VersionCount)
				items = append(items, adminopenapi.ArtifactSummary{
					Coordinate: project.Project, Digest: &digest, Version: &version, VersionCount: &versionCount,
					CreatedAt: &createdAt, Size: &size, Publisher: optionalPublisher(project.Latest.Publisher),
				})
				lastCoordinate = project.Project
			}
		case repository.FormatGo:
			projected, err := h.searchGroupMemberArtifacts(r, repo, query, pageSize+1, after)
			if err != nil {
				writeHostedProblem(w, 500, "internal_error", "search Go modules failed")
				return
			}
			hasMore = len(projected) > pageSize
			if hasMore {
				projected = projected[:pageSize]
			}
			items = append(items, projected...)
			if len(items) > 0 {
				lastCoordinate = items[len(items)-1].Coordinate
			}
		case repository.FormatAPT:
			assets, err := h.sessions.store.ListAPTAssets(r.Context(), repo.ID, query, pageSize+1, after.Coordinate)
			if err != nil {
				writeHostedProblem(w, 500, "internal_error", "search APT assets failed")
				return
			}
			hasMore = len(assets) > pageSize
			if hasMore {
				assets = assets[:pageSize]
			}
			for _, asset := range assets {
				d, size, created, cached, sourceURL := asset.Digest, asset.Size, asset.CreatedAt, asset.CachedAt, asset.SourceURL
				items = append(items, adminopenapi.ArtifactSummary{Coordinate: asset.Path, Digest: &d, Size: &size, CreatedAt: &created, CachedAt: &cached, SourceUrl: &sourceURL, ContentType: optionalString(asset.ContentType)})
				lastCoordinate = asset.Path
			}
		}
		var next *string
		if hasMore {
			buildNumber := 0
			if last := items[len(items)-1].BuildNumber; last != nil {
				buildNumber = int(*last)
			}
			token := h.encodeArtifactSearchCursor(repo.ID, repo.Format, query, lastCoordinate, buildNumber)
			next = &token
		}
		writeNativeMavenJSON(w, http.StatusOK, adminopenapi.ArtifactSummaryPage{Items: items, NextPageToken: next})
	})
}

func repositoryCapabilities(format repository.Format, repoType repository.RepositoryType) adminopenapi.RepositoryCapabilities {
	profile, _ := repository.FormatProfileFor(format)
	operations := profile.HostedOperations
	if repoType == repository.RepositoryTypeProxy {
		operations = profile.ProxyOperations
	}
	responseOperations := make([]adminopenapi.RepositoryOperation, 0, len(operations))
	for _, operation := range operations {
		responseOperations = append(responseOperations, adminopenapi.RepositoryOperation(operation))
	}
	return adminopenapi.RepositoryCapabilities{Format: adminopenapi.Format(format), Type: adminopenapi.RepositoryCapabilitiesType(repoType), Operations: responseOperations}
}

func formatProfileResponse(profile repository.FormatProfile) adminopenapi.FormatProfile {
	repositoryTypes := make([]adminopenapi.FormatProfileRepositoryTypes, 0, len(profile.RepositoryTypes))
	for _, repositoryType := range profile.RepositoryTypes {
		repositoryTypes = append(repositoryTypes, adminopenapi.FormatProfileRepositoryTypes(repositoryType))
	}
	hostedOperations := make([]adminopenapi.RepositoryOperation, 0, len(profile.HostedOperations))
	for _, operation := range profile.HostedOperations {
		hostedOperations = append(hostedOperations, adminopenapi.RepositoryOperation(operation))
	}
	proxyOperations := make([]adminopenapi.RepositoryOperation, 0, len(profile.ProxyOperations))
	for _, operation := range profile.ProxyOperations {
		proxyOperations = append(proxyOperations, adminopenapi.RepositoryOperation(operation))
	}
	return adminopenapi.FormatProfile{
		Format:           adminopenapi.Format(profile.Format),
		RepositoryTypes:  repositoryTypes,
		GroupSupported:   profile.GroupSupported,
		AnonymousRead:    profile.AnonymousRead,
		HostedOperations: hostedOperations,
		ProxyOperations:  proxyOperations,
	}
}

func validOCIImagePrefix(value string) bool {
	if len(value) > 255 || strings.HasPrefix(value, "/") || strings.Contains(value, "//") {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if component == "." || component == ".." {
			return false
		}
	}
	for _, c := range value {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '-' || c == '/' {
			continue
		}
		return false
	}
	return true
}

func validMavenCoordinatePrefix(value string) bool {
	if len(value) > 255 {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '.' && r != '_' && r != '-' && r != ':' {
			return false
		}
	}
	return true
}

// optionalPublisher maps an empty publisher (no committed publish session was
// recorded, for example replicated or pre-session artifacts) to nil so the
// field is omitted from the JSON response.
func optionalPublisher(publisher string) *string {
	if publisher == "" {
		return nil
	}
	return &publisher
}

// optionalBuildNumber omits the build number for release coordinates (build 0)
// so only SNAPSHOT builds carry it in responses.
func optionalBuildNumber(buildNumber int) *int {
	if buildNumber <= 0 {
		return nil
	}
	return &buildNumber
}

func validConanReferencePrefix(value string) bool {
	if len(value) > 255 || strings.ContainsAny(value, "\\\x00#") || strings.Contains(strings.ToLower(value), "%2f") || strings.Contains(strings.ToLower(value), "%23") {
		return false
	}
	parts := strings.Split(value, "/")
	for i, part := range parts {
		if part == "" && i == len(parts)-1 {
			continue
		}
		if !validConanSegment(part) {
			return false
		}
	}
	return true
}

func validConanRestoreCoordinate(value string) bool {
	_, _, _, _, _, ok := parseConanRestoreCoordinate(value)
	return ok
}

func parseConanRestoreCoordinate(value string) (reference, recipeRevision, packageID, packageRevision string, packageRestore, ok bool) {
	if len(value) > 1024 || strings.ContainsAny(value, "\\\x00") {
		return "", "", "", "", false, false
	}
	reference, remainder, split := strings.Cut(value, "#")
	if !split || strings.Count(reference, "/") != 3 || !validConanReferencePrefix(reference) {
		return "", "", "", "", false, false
	}
	if recipeRevision, remainder, split = strings.Cut(remainder, "/"); !split {
		return reference, recipeRevision, "", "", false, validConanSegment(recipeRevision)
	}
	packageID, packageRevision, split = strings.Cut(remainder, "#")
	if !split || strings.Contains(packageRevision, "/") {
		return "", "", "", "", false, false
	}
	return reference, recipeRevision, packageID, packageRevision, true, validConanSegment(recipeRevision) && validConanSegment(packageID) && validConanSegment(packageRevision)
}

func validRawAssetPrefix(value string) bool {
	if len(value) > 255 || strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\\\x00") {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func validArtifactSearchQuery(format repository.Format, query string) bool {
	switch format {
	case repository.FormatOCI:
		return validOCIImagePrefix(query)
	case repository.FormatMaven:
		return validMavenCoordinatePrefix(query)
	case repository.FormatConan:
		return validConanReferencePrefix(query)
	case repository.FormatRaw:
		return validRawAssetPrefix(query)
	case repository.FormatNPM:
		return npmprotocol.ValidPackagePrefix(query)
	case repository.FormatPyPI:
		return validPyPIProjectSearchPrefix(query)
	case repository.FormatGo:
		return validGoModuleSearchPrefix(query)
	case repository.FormatAPT:
		return validAPTPathPrefix(query)
	default:
		return false
	}
}

func parseNPMVersionCoordinate(value string) (string, string, bool) {
	separator := strings.LastIndex(value, "@")
	if separator <= 0 || separator == len(value)-1 {
		return "", "", false
	}
	name, version := value[:separator], value[separator+1:]
	return name, version, npmprotocol.ValidPackageName(name) && npmprotocol.ValidVersion(version)
}

func validNPMVersionCoordinate(value string) bool {
	_, _, ok := parseNPMVersionCoordinate(value)
	return ok
}

func parsePyPIVersionCoordinate(value string) (string, string, bool) {
	separator := strings.LastIndex(value, "@")
	if separator <= 0 || separator == len(value)-1 {
		return "", "", false
	}
	project, version := value[:separator], value[separator+1:]
	return project, version, validPyPIProject(project) && normalizePyPIProject(project) == project && pypiVersionPattern.MatchString(version)
}

func validPyPIVersionCoordinate(value string) bool {
	_, _, ok := parsePyPIVersionCoordinate(value)
	return ok
}

func (h hostedRepositoryAPIHandler) encodeOCIImageCursor(repositoryID, prefix, name string) string {
	return encodeSignedCursor(h.authenticator.AdminToken, ociImagePageCursor{Endpoint: "oci-images", RepositoryID: repositoryID, Prefix: prefix, Name: name, ExpiresAt: time.Now().UTC().Add(15 * time.Minute).Unix()})
}

func (h hostedRepositoryAPIHandler) decodeOCIImageCursor(token, repositoryID, prefix string) (string, error) {
	if token == "" {
		return "", nil
	}
	var cursor ociImagePageCursor
	if decodeSignedCursor(h.authenticator.AdminToken, token, &cursor) != nil || cursor.Endpoint != "oci-images" || cursor.RepositoryID != repositoryID || cursor.Prefix != prefix || cursor.Name == "" || time.Now().UTC().Unix() >= cursor.ExpiresAt {
		return "", errors.New("invalid cursor")
	}
	return cursor.Name, nil
}

func (h hostedRepositoryAPIHandler) encodeOCIManifestCursor(repositoryID, name, digest string) string {
	return encodeSignedCursor(h.authenticator.AdminToken, ociManifestPageCursor{Endpoint: "oci-manifests", RepositoryID: repositoryID, Name: name, Digest: digest, ExpiresAt: time.Now().UTC().Add(15 * time.Minute).Unix()})
}

func (h hostedRepositoryAPIHandler) decodeOCIManifestCursor(token, repositoryID, name string) (string, error) {
	if token == "" {
		return "", nil
	}
	var cursor ociManifestPageCursor
	if decodeSignedCursor(h.authenticator.AdminToken, token, &cursor) != nil || cursor.Endpoint != "oci-manifests" || cursor.RepositoryID != repositoryID || cursor.Name != name || cursor.Digest == "" || time.Now().UTC().Unix() >= cursor.ExpiresAt {
		return "", errors.New("invalid cursor")
	}
	return cursor.Digest, nil
}

func (h hostedRepositoryAPIHandler) encodeMavenCoordinateCursor(repositoryID, prefix, coordinate string, buildNumber int) string {
	return encodeSignedCursor(h.authenticator.AdminToken, mavenCoordinatePageCursor{Endpoint: "maven-coordinates", RepositoryID: repositoryID, Prefix: prefix, Coordinate: coordinate, BuildNumber: buildNumber, ExpiresAt: time.Now().UTC().Add(15 * time.Minute).Unix()})
}

func (h hostedRepositoryAPIHandler) decodeMavenCoordinateCursor(token, repositoryID, prefix string) (repository.MavenArtifactCursor, error) {
	if token == "" {
		return repository.MavenArtifactCursor{}, nil
	}
	var cursor mavenCoordinatePageCursor
	if decodeSignedCursor(h.authenticator.AdminToken, token, &cursor) != nil || cursor.Endpoint != "maven-coordinates" || cursor.RepositoryID != repositoryID || cursor.Prefix != prefix || cursor.Coordinate == "" || time.Now().UTC().Unix() >= cursor.ExpiresAt {
		return repository.MavenArtifactCursor{}, errors.New("invalid cursor")
	}
	return repository.MavenArtifactCursor{Coordinate: cursor.Coordinate, BuildNumber: cursor.BuildNumber}, nil
}

func (h hostedRepositoryAPIHandler) encodeConanReferenceCursor(repositoryID, prefix, reference string) string {
	return encodeSignedCursor(h.authenticator.AdminToken, conanReferencePageCursor{Endpoint: "conan-references", RepositoryID: repositoryID, Prefix: prefix, Reference: reference, ExpiresAt: time.Now().UTC().Add(15 * time.Minute).Unix()})
}

func (h hostedRepositoryAPIHandler) decodeConanReferenceCursor(token, repositoryID, prefix string) (string, error) {
	if token == "" {
		return "", nil
	}
	var cursor conanReferencePageCursor
	if decodeSignedCursor(h.authenticator.AdminToken, token, &cursor) != nil || cursor.Endpoint != "conan-references" || cursor.RepositoryID != repositoryID || cursor.Prefix != prefix || cursor.Reference == "" || time.Now().UTC().Unix() >= cursor.ExpiresAt {
		return "", errors.New("invalid cursor")
	}
	return cursor.Reference, nil
}

func (h hostedRepositoryAPIHandler) encodeConanRevisionCursor(repositoryID, reference, query, revision string) string {
	return encodeSignedCursor(h.authenticator.AdminToken, conanRevisionPageCursor{Endpoint: "conan-revisions", RepositoryID: repositoryID, Reference: reference, Query: query, Revision: revision, ExpiresAt: time.Now().UTC().Add(15 * time.Minute).Unix()})
}

func (h hostedRepositoryAPIHandler) decodeConanRevisionCursor(token, repositoryID, reference, query string) (string, error) {
	if token == "" {
		return "", nil
	}
	var cursor conanRevisionPageCursor
	if decodeSignedCursor(h.authenticator.AdminToken, token, &cursor) != nil || cursor.Endpoint != "conan-revisions" || cursor.RepositoryID != repositoryID || cursor.Reference != reference || cursor.Query != query || cursor.Revision == "" || time.Now().UTC().Unix() >= cursor.ExpiresAt {
		return "", errors.New("invalid cursor")
	}
	return cursor.Revision, nil
}

func (h hostedRepositoryAPIHandler) encodeArtifactSearchCursor(repositoryID string, format repository.Format, query, coordinate string, buildNumber int) string {
	return encodeSignedCursor(h.authenticator.AdminToken, artifactSearchPageCursor{Endpoint: "artifact-search", RepositoryID: repositoryID, Format: string(format), Query: query, Coordinate: coordinate, BuildNumber: buildNumber, ExpiresAt: time.Now().UTC().Add(15 * time.Minute).Unix()})
}

func (h hostedRepositoryAPIHandler) decodeArtifactSearchCursor(token, repositoryID string, format repository.Format, query string) (artifactSearchPosition, error) {
	if token == "" {
		return artifactSearchPosition{}, nil
	}
	var cursor artifactSearchPageCursor
	if decodeSignedCursor(h.authenticator.AdminToken, token, &cursor) != nil || cursor.Endpoint != "artifact-search" || cursor.RepositoryID != repositoryID || cursor.Format != string(format) || cursor.Query != query || cursor.Coordinate == "" || time.Now().UTC().Unix() >= cursor.ExpiresAt {
		return artifactSearchPosition{}, errors.New("invalid cursor")
	}
	return artifactSearchPosition{Coordinate: cursor.Coordinate, BuildNumber: cursor.BuildNumber}, nil
}

func (h hostedRepositoryAPIHandler) encodeRetentionDryRunCursor(repositoryID, policyVersion, coordinate, artifactID string) string {
	return encodeSignedCursor(h.authenticator.AdminToken, retentionDryRunPageCursor{Endpoint: "retention-dry-run", RepositoryID: repositoryID, PolicyVersion: policyVersion, Coordinate: coordinate, ArtifactID: artifactID, ExpiresAt: time.Now().UTC().Add(15 * time.Minute).Unix()})
}

func (h hostedRepositoryAPIHandler) decodeRetentionDryRunCursor(token, repositoryID, policyVersion string) (string, string, error) {
	if token == "" {
		return "", "", nil
	}
	var cursor retentionDryRunPageCursor
	if decodeSignedCursor(h.authenticator.AdminToken, token, &cursor) != nil || cursor.Endpoint != "retention-dry-run" || cursor.RepositoryID != repositoryID || cursor.PolicyVersion != policyVersion || cursor.Coordinate == "" || cursor.ArtifactID == "" || time.Now().UTC().Unix() >= cursor.ExpiresAt {
		return "", "", errors.New("invalid cursor")
	}
	return cursor.Coordinate, cursor.ArtifactID, nil
}

func (h hostedRepositoryAPIHandler) encodeTombstoneCursor(repositoryID string, format repository.Format, prefix, coordinate string) string {
	return encodeSignedCursor(h.authenticator.AdminToken, tombstonePageCursor{Endpoint: "tombstones", RepositoryID: repositoryID, Format: string(format), Prefix: prefix, Coordinate: coordinate, ExpiresAt: time.Now().UTC().Add(15 * time.Minute).Unix()})
}

func (h hostedRepositoryAPIHandler) decodeTombstoneCursor(token, repositoryID string, format repository.Format, prefix string) (string, error) {
	if token == "" {
		return "", nil
	}
	var cursor tombstonePageCursor
	if decodeSignedCursor(h.authenticator.AdminToken, token, &cursor) != nil || cursor.Endpoint != "tombstones" || cursor.RepositoryID != repositoryID || cursor.Format != string(format) || cursor.Prefix != prefix || cursor.Coordinate == "" || time.Now().UTC().Unix() >= cursor.ExpiresAt {
		return "", errors.New("invalid cursor")
	}
	return cursor.Coordinate, nil
}
