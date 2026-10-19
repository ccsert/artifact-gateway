package repository

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"unicode"
	"unicode/utf8"
)

const NPMDeprecationLimit = 4096
const NPMMetadataVersionLimit = 1000

var ErrInvalidNPMMetadata = errors.New("invalid npm metadata mutation")

// NPMMetadataStore is the native registry mutation port. It is injected
// independently of the legacy aggregate and never expands GatewayStore.
type NPMMetadataStore interface {
	UpdateNPMMetadata(context.Context, NPMMetadataMutation) (NPMMetadataChange, error)
}

// NPMRestoreStore owns tombstone inspection and audited restoration of the
// original identity. A management handler does not need the native publisher.
type NPMRestoreStore interface {
	GetNPMPackage(context.Context, string, string) (NPMPackage, error)
	RestoreNPMVersion(context.Context, NPMRestoreRequest) (NPMVersion, error)
}

// NPMCollectionStore persists the delete boundary under the collector's lease.
type NPMCollectionStore interface {
	MarkNPMObjectCollecting(context.Context, string) error
}

// NPMMetadataMutation changes only registry metadata. Exactly one of
// Deprecations and Unpublish must be supplied. ExpectedRevision fences the
// whole package, including concurrent publication, tags, tombstone and restore.
// Unpublish tombstones all selected versions and replaces tags in one commit;
// it never deletes object bytes. Audit commits with the mutation.
type NPMMetadataMutation struct {
	RepositoryID     string
	PackageName      string
	ExpectedRevision int64
	Deprecations     map[string]string
	Unpublish        []string
	DistTags         map[string]string
	Audit            AuditRecord
}

type NPMMetadataChange struct {
	Revision int64
	Versions []string
	Changed  bool
}

// NPMRestoreRequest restores the existing identity under the same object lease
// as reclaim, with the caller's audit committed before visibility returns.
type NPMRestoreRequest struct {
	RepositoryID string
	PackageName  string
	Version      string
	Audit        AuditRecord
}

func (m NPMRestoreRequest) auditRecord(item NPMVersion) AuditRecord {
	record := m.Audit
	if record.Actor == "" {
		record.Actor = "system"
	}
	if record.Repository == "" {
		record.Repository = m.RepositoryID
	}
	if record.GroupName == "" {
		record.GroupName = record.Repository
	}
	record.Format, record.Resource, record.Operation = "npm", m.PackageName+"@"+m.Version, "artifact.restore"
	record.Outcome = AuditResolved
	record.Evidence = cloneStringMap(m.Audit.Evidence)
	record.Evidence["version"], record.Evidence["digest"] = item.Version, item.Digest
	return record
}

func ValidNPMDeprecation(message string) bool {
	if len(message) > NPMDeprecationLimit || !utf8.ValidString(message) {
		return false
	}
	for _, r := range message {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return false
		}
	}
	return true
}

func (m NPMMetadataMutation) selectedVersions() ([]string, error) {
	if m.ExpectedRevision < 1 || (m.Deprecations == nil) == (m.Unpublish == nil) {
		return nil, ErrInvalidNPMMetadata
	}
	selected := make(map[string]bool)
	for version, message := range m.Deprecations {
		if !ValidNPMDeprecation(message) {
			return nil, ErrInvalidNPMMetadata
		}
		selected[version] = true
	}
	for _, version := range m.Unpublish {
		if selected[version] {
			return nil, ErrInvalidNPMMetadata
		}
		selected[version] = true
	}
	if len(selected) == 0 && m.Unpublish != nil || len(selected) > NPMMetadataVersionLimit {
		return nil, ErrInvalidNPMMetadata
	}
	versions := make([]string, 0, len(selected))
	for version := range selected {
		if version == "" {
			return nil, ErrInvalidNPMMetadata
		}
		versions = append(versions, version)
	}
	sort.Strings(versions)
	return versions, nil
}

func (m NPMMetadataMutation) auditRecord(change NPMMetadataChange) AuditRecord {
	record := m.Audit
	record.Evidence = cloneStringMap(m.Audit.Evidence)
	versions, _ := json.Marshal(change.Versions)
	record.Evidence["versions"] = string(versions)
	record.Evidence["previousRevision"] = strconv.FormatInt(m.ExpectedRevision, 10)
	record.Evidence["revision"] = strconv.FormatInt(change.Revision, 10)
	record.Evidence["changed"] = strconv.FormatBool(change.Changed)
	return record
}
