package app

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/aptpublication"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

const aptSnapshotArchiveContentType = "application/vnd.artifact-gateway.apt-snapshot.v1+tar"

func (h generatedRepositoryAPIAdapter) ExportAPTRepositorySnapshot(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, snapshotID adminopenapi.SnapshotId) {
	h.withRepositoryScope(w, r, repositoryID.String(), RepositoryAdmin, func(principal Principal, repo repository.HostedRepository) {
		if repo.Format != repository.FormatAPT || repo.Type != repository.RepositoryTypeHosted {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "APT repository snapshot not found")
			return
		}
		snapshot, _, err := h.aptPublications.GetAPTRepositorySnapshot(r.Context(), snapshotID.String())
		if errors.Is(err, repository.ErrNotFound) || (err == nil && snapshot.RepositoryID != repo.ID) {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "APT repository snapshot not found")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get APT repository snapshot failed")
			return
		}
		prepared, err := h.aptSnapshotExporter.Prepare(r.Context(), snapshot.ID)
		if err != nil {
			writeAPTSnapshotExportProblem(w, err)
			return
		}
		w.Header().Set("Content-Type", aptSnapshotArchiveContentType)
		w.Header().Set("Content-Disposition", `attachment; filename="apt-`+snapshot.Suite+`-`+strconv.FormatInt(snapshot.Sequence, 10)+`.tar"`)
		w.Header().Set("Content-Length", strconv.FormatInt(prepared.Size(), 10))
		w.Header().Set("Cache-Control", "private, no-store")
		w.WriteHeader(http.StatusOK)
		if err = prepared.WriteTo(r.Context(), w); err != nil {
			panic(http.ErrAbortHandler)
		}
		if h.audit != nil {
			_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{
				Repository: repo.Name, GroupName: repo.Name, Actor: principal.Actor,
				Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(), Format: "apt",
				Resource: "snapshots/" + snapshot.ID + "/archive", Representation: snapshot.ReleaseDigest,
				Operation: "apt.repository_snapshot.export", Status: http.StatusOK, CacheDisposition: "bypass",
				AuthorizationSource: "repository_admin", AuthorizationReason: "verified_snapshot_archive",
			})
		}
	})
}

func writeAPTSnapshotExportProblem(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		writeHostedProblem(w, http.StatusNotFound, "not_found", "APT repository snapshot not found")
	case errors.Is(err, repository.ErrDisabled):
		writeHostedProblem(w, http.StatusConflict, "invalid_state", "APT repository snapshot is not exportable")
	case errors.Is(err, aptpublication.ErrSnapshotArchiveCorrupt):
		writeHostedProblem(w, http.StatusConflict, "snapshot_corrupt", "APT repository snapshot failed integrity verification")
	default:
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "export APT repository snapshot failed")
	}
}
