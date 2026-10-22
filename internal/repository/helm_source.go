package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/google/uuid"
)

var (
	ErrHelmSourceChanged = errors.New("helm source binding changed")
	ErrHelmCacheLimit    = errors.New("helm source cache exceeds preview bound")
)

// Source documents are bounded transport metadata, never visible artifacts.
type HelmSourceDocument struct {
	RepositoryID, RepositoryVersion, Digest, ETag, LastModified string
	Body                                                        []byte
	FetchedAt, ExpiresAt                                        time.Time
}

// Entry pins index identity, URL and source configuration. Metadata can exist
// before chart bytes are cached and is not an admitted Artifact Identity.
type HelmSourceEntry struct {
	Name, Version, Digest, RepositoryID, RepositoryVersion, URL string
	MetadataJSON                                                []byte
	CreatedAt                                                   time.Time
}
type HelmSourceSnapshot struct {
	ID, ViewID, ViewVersion, Digest string
	Entries                         []HelmSourceEntry
	CreatedAt                       time.Time
}
type HelmGroupAuthority struct {
	GroupID   string
	Entry     HelmSourceEntry
	CreatedAt time.Time
}
type HelmSourceStore interface {
	GetHelmSourceDocument(context.Context, string, string) (HelmSourceDocument, error)
	PutHelmSourceDocument(context.Context, HelmSourceDocument) error
	PutHelmSourceSnapshot(context.Context, HelmSourceSnapshot) (HelmSourceSnapshot, error)
	GetHelmSourceSnapshot(context.Context, string, string) (HelmSourceSnapshot, error)
	BindHelmGroupAuthority(context.Context, string, HelmGroupAuthority) (HelmGroupAuthority, error)
	GetHelmGroupAuthority(context.Context, string, string, string) (HelmGroupAuthority, error)
}

func helmDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func validHelmDocument(in HelmSourceDocument) bool {
	return in.RepositoryID != "" && sourceVersionValid(in.RepositoryVersion) && len(in.Body) > 0 && len(in.Body) <= 1<<20 && in.Digest == helmDigest(in.Body) && len(in.ETag) <= 1024 && len(in.LastModified) <= 128 && !strings.ContainsAny(in.ETag+in.LastModified, "\x00\r\n") && !in.FetchedAt.IsZero() && in.ExpiresAt.After(in.FetchedAt) && in.ExpiresAt.Sub(in.FetchedAt) <= time.Hour
}
func validHelmSourceEntry(in HelmSourceEntry) bool {
	var meta struct{ Name, Version string }
	_, err := uuid.Parse(in.RepositoryID)
	_, versionErr := semver.StrictNewVersion(in.Version)
	return err == nil && versionErr == nil && helmName.MatchString(in.Name) && len(in.Version) <= 256 && validSHA256Digest(in.Digest) && sourceVersionValid(in.RepositoryVersion) && len(in.URL) <= 4096 && !strings.ContainsAny(in.URL, "\x00\r\n") && len(in.MetadataJSON) > 0 && len(in.MetadataJSON) <= 256<<10 && json.Unmarshal(in.MetadataJSON, &meta) == nil && meta.Name == in.Name && meta.Version == in.Version
}
func validHelmSnapshot(in HelmSourceSnapshot) bool {
	_, e := uuid.Parse(in.ID)
	_, v := uuid.Parse(in.ViewID)
	if e != nil || v != nil || !sourceVersionValid(in.ViewVersion) || len(in.Entries) > 4096 {
		return false
	}
	seen := map[string]bool{}
	for _, entry := range in.Entries {
		k := entry.Name + "@" + entry.Version
		if !validHelmSourceEntry(entry) || seen[k] {
			return false
		}
		seen[k] = true
	}
	body, e := json.Marshal(in.Entries)
	return e == nil && len(body) <= 8<<20 && in.Digest == helmDigest(body)
}
func cloneHelmDocument(in HelmSourceDocument) HelmSourceDocument {
	in.Body = append([]byte(nil), in.Body...)
	return in
}
func cloneHelmEntry(in HelmSourceEntry) HelmSourceEntry {
	in.MetadataJSON = append([]byte(nil), in.MetadataJSON...)
	return in
}
func cloneHelmSnapshot(in HelmSourceSnapshot) HelmSourceSnapshot {
	in.Entries = append([]HelmSourceEntry{}, in.Entries...)
	for i := range in.Entries {
		in.Entries[i] = cloneHelmEntry(in.Entries[i])
	}
	return in
}
