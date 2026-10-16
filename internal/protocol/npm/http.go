// Package npm owns the npm registry path grammar and publication validation.
package npm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
)

type RouteKind uint8

const (
	RoutePackage RouteKind = iota + 1
	RouteVersion
	RouteTarball
	RoutePing
	RouteAuditBulk
	RouteAuditQuick
	RouteWhoami
	// RouteDistTags is npm's dist-tag API: `/-/package/<name>/dist-tags` lists
	// tags and `/-/package/<name>/dist-tags/<tag>` sets or removes one.
	RouteDistTags
)

type Route struct {
	Repository string
	Package    string
	Version    string
	Tarball    string
	Tag        string
	Kind       RouteKind
}

func ParsePath(escapedPath string) (Route, bool) {
	const prefix = "/npm/"
	if !strings.HasPrefix(escapedPath, prefix) {
		return Route{}, false
	}
	rest := strings.TrimPrefix(escapedPath, prefix)
	separator := strings.IndexByte(rest, '/')
	if separator <= 0 || separator == len(rest)-1 {
		return Route{}, false
	}
	repositoryName, err := url.PathUnescape(rest[:separator])
	if err != nil || repositoryName == "" || strings.Contains(repositoryName, "/") {
		return Route{}, false
	}
	resource, err := url.PathUnescape(rest[separator+1:])
	if err != nil || resource == "" || strings.ContainsAny(resource, "\\\x00") {
		return Route{}, false
	}
	switch resource {
	case "-/ping":
		return Route{Repository: repositoryName, Kind: RoutePing}, true
	case "-/whoami":
		return Route{Repository: repositoryName, Kind: RouteWhoami}, true
	case "-/npm/v1/security/advisories/bulk":
		return Route{Repository: repositoryName, Kind: RouteAuditBulk}, true
	case "-/npm/v1/security/audits/quick":
		return Route{Repository: repositoryName, Kind: RouteAuditQuick}, true
	}
	if strings.HasPrefix(resource, "-/package/") {
		return parseDistTagsPath(repositoryName, strings.TrimPrefix(resource, "-/package/"))
	}
	if marker := strings.LastIndex(resource, "/-/"); marker >= 0 {
		packageName, tarballName := resource[:marker], resource[marker+3:]
		if !ValidPackageName(packageName) || !ValidTarballName(tarballName) {
			return Route{}, false
		}
		return Route{Repository: repositoryName, Package: packageName, Tarball: tarballName, Kind: RouteTarball}, true
	}
	if separator := strings.LastIndexByte(resource, '/'); separator > 0 && separator < len(resource)-1 {
		packageName, version := resource[:separator], resource[separator+1:]
		if ValidPackageName(packageName) && ValidVersion(version) {
			return Route{Repository: repositoryName, Package: packageName, Version: version, Kind: RouteVersion}, true
		}
	}
	if !ValidPackageName(resource) {
		return Route{}, false
	}
	return Route{Repository: repositoryName, Package: resource, Kind: RoutePackage}, true
}

func parseDistTagsPath(repositoryName, rest string) (Route, bool) {
	if packageName, ok := strings.CutSuffix(rest, "/dist-tags"); ok {
		if !ValidPackageName(packageName) {
			return Route{}, false
		}
		return Route{Repository: repositoryName, Package: packageName, Kind: RouteDistTags}, true
	}
	marker := strings.LastIndex(rest, "/dist-tags/")
	if marker <= 0 {
		return Route{}, false
	}
	packageName, tag := rest[:marker], rest[marker+len("/dist-tags/"):]
	if !ValidPackageName(packageName) || !ValidDistTag(tag) {
		return Route{}, false
	}
	return Route{Repository: repositoryName, Package: packageName, Tag: tag, Kind: RouteDistTags}, true
}

// ValidDistTag accepts the dist-tag names the publish document already admits
// and, like the npm CLI's semver.validRange check, refuses version ranges.
func ValidDistTag(tag string) bool {
	if tag == "" || len(tag) > 128 || strings.TrimSpace(tag) != tag || strings.ContainsAny(tag, "/\\\x00 \t\r\n<>=^|") {
		return false
	}
	return !npmDistTagIsRange(tag)
}

// Range operators other than ~ are not legal tag characters and are rejected
// above. The remaining npm ranges are single versions, x-ranges, and tildes.
// Unlike a numeric-prefix heuristic, npm's strict range grammar keeps names
// such as 2026-release and 1.2-beta available as tags.
// A prerelease suffix needs all three version components; wildcard components
// (including x.x, x.1, and v*) remain ranges. Uppercase V is an ordinary tag.
var npmDistTagRange = regexp.MustCompile(`^(~?)(v*)(0|[1-9][0-9]*|[xX*])(?:\.(0|[1-9][0-9]*|[xX*])(?:\.(0|[1-9][0-9]*|[xX*])(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?)?)?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

func npmDistTagIsRange(tag string) bool {
	parts := npmDistTagRange.FindStringSubmatch(tag)
	if parts == nil || parts[6] != "" && !validIdentifiers(parts[6], true) {
		return false
	}
	wildcard := 3
	var numbers [3]uint64
	for index, component := range parts[3:6] {
		if component == "" || strings.ContainsAny(component, "xX*") {
			wildcard = min(wildcard, index)
		}
		if index < wildcard {
			// node-semver bounds numeric components to JavaScript's safe integers.
			value, err := strconv.ParseUint(component, 10, 53)
			if err != nil {
				return false
			}
			numbers[index] = value
		}
	}
	if wildcard == 3 && parts[1] == "" && parts[2] != "" && parts[2] != "v" {
		return false
	}
	// Expanding a partial range can overflow npm's numeric bound even
	// though the literal component fits (for example 9007199254740991).
	increment := -1
	if wildcard > 0 {
		if parts[1] == "~" {
			increment = min(wildcard-1, 1)
		} else if wildcard < 3 {
			increment = wildcard - 1
		}
	}
	if increment >= 0 && numbers[increment] == 1<<53-1 {
		return false
	}
	return true
}

func ValidPackageName(name string) bool {
	if name == "" || len(name) > 214 || strings.ToLower(name) != name || strings.ContainsAny(name, "\\\x00") {
		return false
	}
	if strings.HasPrefix(name, "@") {
		parts := strings.Split(name, "/")
		return len(parts) == 2 && validNamePart(strings.TrimPrefix(parts[0], "@")) && validNamePart(parts[1])
	}
	return !strings.Contains(name, "/") && validNamePart(name)
}

func ValidPackagePrefix(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 214 || strings.ToLower(value) != value || strings.ContainsAny(value, "\\\x00") || strings.Count(value, "/") > 1 {
		return false
	}
	for index, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || strings.ContainsRune("@/-._~", character) {
			if character == '@' && index != 0 {
				return false
			}
			continue
		}
		return false
	}
	return !strings.Contains(value, "//")
}

func validNamePart(value string) bool {
	if value == "" || value[0] == '.' || value[0] == '_' {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || strings.ContainsRune("-._~", character) {
			continue
		}
		return false
	}
	return true
}

func ValidTarballName(name string) bool {
	return name != "" && len(name) <= 255 && strings.HasSuffix(name, ".tgz") && !strings.ContainsAny(name, "/\\\x00")
}

// ValidPublishAttachmentName accepts the flat tarball names used by
// unscoped packages and npm's scoped publication key (`@scope/name-x.y.z.tgz`).
// The latter is normalized to its basename before it becomes a download URL.
func ValidPublishAttachmentName(name, packageName, version string) bool {
	if ValidTarballName(name) {
		return true
	}
	return strings.HasPrefix(packageName, "@") &&
		name == packageName+"-"+version+".tgz" &&
		ValidTarballName(name[strings.LastIndexByte(name, '/')+1:])
}

func ValidVersion(version string) bool {
	if version == "" || len(version) > 128 || strings.TrimSpace(version) != version || strings.HasPrefix(version, "v") {
		return false
	}
	coreAndPre := version
	if plus := strings.IndexByte(coreAndPre, '+'); plus >= 0 {
		if !validIdentifiers(coreAndPre[plus+1:], false) {
			return false
		}
		coreAndPre = coreAndPre[:plus]
	}
	core := coreAndPre
	if dash := strings.IndexByte(coreAndPre, '-'); dash >= 0 {
		if !validIdentifiers(coreAndPre[dash+1:], true) {
			return false
		}
		core = coreAndPre[:dash]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if !validNumericIdentifier(part) {
			return false
		}
	}
	return true
}

func validIdentifiers(value string, rejectNumericLeadingZero bool) bool {
	parts := strings.Split(value, ".")
	for _, part := range parts {
		if part == "" || rejectNumericLeadingZero && allDigits(part) && len(part) > 1 && part[0] == '0' {
			return false
		}
		for _, character := range part {
			if character >= '0' && character <= '9' || character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func validNumericIdentifier(value string) bool {
	return value != "" && allDigits(value) && (len(value) == 1 || value[0] != '0')
}

func allDigits(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func ValidateTarball(body []byte, packageName, version string) error {
	return ValidateTarballReader(bytes.NewReader(body), packageName, version)
}

func ValidateTarballReader(body io.Reader, packageName, version string) error {
	gzipReader, err := gzip.NewReader(body)
	if err != nil {
		return errors.New("attachment is not a gzip-compressed npm tarball")
	}
	defer func() { _ = gzipReader.Close() }()
	tarReader := tar.NewReader(io.LimitReader(gzipReader, 1<<30))
	manifestFound := false
	for entries := 0; ; entries++ {
		if entries >= 10000 {
			return errors.New("attachment contains too many tar entries")
		}
		header, nextErr := tarReader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return errors.New("attachment contains an invalid tar archive")
		}
		entryPath, pathErr := canonicalNPMTarEntryPath(header.Name)
		if pathErr != nil {
			return pathErr
		}
		segments := strings.Split(entryPath, "/")
		if len(segments) != 2 || segments[1] != "package.json" {
			continue
		}
		if strings.Contains(segments[0], ":") {
			return errors.New("attachment contains an unsafe package manifest root")
		}
		if manifestFound {
			return errors.New("attachment contains multiple package manifests")
		}
		manifestFound = true
		if header.Size <= 0 || header.Size > 1<<20 {
			return errors.New("top-level package.json has an invalid size")
		}
		var manifest struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		decoder := json.NewDecoder(io.LimitReader(tarReader, header.Size))
		if err = decoder.Decode(&manifest); err != nil || manifest.Name != packageName || manifest.Version != version {
			return errors.New("top-level package.json identity does not match the publication")
		}
	}
	if manifestFound {
		return nil
	}
	return errors.New("attachment does not contain one top-level package.json")
}

func canonicalNPMTarEntryPath(name string) (string, error) {
	if name == "" || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\\x00") {
		return "", errors.New("attachment contains an unsafe tar path")
	}
	canonical := strings.TrimSuffix(name, "/")
	if canonical == "" {
		return "", errors.New("attachment contains an unsafe tar path")
	}
	for _, segment := range strings.Split(canonical, "/") {
		if segment == "" || segment == ".." {
			return "", errors.New("attachment contains an unsafe tar path")
		}
	}
	cleaned := path.Clean(canonical)
	if cleaned == "." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("attachment contains an unsafe tar path")
	}
	return cleaned, nil
}

func PackagePath(name string) string {
	parts := strings.Split(name, "/")
	for index := range parts {
		parts[index] = url.PathEscape(parts[index])
	}
	return strings.Join(parts, "/")
}
