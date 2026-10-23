package identity

import (
	"errors"
	"path"
	"regexp"
	"strings"
)

var ErrPyPIImport = errors.New("invalid PyPI import closure")
var pypiSeparators = regexp.MustCompile(`[-_.]+`)
var pypiNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,253}[a-z0-9])?$`)
var pypiVersionPattern = regexp.MustCompile(`^[0-9]+(?:![0-9]+)?(?:\.[0-9]+)*(?:(?:a|b|rc)[0-9]+)?(?:\.post[0-9]+)?(?:\.dev[0-9]+)?(?:\+[a-z0-9]+(?:[._-][a-z0-9]+)*)?$`)
var pypiTagPattern = regexp.MustCompile(`^[A-Za-z0-9_]+(?:\.[A-Za-z0-9_]+)*$`)
var pypiBuildPattern = regexp.MustCompile(`^[0-9][A-Za-z0-9_]*$`)

func NormalizePyPIName(s string) string {
	return pypiSeparators.ReplaceAllString(strings.ToLower(s), "-")
}
func ValidPyPIName(s string) bool    { return pypiNamePattern.MatchString(s) }
func ValidPyPIVersion(s string) bool { return len(s) <= 256 && pypiVersionPattern.MatchString(s) }
func ValidPyPIPath(s string) bool {
	return s != "" && len(s) <= 4096 && !strings.HasPrefix(s, "/") && path.Clean(s) == s && s != ".." && !strings.HasPrefix(s, "../") && !strings.ContainsAny(s, "\\%?#\x00\r\n")
}
func ValidPyPIDigest(s string) bool { return len(s) == 64 && strings.Trim(s, "0123456789abcdef") == "" }

type PyPIDistribution struct{ Project, Version, Filename, FileType, PythonVersion, RequiresPython string }

// PyPIFilenameIdentity accepts only the explicitly admitted wheel and modern sdist shapes.
func PyPIFilenameIdentity(filename string) (PyPIDistribution, error) {
	d := PyPIDistribution{Filename: filename}
	if !ValidPyPIPath(filename) || strings.Contains(filename, "/") {
		return d, ErrPyPIImport
	}
	if strings.HasSuffix(filename, ".whl") {
		parts := strings.Split(strings.TrimSuffix(filename, ".whl"), "-")
		if len(parts) != 5 && len(parts) != 6 {
			return d, ErrPyPIImport
		}
		d.Project, d.Version, d.FileType, d.PythonVersion = NormalizePyPIName(parts[0]), parts[1], "bdist_wheel", parts[len(parts)-3]
		if len(parts) == 6 && !pypiBuildPattern.MatchString(parts[2]) {
			return d, ErrPyPIImport
		}
		for _, tag := range parts[len(parts)-3:] {
			if !pypiTagPattern.MatchString(tag) {
				return d, ErrPyPIImport
			}
		}
	} else if strings.HasSuffix(filename, ".tar.gz") {
		base := strings.TrimSuffix(filename, ".tar.gz")
		i := strings.LastIndex(base, "-")
		if i < 1 {
			return d, ErrPyPIImport
		}
		d.Project, d.Version, d.FileType, d.PythonVersion = NormalizePyPIName(base[:i]), base[i+1:], "sdist", "source"
	} else {
		return d, ErrPyPIImport
	}
	if !ValidPyPIName(d.Project) || !ValidPyPIVersion(d.Version) {
		return d, ErrPyPIImport
	}
	return d, nil
}
