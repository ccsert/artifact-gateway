package repository

import (
	"context"
	"time"
)

// ArtifactUsageStat is the lifecycle download usage of one artifact address:
// the (repository, format, resource) tuple that clients resolve. The Gateway
// folds it into a durable aggregate on every successful download, so the
// count survives audit log retention and stays queryable for the artifact's
// whole lifecycle.
type ArtifactUsageStat struct {
	Repository        string
	Format            string
	Resource          string
	DownloadCount     int64
	TotalBytes        int64
	FirstDownloadedAt time.Time
	LastDownloadedAt  time.Time
	LastActor         string
}

// ArtifactUsageTotals summarizes one repository's usage aggregates.
type ArtifactUsageTotals struct {
	DownloadCount int64 `json:"downloadCount"`
	TotalBytes    int64 `json:"totalBytes"`
	Resources     int64 `json:"resources"`
}

// ArtifactUsageStore reads the durable download usage aggregates. Writes
// happen implicitly on RecordAudit so every download path is counted without
// each protocol handler having to opt in.
type ArtifactUsageStore interface {
	ListArtifactUsage(ctx context.Context, repository string, limit int) ([]ArtifactUsageStat, error)
	// WalkArtifactUsage visits every aggregate for one repository. Retention
	// cannot use the bounded management API list without missing candidates.
	WalkArtifactUsage(ctx context.Context, repository string, visit func(ArtifactUsageStat) error) error
	ArtifactUsageTotals(ctx context.Context, repository string) (ArtifactUsageTotals, error)
}

// artifactFormatsCountedForUsage are the formats whose resolved GET audits
// represent artifact downloads. Management bookkeeping reuses the audit log
// with format "management" or "" and must never inflate artifact usage.
var artifactFormatsCountedForUsage = map[Format]bool{
	FormatMaven: true,
	FormatOCI:   true,
	FormatRaw:   true,
	FormatNPM:   true,
	FormatPyPI:  true,
	FormatGo:    true,
	FormatAPT:   true,
	FormatConan: true,
}

// IsArtifactDownload reports whether an audit record represents one
// successful content download served to a client: a resolved GET with a 200
// response in a real artifact format. Publishes, metadata writes, management
// operations, and revalidations (HEAD or 304, where no content moved) are not
// counted.
func (a AuditRecord) IsArtifactDownload() bool {
	if a.Outcome != AuditResolved || a.Operation != "get" || a.Status != 200 {
		return false
	}
	if a.Repository == "" || a.Resource == "" {
		return false
	}
	return artifactFormatsCountedForUsage[Format(a.Format)]
}

// UsageIncrement is the usage delta carried by one download audit.
func (a AuditRecord) UsageIncrement() ArtifactUsageStat {
	return ArtifactUsageStat{
		Repository:       a.Repository,
		Format:           a.Format,
		Resource:         a.Resource,
		TotalBytes:       a.Bytes,
		LastDownloadedAt: a.OccurredAt,
		LastActor:        a.Actor,
	}
}

// artifactUsageAddress keys one aggregate in the memory store.
func artifactUsageAddress(repository, format, resource string) string {
	return repository + "\x00" + format + "\x00" + resource
}
