package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"time"

	npmprotocol "github.com/ccsert/artifact-gateway/internal/protocol/npm"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

func (h nativeNPMHandler) serveNPMRevision(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, route npmprotocol.Route, actor string) {
	if repo.Type != repository.RepositoryTypeHosted {
		h.writeError(w, http.StatusMethodNotAllowed, "npm metadata changes require a Hosted repository")
		return
	}
	if route.Kind == npmprotocol.RoutePackageRevision && r.Method == http.MethodPut {
		var document npmPublishDocument
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, npmPackumentLimit))
		if decoder.Decode(&document) != nil || decoder.Decode(&struct{}{}) != io.EOF || document.Revision != route.Revision || len(document.Attachments) != 0 {
			h.writeError(w, http.StatusBadRequest, "invalid npm unpublish document or revision")
			return
		}
		h.updateNPMMetadata(w, r, repo, route.Package, document, true, actor)
		return
	}
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	pkg, err := h.store.GetNPMPackage(r.Context(), repo.ID, route.Package)
	if err != nil {
		h.writeNPMMetadataResult(w, r, repo, route.Package, actor, repository.NPMMetadataChange{}, err)
		return
	}
	expected, _ := strconv.ParseInt(route.Revision, 10, 64)
	if pkg.Revision != expected {
		h.writeNPMMetadataResult(w, r, repo, route.Package, actor, repository.NPMMetadataChange{}, repository.ErrVersionConflict)
		return
	}
	if route.Kind == npmprotocol.RouteTarballRevision {
		// npm cleans the tarball URL after the packument mutation. The bytes
		// stay under the existing retention/lease/GC contract, even on replay.
		for _, version := range pkg.TombstonedVersions {
			if version.TarballName == route.Tarball {
				if h.audit != nil {
					_ = h.audit.RecordAudit(r.Context(), h.npmMetadataAudit(repo, route.Package, actor, "npm.unpublish.cleanup", http.StatusOK))
				}
				h.writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "rev": route.Revision})
				return
			}
		}
		h.writeError(w, http.StatusConflict, "unpublish package metadata before cleaning a tarball")
		return
	}
	if len(pkg.Versions) == 0 {
		h.writeNPMMetadataResult(w, r, repo, route.Package, actor, repository.NPMMetadataChange{}, repository.ErrNotFound)
		return
	}
	versions := make([]string, 0, len(pkg.Versions))
	for _, version := range pkg.Versions {
		versions = append(versions, version.Version)
	}
	change, err := h.metadata.UpdateNPMMetadata(r.Context(), repository.NPMMetadataMutation{
		RepositoryID: repo.ID, PackageName: route.Package, ExpectedRevision: expected,
		Unpublish: versions, DistTags: map[string]string{}, Audit: h.npmMetadataAudit(repo, route.Package, actor, "npm.unpublish", http.StatusOK),
	})
	h.writeNPMMetadataResult(w, r, repo, route.Package, actor, change, err)
}

func (h nativeNPMHandler) updateNPMMetadata(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, name string, document npmPublishDocument, unpublish bool, actor string) {
	if !npmprotocol.ValidRevision(document.Revision) || document.Name != name || document.ID != "" && document.ID != name || document.Versions == nil || len(document.Versions) > repository.NPMMetadataVersionLimit {
		h.writeError(w, http.StatusBadRequest, "metadata must name the package, its revision and at most 1000 versions")
		return
	}
	pkg, err := h.store.GetNPMPackage(r.Context(), repo.ID, name)
	if err != nil {
		h.writeNPMMetadataResult(w, r, repo, name, actor, repository.NPMMetadataChange{}, err)
		return
	}
	expected, _ := strconv.ParseInt(document.Revision, 10, 64)
	if expected != pkg.Revision {
		h.writeNPMMetadataResult(w, r, repo, name, actor, repository.NPMMetadataChange{}, repository.ErrVersionConflict)
		return
	}
	pkg, _, err = h.filterQuarantinedNPMVersions(r.Context(), repo, pkg)
	if err != nil {
		h.writeNPMMetadataResult(w, r, repo, name, actor, repository.NPMMetadataChange{}, err)
		return
	}
	mutation := repository.NPMMetadataMutation{RepositoryID: repo.ID, PackageName: name, ExpectedRevision: expected}
	operation := "npm.deprecate"
	if unpublish {
		mutation.Unpublish = []string{}
		mutation.DistTags = document.DistTags
		operation = "npm.unpublish"
	} else {
		mutation.Deprecations = map[string]string{}
	}
	known := make(map[string]bool, len(pkg.Versions))
	for _, version := range pkg.Versions {
		known[version.Version] = true
		incoming, exists := document.Versions[version.Version]
		if !exists {
			if !unpublish {
				h.writeError(w, http.StatusBadRequest, "deprecation must preserve all visible versions")
				return
			}
			mutation.Unpublish = append(mutation.Unpublish, version.Version)
			continue
		}
		message, valid := h.npmMetadataMessage(r, repo.Name, version, incoming)
		if !valid || unpublish && message != version.Deprecated {
			h.writeError(w, http.StatusBadRequest, "npm metadata update cannot change immutable version fields")
			return
		}
		if !unpublish && message != version.Deprecated {
			mutation.Deprecations[version.Version] = message
		}
	}
	for version := range document.Versions {
		if !known[version] {
			h.writeNPMMetadataResult(w, r, repo, name, actor, repository.NPMMetadataChange{}, repository.ErrNotFound)
			return
		}
	}
	if !unpublish && !reflect.DeepEqual(document.DistTags, pkg.DistTags) {
		h.writeError(w, http.StatusBadRequest, "deprecation cannot change dist-tags")
		return
	}
	if unpublish {
		for tag, target := range document.DistTags {
			if !npmprotocol.ValidDistTag(tag) {
				h.writeError(w, http.StatusBadRequest, "invalid dist-tag")
				return
			}
			if _, exists := document.Versions[target]; !exists {
				h.writeError(w, http.StatusBadRequest, "unpublish tag must name a retained version")
				return
			}
		}
	}
	mutation.Audit = h.npmMetadataAudit(repo, name, actor, operation, http.StatusOK)
	change, err := h.metadata.UpdateNPMMetadata(r.Context(), mutation)
	h.writeNPMMetadataResult(w, r, repo, name, actor, change, err)
}

func (h nativeNPMHandler) npmMetadataMessage(r *http.Request, registry string, version repository.NPMVersion, incoming json.RawMessage) (string, bool) {
	var next map[string]any
	if json.Unmarshal(incoming, &next) != nil || next == nil {
		return "", false
	}
	message := ""
	if raw, exists := next["deprecated"]; exists {
		var ok bool
		message, ok = raw.(string)
		if !ok || !repository.ValidNPMDeprecation(message) {
			return "", false
		}
	}
	current, ok := h.npmVersionDocument(r, registry, version)
	if !ok {
		return "", false
	}
	delete(current, "deprecated")
	delete(next, "deprecated")
	currentJSON, currentErr := json.Marshal(current)
	nextJSON, nextErr := json.Marshal(next)
	return message, currentErr == nil && nextErr == nil && bytes.Equal(currentJSON, nextJSON)
}

func (h nativeNPMHandler) npmMetadataAudit(repo repository.HostedRepository, name, actor, operation string, status int) repository.AuditRecord {
	return repository.AuditRecord{Repository: repo.Name, GroupName: repo.Name, Actor: actor, Format: "npm", Resource: name,
		Representation: "package", Operation: operation, Outcome: repository.AuditResolved, Status: status,
		OccurredAt: time.Now().UTC(), MemberType: string(repo.Type), CacheDisposition: "bypass", Evidence: map[string]string{"reason": "native npm metadata request"}}
}

func (h nativeNPMHandler) recordNPMWriteDenial(r *http.Request, repo repository.HostedRepository, route npmprotocol.Route, principal Principal, source, reason string, status int) {
	metadataWrite := route.Kind == npmprotocol.RoutePackage && r.Method == http.MethodPut || route.Kind == npmprotocol.RoutePackageRevision || route.Kind == npmprotocol.RouteTarballRevision
	if h.audit == nil || !metadataWrite {
		return
	}
	operation := "npm.package.write"
	if route.Kind == npmprotocol.RoutePackageRevision || route.Kind == npmprotocol.RouteTarballRevision {
		operation = "npm.unpublish"
	}
	audit := h.npmMetadataAudit(repo, route.Package, principal.Actor, operation, status)
	audit.Outcome, audit.AuthorizationSource, audit.AuthorizationReason = repository.AuditAccessDenied, source, reason
	_ = h.audit.RecordAudit(r.Context(), audit)
}

func (h nativeNPMHandler) writeNPMMetadataResult(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, name, actor string, change repository.NPMMetadataChange, err error) {
	if err == nil {
		h.writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "id": name, "rev": strconv.FormatInt(change.Revision, 10)})
		return
	}
	status, message := http.StatusServiceUnavailable, "npm metadata storage is unavailable"
	switch {
	case errors.Is(err, repository.ErrVersionConflict):
		status, message = http.StatusConflict, "package revision changed; read the current packument before retrying"
	case errors.Is(err, repository.ErrNotFound):
		status, message = http.StatusNotFound, "package version is unavailable"
	case errors.Is(err, repository.ErrInvalidNPMMetadata), errors.Is(err, repository.ErrNPMDistTagTargetUnavailable):
		status, message = http.StatusBadRequest, "invalid npm metadata selection or dist-tags"
	}
	h.writeError(w, status, message)
	h.recordAuditWithDisposition(r, repo, name, actor, repository.AuditStorageError, status, 0, "bypass")
}
