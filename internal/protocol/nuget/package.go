// Package nuget owns validation and immutable identity derivation for NuGet
// package archives. Protocol admission remains separate from this byte parser.
package nuget

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/artifact-gateway/artifact-gateway/internal/protocol/identity"
)

const (
	maxPackageEntries = 10_000
	maxPackagePathLen = 1_024
	maxNuspecBytes    = 1 << 20
	maxPackageIDBytes = 100
)

var (
	packageIDPattern        = regexp.MustCompile(`^[A-Za-z0-9_]+(?:[.-][A-Za-z0-9_]+)*$`)
	versionLabel            = regexp.MustCompile(`^[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*$`)
	allowedNuspecNamespaces = map[string]struct{}{
		"": {}, // Legacy packages may omit the default namespace.
		"http://schemas.microsoft.com/packaging/2010/07/nuspec.xsd": {},
		"http://schemas.microsoft.com/packaging/2011/08/nuspec.xsd": {},
		"http://schemas.microsoft.com/packaging/2011/10/nuspec.xsd": {},
		"http://schemas.microsoft.com/packaging/2012/06/nuspec.xsd": {},
		"http://schemas.microsoft.com/packaging/2013/01/nuspec.xsd": {},
		"http://schemas.microsoft.com/packaging/2013/05/nuspec.xsd": {},
	}
)

// PackageMetadata is derived exclusively from the root .nuspec manifest in a
// complete .nupkg ZIP archive. CanonicalIdentity is case-insensitive and uses
// NuGet's normalized repository version rather than the declared spelling.
type PackageMetadata struct {
	ID                string
	DeclaredVersion   string
	NormalizedVersion string
	CanonicalIdentity string
}

// ParsePackage validates the package ZIP structure, bounds manifest expansion,
// and derives the immutable NuGet package identity. The caller retains
// ownership of reader.
func ParsePackage(reader io.ReaderAt, size int64) (PackageMetadata, error) {
	if reader == nil || size <= 0 {
		return PackageMetadata{}, errors.New("NuGet package archive is empty")
	}
	archive, err := zip.NewReader(reader, size)
	if err != nil {
		return PackageMetadata{}, errors.New("invalid NuGet package archive")
	}
	if len(archive.File) == 0 || len(archive.File) > maxPackageEntries {
		return PackageMetadata{}, errors.New("NuGet package entry count is invalid")
	}

	seenPaths := make(map[string]struct{}, len(archive.File))
	var manifest *zip.File
	for _, file := range archive.File {
		cleaned, pathErr := validatePackagePath(file.Name)
		if pathErr != nil {
			return PackageMetadata{}, pathErr
		}
		folded := strings.ToLower(cleaned)
		if _, duplicate := seenPaths[folded]; duplicate {
			return PackageMetadata{}, errors.New("duplicate NuGet package entry path")
		}
		seenPaths[folded] = struct{}{}
		if file.FileInfo().IsDir() || !strings.EqualFold(path.Ext(cleaned), ".nuspec") {
			continue
		}
		if strings.Contains(cleaned, "/") {
			return PackageMetadata{}, errors.New("nuspec manifest must be at the package root")
		}
		if manifest != nil {
			return PackageMetadata{}, errors.New("multiple nuspec manifests at the package root")
		}
		manifest = file
	}
	if manifest == nil {
		return PackageMetadata{}, errors.New("nuspec manifest is missing")
	}
	if !manifest.Mode().IsRegular() || manifest.UncompressedSize64 > maxNuspecBytes {
		return PackageMetadata{}, errors.New("nuspec manifest is too large")
	}
	manifestReader, err := manifest.Open()
	if err != nil {
		return PackageMetadata{}, errors.New("read nuspec manifest")
	}
	body, readErr := io.ReadAll(io.LimitReader(manifestReader, maxNuspecBytes+1))
	closeErr := manifestReader.Close()
	if readErr != nil || closeErr != nil {
		return PackageMetadata{}, errors.New("read nuspec manifest")
	}
	if len(body) > maxNuspecBytes {
		return PackageMetadata{}, errors.New("nuspec manifest is too large")
	}

	id, declaredVersion, err := parseNuspec(body)
	if err != nil {
		return PackageMetadata{}, err
	}
	if len(id) > maxPackageIDBytes || !packageIDPattern.MatchString(id) {
		return PackageMetadata{}, errors.New("invalid NuGet package id")
	}
	normalizedVersion, err := NormalizeVersion(declaredVersion)
	if err != nil {
		return PackageMetadata{}, fmt.Errorf("invalid NuGet package version: %w", err)
	}
	return PackageMetadata{
		ID:                id,
		DeclaredVersion:   declaredVersion,
		NormalizedVersion: normalizedVersion,
		CanonicalIdentity: identity.NuGetVersion(strings.ToLower(id), strings.ToLower(normalizedVersion)),
	}, nil
}

func validatePackagePath(name string) (string, error) {
	if name == "" || len(name) > maxPackagePathLen || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") {
		return "", errors.New("invalid package entry path")
	}
	trimmed := strings.TrimSuffix(name, "/")
	if trimmed == "" || path.Clean(trimmed) != trimmed || trimmed == ".." || strings.HasPrefix(trimmed, "../") {
		return "", errors.New("invalid package entry path")
	}
	return trimmed, nil
}

func parseNuspec(body []byte) (string, string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	decoder.Strict = true
	var (
		stack                     []xml.Name
		rootCount, metadataCount  int
		idCount, versionCount     int
		packageID, packageVersion string
		packageNamespace          string
	)
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", "", errors.New("invalid nuspec XML")
		}
		switch value := token.(type) {
		case xml.Directive:
			return "", "", errors.New("nuspec XML directives are not allowed")
		case xml.CharData:
			if len(stack) == 0 && strings.TrimSpace(string(value)) != "" {
				return "", "", errors.New("invalid nuspec XML")
			}
		case xml.StartElement:
			depth := len(stack)
			if depth == 0 {
				rootCount++
				if rootCount != 1 || value.Name.Local != "package" {
					return "", "", errors.New("nuspec root element must be package")
				}
				if _, allowed := allowedNuspecNamespaces[value.Name.Space]; !allowed {
					return "", "", errors.New("unsupported nuspec namespace")
				}
				packageNamespace = value.Name.Space
			}
			if depth == 1 && stack[0].Local == "package" && value.Name.Local == "metadata" {
				if value.Name.Space != packageNamespace {
					return "", "", errors.New("nuspec element namespace does not match package")
				}
				metadataCount++
			}
			if depth == 2 && stack[1].Local == "metadata" {
				switch value.Name.Local {
				case "id":
					if value.Name.Space != packageNamespace {
						return "", "", errors.New("nuspec element namespace does not match package")
					}
					idCount++
					packageID, err = readSimpleElement(decoder, value)
					if err != nil {
						return "", "", err
					}
					continue
				case "version":
					if value.Name.Space != packageNamespace {
						return "", "", errors.New("nuspec element namespace does not match package")
					}
					versionCount++
					packageVersion, err = readSimpleElement(decoder, value)
					if err != nil {
						return "", "", err
					}
					continue
				}
			}
			stack = append(stack, value.Name)
		case xml.EndElement:
			if len(stack) == 0 || stack[len(stack)-1] != value.Name {
				return "", "", errors.New("invalid nuspec XML")
			}
			stack = stack[:len(stack)-1]
		}
	}
	if len(stack) != 0 || rootCount != 1 {
		return "", "", errors.New("invalid nuspec XML")
	}
	if metadataCount != 1 {
		return "", "", errors.New("nuspec must contain exactly one metadata element")
	}
	if idCount != 1 {
		return "", "", errors.New("nuspec must contain exactly one package id")
	}
	if versionCount != 1 {
		return "", "", errors.New("nuspec must contain exactly one package version")
	}
	packageID = strings.TrimSpace(packageID)
	packageVersion = strings.TrimSpace(packageVersion)
	if packageID == "" || packageVersion == "" {
		return "", "", errors.New("nuspec package identity is empty")
	}
	return packageID, packageVersion, nil
}

func readSimpleElement(decoder *xml.Decoder, start xml.StartElement) (string, error) {
	var content strings.Builder
	for {
		token, err := decoder.Token()
		if err != nil {
			return "", errors.New("invalid nuspec XML")
		}
		switch value := token.(type) {
		case xml.CharData:
			content.Write(value)
		case xml.Comment:
		case xml.EndElement:
			if value.Name != start.Name {
				return "", errors.New("invalid nuspec XML")
			}
			return content.String(), nil
		case xml.Directive:
			return "", errors.New("nuspec XML directives are not allowed")
		default:
			return "", errors.New("nuspec identity elements must contain plain text")
		}
	}
}

// NormalizeVersion applies NuGet's repository identity rules: one to four
// numeric components are accepted, at least three are emitted, a zero revision
// and SemVer build metadata are omitted. Prerelease spelling is retained for
// display while CanonicalIdentity folds the complete version for comparison.
func NormalizeVersion(version string) (string, error) {
	if version == "" || strings.TrimSpace(version) != version {
		return "", errors.New("version is empty or contains surrounding whitespace")
	}
	coreAndPrerelease := version
	if before, metadata, found := strings.Cut(version, "+"); found {
		if strings.Contains(metadata, "+") || !versionLabel.MatchString(metadata) {
			return "", errors.New("invalid build metadata")
		}
		coreAndPrerelease = before
	}
	core := coreAndPrerelease
	prerelease := ""
	if before, suffix, found := strings.Cut(coreAndPrerelease, "-"); found {
		if !versionLabel.MatchString(suffix) {
			return "", errors.New("invalid prerelease label")
		}
		for label := range strings.SplitSeq(suffix, ".") {
			if len(label) > 1 && label[0] == '0' && numericLabel(label) {
				return "", errors.New("numeric prerelease label has a leading zero")
			}
		}
		core = before
		prerelease = suffix
	}
	parts := strings.Split(core, ".")
	if len(parts) < 1 || len(parts) > 4 {
		return "", errors.New("version must contain one to four numeric components")
	}
	numbers := make([]uint64, 0, 4)
	for _, part := range parts {
		if part == "" {
			return "", errors.New("version contains an empty numeric component")
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return "", errors.New("version contains a non-numeric component")
			}
		}
		number, err := strconv.ParseUint(part, 10, 31)
		if err != nil || number > math.MaxInt32 {
			return "", errors.New("version component exceeds NuGet limits")
		}
		numbers = append(numbers, number)
	}
	for len(numbers) < 3 {
		numbers = append(numbers, 0)
	}
	if len(numbers) == 4 && numbers[3] == 0 {
		numbers = numbers[:3]
	}
	normalized := make([]string, len(numbers))
	for index, number := range numbers {
		normalized[index] = strconv.FormatUint(number, 10)
	}
	result := strings.Join(normalized, ".")
	if prerelease != "" {
		result += "-" + prerelease
	}
	return result, nil
}

func numericLabel(label string) bool {
	for _, character := range label {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
