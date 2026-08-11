package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func normalizeAndValidateRetentionPolicy(policy *repository.RepositoryRetentionPolicy) error {
	if policy.KeepDays < 1 || policy.KeepDays > 36500 {
		return errors.New("keepDays must be between 1 and 36500")
	}
	if policy.SnapshotKeepDays == 0 {
		policy.SnapshotKeepDays = policy.KeepDays
	}
	if policy.SnapshotKeepDays < 1 || policy.SnapshotKeepDays > 36500 {
		return errors.New("snapshotKeepDays must be between 1 and 36500")
	}
	if policy.MinimumVersions < 1 || policy.MinimumVersions > 100000 {
		return errors.New("minimumVersions must be between 1 and 100000")
	}
	if policy.MaximumVersions < 0 || policy.MaximumVersions > 100000 {
		return errors.New("maximumVersions must be between 0 and 100000")
	}
	if policy.MaximumVersions > 0 && policy.MaximumVersions < policy.MinimumVersions {
		return errors.New("maximumVersions must be zero or greater than or equal to minimumVersions")
	}
	var err error
	policy.CoordinatePatterns, err = normalizeRetentionPatterns(policy.CoordinatePatterns)
	if err != nil {
		return fmt.Errorf("coordinatePatterns %w", err)
	}
	policy.ProtectedPatterns, err = normalizeRetentionPatterns(policy.ProtectedPatterns)
	if err != nil {
		return fmt.Errorf("protectedPatterns %w", err)
	}
	return nil
}

func normalizeRetentionPatterns(patterns []string) ([]string, error) {
	if len(patterns) > 20 {
		return nil, errors.New("must contain at most 20 regular expressions")
	}
	result := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" || len(pattern) > 256 {
			return nil, errors.New("must contain non-empty expressions of at most 256 characters")
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return nil, fmt.Errorf("contains invalid regular expression %q", pattern)
		}
		result = append(result, pattern)
	}
	return result, nil
}

func normalizeAndValidateSecurityPolicy(policy *repository.RepositorySecurityPolicy) error {
	if policy.Version == "" {
		return errors.New("version must be valid")
	}
	if policy.MaxAllowedSeverity == "" {
		policy.MaxAllowedSeverity = repository.SecuritySeverityCritical
	}
	if !repository.ValidSecuritySeverity(policy.MaxAllowedSeverity) {
		return errors.New("maxAllowedSeverity must be none, low, medium, high, or critical")
	}
	if len(policy.AllowedLicenses) > 100 {
		return errors.New("allowedLicenses must contain at most 100 entries")
	}
	licenses := make([]string, 0, len(policy.AllowedLicenses))
	seen := make(map[string]struct{}, len(policy.AllowedLicenses))
	for _, license := range policy.AllowedLicenses {
		license = strings.TrimSpace(license)
		if license == "" || len(license) > 128 {
			return errors.New("allowedLicenses must contain non-empty values of at most 128 characters")
		}
		key := strings.ToLower(license)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		licenses = append(licenses, license)
	}
	policy.AllowedLicenses = licenses
	return nil
}

func securityPolicyResponse(policy repository.RepositorySecurityPolicy) adminopenapi.SecurityPolicy {
	enabled := policy.Enabled
	autoScanOnPublish := policy.AutoScanOnPublish
	requireSignature := policy.RequireSignature
	requireVerifiedSignature := policy.RequireVerifiedSignature
	requireSBOM := policy.RequireSBOM
	requireProvenance := policy.RequireProvenance
	requireVulnerabilityScan := policy.RequireVulnerabilityScan
	failOnScanError := policy.FailOnScanError
	severity := adminopenapi.SecurityPolicyMaxAllowedSeverity(policy.MaxAllowedSeverity)
	licenses := append([]string{}, policy.AllowedLicenses...)
	return adminopenapi.SecurityPolicy{
		Version:                  policy.Version,
		Enabled:                  &enabled,
		AutoScanOnPublish:        &autoScanOnPublish,
		RequireSignature:         &requireSignature,
		RequireVerifiedSignature: &requireVerifiedSignature,
		RequireSbom:              &requireSBOM,
		RequireProvenance:        &requireProvenance,
		RequireVulnerabilityScan: &requireVulnerabilityScan,
		MaxAllowedSeverity:       &severity,
		FailOnScanError:          &failOnScanError,
		AllowedLicenses:          &licenses,
	}
}

func repositorySecurityPolicyFromRequest(input adminopenapi.SecurityPolicy) (repository.RepositorySecurityPolicy, error) {
	policy := repository.DefaultRepositorySecurityPolicy()
	policy.Version = strings.TrimSpace(input.Version)
	if input.Enabled != nil {
		policy.Enabled = *input.Enabled
	}
	if input.AutoScanOnPublish != nil {
		policy.AutoScanOnPublish = *input.AutoScanOnPublish
	}
	if input.RequireSignature != nil {
		policy.RequireSignature = *input.RequireSignature
	}
	if input.RequireVerifiedSignature != nil {
		policy.RequireVerifiedSignature = *input.RequireVerifiedSignature
	}
	if input.RequireSbom != nil {
		policy.RequireSBOM = *input.RequireSbom
	}
	if input.RequireProvenance != nil {
		policy.RequireProvenance = *input.RequireProvenance
	}
	if input.RequireVulnerabilityScan != nil {
		policy.RequireVulnerabilityScan = *input.RequireVulnerabilityScan
	}
	if input.MaxAllowedSeverity != nil {
		policy.MaxAllowedSeverity = string(*input.MaxAllowedSeverity)
	}
	if input.FailOnScanError != nil {
		policy.FailOnScanError = *input.FailOnScanError
	}
	if input.AllowedLicenses != nil {
		policy.AllowedLicenses = append([]string{}, (*input.AllowedLicenses)...)
	}
	if err := normalizeAndValidateSecurityPolicy(&policy); err != nil {
		return repository.RepositorySecurityPolicy{}, err
	}
	return policy, nil
}

func securityPolicyEvaluationResponse(value repository.SecurityPolicyEvaluation) adminopenapi.SecurityPolicyEvaluation {
	return adminopenapi.SecurityPolicyEvaluation{
		Allowed:             value.Allowed,
		Enforced:            value.Enforced,
		PolicyVersion:       value.PolicyVersion,
		IntelligencePresent: value.IntelligencePresent,
		Reasons:             append([]string{}, value.Reasons...),
	}
}

func artifactSearchItemMatchesCanonicalIdentity(format repository.Format, item repository.ArtifactSearchItem, coordinate, digest string) bool {
	if item.Digest != digest {
		return false
	}
	switch format {
	case repository.FormatMaven, repository.FormatOCI, repository.FormatRaw, repository.FormatAPT:
		return item.Coordinate == coordinate
	case repository.FormatNPM, repository.FormatPyPI, repository.FormatGo:
		return item.Coordinate != "" && item.Version != "" && item.Coordinate+"@"+item.Version == coordinate
	default:
		return false
	}
}

func nativeVersionedArtifactVisible(ctx context.Context, store repository.ArtifactSearchStore, repositoryID string, format repository.Format, coordinate, digest string) (visible, handled bool, err error) {
	switch format {
	case repository.FormatNPM:
		native, ok := store.(repository.NativeNPMStore)
		if !ok {
			return false, false, nil
		}
		packageName, version, ok := parseNPMVersionCoordinate(coordinate)
		if !ok {
			return false, true, nil
		}
		item, err := native.GetNPMVersion(ctx, repositoryID, packageName, version)
		if errors.Is(err, repository.ErrNotFound) {
			return false, true, nil
		}
		if err != nil {
			return false, true, err
		}
		return item.RepositoryID == repositoryID && item.PackageName == packageName && item.Version == version && item.Digest == digest && item.State == "visible", true, nil
	case repository.FormatPyPI:
		native, ok := store.(repository.NativePyPIStore)
		if !ok {
			return false, false, nil
		}
		project, version, ok := parsePyPIVersionCoordinate(coordinate)
		if !ok {
			return false, true, nil
		}
		files, err := native.ListPyPIProjectFiles(ctx, repositoryID, project)
		if errors.Is(err, repository.ErrNotFound) {
			return false, true, nil
		}
		if err != nil {
			return false, true, err
		}
		for _, file := range files {
			if file.RepositoryID == repositoryID && file.Project == project && file.Version == version && file.Digest == digest && file.State == "visible" {
				return true, true, nil
			}
		}
		return false, true, nil
	case repository.FormatGo:
		native, ok := store.(repository.NativeGoStore)
		if !ok {
			return false, false, nil
		}
		module, version, ok := splitVersionCoordinate(coordinate)
		if !ok {
			return false, true, nil
		}
		item, err := native.GetGoModuleVersion(ctx, repositoryID, module, version)
		if errors.Is(err, repository.ErrNotFound) {
			return false, true, nil
		}
		if err != nil {
			return false, true, err
		}
		if item.RepositoryID != repositoryID || item.Module != module || item.Version != version {
			return false, true, nil
		}
		for _, kind := range []string{"info", "mod", "zip"} {
			asset, assetErr := native.GetGoModuleAsset(ctx, repositoryID, module, version, kind)
			if errors.Is(assetErr, repository.ErrNotFound) {
				continue
			}
			if assetErr != nil {
				return false, true, assetErr
			}
			if asset.RepositoryID == repositoryID && asset.Module == module && asset.Version == version && asset.Kind == kind && asset.Digest == digest {
				return true, true, nil
			}
		}
		return false, true, nil
	default:
		return false, false, nil
	}
}

func securityPolicyArtifactVisible(ctx context.Context, store repository.ArtifactSearchStore, repositoryID string, format repository.Format, coordinate, digest string) (bool, error) {
	if format == repository.FormatConan {
		conanStore, ok := store.(repository.NativeConanStore)
		if !ok {
			return false, nil
		}
		reference, recipeRevision, packageID, packageRevision, packageCoordinate, ok := parseConanRestoreCoordinate(coordinate)
		if !ok {
			return false, nil
		}
		if packageCoordinate {
			item, err := conanStore.GetConanPackageRevision(ctx, repositoryID, reference, recipeRevision, packageID, packageRevision)
			if errors.Is(err, repository.ErrNotFound) {
				return false, nil
			}
			return err == nil && item.State == "visible" && item.Digest == digest, err
		}
		item, err := conanStore.GetConanRecipeRevision(ctx, repositoryID, reference, recipeRevision)
		if errors.Is(err, repository.ErrNotFound) {
			return false, nil
		}
		return err == nil && item.State == "visible" && item.Digest == digest, err
	}
	if visible, handled, err := nativeVersionedArtifactVisible(ctx, store, repositoryID, format, coordinate, digest); handled {
		return visible, err
	}

	const pageSize = 200
	after := repository.ArtifactSearchPosition{}
	for {
		items, err := store.SearchArtifactProjection(ctx, repositoryID, format, repository.ArtifactSearchQuery{Mode: repository.ArtifactSearchByDigest, Value: digest}, pageSize, after)
		if err != nil {
			return false, err
		}
		for _, item := range items {
			if artifactSearchItemMatchesCanonicalIdentity(format, item, coordinate, digest) {
				return true, nil
			}
		}
		if len(items) < pageSize {
			return false, nil
		}
		last := items[len(items)-1]
		next := repository.ArtifactSearchPosition{Coordinate: last.Coordinate, BuildNumber: last.BuildNumber, Digest: last.Digest}
		if next == after {
			return false, errors.New("artifact search projection did not advance")
		}
		after = next
	}
}

func (h generatedRepositoryAPIAdapter) GetSecurityPolicy(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryAdmin, func(_ Principal, _ repository.HostedRepository) {
		policy, err := h.securityPolicies.GetRepositorySecurityPolicy(r.Context(), repositoryID.String())
		if errors.Is(err, repository.ErrNotFound) {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get security policy failed")
			return
		}
		w.Header().Set("ETag", policy.Version)
		writeNativeMavenJSON(w, http.StatusOK, securityPolicyResponse(policy))
	})
}

func (h generatedRepositoryAPIAdapter) ReplaceSecurityPolicy(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.ReplaceSecurityPolicyParams) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryAdmin, func(principal Principal, repo repository.HostedRepository) {
		var input adminopenapi.SecurityPolicy
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "security policy payload is invalid")
			return
		}
		policy, err := repositorySecurityPolicyFromRequest(input)
		if err != nil {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		updated, err := h.securityPolicies.ReplaceRepositorySecurityPolicy(r.Context(), repo.ID, policy, string(params.IfMatch))
		if errors.Is(err, repository.ErrVersionConflict) {
			writeHostedProblem(w, http.StatusPreconditionFailed, "version_conflict", "If-Match does not match current security policy version")
			return
		}
		if errors.Is(err, repository.ErrNotFound) {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "replace security policy failed")
			return
		}
		if h.audit != nil {
			_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{GroupName: repo.Name, Repository: repo.Name, Actor: principal.Actor, Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(), Format: "management", Resource: "repositories/" + repo.ID + "/security-policy", Operation: "security_policy.replace", Status: http.StatusOK, CacheDisposition: "bypass"})
		}
		w.Header().Set("ETag", updated.Version)
		writeNativeMavenJSON(w, http.StatusOK, securityPolicyResponse(updated))
	})
}

func (h generatedRepositoryAPIAdapter) EvaluateSecurityPolicy(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryAdmin, func(principal Principal, target repository.HostedRepository) {
		var input adminopenapi.SecurityPolicyEvaluationRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || input.SourceRepositoryId == uuid.Nil {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "sourceRepositoryId, coordinate, and digest are required")
			return
		}
		source, err := h.store.GetHostedRepository(r.Context(), input.SourceRepositoryId.String())
		if errors.Is(err, repository.ErrNotFound) {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "source repository not found")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get source repository failed")
			return
		}
		if source.Format != target.Format || source.State != repository.RepositoryActive {
			writeHostedProblem(w, http.StatusConflict, "invalid_source", "source must be an active repository with the same format")
			return
		}
		if decision := h.authorizer.Authorize(r.Context(), principal, source, RepositoryRead); !decision.Allowed {
			h.recordAuthorizationDenial(r, principal, source, RepositoryRead, decision)
			writeHostedProblem(w, http.StatusForbidden, "access_denied", "source repository read scope is required")
			return
		}
		coordinate := strings.TrimSpace(input.Coordinate)
		digest := strings.TrimSpace(input.Digest)
		if !validArtifactIntelligenceIdentity(target.Format, coordinate, digest) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "coordinate and digest must identify an artifact")
			return
		}
		if h.searchProjection == nil {
			writeHostedProblem(w, http.StatusNotImplemented, "not_supported", "artifact search projection is unavailable")
			return
		}
		visible, err := securityPolicyArtifactVisible(r.Context(), h.searchProjection, source.ID, source.Format, coordinate, digest)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "verify source artifact failed")
			return
		}
		if !visible {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "source artifact not found")
			return
		}
		policy, err := h.securityPolicies.GetRepositorySecurityPolicy(r.Context(), target.ID)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get security policy failed")
			return
		}
		var intelligence *repository.ArtifactIntelligence
		value, intelligenceErr := h.intelligence.GetArtifactIntelligence(r.Context(), source.ID, source.Format, coordinate, digest)
		if intelligenceErr == nil {
			intelligence = &value
		} else if !errors.Is(intelligenceErr, repository.ErrNotFound) {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get artifact intelligence failed")
			return
		}
		evaluation := repository.EvaluateRepositorySecurityPolicy(policy, intelligence)
		distributionDigests, quarantineErr := h.artifactDistributionDigests(r.Context(), source, coordinate, digest)
		if quarantineErr != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "evaluate distribution artifact identities failed")
			return
		}
		quarantineAllowed, quarantineErr := repository.ArtifactDistributionAllowedForDigests(r.Context(), h.quarantine, source.ID, source.Format, coordinate, distributionDigests)
		if quarantineErr != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "evaluate artifact quarantine failed")
			return
		}
		if !quarantineAllowed {
			evaluation.Allowed = false
			evaluation.Enforced = true
			evaluation.Reasons = []string{repository.ArtifactQuarantinedReason}
		}
		if !evaluation.Allowed && h.audit != nil {
			_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{GroupName: target.Name, Repository: target.Name, Actor: principal.Actor, Outcome: repository.AuditAccessDenied, OccurredAt: time.Now().UTC(), Format: "management", Resource: coordinate, Representation: digest, Operation: "security_policy.evaluate", Status: http.StatusOK, AuthorizationReason: strings.Join(evaluation.Reasons, ",")})
		}
		writeNativeMavenJSON(w, http.StatusOK, securityPolicyEvaluationResponse(evaluation))
	})
}

func (h generatedRepositoryAPIAdapter) GetRetentionPolicy(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryRead, func(_ Principal, repo repository.HostedRepository) {
		if repo.Type != repository.RepositoryTypeHosted || !supportsRepositoryRetention(repo.Format) {
			writeHostedProblem(w, http.StatusConflict, "unsupported_operation", "retention policies are supported for Maven, OCI, Conan, and Raw hosted repositories")
			return
		}
		policy, err := h.retentionPolicies.GetRepositoryRetentionPolicy(r.Context(), repositoryID.String())
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get retention policy failed")
			return
		}
		writeNativeMavenJSON(w, http.StatusOK, policy)
	})
}

func (h generatedRepositoryAPIAdapter) ReplaceRetentionPolicy(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.ReplaceRetentionPolicyParams) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryAdmin, func(_ Principal, repo repository.HostedRepository) {
		if repo.Type != repository.RepositoryTypeHosted || !supportsRepositoryRetention(repo.Format) {
			writeHostedProblem(w, http.StatusConflict, "unsupported_operation", "retention policies are supported for Maven, OCI, Conan, and Raw hosted repositories")
			return
		}
		var policy repository.RepositoryRetentionPolicy
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&policy); err != nil || policy.Version == "" {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "version must be valid")
			return
		}
		if err := normalizeAndValidateRetentionPolicy(&policy); err != nil {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		updated, err := h.retentionPolicies.ReplaceRepositoryRetentionPolicy(r.Context(), repositoryID.String(), policy, string(params.IfMatch))
		if errors.Is(err, repository.ErrNotFound) {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
			return
		}
		if errors.Is(err, repository.ErrVersionConflict) {
			writeHostedProblem(w, http.StatusPreconditionFailed, "version_conflict", "If-Match does not match current version")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "replace retention policy failed")
			return
		}
		writeNativeMavenJSON(w, http.StatusOK, updated)
	})
}

func (h generatedRepositoryAPIAdapter) GetRepositoryCapacity(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryRead, func(_ Principal, repo repository.HostedRepository) {
		capacity, err := h.capacities.GetRepositoryCapacity(r.Context(), repositoryID.String())
		if errors.Is(err, repository.ErrNotFound) {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get repository capacity failed")
			return
		}
		if repo.Type == repository.RepositoryTypeProxy && h.maintenance != nil {
			// Proxy repositories do not own Hosted artifacts, but they do own
			// read-through cache bytes. Keep quota from the capacity store and
			// replace usage with live cache usage so the Console does not show 0.
			if proxyCapacity, err := (proxyCacheBrowseHandler{store: h.store, maintenance: h.maintenance, authenticator: h.authenticator, authorizer: h.authorizer}).proxyCacheCapacity(r.Context(), repo, capacity); err == nil {
				capacity = proxyCapacity
			}
		}
		writeNativeMavenJSON(w, http.StatusOK, capacity)
	})
}

func (h generatedRepositoryAPIAdapter) ListRepositoryCapacities(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	store, ok := h.capacities.(repository.RepositoryCapacityRecordStore)
	if !ok {
		writeHostedProblem(w, http.StatusNotImplemented, "not_supported", "repository capacity aggregation is unavailable")
		return
	}
	records, err := store.ListRepositoryCapacityRecords(r.Context())
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list repository capacities failed")
		return
	}
	proxyCapacities, proxyErr := (proxyCacheBrowseHandler{store: h.store, maintenance: h.maintenance, authenticator: h.authenticator, authorizer: h.authorizer}).proxyCacheCapacities(r.Context(), records)
	items := make(adminopenapi.RepositoryCapacityList, 0, len(records))
	for _, record := range records {
		capacity := record.Capacity
		if proxyErr == nil {
			capacity = proxyCapacities[capacity.RepositoryID]
		}
		item := adminopenapi.RepositoryCapacity{
			RepositoryId: uuid.MustParse(capacity.RepositoryID),
			Format:       adminopenapi.Format(capacity.Format),
			UsedBytes:    capacity.UsedBytes,
			ObjectCount:  capacity.ObjectCount,
			QuotaBytes:   capacity.QuotaBytes,
		}
		if capacity.PrimaryBytes != 0 {
			item.PrimaryBytes = &capacity.PrimaryBytes
		}
		if capacity.SidecarBytes != 0 {
			item.SidecarBytes = &capacity.SidecarBytes
		}
		if capacity.NegativeCount != 0 {
			item.NegativeCount = &capacity.NegativeCount
		}
		if capacity.ExpiredObjectCount != 0 {
			item.ExpiredObjectCount = &capacity.ExpiredObjectCount
		}
		if capacity.ReclaimableBytes != 0 {
			item.ReclaimableBytes = &capacity.ReclaimableBytes
		}
		items = append(items, item)
	}
	writeNativeMavenJSON(w, http.StatusOK, items)
}

func (h generatedRepositoryAPIAdapter) ReplaceRepositoryCapacity(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryAdmin, func(principal Principal, repo repository.HostedRepository) {
		var request adminopenapi.RepositoryCapacityQuota
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || request.QuotaBytes < 0 {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "quotaBytes must be a non-negative integer")
			return
		}
		capacity, err := h.capacities.ReplaceRepositoryCapacityQuota(r.Context(), repositoryID.String(), request.QuotaBytes)
		if repository.IsQuotaExceeded(err) {
			writeHostedProblem(w, http.StatusConflict, "quota_exceeded", "quotaBytes is lower than current repository usage")
			return
		}
		if errors.Is(err, repository.ErrNotFound) {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "replace repository capacity failed")
			return
		}
		if h.audit != nil {
			_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{GroupName: repo.Name, Repository: repo.Name, Actor: principal.Actor, Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(), Format: "management", Resource: "repositories/" + repo.ID + "/capacity", Operation: "capacity.configure", Status: http.StatusOK, CacheDisposition: "bypass"})
		}
		writeNativeMavenJSON(w, http.StatusOK, capacity)
	})
}
