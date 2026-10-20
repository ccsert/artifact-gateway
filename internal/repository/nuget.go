package repository

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// FormatNuGet is an internal Hosted preview identity. It is deliberately not
// present in SupportedFormatProfiles or the public repository Format enum.
const FormatNuGet Format = "nuget"

var (
	ErrNuGetPublicationConflict = errors.New("NuGet package identity already contains different content")
	ErrInvalidNuGetPublication  = errors.New("invalid NuGet publication")
	ErrNuGetSessionExpired      = errors.New("NuGet publication session expired")
	ErrNuGetSessionState        = errors.New("NuGet publication session is not staged")
)

type NuGetSession struct {
	ID, RepositoryID, PackageID, Version, Digest, Actor string
	Size                                                int64
	State                                               string
	ObjectKey                                           string
	MetadataJSON, Nuspec                                []byte
	CreatedAt, ExpiresAt, CommittedAt                   time.Time
	CollectedAt                                         time.Time
}

type NuGetPublication struct {
	RepositoryID, PackageID, Version, Digest, ObjectKey, SessionID, Publisher string
	Size                                                                      int64
	MetadataJSON, Nuspec                                                      []byte
	PublishedAt                                                               time.Time
}

// NativeNuGetStore is injected independently; it is never part of GatewayStore.
// Package identity and bytes are inspected by the format module before staging.
type NativeNuGetStore interface {
	ReserveNuGetPublication(context.Context, NuGetSession) (NuGetSession, bool, error)
	GetNuGetSession(context.Context, string) (NuGetSession, error)
	StageNuGetPublication(context.Context, NuGetSession) (NuGetSession, error)
	CommitNuGetPublication(context.Context, string, string) (NuGetPublication, bool, error)
	GetNuGetPublication(context.Context, string, string, string) (NuGetPublication, error)
	ListNuGetPublications(context.Context, string, string, int, string) ([]NuGetPublication, error)
	ExpireNuGetSessions(context.Context, time.Time, int) ([]NuGetSession, error)
	MarkNuGetSessionCollected(context.Context, string, string) error
	LockNuGetPublication(context.Context, string, string, string) (context.Context, func(), error)
}

var nuGetID = regexp.MustCompile(`^[a-z0-9_]+(?:[.-][a-z0-9_]+)*$`)
var nuGetVersion = regexp.MustCompile(`^(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:\.[1-9][0-9]*)?(?:-[a-z0-9-]+(?:\.[a-z0-9-]+)*)?$`)

func validNuGetSession(in NuGetSession) bool {
	_, idErr := uuid.Parse(in.ID)
	_, repoErr := uuid.Parse(in.RepositoryID)
	digest, err := hex.DecodeString(strings.TrimPrefix(in.Digest, "sha256:"))
	return idErr == nil && repoErr == nil && len(in.PackageID) <= 100 && nuGetID.MatchString(in.PackageID) &&
		len(in.Version) <= 256 && nuGetVersion.MatchString(in.Version) && in.Size > 0 && in.Size <= 256<<20 &&
		strings.HasPrefix(in.Digest, "sha256:") && err == nil && len(digest) == 32 && in.Digest == strings.ToLower(in.Digest) &&
		in.Actor != "" && len(in.Actor) <= 512 && !strings.ContainsAny(in.Actor, "\x00\r\n") && !in.ExpiresAt.IsZero()
}

func validNuGetStaging(in NuGetSession) bool {
	var meta struct{ ID, Version string }
	return validNuGetSession(in) && len(in.Nuspec) > 0 && len(in.Nuspec) <= 1<<20 && len(in.MetadataJSON) <= 256<<10 &&
		json.Unmarshal(in.MetadataJSON, &meta) == nil && strings.EqualFold(meta.ID, in.PackageID) && strings.EqualFold(meta.Version, in.Version)
}

func nuGetSessionObjectKey(in NuGetSession) string {
	return "native/nuget/" + in.RepositoryID + "/sessions/" + in.ID + "/sha256/" + strings.TrimPrefix(in.Digest, "sha256:")
}
func nuGetPublicationKey(repo, id, version string) string {
	return repo + "\x00" + id + "\x00" + version
}
func nuGetSessionSame(a, b NuGetSession) bool {
	return a.RepositoryID == b.RepositoryID && a.PackageID == b.PackageID && a.Version == b.Version && a.Digest == b.Digest && a.Size == b.Size
}
func cloneNuGetSession(in NuGetSession) NuGetSession {
	in.MetadataJSON = append([]byte(nil), in.MetadataJSON...)
	in.Nuspec = append([]byte(nil), in.Nuspec...)
	return in
}
func cloneNuGetPublication(in NuGetPublication) NuGetPublication {
	in.MetadataJSON = append([]byte(nil), in.MetadataJSON...)
	in.Nuspec = append([]byte(nil), in.Nuspec...)
	return in
}
func nuGetPublication(in NuGetSession, published time.Time) NuGetPublication {
	return NuGetPublication{RepositoryID: in.RepositoryID, PackageID: in.PackageID, Version: in.Version, Digest: in.Digest, ObjectKey: in.ObjectKey, SessionID: in.ID, Publisher: in.Actor, Size: in.Size, MetadataJSON: append([]byte(nil), in.MetadataJSON...), Nuspec: append([]byte(nil), in.Nuspec...), PublishedAt: published}
}
func nuGetPublicationAudit(in NuGetPublication, repoName string) AuditRecord {
	return AuditRecord{Repository: repoName, GroupName: repoName, Actor: in.Publisher, Format: string(FormatNuGet), Resource: in.PackageID + "@" + in.Version, Operation: "nuget.publish", Outcome: AuditResolved, Status: 201, OccurredAt: in.PublishedAt, Evidence: map[string]string{"digest": in.Digest, "sessionId": in.SessionID}}
}
