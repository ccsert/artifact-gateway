package repository

import (
	"context"
	"strings"

	"github.com/ccsert/artifact-gateway/internal/protocol/identity"
)

// PyPIImportStore is deliberately separate from GatewayStore. Production imports
// require a transaction-owned migration guard and completion callback.
type PyPIImportStore interface {
	CheckPyPIImportActor(context.Context, string) error
	PublishPyPIImport(context.Context, PyPIImportClosure) error
}
type PyPIImportClosure struct {
	RepositoryID, Project, Version string
	Files                          []PyPIFile
	Audit                          AuditRecord
}

func validatePyPIImport(c PyPIImportClosure) error {
	if c.RepositoryID == "" || !identity.ValidPyPIName(c.Project) || !identity.ValidPyPIVersion(c.Version) || len(c.Files) < 1 || len(c.Files) > 1000 {
		return ErrDisabled
	}
	seen := map[string]bool{}
	for _, f := range c.Files {
		d, err := identity.PyPIFilenameIdentity(f.Filename)
		if err != nil || d.Project != c.Project || d.Version != c.Version || d.FileType != f.FileType || d.PythonVersion != f.PythonVersion || f.RepositoryID != c.RepositoryID || f.Project != c.Project || f.Version != c.Version || !identity.ValidPyPIDigest(strings.TrimPrefix(f.Digest, "sha256:")) || !strings.HasPrefix(f.Digest, "sha256:") || f.ObjectKey == "" || f.Size <= 0 || f.Size > 64<<20 || seen[f.Filename] || !ValidPyPIYankReason(f.YankReason) || !f.Yanked && f.YankReason != "" {
			return ErrDisabled
		}
		seen[f.Filename] = true
	}
	return nil
}
func PyPIImportFileMatches(a, b PyPIFile) bool {
	return a.State == "visible" && a.Project == b.Project && a.Version == b.Version && a.Filename == b.Filename && a.FileType == b.FileType && a.PythonVersion == b.PythonVersion && a.RequiresPython == b.RequiresPython && a.Digest == b.Digest && a.Size == b.Size && a.Yanked == b.Yanked && a.YankReason == b.YankReason && a.SourceURL == ""
}
