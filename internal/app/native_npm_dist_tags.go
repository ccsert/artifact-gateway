package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	npmprotocol "github.com/ccsert/artifact-gateway/internal/protocol/npm"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

const (
	npmDistTagBodyLimit       = 1 << 10
	npmDistTagSetOperation    = "npm.dist-tag.set"
	npmDistTagDeleteOperation = "npm.dist-tag.delete"
)

// serveDistTags answers the registry API behind `npm dist-tag ls/add/rm`.
// Only Hosted repositories accept tag changes; Proxy repositories expose the
// upstream tags they cache and never rewrite them.
func (h nativeNPMHandler) serveDistTags(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, route npmprotocol.Route, actor string) {
	switch {
	case route.Tag == "" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		h.listDistTags(w, r, repo, route.Package, actor)
	case route.Tag != "" && (r.Method == http.MethodPut || r.Method == http.MethodDelete):
		if repo.Type != repository.RepositoryTypeHosted {
			h.writeError(w, http.StatusMethodNotAllowed, "dist-tags can only be changed in a Hosted repository")
			h.recordDistTagAudit(r, repo, route.Package, route.Tag, "", actor, repository.AuditAccessDenied, http.StatusMethodNotAllowed)
			return
		}
		if r.Method == http.MethodPut {
			h.setDistTag(w, r, repo, route.Package, route.Tag, actor)
		} else {
			h.deleteDistTag(w, r, repo, route.Package, route.Tag, actor)
		}
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h nativeNPMHandler) listDistTags(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, packageName, actor string) {
	var pkg repository.NPMPackage
	disposition := "bypass"
	target := npmAuditTarget{GroupName: repo.Name, Repository: repo.Name}
	if repo.Type == repository.RepositoryTypeProxy {
		resolved, err := h.resolveProxyPackage(r, repo, packageName, actor)
		if err != nil {
			var responseError *npmProxyPackageError
			if errors.As(err, &responseError) {
				h.recordNPMProxyPackageError(r, repo, target, packageName, actor, responseError)
				h.writeError(w, responseError.Status, responseError.Message)
				return
			}
			h.recordAuditForTarget(r, repo, target, packageName, actor, repository.AuditStorageError, http.StatusServiceUnavailable, 0, "bypass")
			h.writeError(w, http.StatusServiceUnavailable, "package metadata unavailable")
			return
		}
		if resolved.Stale {
			w.Header().Set("Warning", `110 Artifact-Gateway "Response is stale"`)
		}
		pkg, disposition = resolved.Package, resolved.Disposition
	} else {
		var err error
		pkg, err = h.store.GetNPMPackage(r.Context(), repo.ID, packageName)
		if errors.Is(err, repository.ErrNotFound) {
			h.writeError(w, http.StatusNotFound, "package not found")
			h.recordAuditForTarget(r, repo, target, packageName, actor, repository.AuditNotFound, http.StatusNotFound, 0, disposition)
			return
		}
		if err != nil {
			h.writeError(w, http.StatusServiceUnavailable, "package metadata unavailable")
			h.recordAuditForTarget(r, repo, target, packageName, actor, repository.AuditStorageError, http.StatusServiceUnavailable, 0, disposition)
			return
		}
	}
	pkg, _, err := h.filterQuarantinedNPMVersions(r.Context(), repo, pkg)
	if err != nil {
		h.writeError(w, http.StatusServiceUnavailable, "package metadata unavailable")
		h.recordAuditForTarget(r, repo, target, packageName, actor, repository.AuditStorageError, http.StatusServiceUnavailable, 0, disposition)
		return
	}
	if len(pkg.Versions) == 0 {
		h.writeError(w, http.StatusNotFound, "package not found")
		h.recordAuditForTarget(r, repo, target, packageName, actor, repository.AuditNotFound, http.StatusNotFound, 0, disposition)
		return
	}
	h.writeNPMMetadataJSON(w, r, repo, packageName, actor, disposition, target, visibleNPMDistTags(pkg))
}

// visibleNPMDistTags keeps only tags whose target is one of the package's
// visible versions, so a read never names a version a client cannot install.
func visibleNPMDistTags(pkg repository.NPMPackage) map[string]string {
	visible := make(map[string]bool, len(pkg.Versions))
	for _, version := range pkg.Versions {
		visible[version.Version] = true
	}
	tags := make(map[string]string, len(pkg.DistTags))
	for tag, version := range pkg.DistTags {
		if visible[version] {
			tags[tag] = version
		}
	}
	return tags
}

func (h nativeNPMHandler) setDistTag(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, packageName, tag, actor string) {
	// npm sends the target version as a JSON string body.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, npmDistTagBodyLimit))
	var version string
	if err != nil || json.Unmarshal(body, &version) != nil {
		h.writeError(w, http.StatusBadRequest, "dist-tag body must be a JSON string naming a version")
		return
	}
	if !npmprotocol.ValidVersion(version) {
		h.writeError(w, http.StatusBadRequest, "dist-tag target must be a valid SemVer version")
		return
	}
	stored, err := h.store.GetNPMVersion(r.Context(), repo.ID, packageName, version)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		h.writeError(w, http.StatusServiceUnavailable, "package metadata unavailable")
		h.recordDistTagAudit(r, repo, packageName, tag, version, actor, repository.AuditStorageError, http.StatusServiceUnavailable)
		return
	}
	if err == nil {
		blocked, blockErr := h.npmVersionReadBlocked(r.Context(), repo, stored)
		if blockErr != nil {
			h.writeError(w, http.StatusServiceUnavailable, "package metadata unavailable")
			h.recordDistTagAudit(r, repo, packageName, tag, version, actor, repository.AuditStorageError, http.StatusServiceUnavailable)
			return
		}
		if blocked {
			h.writeError(w, http.StatusConflict, "dist-tag target version is quarantined")
			h.recordDistTagAudit(r, repo, packageName, tag, version, actor, repository.AuditAccessDenied, http.StatusConflict)
			return
		}
	}
	// The store re-checks visibility under its row locks; the read above only
	// adds the quarantine policy, which reads filter again on every request.
	change, err := h.store.SetNPMDistTag(r.Context(), repository.NPMDistTagMutation{
		RepositoryID: repo.ID, PackageName: packageName, Tag: tag, Version: version,
		Audit: h.distTagAuditRecord(r, repo, packageName, actor, npmDistTagSetOperation, repository.AuditResolved, http.StatusOK),
	})
	h.writeDistTagResult(w, r, repo, packageName, tag, version, actor, change, err)
}

func (h nativeNPMHandler) deleteDistTag(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, packageName, tag, actor string) {
	if tag == "latest" {
		// npm resolves a bare package name through `latest`; it can be moved
		// with `npm dist-tag add` but not removed.
		h.writeError(w, http.StatusBadRequest, "the latest dist-tag cannot be removed")
		return
	}
	change, err := h.store.DeleteNPMDistTag(r.Context(), repository.NPMDistTagMutation{
		RepositoryID: repo.ID, PackageName: packageName, Tag: tag,
		Audit: h.distTagAuditRecord(r, repo, packageName, actor, npmDistTagDeleteOperation, repository.AuditResolved, http.StatusOK),
	})
	h.writeDistTagResult(w, r, repo, packageName, tag, "", actor, change, err)
}

func (h nativeNPMHandler) writeDistTagResult(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, packageName, tag, version, actor string, change repository.NPMDistTagChange, err error) {
	switch {
	case errors.Is(err, repository.ErrNPMDistTagTargetUnavailable):
		h.writeError(w, http.StatusNotFound, "dist-tag target version not found")
		h.recordDistTagAudit(r, repo, packageName, tag, version, actor, repository.AuditNotFound, http.StatusNotFound)
	case errors.Is(err, repository.ErrNotFound) && r.Method == http.MethodDelete:
		h.writeError(w, http.StatusNotFound, "dist-tag not found")
		h.recordDistTagAudit(r, repo, packageName, tag, version, actor, repository.AuditNotFound, http.StatusNotFound)
	case errors.Is(err, repository.ErrNotFound):
		h.writeError(w, http.StatusNotFound, "package not found")
		h.recordDistTagAudit(r, repo, packageName, tag, version, actor, repository.AuditNotFound, http.StatusNotFound)
	case err != nil:
		h.writeError(w, http.StatusServiceUnavailable, "dist-tag change failed")
		h.recordDistTagAudit(r, repo, packageName, tag, version, actor, repository.AuditStorageError, http.StatusServiceUnavailable)
	default:
		if h.metrics != nil {
			h.metrics.recordNPMAudit(repository.AuditResolved, 0, false)
		}
		h.writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "id": packageName, "dist-tags": change.DistTags})
	}
}

func (h nativeNPMHandler) distTagAuditRecord(r *http.Request, repo repository.HostedRepository, packageName, actor, operation string, outcome repository.AuditOutcome, status int) repository.AuditRecord {
	if actor == "" {
		actor = anonymousActor
	}
	return repository.AuditRecord{
		GroupName: repo.Name, Repository: repo.Name, Actor: actor, Outcome: outcome, OccurredAt: time.Now().UTC(),
		Format: string(repository.FormatNPM), Resource: packageName, Representation: "dist-tag", MemberType: string(repo.Type),
		Operation: operation, Status: status, CacheDisposition: "bypass",
	}
}

// recordDistTagAudit records a refused or failed tag change. Successful
// changes are audited by the store in the mutation's own transaction.
func (h nativeNPMHandler) recordDistTagAudit(r *http.Request, repo repository.HostedRepository, packageName, tag, version, actor string, outcome repository.AuditOutcome, status int) {
	if h.metrics != nil {
		h.metrics.recordNPMAudit(outcome, 0, false)
	}
	if h.audit == nil {
		return
	}
	operation := npmDistTagSetOperation
	if r.Method == http.MethodDelete {
		operation = npmDistTagDeleteOperation
	}
	record := h.distTagAuditRecord(r, repo, packageName, actor, operation, outcome, status)
	record.Evidence = map[string]string{"distTag": tag, "version": version}
	_ = h.audit.RecordAudit(r.Context(), record)
}

// isNPMWriteMethod reports the request methods that mutate npm state.
func isNPMWriteMethod(method string) bool {
	return method == http.MethodPut || method == http.MethodDelete
}
