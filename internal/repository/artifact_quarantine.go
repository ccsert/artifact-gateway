package repository

import (
	"strings"
	"unicode/utf8"
)

func artifactQuarantineKey(repositoryID string, format Format, coordinate, digest string) string {
	return strings.Join([]string{repositoryID, string(format), coordinate, digest}, "\x00")
}

func validArtifactQuarantine(value ArtifactQuarantine) bool {
	if value.RepositoryID == "" || !IsSupportedFormat(value.Format) || strings.TrimSpace(value.Coordinate) == "" || utf8.RuneCountInString(value.Coordinate) > 1024 || strings.ContainsRune(value.Coordinate, '\x00') {
		return false
	}
	if value.Format == FormatConan && !validConanQuarantineAnchor(value.Coordinate) {
		return false
	}
	if len(value.Digest) != len("sha256:")+64 || !strings.HasPrefix(value.Digest, "sha256:") {
		return false
	}
	for _, character := range value.Digest[len("sha256:"):] {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	if value.State != ArtifactQuarantineStateQuarantined && value.State != ArtifactQuarantineStateReleased {
		return false
	}
	if strings.TrimSpace(value.Reason) == "" || utf8.RuneCountInString(value.Reason) > 1024 || strings.ContainsRune(value.Reason, '\x00') {
		return false
	}
	return strings.TrimSpace(value.UpdatedBy) != "" && utf8.RuneCountInString(value.UpdatedBy) <= 512 && !strings.ContainsRune(value.UpdatedBy, '\x00')
}

func validConanQuarantineAnchor(coordinate string) bool {
	reference, revision, found := strings.Cut(coordinate, "#")
	if !found || revision == "" || revision == "." || revision == ".." || strings.ContainsAny(revision, "/\\#") {
		return false
	}
	parts := strings.Split(reference, "/")
	if len(parts) != 4 {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\#") {
			return false
		}
	}
	return true
}
