package app

import "testing"

func TestParseMavenCacheCoordinateSnapshotAndFilenameBoundary(t *testing.T) {
	for _, tc := range []struct {
		version string
		file    string
		valid   bool
	}{
		{"1.0", "widget-1.0.jar", true},
		{"1.0", "widget-1.0-sources.jar.sha256", true},
		{"1.0", "widget-1.01.jar", false},
		{"1.0", "maven-metadata.xml", false},
		{"2.0-SNAPSHOT", "widget-2.0-SNAPSHOT.jar", true},
		{"2.0-SNAPSHOT", "widget-2.0-20260907.010203-2.jar", true},
		{"2.0-SNAPSHOT", "widget-2.0-20260907.010203-2-sources.jar.sha256", true},
		{"2.0-SNAPSHOT", "widget-2.01-20260907.010203-2.jar", false},
		{"2.0-SNAPSHOT", "widget-2.0-20260907.010203-0.jar", false},
	} {
		t.Run(tc.file, func(t *testing.T) {
			got, ok := parseMavenCacheCoordinate("com/acme/widget/" + tc.version + "/" + tc.file)
			if ok != tc.valid {
				t.Fatalf("valid=%v want=%v", ok, tc.valid)
			}
			if ok && (got.GroupID != "com.acme" || got.ArtifactID != "widget" || got.Version != tc.version || got.FileName != tc.file) {
				t.Fatalf("coordinate=%+v", got)
			}
		})
	}
}
