package nexusmigration

import (
	"path"
	"strings"
)

// ScheduleEntriesSuccessful keeps the original unsupported counters intact.
// Only the two existing Maven regeneration categories may accompany verified
// native content; arbitrary unsupported payloads never become plan success.
func ScheduleEntriesSuccessful(entries []Entry) bool {
	primary := map[string]string{}
	for _, e := range entries {
		if e.Format == "maven" && (e.State == "committed" || e.State == "skipped") {
			for _, a := range e.Assets {
				primary[e.SourceRepository+"\x00"+e.TargetRepositoryID+"\x00"+a.SourcePath] = e.Identity
			}
		}
	}
	for _, e := range entries {
		if e.State == "committed" || e.State == "skipped" {
			continue
		}
		original, identity, valid := MavenRegenerationReference(e)
		if !valid || original != "" && primary[e.SourceRepository+"\x00"+e.TargetRepositoryID+"\x00"+original] != identity {
			return false
		}
	}
	return true
}

// MavenRegenerationReference validates only the two existing report categories.
// A nonempty original path must be linked to the same batch's verified Maven
// entry using the returned native coordinate. Empty original denotes metadata.
func MavenRegenerationReference(e Entry) (original, identity string, valid bool) {
	if e.State != "unsupported" || e.Format != "maven" || len(e.Assets) != 0 || !strings.HasPrefix(e.Identity, "/unsupported/") {
		return "", "", false
	}
	p := strings.TrimPrefix(e.Identity, "/unsupported/")
	if p == "" || len(p) > 2048 || path.IsAbs(p) || path.Clean(p) != p || strings.ContainsAny(p, "\\\x00\r\n?#%") {
		return "", "", false
	}
	parts := strings.Split(p, "/")
	if len(parts) < 2 {
		return "", "", false
	}
	for _, part := range parts[:len(parts)-1] {
		if part == "." || part == ".." || part == "" || strings.HasSuffix(strings.ToUpper(part), "-SNAPSHOT") {
			return "", "", false
		}
	}
	base := path.Base(p)
	metadata := base == "maven-metadata.xml" || base == "maven-metadata.xml.sha1" || base == "maven-metadata.xml.sha256" || base == "maven-metadata.xml.sha512" || base == "maven-metadata.xml.md5"
	switch e.LastError {
	case "maven_mixed_metadata_requires_regeneration":
		return "", "", metadata
	case "native_checksum_or_metadata_regenerated":
		if metadata {
			return "", "", true
		}
		original = ""
		for _, extension := range []string{".sha1", ".sha256", ".sha512", ".md5"} {
			if strings.HasSuffix(base, extension) {
				original = strings.TrimSuffix(p, extension)
				break
			}
		}
		identity, release := mavenIdentity(original)
		return original, identity, release
	default:
		return "", "", false
	}
}
