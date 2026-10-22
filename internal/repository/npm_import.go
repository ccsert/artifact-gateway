package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
)

// NPMImportStore is the narrow atomic publication port for a complete, verified
// Hosted package closure. Existing tags/versions are preserved; conflicts never
// turn into metadata replacement. It does not extend GatewayStore.
type NPMImportStore interface {
	PublishNPMImport(context.Context, NPMImportClosure) error
}

type NPMImportClosure struct {
	RepositoryID, PackageName string
	Versions                  []NPMVersion
	DistTags                  map[string]string
	Audit                     AuditRecord
}

func validateNPMImport(c NPMImportClosure) error {
	if c.RepositoryID == "" || c.PackageName == "" || len(c.Versions) < 1 || len(c.Versions) > NPMMetadataVersionLimit {
		return ErrInvalidNPMMetadata
	}
	seen, tarballs := map[string]bool{}, map[string]bool{}
	for _, v := range c.Versions {
		if v.RepositoryID != c.RepositoryID || v.PackageName != c.PackageName || v.Version == "" || v.Size <= 0 || v.Digest == "" || v.ObjectKey == "" || v.Integrity == "" || v.Shasum == "" || len(v.Manifest) == 0 || !ValidNPMDeprecation(v.Deprecated) || seen[v.Version] || tarballs[v.TarballName] {
			return ErrInvalidNPMMetadata
		}
		seen[v.Version], tarballs[v.TarballName] = true, true
	}
	for tag, version := range c.DistTags {
		if tag == "" || !seen[version] {
			return ErrInvalidNPMMetadata
		}
	}
	return nil
}

// NPMImportVersionMatches permits identical immutable content at a different
// object key, while preserving every meaningful version-manifest field.
func NPMImportVersionMatches(old, incoming NPMVersion) bool {
	if old.State != "" && old.State != "visible" || old.Digest != incoming.Digest || old.Size != incoming.Size || old.Integrity != incoming.Integrity || old.Shasum != incoming.Shasum || old.TarballName != incoming.TarballName || old.Deprecated != incoming.Deprecated {
		return false
	}
	a, ae := npmImportManifest(old.Manifest)
	b, be := npmImportManifest(incoming.Manifest)
	return ae == nil && be == nil && bytes.Equal(a, b)
}
func npmImportManifest(raw []byte) ([]byte, error) {
	var m map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&m); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, ErrInvalidNPMMetadata
	}
	dist, ok := m["dist"].(map[string]any)
	if !ok {
		return nil, ErrInvalidNPMMetadata
	}
	delete(dist, "tarball")
	delete(dist, "integrity")
	delete(dist, "shasum")
	return json.Marshal(m)
}
func npmImportAudit(c NPMImportClosure, repoName string) AuditRecord {
	record := c.Audit
	record.Repository, record.GroupName, record.Format, record.Resource, record.Operation, record.Outcome = repoName, repoName, "npm", c.PackageName, "publish", AuditResolved
	versions := make([]string, 0, len(c.Versions))
	for _, v := range c.Versions {
		versions = append(versions, v.Version)
	}
	sort.Strings(versions)
	encoded, _ := json.Marshal(versions)
	record.Evidence = cloneStringMap(record.Evidence)
	record.Evidence["versions"] = string(encoded)
	record.Evidence["tagPolicy"] = "create-missing-preserve-existing"
	return record
}
