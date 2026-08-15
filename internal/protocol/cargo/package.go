// Package cargo owns bounded Cargo publication framing, .crate archive
// validation, and sparse-index identity translation. Protocol admission and
// persistence remain separate from this C0 byte boundary.
package cargo

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	mastermindssemver "github.com/Masterminds/semver/v3"
	"github.com/artifact-gateway/artifact-gateway/internal/protocol/identity"
	"github.com/pelletier/go-toml/v2"
	"golang.org/x/mod/semver"
)

const (
	maxPublishMetadataBytes = 1 << 20
	maxCompressedCrateBytes = 128 << 20
	maxExpandedCrateBytes   = 512 << 20
	maxSingleCrateFileBytes = 256 << 20
	maxCrateEntries         = 20_000
	maxCratePathBytes       = 1_024
	maxCargoManifestBytes   = 2 << 20
	maxCargoNameBytes       = 64
	maxCargoStringBytes     = 16 << 10
	maxCargoCollectionItems = 10_000
	cargoRequirementNumber  = `(?:0|[1-9][0-9]*)`
	cargoRequirementSuffix  = `(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?`
	cargoRequirementVersion = cargoRequirementNumber + `(?:\.` + cargoRequirementNumber + `(?:\.` + cargoRequirementNumber + cargoRequirementSuffix + `)?)?`
)

type crateLimits struct {
	compressedBytes int64
	expandedBytes   int64
	singleFileBytes int64
	entries         int
	pathBytes       int
	manifestBytes   int64
}

var defaultCrateLimits = crateLimits{
	compressedBytes: maxCompressedCrateBytes,
	expandedBytes:   maxExpandedCrateBytes,
	singleFileBytes: maxSingleCrateFileBytes,
	entries:         maxCrateEntries,
	pathBytes:       maxCratePathBytes,
	manifestBytes:   maxCargoManifestBytes,
}

var (
	cargoNamePattern                  = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	cargoVersionPattern               = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
	cargoRequirementComparatorPattern = regexp.MustCompile(`^\s*(?:>=|<=|=|>|<|~|\^)?\s*` + cargoRequirementVersion + `\s*$`)
	cargoRequirementWildcardPattern   = regexp.MustCompile(`^\s*(?:\*|(?:>=|<=|=|>|<|~|\^)?\s*` + cargoRequirementNumber + `\.(?:\*(?:\.\*)?|` + cargoRequirementNumber + `\.\*))\s*$`)
	checksumPattern                   = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// PublishEnvelope describes the two bounded sections of Cargo's framed
// publication body. CrateOffset and CrateSize let callers stream the archive
// from the original ReaderAt without materializing it in memory.
type PublishEnvelope struct {
	Metadata    PublishMetadata
	CrateOffset int64
	CrateSize   int64
}

type PublishMetadata struct {
	Name          string                       `json:"name"`
	Version       string                       `json:"vers"`
	Dependencies  []PublishDependency          `json:"deps"`
	Features      map[string][]string          `json:"features"`
	Authors       []string                     `json:"authors,omitempty"`
	Description   *string                      `json:"description,omitempty"`
	Documentation *string                      `json:"documentation,omitempty"`
	Homepage      *string                      `json:"homepage,omitempty"`
	Readme        *string                      `json:"readme,omitempty"`
	ReadmeFile    *string                      `json:"readme_file,omitempty"`
	Keywords      []string                     `json:"keywords,omitempty"`
	Categories    []string                     `json:"categories,omitempty"`
	License       *string                      `json:"license,omitempty"`
	LicenseFile   *string                      `json:"license_file,omitempty"`
	Repository    *string                      `json:"repository,omitempty"`
	Badges        map[string]map[string]string `json:"badges,omitempty"`
	Links         *string                      `json:"links,omitempty"`
	RustVersion   *string                      `json:"rust_version,omitempty"`
}

type PublishDependency struct {
	Name               string   `json:"name"`
	VersionRequirement string   `json:"version_req"`
	Features           []string `json:"features"`
	Optional           bool     `json:"optional"`
	DefaultFeatures    bool     `json:"default_features"`
	Target             *string  `json:"target"`
	Kind               string   `json:"kind"`
	Registry           *string  `json:"registry"`
	ExplicitNameInTOML *string  `json:"explicit_name_in_toml"`
}

// CrateMetadata is derived only from the normalized Cargo.toml inside a
// complete .crate archive.
type CrateMetadata struct {
	Name              string
	NormalizedName    string
	CollisionKey      string
	Version           string
	VersionKey        string
	CanonicalIdentity string
	IndexPath         string
	EntryCount        int
}

type NormalizedIdentity struct {
	NormalizedName    string
	CollisionKey      string
	VersionKey        string
	CanonicalIdentity string
}

type IndexEntry struct {
	Name          string              `json:"name"`
	Version       string              `json:"vers"`
	Dependencies  []IndexDependency   `json:"deps"`
	Checksum      string              `json:"cksum"`
	Features      map[string][]string `json:"features"`
	Yanked        bool                `json:"yanked"`
	Links         *string             `json:"links,omitempty"`
	SchemaVersion uint32              `json:"v,omitempty"`
	Features2     map[string][]string `json:"features2,omitempty"`
	RustVersion   *string             `json:"rust_version,omitempty"`
	PublishedAt   string              `json:"pubtime,omitempty"`
}

type IndexDependency struct {
	Name            string   `json:"name"`
	Requirement     string   `json:"req"`
	Features        []string `json:"features"`
	Optional        bool     `json:"optional"`
	DefaultFeatures bool     `json:"default_features"`
	Target          *string  `json:"target"`
	Kind            string   `json:"kind"`
	Registry        *string  `json:"registry"`
	Package         *string  `json:"package"`
}

// ParsePublishEnvelope validates Cargo's little-endian publish framing and
// strict, bounded JSON metadata. It performs no object or repository writes.
func ParsePublishEnvelope(ctx context.Context, reader io.ReaderAt, size int64) (PublishEnvelope, error) {
	if ctx == nil || reader == nil || size < 8 || size > int64(maxPublishMetadataBytes+maxCompressedCrateBytes+8) {
		return PublishEnvelope{}, errors.New("cargo publish body size is invalid")
	}
	if err := ctx.Err(); err != nil {
		return PublishEnvelope{}, err
	}
	metadataLength, err := readUint32At(reader, 0)
	if err != nil || metadataLength == 0 || metadataLength > maxPublishMetadataBytes {
		return PublishEnvelope{}, errors.New("cargo publish metadata length is invalid")
	}
	crateLengthOffset := int64(4 + metadataLength)
	if crateLengthOffset+4 > size {
		return PublishEnvelope{}, errors.New("cargo publish body is truncated")
	}
	crateLength, err := readUint32At(reader, crateLengthOffset)
	if err != nil || crateLength == 0 || crateLength > maxCompressedCrateBytes {
		return PublishEnvelope{}, errors.New("cargo crate length is invalid")
	}
	crateOffset := crateLengthOffset + 4
	if crateOffset+int64(crateLength) != size {
		return PublishEnvelope{}, errors.New("cargo publish framing does not match body size")
	}
	metadataBody := make([]byte, metadataLength)
	if _, err = reader.ReadAt(metadataBody, 4); err != nil {
		return PublishEnvelope{}, errors.New("read Cargo publish metadata")
	}
	decoder := json.NewDecoder(bytes.NewReader(metadataBody))
	var metadata PublishMetadata
	if err = decoder.Decode(&metadata); err != nil || decoder.Decode(&struct{}{}) != io.EOF || validatePublishMetadata(metadata) != nil {
		return PublishEnvelope{}, errors.New("cargo publish metadata is invalid")
	}
	if err = ctx.Err(); err != nil {
		return PublishEnvelope{}, err
	}
	return PublishEnvelope{Metadata: metadata, CrateOffset: crateOffset, CrateSize: int64(crateLength)}, nil
}

func readUint32At(reader io.ReaderAt, offset int64) (uint32, error) {
	var body [4]byte
	if _, err := reader.ReadAt(body[:], offset); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(body[:]), nil
}

// ParseCrate validates the complete gzip/tar archive and derives package
// identity from its root normalized Cargo.toml.
func ParseCrate(ctx context.Context, reader io.Reader, size int64) (CrateMetadata, error) {
	return parseCrate(ctx, reader, size, defaultCrateLimits)
}

func parseCrate(ctx context.Context, reader io.Reader, size int64, limits crateLimits) (CrateMetadata, error) {
	if ctx == nil || reader == nil || size <= 0 || size > limits.compressedBytes || limits.expandedBytes <= 0 ||
		limits.singleFileBytes <= 0 || limits.entries <= 0 || limits.pathBytes <= 0 || limits.manifestBytes <= 0 {
		return CrateMetadata{}, errors.New("cargo crate size is invalid")
	}
	if err := ctx.Err(); err != nil {
		return CrateMetadata{}, err
	}
	compressed := &io.LimitedReader{R: contextReader{ctx: ctx, reader: reader}, N: size}
	buffered := bufio.NewReaderSize(compressed, 32<<10)
	gzipReader, err := gzip.NewReader(buffered)
	if err != nil {
		return CrateMetadata{}, errors.New("invalid Cargo crate compression")
	}
	gzipReader.Multistream(false)
	expanded := &io.LimitedReader{R: gzipReader, N: limits.expandedBytes + 1}
	archive := tar.NewReader(expanded)
	seenPaths := make(map[string]struct{})
	root := ""
	entryCount := 0
	var manifest []byte
	for {
		if err = ctx.Err(); err != nil {
			_ = gzipReader.Close()
			return CrateMetadata{}, err
		}
		header, nextErr := archive.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			_ = gzipReader.Close()
			if contextErr := ctx.Err(); contextErr != nil {
				return CrateMetadata{}, contextErr
			}
			return CrateMetadata{}, errors.New("invalid Cargo crate tar archive")
		}
		entryCount++
		if entryCount > limits.entries {
			_ = gzipReader.Close()
			return CrateMetadata{}, errors.New("cargo crate entry count is too large")
		}
		cleaned, pathErr := validCratePath(header.Name, limits.pathBytes)
		if pathErr != nil {
			_ = gzipReader.Close()
			return CrateMetadata{}, pathErr
		}
		folded := strings.ToLower(cleaned)
		if _, duplicate := seenPaths[folded]; duplicate {
			_ = gzipReader.Close()
			return CrateMetadata{}, errors.New("duplicate Cargo crate entry path")
		}
		seenPaths[folded] = struct{}{}
		entryRoot := strings.SplitN(cleaned, "/", 2)[0]
		if root == "" {
			root = entryRoot
		} else if root != entryRoot {
			_ = gzipReader.Close()
			return CrateMetadata{}, errors.New("cargo crate entries must share one root directory")
		}
		if header.Size < 0 || header.Size > limits.singleFileBytes {
			_ = gzipReader.Close()
			return CrateMetadata{}, errors.New("cargo crate entry size is invalid")
		}
		switch header.Typeflag {
		case tar.TypeReg:
		case tar.TypeDir:
			continue
		default:
			_ = gzipReader.Close()
			return CrateMetadata{}, errors.New("cargo crate links and special files are not allowed")
		}
		if path.Base(cleaned) == "Cargo.toml" {
			if cleaned != root+"/Cargo.toml" || manifest != nil || header.Size > limits.manifestBytes {
				_ = gzipReader.Close()
				return CrateMetadata{}, errors.New("cargo crate must contain one bounded root Cargo.toml")
			}
			manifest, err = io.ReadAll(io.LimitReader(archive, limits.manifestBytes+1))
			if err != nil || int64(len(manifest)) > limits.manifestBytes {
				_ = gzipReader.Close()
				return CrateMetadata{}, errors.New("read Cargo crate manifest")
			}
			continue
		}
		if _, err = io.Copy(io.Discard, archive); err != nil {
			_ = gzipReader.Close()
			if contextErr := ctx.Err(); contextErr != nil {
				return CrateMetadata{}, contextErr
			}
			return CrateMetadata{}, errors.New("read Cargo crate entry")
		}
	}
	extra, drainErr := io.Copy(io.Discard, expanded)
	closeErr := gzipReader.Close()
	if contextErr := ctx.Err(); contextErr != nil {
		return CrateMetadata{}, contextErr
	}
	if drainErr != nil || closeErr != nil || extra != 0 || expanded.N == 0 || buffered.Buffered() != 0 || compressed.N != 0 {
		return CrateMetadata{}, errors.New("cargo crate stream is invalid or too large")
	}
	if manifest == nil || entryCount == 0 {
		return CrateMetadata{}, errors.New("cargo crate manifest is missing")
	}
	var parsed struct {
		Package struct {
			Name    string `toml:"name"`
			Version string `toml:"version"`
		} `toml:"package"`
	}
	if err = ctx.Err(); err != nil {
		return CrateMetadata{}, err
	}
	if err = toml.Unmarshal(manifest, &parsed); err != nil {
		return CrateMetadata{}, errors.New("cargo crate manifest is invalid")
	}
	normalized, err := NormalizeIdentity(parsed.Package.Name, parsed.Package.Version)
	if err != nil || root != parsed.Package.Name+"-"+parsed.Package.Version {
		return CrateMetadata{}, errors.New("cargo crate root and manifest identity do not match")
	}
	indexPath, err := SparseIndexPath(parsed.Package.Name)
	if err != nil {
		return CrateMetadata{}, err
	}
	return CrateMetadata{
		Name: parsed.Package.Name, NormalizedName: normalized.NormalizedName, CollisionKey: normalized.CollisionKey,
		Version: parsed.Package.Version, VersionKey: normalized.VersionKey, CanonicalIdentity: normalized.CanonicalIdentity,
		IndexPath: indexPath, EntryCount: entryCount,
	}, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(body []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(body)
}

func validCratePath(name string, maxBytes int) (string, error) {
	if name == "" || len(name) > maxBytes || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") {
		return "", errors.New("cargo crate entry path is invalid")
	}
	cleaned := strings.TrimSuffix(name, "/")
	if cleaned == "" || path.Clean(cleaned) != cleaned || cleaned == ".." || strings.HasPrefix(cleaned, "../") || !strings.Contains(cleaned, "/") {
		return "", errors.New("cargo crate entry path is invalid")
	}
	return cleaned, nil
}

// CrossCheckPublishIdentity prevents a caller-controlled publish envelope from
// naming different bytes than the normalized manifest inside the .crate.
func CrossCheckPublishIdentity(metadata PublishMetadata, crate CrateMetadata) error {
	if validatePublishMetadata(metadata) != nil || metadata.Name != crate.Name || metadata.Version != crate.Version {
		return errors.New("cargo publish metadata does not match crate identity")
	}
	return nil
}

func NormalizeIdentity(name, version string) (NormalizedIdentity, error) {
	if !validCargoName(name) || !validCargoVersion(version) {
		return NormalizedIdentity{}, errors.New("cargo package identity is invalid")
	}
	normalizedName := strings.ToLower(name)
	var collision strings.Builder
	for _, character := range normalizedName {
		if character == '-' || character == '_' {
			collision.WriteByte('-')
			continue
		}
		collision.WriteRune(character)
	}
	versionKey := strings.SplitN(version, "+", 2)[0]
	collisionKey := collision.String()
	return NormalizedIdentity{
		NormalizedName: normalizedName, CollisionKey: collisionKey, VersionKey: versionKey,
		CanonicalIdentity: identity.CargoVersion(collisionKey, versionKey),
	}, nil
}

func SparseIndexPath(name string) (string, error) {
	if !validCargoName(name) {
		return "", errors.New("cargo package name is invalid")
	}
	name = strings.ToLower(name)
	switch len(name) {
	case 1:
		return "1/" + name, nil
	case 2:
		return "2/" + name, nil
	case 3:
		return "3/" + name[:1] + "/" + name, nil
	default:
		return name[:2] + "/" + name[2:4] + "/" + name, nil
	}
}

func validCargoName(name string) bool {
	return name != "" && len(name) <= maxCargoNameBytes && cargoNamePattern.MatchString(name) && asciiLetter(name[0]) && strings.IndexFunc(name, func(r rune) bool {
		return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
	}) >= 0
}

func asciiLetter(value byte) bool {
	return (value >= 'A' && value <= 'Z') || (value >= 'a' && value <= 'z')
}

func validCargoVersion(version string) bool {
	return len(version) <= 256 && cargoVersionPattern.MatchString(version) && semver.IsValid("v"+version)
}

// TranslateIndexEntry maps the Cargo publish schema to the immutable sparse
// index schema. The checksum is repository-computed, never client-owned.
func TranslateIndexEntry(metadata PublishMetadata, checksum string, publishedAt time.Time, currentRegistry string) (IndexEntry, error) {
	if validatePublishMetadata(metadata) != nil || !checksumPattern.MatchString(checksum) || publishedAt.IsZero() || !validSparseRegistryURL(currentRegistry) {
		return IndexEntry{}, errors.New("cargo index entry input is invalid")
	}
	dependencies := make([]IndexDependency, 0, len(metadata.Dependencies))
	for _, dependency := range metadata.Dependencies {
		name := dependency.Name
		var packageName *string
		if dependency.ExplicitNameInTOML != nil {
			name = *dependency.ExplicitNameInTOML
			original := dependency.Name
			packageName = &original
		}
		registry := dependency.Registry
		if registry != nil && strings.TrimSuffix(*registry, "/") == strings.TrimSuffix(currentRegistry, "/") {
			registry = nil
		}
		dependencies = append(dependencies, IndexDependency{
			Name: name, Requirement: dependency.VersionRequirement, Features: append([]string(nil), dependency.Features...),
			Optional: dependency.Optional, DefaultFeatures: dependency.DefaultFeatures, Target: copyString(dependency.Target),
			Kind: dependency.Kind, Registry: copyString(registry), Package: packageName,
		})
	}
	features := make(map[string][]string)
	features2 := make(map[string][]string)
	for feature, values := range metadata.Features {
		for _, value := range values {
			if strings.HasPrefix(value, "dep:") || strings.Contains(value, "?/") {
				features2[feature] = append(features2[feature], value)
			} else {
				features[feature] = append(features[feature], value)
			}
		}
		if _, exists := features[feature]; !exists && len(features2[feature]) == 0 {
			features[feature] = []string{}
		}
	}
	schemaVersion := uint32(0)
	if len(features2) > 0 {
		schemaVersion = 2
	}
	return IndexEntry{
		Name: metadata.Name, Version: metadata.Version, Dependencies: dependencies, Checksum: checksum,
		Features: features, Yanked: false, Links: copyString(metadata.Links), SchemaVersion: schemaVersion,
		Features2: features2, RustVersion: copyString(metadata.RustVersion), PublishedAt: publishedAt.UTC().Truncate(time.Second).Format(time.RFC3339),
	}, nil
}

func validatePublishMetadata(metadata PublishMetadata) error {
	if !validCargoName(metadata.Name) || !validCargoVersion(metadata.Version) || len(metadata.Dependencies) > maxCargoCollectionItems ||
		len(metadata.Features) > maxCargoCollectionItems {
		return errors.New("cargo publish identity or collections are invalid")
	}
	for _, dependency := range metadata.Dependencies {
		if !validCargoName(dependency.Name) || !validCargoVersionRequirement(dependency.VersionRequirement) ||
			len(dependency.Features) > maxCargoCollectionItems || !validCargoStrings(dependency.Features) ||
			(dependency.Kind != "normal" && dependency.Kind != "dev" && dependency.Kind != "build") ||
			!validOptionalCargoName(dependency.ExplicitNameInTOML) || !validOptionalCargoString(dependency.Target) || !validOptionalURL(dependency.Registry) {
			return errors.New("cargo publish dependency is invalid")
		}
	}
	for name, values := range metadata.Features {
		if name == "" || !validCargoString(name) || len(values) > maxCargoCollectionItems || !validCargoStrings(values) {
			return errors.New("cargo publish features are invalid")
		}
	}
	if !validCargoStrings(metadata.Authors) || !validCargoStrings(metadata.Keywords) || !validCargoStrings(metadata.Categories) ||
		!validOptionalCargoString(metadata.Description) || !validOptionalCargoString(metadata.Documentation) ||
		!validOptionalCargoString(metadata.Homepage) || !validOptionalCargoString(metadata.Readme) ||
		!validOptionalCargoString(metadata.ReadmeFile) || !validOptionalCargoString(metadata.License) ||
		!validOptionalCargoString(metadata.LicenseFile) || !validOptionalCargoString(metadata.Repository) ||
		!validOptionalCargoString(metadata.Links) || !validOptionalCargoString(metadata.RustVersion) {
		return errors.New("cargo publish descriptive metadata is invalid")
	}
	if metadata.RustVersion != nil && !semver.IsValid("v"+*metadata.RustVersion) {
		return errors.New("cargo rust_version is invalid")
	}
	for badge, values := range metadata.Badges {
		if !validCargoString(badge) || len(values) > maxCargoCollectionItems {
			return errors.New("cargo publish badges are invalid")
		}
		for key, value := range values {
			if !validCargoString(key) || !validCargoString(value) {
				return errors.New("cargo publish badges are invalid")
			}
		}
	}
	return nil
}

func validCargoString(value string) bool {
	return len(value) <= maxCargoStringBytes && !strings.ContainsRune(value, '\x00')
}

func validCargoStrings(values []string) bool {
	if len(values) > maxCargoCollectionItems {
		return false
	}
	for _, value := range values {
		if !validCargoString(value) {
			return false
		}
	}
	return true
}

func validOptionalCargoString(value *string) bool { return value == nil || validCargoString(*value) }

func validOptionalCargoName(value *string) bool { return value == nil || validCargoName(*value) }

func validOptionalURL(value *string) bool {
	if value == nil {
		return true
	}
	parsed, err := url.ParseRequestURI(*value)
	return err == nil && parsed.IsAbs() && parsed.Hostname() != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" &&
		(parsed.Scheme == "https" || parsed.Scheme == "sparse+https")
}

func validSparseRegistryURL(value string) bool {
	parsed, err := url.ParseRequestURI(value)
	return err == nil && parsed.IsAbs() && parsed.Hostname() != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" &&
		parsed.Scheme == "sparse+https" && strings.HasSuffix(parsed.Path, "/")
}

func validCargoVersionRequirement(value string) bool {
	if value == "" || !validCargoString(value) {
		return false
	}
	for _, comparator := range strings.Split(value, ",") {
		if !cargoRequirementComparatorPattern.MatchString(comparator) && !cargoRequirementWildcardPattern.MatchString(comparator) {
			return false
		}
	}
	_, err := mastermindssemver.NewConstraint(value)
	return err == nil
}

func copyString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
