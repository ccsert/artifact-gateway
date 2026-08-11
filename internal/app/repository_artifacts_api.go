package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func (h generatedRepositoryAPIAdapter) CreatePublishSession(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.CreatePublishSessionParams) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryWrite, func(principal Principal, _ repository.HostedRepository) {
		h.sessions.createWithIdempotencyKey(w, r, principal, repositoryID.String(), string(params.IdempotencyKey))
	})
}

func (h generatedRepositoryAPIAdapter) ListArtifacts(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, _ adminopenapi.ListArtifactsParams) {
	h.withRepositoryBrowseScope(w, r, repositoryID.String(), func(Principal, repository.HostedRepository) {
		h.sessions.listArtifacts(w, r, repositoryID.String())
	})
}

func (h generatedRepositoryAPIAdapter) GetArtifactIntelligence(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.GetArtifactIntelligenceParams) {
	h.withRepositoryBrowseScope(w, r, repositoryID.String(), func(_ Principal, repo repository.HostedRepository) {
		coordinate := strings.TrimSpace(params.Coordinate)
		digest := strings.TrimSpace(params.Digest)
		if !validArtifactIntelligenceIdentity(repo.Format, coordinate, digest) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "coordinate and digest must identify an artifact")
			return
		}
		value, err := h.intelligence.GetArtifactIntelligence(r.Context(), repo.ID, repo.Format, coordinate, digest)
		if errors.Is(err, repository.ErrNotFound) {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "artifact intelligence not found")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get artifact intelligence failed")
			return
		}
		writeArtifactIntelligence(w, value)
	})
}

func (h generatedRepositoryAPIAdapter) ReplaceArtifactIntelligence(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.ReplaceArtifactIntelligenceParams) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryIntelligence, func(principal Principal, repo repository.HostedRepository) {
		coordinate := strings.TrimSpace(params.Coordinate)
		digest := strings.TrimSpace(params.Digest)
		if !validArtifactIntelligenceIdentity(repo.Format, coordinate, digest) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "coordinate and digest must identify an artifact")
			return
		}
		if h.searchProjection != nil {
			visible, err := securityPolicyArtifactVisible(r.Context(), h.searchProjection, repo.ID, repo.Format, coordinate, digest)
			if err != nil {
				writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "verify artifact intelligence identity failed")
				return
			}
			if !visible {
				writeHostedProblem(w, http.StatusNotFound, "not_found", "artifact for intelligence metadata not found")
				return
			}
		}
		var input adminopenapi.ArtifactIntelligenceWritable
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || !validArtifactIntelligencePayload(input) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "artifact intelligence payload is invalid")
			return
		}
		payload, err := json.Marshal(input)
		if err != nil {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "artifact intelligence payload is invalid")
			return
		}
		var metadata struct {
			Signatures    []repository.ArtifactSignature           `json:"signatures"`
			SBOMs         []repository.ArtifactSBOM                `json:"sboms"`
			Provenance    *repository.ArtifactProvenance           `json:"provenance,omitempty"`
			Licenses      []repository.ArtifactLicense             `json:"licenses"`
			Vulnerability *repository.ArtifactVulnerabilitySummary `json:"vulnerability,omitempty"`
		}
		if err := json.Unmarshal(payload, &metadata); err != nil {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "artifact intelligence payload is invalid")
			return
		}
		value := repository.ArtifactIntelligence{RepositoryID: repo.ID, Format: repo.Format, Coordinate: coordinate, Digest: digest, Signatures: metadata.Signatures, SBOMs: metadata.SBOMs, Provenance: metadata.Provenance, Licenses: metadata.Licenses, Vulnerability: metadata.Vulnerability, UpdatedBy: principal.Actor}
		expected := ""
		if params.IfMatch != nil {
			expected = string(*params.IfMatch)
		}
		updated, err := h.intelligence.ReplaceArtifactIntelligence(r.Context(), value, expected)
		if errors.Is(err, repository.ErrVersionConflict) {
			writeHostedProblem(w, http.StatusPreconditionFailed, "version_conflict", "If-Match does not match current intelligence version")
			return
		}
		if errors.Is(err, repository.ErrNotFound) {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "repository or artifact intelligence not found")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "replace artifact intelligence failed")
			return
		}
		if h.audit != nil {
			_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{
				GroupName: repo.Name, Repository: repo.Name, Actor: principal.Actor,
				Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(),
				Format: string(repo.Format), Resource: coordinate, Representation: digest,
				Operation: "artifact.intelligence.replace", Status: http.StatusOK,
				CacheDisposition: "bypass",
			})
		}
		writeArtifactIntelligence(w, updated)
	})
}

func validArtifactIntelligenceIdentity(format repository.Format, coordinate, digest string) bool {
	if coordinate == "" || len(coordinate) > 1024 || strings.ContainsRune(coordinate, '\x00') || !validSHA256Digest(digest) {
		return false
	}
	return repository.IsSupportedFormat(format)
}

func validSHA256Digest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, r := range value[len("sha256:"):] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func validArtifactText(value string, max int) bool {
	return strings.TrimSpace(value) != "" && len(value) <= max && !strings.ContainsRune(value, '\x00')
}

func validArtifactIntelligencePayload(input adminopenapi.ArtifactIntelligenceWritable) bool {
	if input.Signatures == nil || input.Sboms == nil || input.Licenses == nil || len(input.Signatures) > 20 || len(input.Sboms) > 20 || len(input.Licenses) > 100 {
		return false
	}
	for _, signature := range input.Signatures {
		if !validArtifactText(signature.KeyId, 512) || !validArtifactText(signature.Algorithm, 128) || !validArtifactText(signature.Identity, 1024) || !validArtifactText(signature.Signature, 4096) {
			return false
		}
	}
	for _, sbom := range input.Sboms {
		if !validArtifactText(sbom.MediaType, 255) || !validSHA256Digest(sbom.Digest) || sbom.Size != nil && *sbom.Size < 0 {
			return false
		}
		if sbom.Url != nil {
			if len(*sbom.Url) > 2048 || strings.TrimSpace(*sbom.Url) == "" || strings.ContainsRune(*sbom.Url, '\x00') {
				return false
			}
			if parsed, err := url.Parse(*sbom.Url); err != nil || parsed.Scheme == "" || parsed.Host == "" {
				return false
			}
		}
	}
	for _, license := range input.Licenses {
		if !validArtifactText(license.SpdxId, 128) || !validArtifactText(license.Name, 512) {
			return false
		}
		if license.Source != nil && (len(*license.Source) > 2048 || strings.ContainsRune(*license.Source, '\x00')) {
			return false
		}
	}
	if input.Provenance != nil {
		provenance := input.Provenance
		if !validArtifactText(provenance.Builder, 512) || !validArtifactText(provenance.BuildType, 512) || !validArtifactText(provenance.SourceRepository, 2048) || !validArtifactText(provenance.SourceCommit, 256) || !validArtifactText(provenance.BuildId, 512) {
			return false
		}
	}
	if input.Vulnerability != nil {
		payload, err := json.Marshal(input.Vulnerability)
		if err != nil {
			return false
		}
		var vulnerability repository.ArtifactVulnerabilitySummary
		if err := json.Unmarshal(payload, &vulnerability); err != nil || !repository.ValidArtifactVulnerabilitySummary(vulnerability) {
			return false
		}
	}
	return true
}

func writeArtifactIntelligence(w http.ResponseWriter, value repository.ArtifactIntelligence) {
	payload, _ := json.Marshal(value)
	var response adminopenapi.ArtifactIntelligence
	_ = json.Unmarshal(payload, &response)
	response.RepositoryId = uuid.MustParse(value.RepositoryID)
	response.Format = adminopenapi.Format(value.Format)
	w.Header().Set("ETag", value.Version)
	writeNativeMavenJSON(w, http.StatusOK, response)
}

func (h generatedRepositoryAPIAdapter) ListOCIImages(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.ListOCIImagesParams) {
	h.withRepositoryBrowseScope(w, r, repositoryID.String(), func(_ Principal, repo repository.HostedRepository) {
		if repo.Format != repository.FormatOCI {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "OCI repository not found")
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
		prefix := ""
		if params.Q != nil {
			prefix = string(*params.Q)
		}
		if !validOCIImagePrefix(prefix) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "q must be a valid OCI image-name prefix")
			return
		}
		pageToken := ""
		if params.PageToken != nil {
			pageToken = string(*params.PageToken)
		}
		after, err := h.decodeOCIImageCursor(pageToken, repo.ID, prefix)
		if err != nil {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_page_token", "page token is invalid or expired")
			return
		}
		names, err := h.oci.SearchOCIManifestNames(r.Context(), repo.ID, prefix, pageSize+1, after)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list OCI images failed")
			return
		}
		var next *string
		if len(names) > pageSize {
			names = names[:pageSize]
			token := h.encodeOCIImageCursor(repo.ID, prefix, names[len(names)-1])
			next = &token
		}
		items := make([]adminopenapi.OCIImage, 0, len(names))
		for _, name := range names {
			items = append(items, adminopenapi.OCIImage{Name: name})
		}
		writeNativeMavenJSON(w, http.StatusOK, adminopenapi.OCIImagePage{Items: items, NextPageToken: next})
	})
}

func (h generatedRepositoryAPIAdapter) ListOCIManifests(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.ListOCIManifestsParams) {
	h.withRepositoryBrowseScope(w, r, repositoryID.String(), func(_ Principal, repo repository.HostedRepository) {
		name := strings.TrimSpace(params.Name)
		if repo.Format != repository.FormatOCI || name == "" || !validOCIImagePrefix(name) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "name must identify an OCI image")
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
		after, err := h.decodeOCIManifestCursor(pageToken, repo.ID, name)
		if err != nil {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_page_token", "page token is invalid or expired")
			return
		}
		manifests, err := h.oci.ListOCIManifests(r.Context(), repo.ID, name, pageSize+1, after)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list OCI manifests failed")
			return
		}
		var next *string
		if len(manifests) > pageSize {
			manifests = manifests[:pageSize]
			token := h.encodeOCIManifestCursor(repo.ID, name, manifests[len(manifests)-1].Digest)
			next = &token
		}
		items := make([]adminopenapi.OCIManifestSummary, 0, len(manifests))
		for _, manifest := range manifests {
			tags := append([]string{}, manifest.Tags...)
			item := adminopenapi.OCIManifestSummary{Digest: manifest.Digest, MediaType: manifest.MediaType, Size: manifest.Size, Tags: tags}
			if manifest.SubjectDigest != "" {
				item.SubjectDigest = &manifest.SubjectDigest
			}
			if manifest.ArtifactType != "" {
				item.ArtifactType = &manifest.ArtifactType
			}
			items = append(items, item)
		}
		writeNativeMavenJSON(w, http.StatusOK, adminopenapi.OCIManifestSummaryPage{Items: items, NextPageToken: next})
	})
}

func (h generatedRepositoryAPIAdapter) ListMavenCoordinates(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.ListMavenCoordinatesParams) {
	h.withRepositoryBrowseScope(w, r, repositoryID.String(), func(_ Principal, repo repository.HostedRepository) {
		if repo.Format != repository.FormatMaven {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "Maven repository not found")
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
		prefix := ""
		if params.Q != nil {
			prefix = string(*params.Q)
		}
		if !validMavenCoordinatePrefix(prefix) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "q must be a valid Maven coordinate prefix")
			return
		}
		pageToken := ""
		if params.PageToken != nil {
			pageToken = string(*params.PageToken)
		}
		after, err := h.decodeMavenCoordinateCursor(pageToken, repo.ID, prefix)
		if err != nil {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_page_token", "page token is invalid or expired")
			return
		}
		artifacts, err := h.sessions.store.SearchMavenArtifacts(r.Context(), repo.ID, prefix, pageSize+1, after)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list Maven coordinates failed")
			return
		}
		var next *string
		if len(artifacts) > pageSize {
			artifacts = artifacts[:pageSize]
			last := artifacts[len(artifacts)-1]
			token := h.encodeMavenCoordinateCursor(repo.ID, prefix, last.Coordinate, last.BuildNumber)
			next = &token
		}
		items := make([]adminopenapi.MavenCoordinate, 0, len(artifacts))
		for _, artifact := range artifacts {
			items = append(items, adminopenapi.MavenCoordinate{Coordinate: artifact.Coordinate, Digest: artifact.Digest, CreatedAt: artifact.CreatedAt, Publisher: optionalPublisher(artifact.Publisher), BuildNumber: optionalBuildNumber(artifact.BuildNumber)})
		}
		writeNativeMavenJSON(w, http.StatusOK, adminopenapi.MavenCoordinatePage{Items: items, NextPageToken: next})
	})
}

func (h generatedRepositoryAPIAdapter) ListConanReferences(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.ListConanReferencesParams) {
	h.withRepositoryBrowseScope(w, r, repositoryID.String(), func(_ Principal, repo repository.HostedRepository) {
		if repo.Format != repository.FormatConan {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "Conan repository not found")
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
		prefix := ""
		if params.Q != nil {
			prefix = string(*params.Q)
		}
		if !validConanReferencePrefix(prefix) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "q must be a valid Conan reference prefix")
			return
		}
		pageToken := ""
		if params.PageToken != nil {
			pageToken = string(*params.PageToken)
		}
		after, err := h.decodeConanReferenceCursor(pageToken, repo.ID, prefix)
		if err != nil {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_page_token", "page token is invalid or expired")
			return
		}
		references, err := h.conan.SearchConanReferences(r.Context(), repo.ID, prefix, pageSize+1, after)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list Conan references failed")
			return
		}
		var next *string
		if len(references) > pageSize {
			references = references[:pageSize]
			token := h.encodeConanReferenceCursor(repo.ID, prefix, references[len(references)-1].Reference)
			next = &token
		}
		items := make([]adminopenapi.ConanReference, 0, len(references))
		for _, reference := range references {
			items = append(items, adminopenapi.ConanReference{Reference: reference.Reference, Publisher: optionalPublisher(reference.Publisher)})
		}
		writeNativeMavenJSON(w, http.StatusOK, adminopenapi.ConanReferencePage{Items: items, NextPageToken: next})
	})
}

func (h generatedRepositoryAPIAdapter) ListConanRecipeRevisions(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.ListConanRecipeRevisionsParams) {
	if _, err := h.store.GetHostedRepository(r.Context(), repositoryID.String()); errors.Is(err, repository.ErrNotFound) {
		group, groupErr := h.groups.GetHostedGroup(r.Context(), repositoryID.String())
		if groupErr == nil {
			if group.Format != repository.FormatConan {
				writeHostedProblem(w, http.StatusNotFound, "not_found", "Conan repository not found")
				return
			}
			if !anonymousHostedGroupReadAllowed(r.Context(), h.store, h.store, group, r.Method) {
				writeHostedProblem(w, http.StatusForbidden, "access_denied", "group anonymous read is not enabled")
				return
			}
			h.listHostedGroupConanRecipeRevisions(w, r, group, params)
			return
		}
	}
	h.withRepositoryBrowseScope(w, r, repositoryID.String(), func(_ Principal, repo repository.HostedRepository) {
		reference := strings.TrimSuffix(strings.TrimSpace(params.Reference), "/")
		if repo.Format != repository.FormatConan || repo.Type == repository.RepositoryTypeProxy || !validConanReferencePrefix(reference) || strings.Count(reference, "/") != 3 {
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
		after, err := h.decodeConanRevisionCursor(pageToken, repo.ID, reference, query)
		if err != nil {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_page_token", "page token is invalid or expired")
			return
		}
		revisions, err := h.conan.SearchConanRecipeRevisions(r.Context(), repo.ID, reference, query, pageSize+1, after)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list Conan recipe revisions failed")
			return
		}
		var next *string
		if len(revisions) > pageSize {
			revisions = revisions[:pageSize]
			token := h.encodeConanRevisionCursor(repo.ID, reference, query, revisions[len(revisions)-1].Revision)
			next = &token
		}
		items := make([]adminopenapi.ConanRecipeRevision, 0, len(revisions))
		for _, revision := range revisions {
			items = append(items, adminopenapi.ConanRecipeRevision{Reference: revision.Reference, Revision: revision.Revision, Digest: revision.Digest, State: adminopenapi.ConanRecipeRevisionState(revision.State), CreatedAt: revision.CreatedAt})
		}
		writeNativeMavenJSON(w, http.StatusOK, adminopenapi.ConanRecipeRevisionList{Items: items, NextPageToken: next})
	})
}

func (h generatedRepositoryAPIAdapter) ListConanPackageRevisions(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.ListConanPackageRevisionsParams) {
	h.withRepositoryBrowseScope(w, r, repositoryID.String(), func(_ Principal, repo repository.HostedRepository) {
		reference, recipeRevision, packageID := strings.TrimSuffix(strings.TrimSpace(params.Reference), "/"), strings.TrimSpace(params.RecipeRevision), strings.TrimSpace(params.PackageId)
		if repo.Format != repository.FormatConan || repo.Type == repository.RepositoryTypeProxy || !validConanReferencePrefix(reference) || strings.Count(reference, "/") != 3 || !validConanSegment(recipeRevision) || !validConanSegment(packageID) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "reference, recipeRevision, and packageId must identify a Conan package")
			return
		}
		revisions, err := h.conan.ListConanPackageRevisions(r.Context(), repo.ID, reference, recipeRevision, packageID)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list Conan package revisions failed")
			return
		}
		items := make([]adminopenapi.ConanPackageRevision, 0, len(revisions))
		for _, revision := range revisions {
			items = append(items, adminopenapi.ConanPackageRevision{Reference: revision.Reference, RecipeRevision: revision.RecipeRevision, PackageId: revision.PackageID, Revision: revision.Revision, Digest: revision.Digest, State: adminopenapi.ConanPackageRevisionState(revision.State), CreatedAt: revision.CreatedAt})
		}
		writeNativeMavenJSON(w, http.StatusOK, adminopenapi.ConanPackageRevisionList{Items: items})
	})
}

func (h generatedRepositoryAPIAdapter) ListConanPackageIds(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.ListConanPackageIdsParams) {
	h.withRepositoryBrowseScope(w, r, repositoryID.String(), func(_ Principal, repo repository.HostedRepository) {
		reference, recipeRevision := strings.TrimSuffix(strings.TrimSpace(params.Reference), "/"), strings.TrimSpace(params.RecipeRevision)
		if repo.Format != repository.FormatConan || repo.Type == repository.RepositoryTypeProxy || !validConanReferencePrefix(reference) || strings.Count(reference, "/") != 3 || !validConanSegment(recipeRevision) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "reference and recipeRevision must identify a Conan recipe revision")
			return
		}
		items, err := h.conan.ListConanPackageIDs(r.Context(), repo.ID, reference, recipeRevision)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list Conan package IDs failed")
			return
		}
		if items == nil {
			items = []string{}
		}
		writeNativeMavenJSON(w, http.StatusOK, adminopenapi.ConanPackageIdList{Items: items})
	})
}

func (h generatedRepositoryAPIAdapter) DeleteConanPackageRevision(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, revision string, params adminopenapi.DeleteConanPackageRevisionParams) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryWrite, func(principal Principal, repo repository.HostedRepository) {
		reference, recipeRevision, packageID := strings.TrimSuffix(strings.TrimSpace(params.Reference), "/"), strings.TrimSpace(params.RecipeRevision), strings.TrimSpace(params.PackageId)
		if repo.Format != repository.FormatConan || repo.Type == repository.RepositoryTypeProxy || !validConanReferencePrefix(reference) || strings.Count(reference, "/") != 3 || !validConanSegment(recipeRevision) || !validConanSegment(packageID) || !validConanSegment(revision) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "reference, recipeRevision, packageId, and revision must identify a Conan package revision")
			return
		}
		if _, err := h.conan.TombstoneConanPackageRevision(r.Context(), repo.ID, reference, recipeRevision, packageID, revision); errors.Is(err, repository.ErrNotFound) {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "Conan package revision not found")
			return
		} else if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "tombstone Conan package revision failed")
			return
		}
		_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{Repository: repo.Name, GroupName: repo.Name, Actor: principal.Actor, Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(), Format: "management", Resource: reference + "#" + recipeRevision + "/" + packageID + "#" + revision, Operation: "conan.package_revision.tombstone", Status: http.StatusNoContent})
		w.WriteHeader(http.StatusNoContent)
	})
}

func (h generatedRepositoryAPIAdapter) ListProxyCacheEntries(w http.ResponseWriter, r *http.Request, _ adminopenapi.RepositoryId, _ adminopenapi.ListProxyCacheEntriesParams) {
	h.proxyCache.ServeHTTP(w, r)
}

func (h generatedRepositoryAPIAdapter) InvalidateProxyCache(w http.ResponseWriter, r *http.Request, _ adminopenapi.RepositoryId) {
	h.proxyCache.Invalidate(w, r)
}

func (h generatedRepositoryAPIAdapter) ClearProxyNegativeCache(w http.ResponseWriter, r *http.Request, _ adminopenapi.RepositoryId) {
	h.proxyCache.ClearNegative(w, r)
}

func (h generatedRepositoryAPIAdapter) RefreshProxyCache(w http.ResponseWriter, r *http.Request, _ adminopenapi.RepositoryId) {
	h.mavenProxy.Refresh(w, r)
}

func (h generatedRepositoryAPIAdapter) GetProxyHealth(w http.ResponseWriter, r *http.Request, _ adminopenapi.RepositoryId) {
	h.mavenProxy.Health(w, r)
}

func (h generatedRepositoryAPIAdapter) GetArtifact(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, artifactID uuid.UUID) {
	h.withRepositoryBrowseScope(w, r, repositoryID.String(), func(Principal, repository.HostedRepository) {
		h.sessions.getArtifact(w, r, repositoryID.String(), artifactID.String())
	})
}

func (h generatedRepositoryAPIAdapter) DeleteArtifact(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, artifactID uuid.UUID) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryWrite, func(Principal, repository.HostedRepository) {
		h.sessions.deleteArtifact(w, r, repositoryID.String(), artifactID.String())
	})
}

func (h generatedRepositoryAPIAdapter) GetPublishSession(w http.ResponseWriter, r *http.Request, sessionID adminopenapi.SessionId) {
	h.withSessionScope(w, r, sessionID.String(), RepositoryRead, func(Principal) {
		h.sessions.getSession(w, r, sessionID.String())
	})
}

func (h generatedRepositoryAPIAdapter) UploadPublishObject(w http.ResponseWriter, r *http.Request, sessionID adminopenapi.SessionId, objectName string) {
	h.withSessionScope(w, r, sessionID.String(), RepositoryWrite, func(Principal) {
		h.sessions.upload(w, r, sessionID.String(), objectName)
	})
}

func (h generatedRepositoryAPIAdapter) CommitPublishSession(w http.ResponseWriter, r *http.Request, sessionID adminopenapi.SessionId) {
	h.withSessionScope(w, r, sessionID.String(), RepositoryWrite, func(Principal) {
		h.sessions.commit(w, r, sessionID.String())
	})
}
