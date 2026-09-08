package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/aptpublication"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func decodeAPTLifecycleRequest(w http.ResponseWriter, r *http.Request) (aptpublication.LifecycleRequest, bool) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	var request aptpublication.LifecycleRequest
	if err := decoder.Decode(&request); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeHostedProblem(w, 400, "invalid_request", "APT lifecycle request is invalid")
		return request, false
	}
	return request, true
}
func aptLifecycleRepository(w http.ResponseWriter, repo repository.HostedRepository) bool {
	if repo.Format != repository.FormatAPT || repo.Type != repository.RepositoryTypeHosted || repo.State != repository.RepositoryActive {
		writeHostedProblem(w, 404, "not_found", "APT Hosted repository not found")
		return false
	}
	return true
}
func (h generatedRepositoryAPIAdapter) ApplyAPTLifecycle(w http.ResponseWriter, r *http.Request, repoID adminopenapi.RepositoryId, params adminopenapi.ApplyAPTLifecycleParams) {
	h.withRepositoryScope(w, r, repoID.String(), RepositoryAdmin, func(principal Principal, repo repository.HostedRepository) {
		if !aptLifecycleRepository(w, repo) {
			return
		}
		if h.aptSnapshotPublisher == nil {
			writeHostedProblem(w, 503, "signer_unavailable", "APT Release signer is not configured")
			return
		}
		request, ok := decodeAPTLifecycleRequest(w, r)
		if !ok {
			return
		}
		snapshot, err := (aptpublication.Lifecycle{Publisher: h.aptSnapshotPublisher}).Apply(r.Context(), repo.ID, principal.Actor, string(params.IdempotencyKey), request)
		if err != nil {
			writeAPTSnapshotProblem(w, err)
			return
		}
		response, err := aptRepositorySnapshotResponse(snapshot)
		if err != nil {
			writeAPTSnapshotProblem(w, err)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		writeNativeMavenJSON(w, 200, response)
	})
}
func (h generatedRepositoryAPIAdapter) PreviewAPTLifecycle(w http.ResponseWriter, r *http.Request, repoID adminopenapi.RepositoryId) {
	h.withRepositoryScope(w, r, repoID.String(), RepositoryAdmin, func(_ Principal, repo repository.HostedRepository) {
		if !aptLifecycleRepository(w, repo) {
			return
		}
		request, ok := decodeAPTLifecycleRequest(w, r)
		if !ok {
			return
		}
		// Preview needs metadata only and remains available with signing disabled.
		plan, err := aptpublication.PreviewLifecycle(r.Context(), h.aptPublications, repo.ID, request)
		if err != nil {
			writeAPTSnapshotProblem(w, err)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		writeNativeMavenJSON(w, 200, plan)
	})
}
func (h generatedRepositoryAPIAdapter) PruneAPTSnapshots(w http.ResponseWriter, r *http.Request, repoID adminopenapi.RepositoryId) {
	h.withRepositoryScope(w, r, repoID.String(), RepositoryAdmin, func(principal Principal, repo repository.HostedRepository) {
		if !aptLifecycleRepository(w, repo) {
			return
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		var request adminopenapi.APTPruneRequest
		if err := decoder.Decode(&request); err != nil || decoder.Decode(&struct{}{}) != io.EOF || request.SnapshotIds == nil || len(request.SnapshotIds) > 100 {
			writeHostedProblem(w, 400, "invalid_request", "snapshotIds must be an array of at most 100 unique UUIDs")
			return
		}
		ids := make([]string, 0, len(request.SnapshotIds))
		seen := make(map[string]bool)
		for _, id := range request.SnapshotIds {
			if seen[id.String()] {
				writeHostedProblem(w, 400, "invalid_request", "snapshotIds must be unique")
				return
			}
			seen[id.String()] = true
			ids = append(ids, id.String())
		}
		evidence, _ := json.Marshal(ids)
		err := h.aptPublications.PruneAPTSnapshots(r.Context(), repo.ID, ids, time.Now().UTC(), repository.AuditRecord{GroupName: repo.Name, Repository: repo.Name, Actor: principal.Actor, Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(), Format: string(repository.FormatAPT), Resource: repo.ID, Operation: "apt.snapshot.prune", Status: 204, CacheDisposition: "bypass", AuthorizationSource: "repository_admin", AuthorizationReason: "snapshot_grace_elapsed", Evidence: map[string]string{"snapshotIds": string(evidence)}})
		if err != nil {
			writeAPTSnapshotProblem(w, err)
			return
		}
		w.WriteHeader(204)
	})
}
func (h generatedRepositoryAPIAdapter) GetAPTLifecycleState(w http.ResponseWriter, r *http.Request, repoID adminopenapi.RepositoryId, params adminopenapi.GetAPTLifecycleStateParams) {
	h.withRepositoryScope(w, r, repoID.String(), RepositoryAdmin, func(_ Principal, repo repository.HostedRepository) {
		if !aptLifecycleRepository(w, repo) {
			return
		}
		if !repository.ValidAPTPublicationScope(params.Suite) {
			writeHostedProblem(w, 400, "invalid_request", "suite is invalid")
			return
		}
		history, err := h.aptPublications.ListAPTRepositorySnapshots(r.Context(), repo.ID, params.Suite)
		if err != nil {
			writeAPTSnapshotProblem(w, err)
			return
		}
		response := adminopenapi.APTLifecycleState{Snapshots: []adminopenapi.APTSnapshotHistory{}, Packages: []adminopenapi.APTLifecyclePackage{}, Deletions: []adminopenapi.APTPackageDeletion{}}
		for _, item := range history {
			if item.Snapshot.PublishedAt.IsZero() {
				continue
			}
			snapshot, e := aptRepositorySnapshotResponse(item.Snapshot)
			if e != nil {
				writeAPTSnapshotProblem(w, e)
				return
			}
			entry := adminopenapi.APTSnapshotHistory{Snapshot: snapshot}
			if item.Snapshot.State == repository.APTRepositorySnapshotRetired && !item.RetiredAt.IsZero() {
				at := item.RetiredAt.Add(repository.APTSnapshotGracePeriod)
				entry.PrunableAfter = &at
			}
			response.Snapshots = append(response.Snapshots, entry)
			if item.Snapshot.State == repository.APTRepositorySnapshotVisible {
				_, members, e := h.aptPublications.GetAPTRepositorySnapshot(r.Context(), item.Snapshot.ID)
				if e != nil {
					writeAPTSnapshotProblem(w, e)
					return
				}
				for _, m := range members {
					revision, e := h.aptPublications.GetAPTPackageRevisionForSession(r.Context(), m.PublicationSessionID)
					if e != nil {
						writeAPTSnapshotProblem(w, e)
						return
					}
					p, e := aptLifecyclePackageResponse(m.PublicationSessionID, m.Component, revision)
					if e != nil {
						writeAPTSnapshotProblem(w, e)
						return
					}
					response.Packages = append(response.Packages, p)
				}
			}
		}
		deletions, err := h.aptPublications.ListAPTPackageDeletions(r.Context(), repo.ID, params.Suite)
		if err != nil {
			writeAPTSnapshotProblem(w, err)
			return
		}
		for _, d := range deletions {
			id, e := uuid.Parse(d.ID)
			if e != nil {
				writeAPTSnapshotProblem(w, e)
				return
			}
			p, e := aptLifecyclePackageResponse(d.SessionID, d.Component, d.Revision)
			if e != nil {
				writeAPTSnapshotProblem(w, e)
				return
			}
			state := "recoverable"
			if !time.Now().Before(d.RestoreUntil) {
				state = "expired"
			}
			if !d.RestoredAt.IsZero() {
				state = "restored"
			}
			if !d.PurgedAt.IsZero() {
				state = "purged"
			}
			response.Deletions = append(response.Deletions, adminopenapi.APTPackageDeletion{Id: id, Package: p, DeletedAt: d.DeletedAt, RestoreUntil: d.RestoreUntil, State: adminopenapi.APTPackageDeletionState(state)})
		}
		w.Header().Set("Cache-Control", "private, no-store")
		writeNativeMavenJSON(w, 200, response)
	})
}
func aptLifecyclePackageResponse(sessionID, component string, r repository.APTPackageRevision) (adminopenapi.APTLifecyclePackage, error) {
	id, err := uuid.Parse(sessionID)
	if err != nil {
		return adminopenapi.APTLifecyclePackage{}, err
	}
	revision, err := aptPackageRevisionResponse(r)
	if err != nil {
		return adminopenapi.APTLifecyclePackage{}, err
	}
	if component == "" {
		return adminopenapi.APTLifecyclePackage{}, errors.New("APT package component is missing")
	}
	return adminopenapi.APTLifecyclePackage{PublicationSessionId: id, Component: component, Revision: revision}, nil
}
