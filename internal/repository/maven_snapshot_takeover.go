package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
)

var (
	ErrMavenSnapshotTakenOver        = errors.New("snapshot_import_taken_over")
	ErrMavenSnapshotTakeoverNotReady = errors.New("snapshot_takeover_not_ready")
	ErrMavenSnapshotBuildExhausted   = errors.New("snapshot_build_sequence_exhausted")
)

const MaxMavenSnapshotBuildNumber = 2147483647

var mavenSnapshotSuffixPattern = regexp.MustCompile(`^(?:-([A-Za-z0-9][A-Za-z0-9_-]*))?\.([A-Za-z0-9][A-Za-z0-9.-]*)$`)

// MavenSnapshotAssetPair preserves Maven's extension/classifier boundary,
// including multi-part extensions such as jar.asc and tar.gz.
func MavenSnapshotAssetPair(suffix string) (extension, classifier string, ok bool) {
	m := mavenSnapshotSuffixPattern.FindStringSubmatch(suffix)
	if len(m) == 0 {
		return "", "", false
	}
	return m[2], m[1], true
}

type MavenSnapshotTakeoverStore interface {
	CheckMavenSnapshotTakeover(context.Context, MavenSnapshotImportPlan, string) (MavenSnapshotImport, error)
	TakeoverMavenSnapshotImport(context.Context, MavenSnapshotImportPlan, string) (MavenSnapshotImport, error)
	MavenSnapshotImportPathReserved(context.Context, string, string, string) (bool, error)
	FindMavenSnapshotDeployment(context.Context, string, string, string, string, int) (MavenPublishSession, error)
	CreateMavenSnapshotDeployment(context.Context, MavenPublishSession) (MavenPublishSession, error)
	CompleteMavenSnapshotDeployment(context.Context, string, string, string) (MavenSnapshotImport, error)
}

func validMavenSnapshotReceipt(s MavenPublishSession) bool {
	t, err := time.Parse("20060102.150405", s.ClientTimestamp)
	return err == nil && t.Format("20060102.150405") == s.ClientTimestamp && s.ClientBuildNumber > 0 && s.ClientBuildNumber <= MaxMavenSnapshotBuildNumber
}

// MavenSnapshotDeploymentFingerprint fences the exact facts validated before
// metadata completion, including the original client receipt and upload keys.
func MavenSnapshotDeploymentFingerprint(s MavenPublishSession, uploads map[string]string) string {
	data, _ := json.Marshal(struct {
		ID, RepositoryID, Coordinate, Publisher, PomObject, Timestamp string
		Build                                                         int
		Objects                                                       []MavenDeclaredObject
		Uploads                                                       map[string]string
	}{s.ID, s.RepositoryID, s.Coordinate, s.Publisher, s.PomObject, s.ClientTimestamp, s.ClientBuildNumber, s.Objects, uploads})
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func mavenSnapshotDeploymentAliases(v MavenSnapshotImport, s MavenPublishSession, artifact MavenArtifact, mainExtension string) (map[string]string, error) {
	if !v.Writable() || s.ID == v.SessionID || !validMavenSnapshotReceipt(s) || artifact.ID != s.ID || artifact.State != "visible" || artifact.SourceTimestamp != "" || artifact.BuildNumber <= 0 || s.RepositoryID != v.RepositoryID || s.Coordinate != v.Coordinate {
		return nil, ErrMavenSnapshotTakeoverNotReady
	}
	parts := strings.Split(s.Coordinate, ":")
	if len(parts) != 3 {
		return nil, ErrDisabled
	}
	prefix := parts[1] + "-" + parts[2]
	if s.PomObject != prefix+".pom" {
		return nil, ErrMavenSnapshotTakeoverNotReady
	}
	aliases := map[string]string{}
	previous := v.Aliases
	if v.CurrentBuildNumber > 0 {
		previous = v.CurrentAliases
	}
	for canonical, target := range previous {
		aliases[canonical] = target
	}
	pom, main := false, false
	for _, object := range s.Objects {
		if !strings.HasPrefix(object.Name, prefix) || strings.ContainsAny(object.Name, "/\\") || strings.Contains(object.Name, "..") {
			return nil, ErrDisabled
		}
		if _, _, ok := MavenSnapshotAssetPair(strings.TrimPrefix(object.Name, prefix)); !ok {
			return nil, ErrMavenSnapshotTakeoverNotReady
		}
		pom = pom || object.Name == s.PomObject
		main = main || object.Name == prefix+"."+mainExtension
		canonical := mavenArtifactPathPrefix(s.Coordinate) + object.Name
		aliases[canonical] = mavenSnapshotTimestampedPath(canonical, artifact.Coordinate, artifact.CreatedAt, artifact.BuildNumber)
		for _, suffix := range []string{".sha512", ".sha256", ".sha1", ".md5"} {
			aliases[canonical+suffix] = aliases[canonical] + suffix
		}
	}
	if !pom || !main {
		return nil, ErrMavenSnapshotTakeoverNotReady
	}
	return aliases, nil
}

func mavenArchivedNamespaceMatches(a MavenArtifact, path string) bool {
	prefix := mavenArtifactPathPrefix(a.Coordinate) + mavenArtifactFilePrefix(a)
	return strings.HasPrefix(path, prefix+".") || strings.HasPrefix(path, prefix+"-")
}
