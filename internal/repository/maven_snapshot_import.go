package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

var ErrMavenSnapshotImportRetention = errors.New("snapshot_import_retention_enabled")

// MavenSnapshotImportPlan is complete validated content for one coordinate.
// Native BuildNumber remains a unique local sequence for existing cursors;
// SourceTimestamp/SourceBuildNumber are the immutable imported identity.
type MavenSnapshotImportPlan struct {
	RepositoryID, TargetID, TargetBinding, SourceID, ManifestDigest, Coordinate, Actor string
	Artifacts                                                                          []MavenArtifact
	Assets                                                                             []MavenAsset
	Metadata                                                                           *MavenAsset
	Aliases                                                                            map[string]string
}

type MavenSnapshotImport struct {
	RepositoryID, Coordinate, TargetID, TargetBinding, SourceID, ManifestDigest, PlanDigest, SessionID, Actor, State string
	Metadata                                                                                                         *MavenAsset
	Aliases                                                                                                          map[string]string
	CreatedAt                                                                                                        time.Time
	TakeoverKey, TakeoverActor                                                                                       string
	TakenOverAt                                                                                                      time.Time
	CurrentBuildNumber                                                                                               int
	CurrentAliases                                                                                                   map[string]string
}

func (v MavenSnapshotImport) Writable() bool {
	return v.State == "committed" && !v.TakenOverAt.IsZero()
}

type MavenSnapshotImportStore interface {
	LockMavenSnapshotImport(context.Context, string, string) (context.Context, func(), error)
	CheckMavenSnapshotImport(context.Context, MavenSnapshotImportPlan) error
	BeginMavenSnapshotImport(context.Context, MavenSnapshotImportPlan) (MavenSnapshotImport, error)
	CommitMavenSnapshotImport(context.Context, MavenSnapshotImportPlan) (MavenSnapshotImport, error)
	GetMavenSnapshotImport(context.Context, string, string) (MavenSnapshotImport, error)
}

func mavenImportDigest(p MavenSnapshotImportPlan) string {
	// Actor identifies the original accountable operator; it is not part of the
	// content identity, allowing another authorized operator to resume.
	p.Actor = ""
	body, _ := json.Marshal(p)
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validMavenImportPlan(p MavenSnapshotImportPlan) bool {
	if !IsMavenSnapshotCoordinate(p.Coordinate) || len(strings.Split(p.Coordinate, ":")) != 3 || p.RepositoryID == "" || p.TargetID == "" || !ValidAPTSHA256Digest(p.TargetBinding) || p.SourceID == "" || p.Actor == "" || !ValidAPTSHA256Digest(p.ManifestDigest) || len(p.Artifacts) == 0 || len(p.Assets) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, a := range p.Artifacts {
		if a.RepositoryID != p.RepositoryID || a.Coordinate != p.Coordinate || a.SourceBuildNumber <= 0 || a.SourceTimestamp == "" || !ValidAPTSHA256Digest(a.Digest) {
			return false
		}
		if t, err := time.Parse("20060102.150405", a.SourceTimestamp); err != nil || t.Format("20060102.150405") != a.SourceTimestamp {
			return false
		}
		k := a.SourceTimestamp + ":" + strconv.Itoa(a.SourceBuildNumber)
		if seen[k] {
			return false
		}
		seen[k] = true
	}
	paths := map[string]bool{}
	for _, a := range p.Assets {
		if a.RepositoryID != p.RepositoryID || a.Size < 0 || !ValidAPTSHA256Digest(a.Digest) || a.ObjectKey != "native/maven/sha256/"+strings.TrimPrefix(a.Digest, "sha256:") || paths[a.Path] {
			return false
		}
		paths[a.Path] = true
		belongs := p.Metadata != nil && (a == *p.Metadata || strings.HasPrefix(a.Path, p.Metadata.Path+"."))
		for _, b := range p.Artifacts {
			belongs = belongs || mavenAssetBelongsToArtifactBuild(a, b)
		}
		if !belongs {
			return false
		}
	}
	for _, path := range p.Aliases {
		if !paths[path] {
			return false
		}
	}
	return p.Metadata == nil || paths[p.Metadata.Path]
}

func mavenArtifactFilePrefix(a MavenArtifact) string {
	t, b := a.CreatedAt, a.BuildNumber
	if a.SourceTimestamp != "" {
		t, _ = time.Parse("20060102.150405", a.SourceTimestamp)
		b = a.SourceBuildNumber
	}
	return mavenSnapshotBuildFilePrefix(a.Coordinate, t, b)
}

// LockMavenObject serializes importer object I/O and the GC delete interval.
// A carried lock context shares one dedicated coordination connection.
func (s *PostgresStore) LockMavenObject(ctx context.Context, key string) (context.Context, func(), error) {
	return s.lockPostgresAdvisoryKeys(ctx, []string{"native-maven-object:" + key})
}
func (s *MemoryStore) LockMavenObject(ctx context.Context, key string) (context.Context, func(), error) {
	release, err := s.LockOCIObject(ctx, "maven/"+key)
	return ctx, release, err
}
func (s *PostgresStore) LockMavenSnapshotImport(ctx context.Context, repo, coordinate string) (context.Context, func(), error) {
	return s.lockPostgresAdvisoryKeys(ctx, []string{"native-maven-import:" + repo + ":" + coordinate})
}
func (s *MemoryStore) LockMavenSnapshotImport(ctx context.Context, repo, coordinate string) (context.Context, func(), error) {
	release, err := s.LockOCIObject(ctx, "maven-import/"+repo+":"+coordinate)
	return ctx, release, err
}
