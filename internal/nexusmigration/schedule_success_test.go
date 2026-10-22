package nexusmigration_test

import (
	"testing"

	m "github.com/ccsert/artifact-gateway/internal/nexusmigration"
)

func TestScheduleSuccessOnlyExemptsExactRegeneratedMavenMetadata(t *testing.T) {
	primary := m.Entry{SourceRepository: "releases", TargetRepositoryID: "target", Format: "maven", Identity: "org.example:widget:1.0", State: "committed", Assets: []m.Asset{{SourcePath: "org/example/widget/1.0/widget-1.0.jar"}, {SourcePath: "org/example/widget/1.0/widget-1.0.pom"}}}
	metadata := func(path, reason string) m.Entry {
		return m.Entry{SourceRepository: primary.SourceRepository, TargetRepositoryID: primary.TargetRepositoryID, Format: "maven", Identity: "/unsupported/" + path, State: "unsupported", LastError: reason}
	}
	for _, test := range []struct {
		name, path, reason string
		want               bool
	}{
		{"native-metadata", "org/example/widget/maven-metadata.xml", "native_checksum_or_metadata_regenerated", true},
		{"mixed-metadata", "org/example/widget/maven-metadata.xml", "maven_mixed_metadata_requires_regeneration", true},
		{"mixed-sha256", "org/example/widget/maven-metadata.xml.sha256", "maven_mixed_metadata_requires_regeneration", true},
		{"verified-release-sha1", "org/example/widget/1.0/widget-1.0.jar.sha1", "native_checksum_or_metadata_regenerated", true},
		{"verified-pom-md5", "org/example/widget/1.0/widget-1.0.pom.md5", "native_checksum_or_metadata_regenerated", true},
		{"evil-metadata", "org/example/widget/maven-metadata.xml.evil", "maven_mixed_metadata_requires_regeneration", false},
		{"evil-checksum", "org/example/widget/1.0/widget-1.0.jar.sha1.evil", "native_checksum_or_metadata_regenerated", false},
		{"missing-parent-asset", "org/example/widget/1.0/widget-1.0.war.sha1", "native_checksum_or_metadata_regenerated", false},
		{"snapshot", "org/example/widget/1.0-SNAPSHOT/maven-metadata.xml", "native_checksum_or_metadata_regenerated", false},
		{"mixed-snapshot", "org/example/widget/1.0-SNAPSHOT/maven-metadata.xml", "maven_mixed_metadata_requires_regeneration", false},
		{"unknown-packaging", "org/example/widget/1.0/widget-1.0.evil", "maven_release_closure_unsupported", false},
		{"missing-pom", "org/example/widget/1.0/widget-1.0.jar", "maven_complete_release_requires_pom", false},
		{"forged-code-on-payload", "org/example/widget/1.0/widget-1.0.jar", "native_checksum_or_metadata_regenerated", false},
		{"empty", "", "native_checksum_or_metadata_regenerated", false},
		{"root-metadata", "maven-metadata.xml", "native_checksum_or_metadata_regenerated", false},
		{"traversal", "../org/maven-metadata.xml", "native_checksum_or_metadata_regenerated", false},
		{"encoded-path", "org%2fexample/maven-metadata.xml", "native_checksum_or_metadata_regenerated", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			entry := metadata(test.path, test.reason)
			if got := m.ScheduleEntriesSuccessful([]m.Entry{primary, entry}); got != test.want {
				t.Fatalf("accepted=%v want=%v", got, test.want)
			}
			if entry.State != "unsupported" || entry.LastError != test.reason {
				t.Fatal("success classification rewrote original report")
			}
		})
	}
	for _, kind := range []string{"raw", "nonempty-assets", "wrong-target", "wrong-source", "failed", "source-deleted"} {
		t.Run(kind, func(t *testing.T) {
			e := metadata("org/example/widget/1.0/widget-1.0.jar.sha1", "native_checksum_or_metadata_regenerated")
			switch kind {
			case "raw":
				e.Format = "raw"
			case "nonempty-assets":
				e.Assets = []m.Asset{{SourcePath: "unverified"}}
			case "wrong-target":
				e.TargetRepositoryID = "other"
			case "wrong-source":
				e.SourceRepository = "other"
			case "failed":
				e.State = "failed"
			case "source-deleted":
				e.State = "source_deleted"
			}
			if m.ScheduleEntriesSuccessful([]m.Entry{primary, e}) {
				t.Fatal("non-regeneration state accepted")
			}
		})
	}
}
