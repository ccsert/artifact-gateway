package app

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	conanprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/conan"
	mavenprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/maven"
	npmprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/npm"
	ociprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/oci"
	rawprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/raw"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func (h generatedRepositoryAPIAdapter) CreateRepositoryPromotion(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.CreateRepositoryPromotionParams) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryAdmin, func(principal Principal, source repository.HostedRepository) {
		if source.Format != repository.FormatMaven && source.Format != repository.FormatOCI && source.Format != repository.FormatRaw && source.Format != repository.FormatConan && source.Format != repository.FormatNPM && source.Format != repository.FormatPyPI {
			writeHostedProblem(w, http.StatusConflict, "unsupported_operation", "promotion is not supported for this repository format")
			return
		}
		var request adminopenapi.PromotionRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&request); err != nil || !validRepositoryDigest(request.Digest) || (source.Format == repository.FormatMaven && !validMavenCoordinate(request.Coordinate)) || (source.Format == repository.FormatOCI && (request.Coordinate == "" || strings.Contains(request.Coordinate, "@"))) || (source.Format == repository.FormatRaw && strings.Trim(request.Coordinate, "/") == "") || (source.Format == repository.FormatConan && !validConanReplicationCoordinate(request.Coordinate)) || (source.Format == repository.FormatNPM && !validNPMVersionCoordinate(request.Coordinate)) || (source.Format == repository.FormatPyPI && !validPyPIVersionCoordinate(request.Coordinate)) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "targetRepositoryId, immutable artifact coordinate, and digest are required")
			return
		}
		h.withRepositoryScopeForPrincipal(w, r, principal, request.TargetRepositoryId.String(), RepositoryAdmin, func(Principal) {
			target, err := h.sessions.store.GetHostedRepository(r.Context(), request.TargetRepositoryId.String())
			if err != nil || target.ID == source.ID || target.Format != source.Format || target.State != repository.RepositoryActive {
				writeHostedProblem(w, http.StatusConflict, "invalid_target", "target must be an active repository with the same format")
				return
			}
			if h.rejectQuarantinedDistribution(w, r, principal, source, target, request.Coordinate, request.Digest, "promote") {
				return
			}
			policy, policyErr := h.securityPolicies.GetRepositorySecurityPolicy(r.Context(), target.ID)
			if policyErr != nil {
				writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get security policy failed")
				return
			}
			var intelligence *repository.ArtifactIntelligence
			if value, intelligenceErr := h.intelligence.GetArtifactIntelligence(r.Context(), source.ID, source.Format, request.Coordinate, request.Digest); intelligenceErr == nil {
				intelligence = &value
			} else if !errors.Is(intelligenceErr, repository.ErrNotFound) {
				writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get artifact intelligence failed")
				return
			}
			evaluation := repository.EvaluateRepositorySecurityPolicy(policy, intelligence)
			if !evaluation.Allowed {
				if h.audit != nil {
					_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{GroupName: target.Name, Repository: target.Name, Actor: principal.Actor, Outcome: repository.AuditAccessDenied, OccurredAt: time.Now().UTC(), Format: "management", Resource: request.Coordinate, Representation: request.Digest, Operation: "promote.security_policy", Status: http.StatusForbidden, AuthorizationReason: strings.Join(evaluation.Reasons, ",")})
				}
				writeHostedProblem(w, http.StatusForbidden, "security_policy_denied", "promotion denied by security policy: "+strings.Join(evaluation.Reasons, ","))
				return
			}
			var job repository.LifecycleJob
			switch source.Format {
			case repository.FormatMaven:
				promotionID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("maven-promotion:"+target.ID+":"+string(params.IdempotencyKey))).String()
				job, _, err = (mavenprotocol.NativePromotion{Store: h.sessions.store}).Enqueue(r.Context(), target.ID, string(params.IdempotencyKey), mavenprotocol.PromotionPayload{SourceRepositoryID: source.ID, Coordinate: request.Coordinate, Digest: request.Digest, PromotionID: promotionID})
			case repository.FormatOCI:
				job, _, err = (ociprotocol.NativePromotion{Store: h.sessions.store}).Enqueue(r.Context(), target.ID, string(params.IdempotencyKey), ociprotocol.PromotionPayload{SourceRepositoryID: source.ID, Name: request.Coordinate, Digest: request.Digest})
			case repository.FormatRaw:
				job, _, err = (rawprotocol.NativePromotion{Store: h.sessions.store}).Enqueue(r.Context(), target.ID, string(params.IdempotencyKey), rawprotocol.PromotionPayload{SourceRepositoryID: source.ID, Path: request.Coordinate, Digest: request.Digest})
			case repository.FormatConan:
				reference, revision, _ := strings.Cut(request.Coordinate, "#")
				job, _, err = (conanprotocol.NativePromotion{Store: h.sessions.store}).Enqueue(r.Context(), target.ID, string(params.IdempotencyKey), conanprotocol.PromotionPayload{SourceRepositoryID: source.ID, Reference: reference, Revision: revision, Digest: request.Digest})
			case repository.FormatNPM:
				packageName, version, _ := parseNPMVersionCoordinate(request.Coordinate)
				job, _, err = (npmprotocol.NativePromotion{Store: h.sessions.store}).Enqueue(r.Context(), target.ID, string(params.IdempotencyKey), npmprotocol.PromotionPayload{SourceRepositoryID: source.ID, PackageName: packageName, Version: version, Digest: request.Digest})
			case repository.FormatPyPI:
				project, version, _ := parsePyPIVersionCoordinate(request.Coordinate)
				job, _, err = (NativePyPIPromotion{Store: h.sessions.store}).Enqueue(r.Context(), target.ID, string(params.IdempotencyKey), PyPIPromotionPayload{SourceRepositoryID: source.ID, Project: project, Version: version, Digest: request.Digest})
			}
			if errors.Is(err, repository.ErrIdempotencyConflict) {
				writeHostedProblem(w, http.StatusConflict, "idempotency_conflict", "Idempotency-Key conflicts with an existing promotion job")
				return
			}
			if err != nil {
				writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "enqueue promotion job failed")
				return
			}
			_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{Repository: source.Name, GroupName: source.Name, Actor: principal.Actor, Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(), Format: "management", Resource: request.Coordinate, Operation: "promote", Status: http.StatusAccepted})
			writeNativeMavenJSON(w, http.StatusAccepted, lifecycleJobResponse(job))
		})
	})
}

func (h generatedRepositoryAPIAdapter) CreateRepositoryReplication(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.CreateRepositoryReplicationParams) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryAdmin, func(principal Principal, source repository.HostedRepository) {
		var request adminopenapi.ReplicationRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || !repository.FormatSupportsOperation(source.Format, source.Type, repository.RepositoryOperationReplicate) || strings.TrimSpace(request.Coordinate) == "" || !validRepositoryDigest(request.Digest) || (source.Format == repository.FormatMaven && !validMavenCoordinate(request.Coordinate)) || (source.Format == repository.FormatConan && !validConanReplicationCoordinate(request.Coordinate)) || (source.Format == repository.FormatNPM && !validNPMVersionCoordinate(request.Coordinate)) || (source.Format == repository.FormatPyPI && !validPyPIVersionCoordinate(request.Coordinate)) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "replication requires a visible format-specific coordinate and sha256 digest")
			return
		}
		h.withRepositoryScopeForPrincipal(w, r, principal, request.TargetRepositoryId.String(), RepositoryAdmin, func(Principal) {
			target, err := h.store.GetHostedRepository(r.Context(), request.TargetRepositoryId.String())
			if err != nil || target.ID == source.ID || target.Format != source.Format || target.State != repository.RepositoryActive {
				writeHostedProblem(w, http.StatusConflict, "invalid_target", "target must be an active repository with the same format")
				return
			}
			if h.rejectQuarantinedDistribution(w, r, principal, source, target, request.Coordinate, request.Digest, "replicate") {
				return
			}
			format := source.Format
			var checkpoints []repository.ReplicationCheckpoint
			if format == repository.FormatRaw {
				asset, lookupErr := h.sessions.store.GetRawAsset(r.Context(), source.ID, request.Coordinate)
				if errors.Is(lookupErr, repository.ErrNotFound) || asset.Digest != request.Digest {
					writeHostedProblem(w, http.StatusNotFound, "not_found", "source Raw artifact is unavailable")
					return
				}
				if lookupErr != nil {
					writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "lookup source Raw artifact failed")
					return
				}
				checkpoints = []repository.ReplicationCheckpoint{{ObjectKey: asset.ObjectKey, Digest: asset.Digest, Size: asset.Size}}
			} else if format == repository.FormatMaven {
				artifact, lookupErr := h.sessions.store.GetMavenArtifactByCoordinate(r.Context(), source.ID, request.Coordinate)
				if errors.Is(lookupErr, repository.ErrNotFound) || artifact.Digest != request.Digest {
					writeHostedProblem(w, http.StatusNotFound, "not_found", "source Maven artifact is unavailable")
					return
				}
				if lookupErr != nil {
					writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "lookup source Maven artifact failed")
					return
				}
				assets, lookupErr := h.sessions.store.ListMavenAssets(r.Context(), source.ID, request.Coordinate)
				if lookupErr != nil || len(assets) == 0 {
					writeHostedProblem(w, http.StatusNotFound, "not_found", "source Maven assets are unavailable")
					return
				}
				checkpoints = make([]repository.ReplicationCheckpoint, 0, len(assets))
				seenDigests := make(map[string]bool, len(assets))
				for _, asset := range assets {
					key := asset.Digest + "\x00" + strconv.FormatInt(asset.Size, 10)
					if seenDigests[key] {
						continue
					}
					seenDigests[key] = true
					checkpoints = append(checkpoints, repository.ReplicationCheckpoint{SourceObjectKey: asset.ObjectKey, ObjectKey: mavenReplicationTargetObjectKey(target.ID, asset.Digest), Digest: asset.Digest, Size: asset.Size})
				}
			} else if format == repository.FormatOCI {
				manifest, lookupErr := h.sessions.store.GetOCIManifest(r.Context(), source.ID, request.Coordinate, request.Digest)
				if errors.Is(lookupErr, repository.ErrNotFound) || manifest.Digest != request.Digest {
					writeHostedProblem(w, http.StatusNotFound, "not_found", "source OCI manifest is unavailable")
					return
				}
				if lookupErr != nil {
					writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "lookup source OCI manifest failed")
					return
				}
				checkpoints = []repository.ReplicationCheckpoint{{SourceObjectKey: manifest.ObjectKey, ObjectKey: ociReplicationTargetObjectKey(target.ID, manifest.Name, manifest.Digest), Digest: manifest.Digest, Size: manifest.Size}}
			} else if format == repository.FormatNPM {
				packageName, version, _ := parseNPMVersionCoordinate(request.Coordinate)
				item, lookupErr := h.sessions.store.GetNPMVersion(r.Context(), source.ID, packageName, version)
				if errors.Is(lookupErr, repository.ErrNotFound) || item.Digest != request.Digest || item.ObjectKey == "" {
					writeHostedProblem(w, http.StatusNotFound, "not_found", "source npm version is unavailable")
					return
				}
				if lookupErr != nil {
					writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "lookup source npm version failed")
					return
				}
				checkpoints = []repository.ReplicationCheckpoint{{SourceObjectKey: item.ObjectKey, ObjectKey: npmReplicationTargetObjectKey(target.ID, item.Digest), Digest: item.Digest, Size: item.Size}}
			} else if format == repository.FormatPyPI {
				project, version, _ := parsePyPIVersionCoordinate(request.Coordinate)
				files, lookupErr := h.sessions.store.ListPyPIProjectFiles(r.Context(), source.ID, project)
				if lookupErr != nil {
					writeHostedProblem(w, http.StatusNotFound, "not_found", "source PyPI version is unavailable")
					return
				}
				digestMatched := false
				for _, file := range files {
					if file.Version != version {
						continue
					}
					if file.ObjectKey == "" {
						writeHostedProblem(w, http.StatusConflict, "not_cached", "source PyPI version is not fully cached")
						return
					}
					digestMatched = digestMatched || file.Digest == request.Digest
					checkpoints = append(checkpoints, repository.ReplicationCheckpoint{SourceObjectKey: file.ObjectKey, ObjectKey: pypiReplicationTargetObjectKey(target.ID, file.Filename, file.Digest), Digest: file.Digest, Size: file.Size})
				}
				if len(checkpoints) == 0 || !digestMatched {
					writeHostedProblem(w, http.StatusNotFound, "not_found", "source PyPI version is unavailable")
					return
				}
			} else {
				reference, revision, _ := strings.Cut(request.Coordinate, "#")
				recipe, lookupErr := h.conan.GetConanRecipeRevision(r.Context(), source.ID, reference, revision)
				if errors.Is(lookupErr, repository.ErrNotFound) || recipe.Digest != request.Digest || recipe.State != "visible" {
					writeHostedProblem(w, http.StatusNotFound, "not_found", "source Conan recipe revision is unavailable")
					return
				}
				if lookupErr != nil {
					writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "lookup source Conan recipe revision failed")
					return
				}
				checkpoints, lookupErr = conanReplicationCheckpoints(r.Context(), h.conan, source.ID, target.ID, reference, revision)
				if lookupErr != nil || len(checkpoints) == 0 {
					writeHostedProblem(w, http.StatusNotFound, "not_found", "source Conan assets are unavailable")
					return
				}
			}
			plan, _, err := h.replication.CreateReplicationPlan(r.Context(), repository.ReplicationPlan{ID: uuid.NewString(), SourceRepositoryID: source.ID, TargetRepositoryID: target.ID, Format: format, Coordinate: request.Coordinate, Digest: request.Digest, IdempotencyKey: string(params.IdempotencyKey)}, checkpoints)
			if errors.Is(err, repository.ErrIdempotencyConflict) {
				writeHostedProblem(w, http.StatusConflict, "idempotency_conflict", "Idempotency-Key conflicts with an existing replication plan")
				return
			}
			if err != nil {
				writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "create replication plan failed")
				return
			}
			_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{Repository: source.Name, GroupName: source.Name, Actor: principal.Actor, Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(), Format: "management", Resource: request.Coordinate, Operation: "replicate", Status: http.StatusAccepted})
			writeNativeMavenJSON(w, http.StatusAccepted, toOpenAPIReplicationPlan(plan))
		})
	})
}

func (h generatedRepositoryAPIAdapter) rejectQuarantinedDistribution(w http.ResponseWriter, r *http.Request, principal Principal, source, target repository.HostedRepository, coordinate, digest, operation string) bool {
	digests, err := h.artifactDistributionDigests(r.Context(), source, coordinate, digest)
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "evaluate distribution artifact identities failed")
		return true
	}
	allowed, err := repository.ArtifactDistributionAllowedForDigests(r.Context(), h.quarantine, source.ID, source.Format, coordinate, digests)
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "evaluate artifact quarantine failed")
		return true
	}
	if allowed {
		return false
	}
	if h.audit != nil {
		_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{
			GroupName: target.Name, Repository: target.Name, Actor: principal.Actor,
			Outcome: repository.AuditAccessDenied, OccurredAt: time.Now().UTC(),
			Format: string(source.Format), Resource: coordinate, Representation: digest,
			Operation: operation + ".quarantine", Status: http.StatusForbidden,
			AuthorizationReason: repository.ArtifactQuarantinedReason,
			CacheDisposition:    "bypass",
		})
	}
	writeHostedProblem(w, http.StatusForbidden, repository.ArtifactQuarantinedReason, "artifact is quarantined and cannot be distributed")
	return true
}

func validConanReplicationCoordinate(value string) bool {
	reference, revision, found := strings.Cut(value, "#")
	return found && reference != "" && revision != "" && !strings.Contains(revision, "/") && validConanPublishRequest(nativeConanPublishRequest{Kind: "recipe", Reference: reference, RecipeRevision: revision, Objects: []repository.MavenDeclaredObject{{Name: "object", Digest: "sha256:" + strings.Repeat("0", 64), Size: 1}}})
}

func (h generatedRepositoryAPIAdapter) ListRepositoryReplications(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryAdmin, func(Principal, repository.HostedRepository) {
		plans, err := h.replication.ListReplicationPlans(r.Context(), repositoryID.String(), 100)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list replication plans failed")
			return
		}
		items := make([]adminopenapi.ReplicationPlan, 0, len(plans))
		for _, plan := range plans {
			items = append(items, toOpenAPIReplicationPlan(plan))
		}
		writeNativeMavenJSON(w, http.StatusOK, items)
	})
}

func (h generatedRepositoryAPIAdapter) GetRepositoryReplication(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, replicationPlanID adminopenapi.ReplicationPlanId) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryAdmin, func(_ Principal, _ repository.HostedRepository) {
		plan, err := h.replication.GetReplicationPlan(r.Context(), repositoryID.String(), replicationPlanID.String())
		if errors.Is(err, repository.ErrNotFound) {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "replication plan not found")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get replication plan failed")
			return
		}
		checkpoints, err := h.replication.ListReplicationCheckpoints(r.Context(), plan.ID)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list replication checkpoints failed")
			return
		}
		writeNativeMavenJSON(w, http.StatusOK, toOpenAPIReplicationPlanDetail(plan, checkpoints))
	})
}

func (h generatedRepositoryAPIAdapter) DeleteRepositoryReplication(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, replicationPlanID adminopenapi.ReplicationPlanId) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryAdmin, func(_ Principal, _ repository.HostedRepository) {
		plan, err := h.replication.GetReplicationPlan(r.Context(), repositoryID.String(), replicationPlanID.String())
		if errors.Is(err, repository.ErrNotFound) {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "replication plan not found")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get replication plan failed")
			return
		}
		// Only pending or failed plans can be cancelled: the worker owns a
		// running plan mid-flight, and completed/cancelled plans are terminal.
		if plan.State != "pending" && plan.State != "failed" {
			writeHostedProblem(w, http.StatusConflict, "invalid_state", "only pending or failed replication plans can be cancelled")
			return
		}
		if err := h.replication.CancelReplicationPlan(r.Context(), repositoryID.String(), replicationPlanID.String()); err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				writeHostedProblem(w, http.StatusConflict, "invalid_state", "replication plan was claimed or completed before cancellation")
				return
			}
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "cancel replication plan failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func validRepositoryDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func validOCIRestoreCoordinate(value string) bool {
	_, _, ok := parseOCIRestoreCoordinate(value)
	return ok
}

func parseOCIRestoreCoordinate(value string) (name, digest string, ok bool) {
	if len(value) > 1024 || strings.ContainsAny(value, "\\\x00") {
		return "", "", false
	}
	name, digest, split := strings.Cut(value, "@")
	if !split || name == "" || digest == "" || strings.Contains(digest, "@") || !validOCIImagePrefix(name) || !validRepositoryDigest(digest) {
		return "", "", false
	}
	// OCI image names cannot contain empty path components. The shared prefix
	// validator intentionally accepts a trailing slash for search prefixes.
	for _, component := range strings.Split(name, "/") {
		if component == "" {
			return "", "", false
		}
	}
	return name, digest, true
}

func toOpenAPIReplicationPlan(plan repository.ReplicationPlan) adminopenapi.ReplicationPlan {
	item := adminopenapi.ReplicationPlan{Id: uuid.MustParse(plan.ID), SourceRepositoryId: uuid.MustParse(plan.SourceRepositoryID), TargetRepositoryId: uuid.MustParse(plan.TargetRepositoryID), Format: adminopenapi.Format(plan.Format), State: adminopenapi.ReplicationPlanState(plan.State), CreatedAt: plan.CreatedAt}
	if plan.Coordinate != "" && plan.Digest != "" {
		item.Coordinate = &plan.Coordinate
		item.Digest = &plan.Digest
	}
	if !plan.StartedAt.IsZero() {
		item.StartedAt = &plan.StartedAt
	}
	if !plan.CompletedAt.IsZero() {
		item.CompletedAt = &plan.CompletedAt
	}
	if plan.LastError != "" {
		item.LastError = &plan.LastError
	}
	return item
}

func toOpenAPIReplicationPlanDetail(plan repository.ReplicationPlan, checkpoints []repository.ReplicationCheckpoint) adminopenapi.ReplicationPlanDetail {
	item := adminopenapi.ReplicationPlanDetail{Id: uuid.MustParse(plan.ID), SourceRepositoryId: uuid.MustParse(plan.SourceRepositoryID), TargetRepositoryId: uuid.MustParse(plan.TargetRepositoryID), Format: adminopenapi.Format(plan.Format), State: adminopenapi.ReplicationPlanDetailState(plan.State), CreatedAt: plan.CreatedAt, Checkpoints: make([]adminopenapi.ReplicationCheckpointProgress, 0, len(checkpoints))}
	if plan.Coordinate != "" && plan.Digest != "" {
		item.Coordinate = &plan.Coordinate
		item.Digest = &plan.Digest
	}
	if !plan.StartedAt.IsZero() {
		item.StartedAt = &plan.StartedAt
	}
	if !plan.CompletedAt.IsZero() {
		item.CompletedAt = &plan.CompletedAt
	}
	if plan.LastError != "" {
		item.LastError = &plan.LastError
	}
	for _, checkpoint := range checkpoints {
		progress := adminopenapi.ReplicationCheckpointProgress{ObjectKey: checkpoint.ObjectKey, Digest: checkpoint.Digest, Size: checkpoint.Size, ByteOffset: checkpoint.ByteOffset, State: adminopenapi.ReplicationCheckpointProgressState(checkpoint.State), Attempts: checkpoint.Attempts}
		if checkpoint.LastError != "" {
			progress.LastError = &checkpoint.LastError
		}
		if !checkpoint.VerifiedAt.IsZero() {
			progress.VerifiedAt = &checkpoint.VerifiedAt
		}
		item.Checkpoints = append(item.Checkpoints, progress)
	}
	return item
}

func (h generatedRepositoryAPIAdapter) RestoreRepositoryArtifact(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryAdmin, func(_ Principal, repo repository.HostedRepository) {
		var request adminopenapi.RestoreArtifact
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || (repo.Format == repository.FormatConan && !validConanRestoreCoordinate(request.Coordinate)) || (repo.Format == repository.FormatMaven && !validMavenCoordinate(request.Coordinate)) || (repo.Format == repository.FormatOCI && !validOCIRestoreCoordinate(request.Coordinate)) || (repo.Format == repository.FormatRaw && (strings.Trim(request.Coordinate, "/") == "" || !validRawAssetPrefix(request.Coordinate))) || (repo.Format == repository.FormatNPM && !validNPMVersionCoordinate(request.Coordinate)) || (repo.Format == repository.FormatPyPI && !validPyPIVersionCoordinate(request.Coordinate)) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "coordinate must identify a supported artifact tombstone")
			return
		}
		if !repository.FormatSupportsOperation(repo.Format, repo.Type, repository.RepositoryOperationRestore) {
			writeHostedProblem(w, http.StatusConflict, "unsupported_operation", "restore is not supported for this repository format")
			return
		}
		if _, err := h.tombstones.GetArtifactTombstone(r.Context(), repo.ID, repo.Format, request.Coordinate); errors.Is(err, repository.ErrNotFound) {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "tombstone not found")
			return
		} else if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get tombstone failed")
			return
		}
		var err error
		switch repo.Format {
		case repository.FormatMaven:
			artifact, getErr := h.sessions.store.GetMavenArtifactByCoordinate(r.Context(), repo.ID, request.Coordinate)
			if getErr != nil {
				err = getErr
			} else {
				_, err = h.sessions.store.RestoreMavenArtifact(r.Context(), repo.ID, artifact.ID)
			}
		case repository.FormatConan:
			err = h.restoreConanCoordinate(r, repo.ID, request.Coordinate)
		case repository.FormatOCI:
			name, digest, _ := parseOCIRestoreCoordinate(request.Coordinate)
			_, err = h.oci.RestoreOCIManifest(r.Context(), repo.ID, name, digest)
		case repository.FormatNPM:
			name, version, _ := parseNPMVersionCoordinate(request.Coordinate)
			_, err = h.sessions.store.RestoreNPMVersion(r.Context(), repo.ID, name, version)
		case repository.FormatPyPI:
			project, version, _ := parsePyPIVersionCoordinate(request.Coordinate)
			_, err = h.sessions.store.RestorePyPIVersion(r.Context(), repo.ID, project, version)
		default:
			_, err = h.sessions.store.RestoreRawAsset(r.Context(), repo.ID, request.Coordinate)
		}
		if errors.Is(err, repository.ErrNotFound) || errors.Is(err, repository.ErrDisabled) || errors.Is(err, repository.ErrNameExists) {
			writeHostedProblem(w, http.StatusConflict, "restore_unavailable", "artifact cannot be restored")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "restore artifact failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func (h generatedRepositoryAPIAdapter) restoreConanCoordinate(r *http.Request, repositoryID, coordinate string) error {
	reference, recipeRevision, packageID, packageRevision, packageRestore, ok := parseConanRestoreCoordinate(coordinate)
	if !ok {
		return repository.ErrNotFound
	}
	if packageRestore {
		_, err := h.conan.RestoreConanPackageRevision(r.Context(), repositoryID, reference, recipeRevision, packageID, packageRevision)
		return err
	}
	_, err := h.conan.RestoreConanRecipeRevision(r.Context(), repositoryID, reference, recipeRevision)
	return err
}
