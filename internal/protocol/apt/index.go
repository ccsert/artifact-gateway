package apt

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// PackageIndexStanza returns the package-owned control fields plus the
// repository-owned immutable location and checksum fields. Client-supplied
// values for those repository fields are deliberately replaced.
func PackageIndexStanza(metadata DebianBinaryMetadata, filename string, size int64, digest string) ([]byte, error) {
	if len(metadata.Control) == 0 || !validPackageIndexFilename(filename) ||
		size <= 0 || len(digest) != 71 || !strings.HasPrefix(digest, "sha256:") {
		return nil, errors.New("invalid Debian package index input")
	}
	for _, r := range digest[7:] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return nil, errors.New("invalid Debian package digest")
		}
	}
	lines := strings.Split(strings.TrimSuffix(string(metadata.Control), "\n"), "\n")
	var output bytes.Buffer
	for index := 0; index < len(lines); {
		line := lines[index]
		if line == "" || line[0] == ' ' || line[0] == '\t' {
			return nil, errors.New("invalid Debian control field ordering")
		}
		name, _, found := strings.Cut(line, ":")
		if !found {
			return nil, errors.New("invalid Debian control field")
		}
		next := index + 1
		for next < len(lines) && lines[next] != "" && (lines[next][0] == ' ' || lines[next][0] == '\t') {
			next++
		}
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "filename", "size", "sha512", "sha256", "sha1", "md5sum":
		default:
			for _, fieldLine := range lines[index:next] {
				output.WriteString(fieldLine)
				output.WriteByte('\n')
			}
		}
		index = next
	}
	if output.Len() == 0 {
		return nil, errors.New("debian control stanza is empty")
	}
	fmt.Fprintf(&output, "Filename: %s\nSize: %s\nSHA256: %s\n\n", filename, strconv.FormatInt(size, 10), strings.TrimPrefix(digest, "sha256:"))
	return output.Bytes(), nil
}

func validPackageIndexFilename(filename string) bool {
	if filename == "" || len(filename) > 2048 || strings.HasPrefix(filename, "/") || strings.ContainsAny(filename, "\\\x00\r\n\t?#") {
		return false
	}
	for _, segment := range strings.Split(filename, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return strings.HasPrefix(filename, "pool/")
}
