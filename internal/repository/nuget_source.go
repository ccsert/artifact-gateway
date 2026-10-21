package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

var (
	ErrNuGetSourceChanged = errors.New("NuGet source binding changed")
	ErrNuGetCacheLimit    = errors.New("NuGet source metadata exceeds preview bound")
)

// NuGetSourceDocument is transport metadata, never an artifact identity. A
// stale positive document may support offline reads, but complete packages
// require independently inspected immutable publication bytes.
type NuGetSourceDocument struct {
	RepositoryID, SourceVersion, URL, Digest, ETag, LastModified string
	Status                                                       int
	Body                                                         []byte
	FetchedAt, ExpiresAt                                         time.Time
}

// NuGetGroupAuthority is create-only ownership of a canonical package version.
// Empty Digest/zero Size represents source selection only. Confirmation adds
// the verified package identity without changing the member or source version.
type NuGetGroupAuthority struct {
	GroupID, PackageID, Version, RepositoryID, RepositoryVersion, Digest string
	Size                                                                 int64
	CreatedAt                                                            time.Time
}

// NuGetSourceStore is a preview-specific port, independent of GatewayStore.
// Writes compare source/Group versions atomically with durable state.
type NuGetSourceStore interface {
	GetNuGetSourceDocument(context.Context, string, string, string) (NuGetSourceDocument, error)
	PutNuGetSourceDocument(context.Context, NuGetSourceDocument) error
	BindNuGetGroupAuthority(context.Context, string, NuGetGroupAuthority) (NuGetGroupAuthority, error)
	GetNuGetGroupAuthority(context.Context, string, string, string) (NuGetGroupAuthority, error)
	RevalidateNuGetGroupAuthority(context.Context, string, string, NuGetGroupAuthority) (NuGetGroupAuthority, error)
}

func validNuGetDocument(in NuGetSourceDocument) bool {
	sum := sha256.Sum256(in.Body)
	return in.RepositoryID != "" && sourceVersionValid(in.SourceVersion) && len(in.URL) > 0 && len(in.URL) <= 4096 &&
		!strings.ContainsAny(in.URL, "\x00\r\n") && (in.Status == 200 || in.Status == 404 && len(in.Body) == 0) && len(in.Body) <= 8<<20 &&
		in.Digest == "sha256:"+hex.EncodeToString(sum[:]) && len(in.ETag) <= 1024 && len(in.LastModified) <= 128 &&
		!strings.ContainsAny(in.ETag+in.LastModified, "\x00\r\n") && !in.FetchedAt.IsZero() && in.ExpiresAt.After(in.FetchedAt) && in.ExpiresAt.Sub(in.FetchedAt) <= time.Hour
}
func sourceVersionValid(version string) bool {
	if len(version) < 1 || len(version) > 20 || version[0] == '0' {
		return false
	}
	for _, r := range version {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
func cloneNuGetDocument(in NuGetSourceDocument) NuGetSourceDocument {
	in.Body = append([]byte(nil), in.Body...)
	return in
}
func nuGetDocumentKey(repo, version, url string) string {
	return repo + "\x00" + version + "\x00" + url
}
func nuGetAuthorityKey(group, id, version string) string {
	return group + "\x00" + id + "\x00" + version
}
func validNuGetAuthority(in NuGetGroupAuthority) bool {
	return in.GroupID != "" && in.RepositoryID != "" && sourceVersionValid(in.RepositoryVersion) && nuGetID.MatchString(in.PackageID) && len(in.PackageID) <= 100 && nuGetVersion.MatchString(in.Version) && len(in.Version) <= 256 &&
		(in.Digest == "" && in.Size == 0 || validSHA256Digest(in.Digest) && in.Size > 0 && in.Size <= 256<<20)
}
