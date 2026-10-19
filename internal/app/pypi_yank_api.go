package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	adminopenapi "github.com/ccsert/artifact-gateway/internal/admin/openapi"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type pypiYankFilesReader interface {
	ListPyPIProjectFiles(context.Context, string, string) ([]repository.PyPIFile, error)
}

// pypiYankManagementHandler owns the file-selection metadata workflow. It has
// no package byte store and does not depend on a native protocol handler.
type pypiYankManagementHandler struct {
	managementScope
	files pypiYankFilesReader
	yank  repository.PyPIYankStore
}

func (h pypiYankManagementHandler) GetPyPIYankState(w http.ResponseWriter, r *http.Request, id adminopenapi.RepositoryId, project, version string) {
	h.withRepositoryBrowseScope(w, r, id.String(), func(_ Principal, repo repository.HostedRepository) {
		if !h.supported(w, repo, project, version) {
			return
		}
		files, err := h.files.ListPyPIProjectFiles(r.Context(), repo.ID, normalizePyPIProject(project))
		if err == nil {
			selected := files[:0]
			for _, file := range files {
				if file.Version == version {
					selected = append(selected, file)
				}
			}
			files = selected
			if len(files) == 0 {
				err = repository.ErrNotFound
			}
		}
		if writePyPIYankError(w, err) {
			return
		}
		writePyPIYankState(w, repo.ID, normalizePyPIProject(project), version, files)
	})
}

func (h pypiYankManagementHandler) UpdatePyPIYankState(w http.ResponseWriter, r *http.Request, id adminopenapi.RepositoryId, project, version string, params adminopenapi.UpdatePyPIYankStateParams) {
	h.withRepositoryScope(w, r, id.String(), RepositoryWrite, func(principal Principal, repo repository.HostedRepository) {
		if !h.supported(w, repo, project, version) {
			return
		}
		if h.yank == nil {
			writeHostedProblem(w, 501, "not_supported", "PyPI yank mutation is not configured")
			return
		}
		var request struct {
			Yanked   *bool           `json:"yanked"`
			Reason   json.RawMessage `json:"reason"`
			Filename json.RawMessage `json:"filename"`
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
		if err != nil || !utf8.Valid(body) {
			writeHostedProblem(w, 400, "invalid_request", "a bounded UTF-8 JSON document is required")
			return
		}
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || request.Yanked == nil {
			writeHostedProblem(w, 400, "invalid_request", "yanked, reason or filename is invalid")
			return
		}
		reason, validReason := pypiYankOptionalString(request.Reason)
		filename, validFilename := pypiYankOptionalString(request.Filename)
		if !validReason || !repository.ValidPyPIYankReason(reason) || !validFilename || len(request.Filename) > 0 && !validPyPIFilename(filename) {
			writeHostedProblem(w, 400, "invalid_request", "yanked, reason or filename is invalid")
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			writeHostedProblem(w, 400, "invalid_request", "one JSON document is required")
			return
		}
		revision := strings.TrimSpace(string(params.IfMatch))
		if len(r.Header.Values("If-Match")) != 1 || len(revision) != 66 || revision[0] != '"' || revision[65] != '"' {
			writeHostedProblem(w, 412, "version_conflict", "a current strong If-Match is required")
			return
		}
		operation := "pypi.yank"
		if !*request.Yanked {
			operation = "pypi.unyank"
		}
		files, err := h.yank.SetPyPIYank(r.Context(), repository.PyPIYankMutation{
			RepositoryID: repo.ID, Project: normalizePyPIProject(project), Version: version, Filename: filename,
			Yanked: *request.Yanked, Reason: reason, ExpectedRevision: revision[1:65],
			Audit: repository.AuditRecord{Repository: repo.Name, GroupName: repo.Name, Actor: principal.Actor, Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(), Format: string(repository.FormatPyPI), Resource: normalizePyPIProject(project) + "@" + version, Operation: operation, Status: 200, CacheDisposition: "bypass"},
		})
		if writePyPIYankError(w, err) {
			return
		}
		writePyPIYankState(w, repo.ID, normalizePyPIProject(project), version, files)
	})
}

func pypiYankOptionalString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", true
	}
	var value string
	if raw[0] != '"' || json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}

func (h pypiYankManagementHandler) supported(w http.ResponseWriter, repo repository.HostedRepository, project, version string) bool {
	if repo.Format != repository.FormatPyPI || repo.Type != repository.RepositoryTypeHosted || !validPyPIProject(normalizePyPIProject(project)) || !pypiVersionPattern.MatchString(version) {
		writeHostedProblem(w, 400, "invalid_request", "a Hosted PyPI project and version are required")
		return false
	}
	if h.files == nil {
		writeHostedProblem(w, 501, "not_supported", "PyPI file metadata is not configured")
		return false
	}
	return true
}

func writePyPIYankError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, repository.ErrNotFound):
		writeHostedProblem(w, 404, "not_found", "PyPI version or file not found")
	case errors.Is(err, repository.ErrVersionConflict):
		writeHostedProblem(w, 412, "version_conflict", "PyPI files changed; refresh their state before retrying")
	case errors.Is(err, repository.ErrInvalidPyPIYank):
		writeHostedProblem(w, 400, "invalid_request", "PyPI yank mutation is invalid")
	default:
		writeHostedProblem(w, 500, "internal_error", "PyPI yank operation failed")
	}
	return true
}

func writePyPIYankState(w http.ResponseWriter, repositoryID, project, version string, files []repository.PyPIFile) {
	sort.Slice(files, func(i, j int) bool { return files[i].Filename < files[j].Filename })
	revision := repository.PyPIYankRevision(files)
	items := make([]adminopenapi.PyPIYankFile, 0, len(files))
	for _, file := range files {
		items = append(items, adminopenapi.PyPIYankFile{Filename: file.Filename, Yanked: file.Yanked, Reason: file.YankReason, Digest: file.Digest, Size: file.Size})
	}
	w.Header().Set("ETag", `"`+revision+`"`)
	writeNativeMavenJSON(w, 200, adminopenapi.PyPIYankState{RepositoryId: uuid.MustParse(repositoryID), Project: project, Version: version, Revision: revision, Files: items})
}
