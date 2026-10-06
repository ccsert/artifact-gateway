package snapshotfixture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/artifact-gateway/artifact-gateway/internal/capacityplan"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/snapshotimport"
)

func WriteSnapshotManifest(t testing.TB, dir string, m snapshotimport.Manifest) string {
	t.Helper()
	body, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "manifest.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// SnapshotBundle contains only invented coordinates and bytes. The source
// selects old build 7 for main files, and the other build 7 for sources.
type BuildIdentity struct {
	Timestamp string
	Number    int
}

func SnapshotBundle(t testing.TB) (string, string, snapshotimport.Manifest, map[string][]byte) {
	return SnapshotBundleWithBuilds(t, []BuildIdentity{{"20260101.000000", 7}, {"20260102.000000", 7}, {"20260103.000000", 8}})
}

// SnapshotArchetypeBundle keeps the same synthetic history and old current
// selectors, with the official archetype packaging and extension declaration.
func SnapshotArchetypeBundle(t testing.TB) (string, string, snapshotimport.Manifest, map[string][]byte) {
	t.Helper()
	dir, _, m, files := SnapshotBundle(t)
	for i := range m.Coordinates[0].Builds {
		f := &m.Coordinates[0].Builds[i].Files[0]
		body := []byte(strings.ReplaceAll(string(files[f.Path]), "</project>", "<packaging>maven-archetype</packaging><build><extensions><extension><groupId>org.apache.maven.archetype</groupId><artifactId>archetype-packaging</artifactId><version>3.4.1</version></extension></extensions></build></project>"))
		if err := os.WriteFile(filepath.Join(dir, f.Path), body, 0600); err != nil {
			t.Fatal(err)
		}
		files[f.Path] = body
		f.Size = int64(len(body))
		sum := sha256.Sum256(body)
		f.Digest = "sha256:" + hex.EncodeToString(sum[:])
	}
	return dir, WriteSnapshotManifest(t, dir, m), m, files
}

func SnapshotBundleWithBuilds(t testing.TB, identities []BuildIdentity) (string, string, snapshotimport.Manifest, map[string][]byte) {
	t.Helper()
	dir := t.TempDir()
	files := map[string][]byte{}
	m := snapshotimport.Manifest{SchemaVersion: 1, SourceID: "synthetic-frozen-source", Complete: true, Coordinates: []snapshotimport.Coordinate{{Coordinate: "org.example:widget:1.0-SNAPSHOT"}}, Excluded: []snapshotimport.Exclusion{}}
	base := "org/example/widget/1.0-SNAPSHOT/"
	add := func(name, body string) snapshotimport.File {
		path := base + name
		data := []byte(body)
		files[path] = data
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, path)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, path), data, 0600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		return snapshotimport.File{Path: path, Digest: "sha256:" + hex.EncodeToString(sum[:]), Size: int64(len(data))}
	}
	if len(identities) != 3 {
		t.Fatal("fixture requires three source identities")
	}
	for i, identity := range identities {
		stamp, build := identity.Timestamp, identity.Number
		prefix := "widget-1.0-" + stamp + "-" + strconv.Itoa(build)
		b := snapshotimport.Build{Timestamp: stamp, BuildNumber: build}
		for _, pair := range []struct{ ext, body string }{{"pom", "<project><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.0-SNAPSHOT</version></project>"}, {"jar", "synthetic jar " + strconv.Itoa(i)}, {"sources.jar", "synthetic sources " + strconv.Itoa(i)}, {"tests.zip", "synthetic zip " + strconv.Itoa(i)}} {
			suffix := "." + pair.ext
			if pair.ext == "sources.jar" {
				suffix = "-sources.jar"
			}
			if pair.ext == "tests.zip" {
				suffix = "-tests.zip"
			}
			b.Files = append(b.Files, add(prefix+suffix, pair.body))
		}
		m.Coordinates[0].Builds = append(m.Coordinates[0].Builds, b)
	}
	metadata := `<metadata><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.0-SNAPSHOT</version><versioning><snapshot><timestamp>` + identities[0].Timestamp + `</timestamp><buildNumber>` + strconv.Itoa(identities[0].Number) + `</buildNumber></snapshot><snapshotVersions>`
	value := func(i int) string { return "1.0-" + identities[i].Timestamp + "-" + strconv.Itoa(identities[i].Number) }
	for _, pair := range []struct{ ext, classifier, value string }{{"pom", "", value(0)}, {"jar", "", value(0)}, {"jar", "sources", value(1)}, {"zip", "tests", value(2)}} {
		metadata += `<snapshotVersion><extension>` + pair.ext + `</extension><classifier>` + pair.classifier + `</classifier><value>` + pair.value + `</value><updated>20260103000000</updated></snapshotVersion>`
	}
	metadata += `</snapshotVersions></versioning></metadata>`
	f := add("maven-metadata.xml", metadata)
	m.Coordinates[0].Metadata = &f
	return dir, WriteSnapshotManifest(t, dir, m), m, files
}

func SnapshotCapacity(t testing.TB, p *snapshotimport.Prepared, repo, target, actor string) capacityplan.Plan {
	t.Helper()
	plans, err := p.References(repo, target, actor, SnapshotTargetBinding)
	if err != nil {
		t.Fatal(err)
	}
	zero, free := int64(0), int64(1<<40)
	refs := snapshotimport.CapacityReferences(plans)
	plan := capacityplan.Plan{SchemaVersion: 1, InventoryID: p.Digest, Complete: true, TargetID: SnapshotTargetBinding, References: refs, Snapshot: capacityplan.Snapshot{TargetID: SnapshotTargetBinding, ObservedAt: time.Now().UTC().Add(-time.Minute), ValidUntil: time.Now().UTC().Add(time.Hour), StoragePoolID: "synthetic-pool", FreeBytes: &free, Repositories: []capacityplan.RepositoryCapacity{{RepositoryID: repo, UsedBytes: &zero, QuotaBytes: &zero}}, Objects: []capacityplan.Presence{}, References: []capacityplan.Membership{}}, Peak: capacityplan.PeakBudget{StoragePoolID: "synthetic-pool", DownloadBytes: &zero, UploadBytes: &zero, BackupBytes: &zero, RestoreBytes: &zero, HeadroomBytes: &zero}}
	seen := map[string]bool{}
	for _, r := range refs {
		if !seen[r.ObjectKey] {
			seen[r.ObjectKey] = true
			plan.Snapshot.Objects = append(plan.Snapshot.Objects, capacityplan.Presence{Key: r.ObjectKey, State: "absent"})
		}
		plan.Snapshot.References = append(plan.Snapshot.References, capacityplan.Membership{RepositoryID: repo, Key: r.LogicalKey, State: "absent"})
	}
	return plan
}

const SnapshotTargetBinding = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
