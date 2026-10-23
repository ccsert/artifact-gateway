// Package rubygemspublication owns immutable RubyGems Hosted preview records and
// narrow persistence ports. SQL adapters live in its pgstore subpackage.
package rubygemspublication

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	protocol "github.com/ccsert/artifact-gateway/internal/protocol/rubygems"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

var ErrInvalid = errors.New("invalid RubyGems publication")
var ErrConflict = errors.New("RubyGem identity already contains different bytes")
var ErrExpired = errors.New("RubyGem upload session expired")
var ErrState = errors.New("RubyGem upload session state prevents this operation")

type Session struct {
	ID, RepositoryID, Name, Version, Platform, Digest, Actor, ObjectKey, State string
	Size                                                                       int64
	MetadataJSON, MetadataYAML                                                 []byte
	CreatedAt, ExpiresAt, CommittedAt, CollectedAt                             time.Time
}
type Publication struct {
	SessionID, RepositoryID, Name, Version, Platform, Digest, Publisher, ObjectKey string
	Size                                                                           int64
	MetadataJSON, MetadataYAML                                                     []byte
	PublishedAt                                                                    time.Time
}
type Capacity struct {
	UsedBytes    int64 `json:"usedBytes"`
	PendingBytes int64 `json:"pendingBytes"`
	ObjectCount  int64 `json:"objectCount"`
	QuotaBytes   int64 `json:"quotaBytes"`
}
type Store interface {
	Lock(context.Context, string, string) (context.Context, func(), error)
	Reserve(context.Context, Session) (Session, bool, error)
	Commit(context.Context, string, string) (Publication, bool, error)
	Get(context.Context, string, string) (Publication, error)
	List(context.Context, string, int) ([]Publication, error)
	GetSession(context.Context, string) (Session, error)
	Expire(context.Context, time.Time, int) ([]Session, error)
	MarkCollected(context.Context, string, string) error
	Capacity(context.Context, string) (Capacity, error)
	SetQuota(context.Context, string, string, int64) (Capacity, error)
}
type RepositoryReader interface {
	GetHostedRepository(context.Context, string) (repository.HostedRepository, error)
}
type Auditor interface {
	RecordAudit(context.Context, repository.AuditRecord) error
}

func Identity(name, version, platform string) string {
	return name + "\x00" + protocol.CanonicalVersion(version) + "\x00" + platform
}
func (s Session) Identity() string { return Identity(s.Name, s.Version, s.Platform) }
func (p Publication) FullName() string {
	m := protocol.Metadata{Name: p.Name, Version: p.Version, Platform: p.Platform}
	return m.FullName()
}
func ValidInput(in Session) bool {
	if _, e := uuid.Parse(in.ID); e != nil {
		return false
	}
	if _, e := uuid.Parse(in.RepositoryID); e != nil {
		return false
	}
	if in.Size < 1 || in.Size > protocol.MaxGemBytes || len(in.Digest) != 71 || in.Digest[:7] != "sha256:" || in.Actor == "" || len(in.Actor) > 256 {
		return false
	}
	if b, e := hex.DecodeString(in.Digest[7:]); e != nil || len(b) != 32 {
		return false
	}
	m, e := protocol.ParseMetadata(in.MetadataYAML)
	if e != nil || m.Name != in.Name || m.Version != in.Version || m.Platform != in.Platform {
		return false
	}
	encoded, _ := json.Marshal(m)
	return bytes.Equal(encoded, in.MetadataJSON)
}
func Same(a, b Session) bool {
	return a.RepositoryID == b.RepositoryID && a.Name == b.Name && a.Version == b.Version && a.Platform == b.Platform && a.Digest == b.Digest && a.Size == b.Size && bytes.Equal(a.MetadataJSON, b.MetadataJSON) && bytes.Equal(a.MetadataYAML, b.MetadataYAML)
}
func Audit(p Publication, repo string) repository.AuditRecord {
	return repository.AuditRecord{Actor: p.Publisher, Repository: repo, GroupName: repo, Format: "rubygems", Resource: p.FullName(), Operation: "rubygems.publish", Outcome: repository.AuditResolved, Status: 201, Bytes: p.Size, OccurredAt: p.PublishedAt, Evidence: map[string]string{"digest": p.Digest, "size": strconv.FormatInt(p.Size, 10), "name": p.Name, "version": p.Version, "platform": p.Platform, "sessionId": p.SessionID}}
}
func Active(repo repository.HostedRepository) bool {
	return repo.State == repository.RepositoryActive && repo.Format == repository.FormatRubyGems && repo.Type == repository.RepositoryTypeHosted
}
func ObjectKey(in Session) string {
	return "native/rubygems/" + in.RepositoryID + "/" + in.ID + "/package.gem"
}
func Visible(in Session) Publication {
	return Publication{SessionID: in.ID, RepositoryID: in.RepositoryID, Name: in.Name, Version: in.Version, Platform: in.Platform, Digest: in.Digest, Publisher: in.Actor, ObjectKey: in.ObjectKey, Size: in.Size, MetadataJSON: append([]byte(nil), in.MetadataJSON...), MetadataYAML: append([]byte(nil), in.MetadataYAML...), PublishedAt: in.CommittedAt}
}
