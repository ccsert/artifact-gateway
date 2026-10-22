package repository

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/google/uuid"
)

// FormatHelm identifies an opt-in traditional HTTP preview. It is intentionally absent
// from SupportedFormatProfiles and the public management Format enum.
const FormatHelm Format = "helm"

var (
	ErrInvalidHelmPublication  = errors.New("invalid Helm publication")
	ErrHelmPublicationConflict = errors.New("helm chart identity contains a different immutable closure")
	ErrHelmSessionExpired      = errors.New("helm publication reservation expired")
	ErrHelmSessionState        = errors.New("helm publication session state prevents this operation")
)

type HelmSession struct {
	ID, RepositoryID, Name, Version, Digest, ProvenanceDigest, Actor string
	SourceVersion                                                    string
	Size, ProvenanceSize                                             int64
	State, ObjectKey, ProvenanceKey                                  string
	MetadataJSON, ChartYAML                                          []byte
	CreatedAt, ExpiresAt, CommittedAt, CollectedAt                   time.Time
}
type HelmPublication struct {
	RepositoryID, Name, Version, Digest, ProvenanceDigest, ObjectKey, ProvenanceKey, SessionID, Publisher string
	Size, ProvenanceSize                                                                                  int64
	MetadataJSON, ChartYAML                                                                               []byte
	PublishedAt                                                                                           time.Time
}

// NativeHelmStore is a narrow, explicitly injected publication port. It is not
// embedded in GatewayStore. Bytes are validated by the format before commit.
type NativeHelmStore interface {
	ReserveHelmPublication(context.Context, HelmSession) (HelmSession, bool, error)
	GetHelmSession(context.Context, string) (HelmSession, error)
	CommitHelmPublication(context.Context, string, string) (HelmPublication, bool, error)
	GetHelmPublication(context.Context, string, string, string) (HelmPublication, error)
	ListHelmPublications(context.Context, string, int) ([]HelmPublication, error)
	ExpireHelmSessions(context.Context, time.Time, int) ([]HelmSession, error)
	MarkHelmSessionCollected(context.Context, string, string) error
	LockHelmPublication(context.Context, string, string, string) (context.Context, func(), error)
}

var helmName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func validHelmSession(in HelmSession) bool {
	_, idErr := uuid.Parse(in.ID)
	_, repoErr := uuid.Parse(in.RepositoryID)
	_, versionErr := semver.StrictNewVersion(in.Version)
	digestOK := func(d string) bool {
		value, err := hex.DecodeString(strings.TrimPrefix(d, "sha256:"))
		return strings.HasPrefix(d, "sha256:") && d == strings.ToLower(d) && err == nil && len(value) == 32
	}
	var meta struct{ Name, Version string }
	return idErr == nil && repoErr == nil && helmName.MatchString(in.Name) && len(in.Version) <= 256 && versionErr == nil &&
		digestOK(in.Digest) && in.Size > 0 && in.Size <= 64<<20 && in.ProvenanceSize >= 0 && in.ProvenanceSize <= 1<<20 &&
		((in.ProvenanceSize == 0 && in.ProvenanceDigest == "") || (in.ProvenanceSize > 0 && digestOK(in.ProvenanceDigest))) &&
		len(in.ChartYAML) > 0 && len(in.ChartYAML) <= 1<<20 && len(in.MetadataJSON) > 0 && len(in.MetadataJSON) <= 256<<10 &&
		json.Unmarshal(in.MetadataJSON, &meta) == nil && meta.Name == in.Name && meta.Version == in.Version &&
		in.Actor != "" && len(in.Actor) <= 512 && !strings.ContainsAny(in.Actor, "\x00\r\n") && !in.ExpiresAt.IsZero()
}
func helmSessionSame(a, b HelmSession) bool {
	return a.RepositoryID == b.RepositoryID && a.Name == b.Name && a.Version == b.Version && a.Digest == b.Digest && a.Size == b.Size && a.ProvenanceDigest == b.ProvenanceDigest && a.ProvenanceSize == b.ProvenanceSize && bytes.Equal(a.MetadataJSON, b.MetadataJSON) && bytes.Equal(a.ChartYAML, b.ChartYAML)
}
func helmObjectKeys(in HelmSession) (string, string) {
	base := "native/helm/" + in.RepositoryID + "/sessions/" + in.ID + "/"
	chart := base + "sha256/" + strings.TrimPrefix(in.Digest, "sha256:")
	prov := ""
	if in.ProvenanceSize > 0 {
		prov = base + "provenance/sha256/" + strings.TrimPrefix(in.ProvenanceDigest, "sha256:")
	}
	return chart, prov
}
func helmKey(repo, name, version string) string { return repo + "\x00" + name + "\x00" + version }
func cloneHelmSession(in HelmSession) HelmSession {
	in.MetadataJSON = append([]byte(nil), in.MetadataJSON...)
	in.ChartYAML = append([]byte(nil), in.ChartYAML...)
	return in
}
func cloneHelmPublication(in HelmPublication) HelmPublication {
	in.MetadataJSON = append([]byte(nil), in.MetadataJSON...)
	in.ChartYAML = append([]byte(nil), in.ChartYAML...)
	return in
}
func helmPublication(in HelmSession) HelmPublication {
	return HelmPublication{RepositoryID: in.RepositoryID, Name: in.Name, Version: in.Version, Digest: in.Digest, ProvenanceDigest: in.ProvenanceDigest, ObjectKey: in.ObjectKey, ProvenanceKey: in.ProvenanceKey, SessionID: in.ID, Publisher: in.Actor, Size: in.Size, ProvenanceSize: in.ProvenanceSize, MetadataJSON: append([]byte(nil), in.MetadataJSON...), ChartYAML: append([]byte(nil), in.ChartYAML...), PublishedAt: in.CommittedAt}
}
func helmPublicationAudit(in HelmPublication, repo string) AuditRecord {
	return AuditRecord{Repository: repo, GroupName: repo, Actor: in.Publisher, Format: string(FormatHelm), Resource: in.Name + "@" + in.Version, Operation: "helm.publish", Outcome: AuditResolved, Status: 201, OccurredAt: in.PublishedAt, Evidence: map[string]string{"digest": in.Digest, "provenanceDigest": in.ProvenanceDigest, "sessionId": in.SessionID}}
}
