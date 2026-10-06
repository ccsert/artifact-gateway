package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/snapshotimport"
)

var mavenClientReceiptPattern = regexp.MustCompile(`^([0-9]{8}\.[0-9]{6})-([1-9][0-9]*)(?:-|\.)`)

func mavenClientReceipt(artifact, version, name string) (string, int, bool) {
	prefix := artifact + "-" + strings.TrimSuffix(version, "-SNAPSHOT") + "-"
	if !strings.HasPrefix(name, prefix) {
		return "", 0, false
	}
	m := mavenClientReceiptPattern.FindStringSubmatch(strings.TrimPrefix(name, prefix))
	if len(m) == 0 {
		return "", 0, false
	}
	n, err := strconv.Atoi(m[2])
	if err != nil || n > repository.MaxMavenSnapshotBuildNumber {
		return "", 0, false
	}
	t, err := time.Parse("20060102.150405", m[1])
	return m[1], n, err == nil && t.Format("20060102.150405") == m[1]
}

func (h nativeMavenHandler) completeSnapshotDeployment(ctx context.Context, imported repository.MavenSnapshotImport, actor string, data []byte) error {
	stamp, number, err := snapshotimport.DeploymentReceipt(data, imported.Coordinate)
	if err != nil {
		return err
	}
	s, err := h.store.FindMavenSnapshotDeployment(ctx, imported.RepositoryID, imported.Coordinate, actor, stamp, number)
	if err != nil {
		return err
	}
	if s.ID == imported.SessionID || s.State != "open" && s.State != "committed" || s.State == "open" && !s.ExpiresAt.After(time.Now()) {
		return repository.ErrMavenSnapshotTakeoverNotReady
	}
	parts := strings.Split(s.Coordinate, ":")
	base := mavenCoordinatePath(s.Coordinate) + "/"
	prefix := parts[1] + "-" + parts[2]
	clientPrefix := parts[1] + "-" + strings.TrimSuffix(parts[2], "-SNAPSHOT") + "-" + stamp + "-" + strconv.Itoa(number)
	allowed := map[string]string{}
	previous := imported.Aliases
	if imported.CurrentBuildNumber > 0 {
		previous = imported.CurrentAliases
	}
	for canonical, target := range previous {
		allowed[canonical] = target
	}
	uploads := map[string]string{}
	var pom []byte
	for _, object := range s.Objects {
		if _, _, ok := repository.MavenSnapshotAssetPair(strings.TrimPrefix(object.Name, prefix)); !strings.HasPrefix(object.Name, prefix) || !ok || strings.ContainsAny(object.Name, "/\\") || strings.Contains(object.Name, "..") {
			return repository.ErrMavenSnapshotTakeoverNotReady
		}
		key := "native/maven/sha256/" + strings.TrimPrefix(object.Digest, "sha256:")
		reader, size, err := h.objects.Open(ctx, key)
		if err != nil {
			return err
		}
		if size != object.Size {
			_ = reader.Close()
			return errors.New("deployment object size changed")
		}
		digest := sha256.New()
		if object.Name == s.PomObject {
			pom, err = io.ReadAll(io.LimitReader(reader, 16<<20+1))
			if len(pom) > 16<<20 {
				err = errors.New("deployment POM is too large")
			}
			_, _ = digest.Write(pom)
		} else {
			var n int64
			n, err = io.Copy(digest, io.LimitReader(reader, object.Size+1))
			if n != object.Size {
				err = errors.New("deployment object size changed")
			}
		}
		closeErr := reader.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if "sha256:"+hex.EncodeToString(digest.Sum(nil)) != object.Digest {
			return errors.New("deployment object bytes changed")
		}
		uploads[object.Name] = key
		allowed[base+object.Name] = base + clientPrefix + strings.TrimPrefix(object.Name, prefix)
	}
	main, err := snapshotimport.POMMainExtension(pom, s.Coordinate)
	if err != nil {
		return err
	}
	_, _, aliases, err := snapshotimport.DeploymentMetadata(data, s.Coordinate, allowed)
	if err != nil {
		return err
	}
	for _, object := range s.Objects {
		canonical := base + object.Name
		if aliases[canonical] != allowed[canonical] {
			return repository.ErrMavenSnapshotTakeoverNotReady
		}
	}

	for _, suffix := range []string{".pom", "." + main} {
		canonical := base + prefix + suffix
		if aliases[canonical] != base+clientPrefix+suffix || uploads[prefix+suffix] == "" {
			return repository.ErrMavenSnapshotTakeoverNotReady
		}
	}
	_, err = h.store.CompleteMavenSnapshotDeployment(ctx, s.ID, repository.MavenSnapshotDeploymentFingerprint(s, uploads), main)
	return err
}

func (h nativeMavenHandler) writeTakenOverSnapshotMetadata(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, imported repository.MavenSnapshotImport, requestedPath, actor string, checksum string, items []repository.MavenArtifact) {
	var current repository.MavenArtifact
	for _, item := range items {
		if item.Coordinate == imported.Coordinate && item.BuildNumber == imported.CurrentBuildNumber && item.SourceTimestamp == "" {
			current = item
		}
	}
	if current.ID == "" {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(imported.Coordinate, ":")
	var body strings.Builder
	body.WriteString("<metadata><groupId>" + parts[0] + "</groupId><artifactId>" + parts[1] + "</artifactId><version>" + parts[2] + "</version><versioning><snapshot><timestamp>" + current.CreatedAt.UTC().Format("20060102.150405") + "</timestamp><buildNumber>" + strconv.Itoa(current.BuildNumber) + "</buildNumber></snapshot><snapshotVersions>")
	keys := make([]string, 0, len(imported.CurrentAliases))
	for key := range imported.CurrentAliases {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, canonical := range keys {
		if mavenChecksumSidecar(canonical) {
			continue
		}
		target := imported.CurrentAliases[canonical]
		if _, err := h.store.GetMavenAsset(r.Context(), repo.ID, target); err != nil {
			http.NotFound(w, r)
			return
		}
		name := canonical[strings.LastIndexByte(canonical, '/')+1:]
		ext, classifier, ok := repository.MavenSnapshotAssetPair(strings.TrimPrefix(name, parts[1]+"-"+parts[2]))
		if !ok {
			http.NotFound(w, r)
			return
		}
		suffix := "." + ext
		if classifier != "" {
			suffix = "-" + classifier + suffix
		}
		targetName := target[strings.LastIndexByte(target, '/')+1:]
		value := strings.TrimSuffix(strings.TrimPrefix(targetName, parts[1]+"-"), suffix)
		body.WriteString("<snapshotVersion><extension>" + ext + "</extension>")
		if classifier != "" {
			body.WriteString("<classifier>" + classifier + "</classifier>")
		}
		body.WriteString("<value>" + mavenXMLText(value) + "</value><updated>" + current.CreatedAt.UTC().Format("20060102150405") + "</updated></snapshotVersion>")
	}
	body.WriteString("</snapshotVersions></versioning></metadata>")
	h.writeGeneratedMavenMetadata(w, r, repo, requestedPath, actor, []byte(body.String()), checksum)
}

func mavenChecksumSidecar(path string) bool {
	for _, suffix := range []string{".sha512", ".sha256", ".sha1", ".md5"} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

func mavenXMLText(value string) string {
	var out strings.Builder
	_ = xml.EscapeText(&out, []byte(value))
	return out.String()
}
