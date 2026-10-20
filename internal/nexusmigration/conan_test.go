package nexusmigration_test

import (
	"strings"
	"testing"

	m "github.com/ccsert/artifact-gateway/internal/nexusmigration"
)

const conanRecipeSource = "conans/demo/1.0/_/_/revisions/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/files/"
const conanPackageSource = "conans/demo/1.0/_/_/revisions/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/packages/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb/revisions/cccccccccccccccccccccccccccccccc/files/"

func TestConanInventoryIdentity(t *testing.T) {
	for _, tc := range []struct{ source, want, file, kind string }{
		{conanRecipeSource + "conanfile.py", "demo/1.0/_/_#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "conanfile.py", "recipe"},
		{conanPackageSource + "conan_package.tgz", "demo/1.0/_/_#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb#cccccccccccccccccccccccccccccccc", "conan_package.tgz", "package"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			identity, file, err := m.ConanAssetIdentity(tc.source)
			if err != nil || identity != tc.want || file != tc.file {
				t.Fatalf("identity=%q file=%q err=%v", identity, file, err)
			}
			coordinate, err := m.ParseConanIdentity(identity)
			if err != nil || coordinate.Kind != tc.kind || coordinate.Reference != "demo/1.0/_/_" || coordinate.Identity() != identity {
				t.Fatalf("coordinate=%+v err=%v", coordinate, err)
			}
		})
	}
}

func TestConanInventoryRejectsUnknownShapes(t *testing.T) {
	for _, source := range []string{
		"_/demo/1.0/_#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/conanfile.py",
		"conans/demo/1.0/_/_/export/conanfile.py",
		"v2/" + conanRecipeSource + "conanfile.py",
		"/" + conanRecipeSource + "conanfile.py",
		strings.Replace(conanRecipeSource, strings.Repeat("a", 32), "git-sha", 1) + "conanfile.py",
		conanRecipeSource + "metadata/foo.json",
		conanRecipeSource + "../conanfile.py",
		conanRecipeSource + "%2e%2e/conanfile.py",
		conanRecipeSource + "conan_export.txz",
	} {
		t.Run(source, func(t *testing.T) {
			if _, _, err := m.ConanAssetIdentity(source); err == nil {
				t.Fatal("unsupported source shape was admitted")
			}
		})
	}
}

func TestConanInventoryRequiresCompleteRevision(t *testing.T) {
	entry := conanInventoryEntry(conanRecipeSource, []string{"conanfile.py", "conanmanifest.txt"})
	if err := m.ValidateConanEntry(entry); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []string{"missing-manifest", "missing-recipe", "duplicate", "mixed-coordinate", "mixed-target", "missing-sha", "oversized-manifest"} {
		t.Run(tc, func(t *testing.T) {
			candidate := entry
			candidate.Assets = append([]m.Asset(nil), entry.Assets...)
			switch tc {
			case "missing-manifest":
				candidate.Assets = candidate.Assets[:1]
			case "missing-recipe":
				candidate.Assets = candidate.Assets[1:]
			case "duplicate":
				candidate.Assets = append(candidate.Assets, candidate.Assets[0])
			case "mixed-coordinate":
				candidate.Assets[0].SourcePath = strings.Replace(candidate.Assets[0].SourcePath, "demo/1.0", "other/1.0", 1)
			case "mixed-target":
				candidate.Assets[0].TargetPath = "other.py"
			case "missing-sha":
				candidate.Assets[0].SHA256 = ""
			case "oversized-manifest":
				candidate.Assets[1].Size = (1 << 20) + 1
			}
			if err := m.ValidateConanEntry(candidate); err == nil {
				t.Fatal("incomplete or mixed inventory was admitted")
			}
		})
	}
	pack := conanInventoryEntry(conanPackageSource, []string{"conaninfo.txt", "conan_package.tgz", "conanmanifest.txt"})
	if err := m.ValidateConanEntry(pack); err != nil {
		t.Fatal(err)
	}
	pack.Assets = pack.Assets[1:]
	if err := m.ValidateConanEntry(pack); err == nil {
		t.Fatal("package without conaninfo was admitted")
	}
}

func conanInventoryEntry(prefix string, names []string) m.Entry {
	identity, _, _ := m.ConanAssetIdentity(prefix + names[0])
	entry := m.Entry{Format: "conan", Identity: identity}
	for _, name := range names {
		entry.Assets = append(entry.Assets, m.Asset{SourcePath: prefix + name, TargetPath: name, Size: 1, SHA256: strings.Repeat("d", 64)})
	}
	return entry
}
