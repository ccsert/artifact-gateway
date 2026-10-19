package conan

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/md5"  // Conan FileTreeManifest uses MD5; stored objects use SHA-256.
	"crypto/sha1" // Conan package IDs are SHA-1 of conaninfo.txt.
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	ManifestName     = "conanmanifest.txt"
	ManifestMaxBytes = 1 << 20
	ManifestMaxFiles = 4096
	ExpandedMaxBytes = 1 << 30
)

type UploadPath struct {
	Repository, Reference, RecipeRevision, PackageID, PackageRevision, File string
}

func (p UploadPath) Kind() string {
	if p.PackageID != "" {
		return "package"
	}
	return "recipe"
}

// ParseUploadPath accepts the fixed Conan 2 hash-revision PUT contract. SCM
// revisions and optional metadata writes are not advertised by this endpoint.
func ParseUploadPath(raw string) (UploadPath, error) {
	parts := strings.Split(strings.TrimPrefix(raw, "/"), "/")
	if len(parts) < 12 || parts[0] != "conan" || parts[2] != "v2" || parts[3] != "conans" {
		return UploadPath{}, errors.New("invalid Conan upload route")
	}
	for _, part := range parts {
		if !validUploadSegment(part) {
			return UploadPath{}, errors.New("invalid Conan upload path")
		}
	}
	p := UploadPath{Repository: parts[1], Reference: strings.Join(parts[4:8], "/"), RecipeRevision: parts[9]}
	if parts[8] != "revisions" || !hexIdentity(p.RecipeRevision, 32) {
		return UploadPath{}, errors.New("a 32-character hash recipe revision is required")
	}
	switch {
	case len(parts) == 12 && parts[10] == "files":
		p.File = parts[11]
	case len(parts) == 16 && parts[10] == "packages" && parts[12] == "revisions" && parts[14] == "files":
		p.PackageID, p.PackageRevision, p.File = parts[11], parts[13], parts[15]
		if !hexIdentity(p.PackageID, 40) || !hexIdentity(p.PackageRevision, 32) {
			return UploadPath{}, errors.New("a 40-character package ID and 32-character package revision are required")
		}
	default:
		return UploadPath{}, errors.New("invalid Conan revision file route")
	}
	if !publicationFile(p.Kind(), p.File) {
		return UploadPath{}, errors.New("unsupported Conan publication file")
	}
	return p, nil
}

func validUploadSegment(value string) bool {
	if publicationControl(value) {
		return false
	}
	return value != "" && value != "." && value != ".." && len(value) <= 255 && utf8.ValidString(value) && !strings.ContainsAny(value, "%/\\\x00\r\n:#? ")
}

func hexIdentity(value string, length int) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(value) == length && len(decoded)*2 == length && strings.ToLower(value) == value
}

func publicationFile(kind, name string) bool {
	if name == ManifestName {
		return true
	}
	if kind == "package" {
		return name == "conaninfo.txt" || name == "conan_package.tgz"
	}
	return name == "conanfile.py" || name == "conan_export.tgz" || name == "conan_sources.tgz"
}

// ValidatePublication reconstructs Conan's declared uncompressed tree without
// extracting executable recipe or archive contents. Every entry must match;
// bounds apply to the combined tree, not just each compressed object.
func ValidatePublication(ctx context.Context, kind, revision, packageID string, manifest []byte, names []string, open func(string) (io.ReadCloser, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	declared, err := parseManifest(manifest)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(declared))
	for name := range declared {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	var canonical strings.Builder
	for _, name := range keys {
		fmt.Fprintf(&canonical, "%s: %s\n", name, declared[name])
	}
	summary := md5.Sum([]byte(canonical.String()))
	if hex.EncodeToString(summary[:]) != revision {
		return errors.New("manifest does not match revision")
	}
	actual := make(map[string]string)
	seen := make(map[string]bool)
	budget := int64(ExpandedMaxBytes)
	for _, name := range names {
		if seen[name] || !publicationFile(kind, name) {
			return errors.New("duplicate or unsupported publication asset")
		}
		seen[name] = true
		if name == ManifestName {
			continue
		}
		body, err := open(name)
		if err != nil {
			return errors.New("declared publication asset unavailable")
		}
		err = func() error {
			defer func() { _ = body.Close() }()
			if strings.HasSuffix(name, ".tgz") {
				prefix := ""
				if name == "conan_sources.tgz" {
					prefix = "export_source/"
				}
				return archiveManifest(conanContextReader{ctx, body}, prefix, actual, &budget)
			}
			md := md5.New()
			sha := sha1.New()
			n, err := io.Copy(io.MultiWriter(md, sha), io.LimitReader(conanContextReader{ctx, body}, budget+1))
			if err != nil || n > budget {
				return errors.New("publication tree exceeds byte limit or is unreadable")
			}
			budget -= n
			if kind == "package" && name == "conaninfo.txt" && hex.EncodeToString(sha.Sum(nil)) != packageID {
				return errors.New("conaninfo.txt does not match package ID")
			}
			return addManifestEntry(actual, name, hex.EncodeToString(md.Sum(nil)))
		}()
		if err != nil {
			return err
		}
	}
	if !seen[ManifestName] || kind == "recipe" && actual["conanfile.py"] == "" || kind == "package" && (!seen["conaninfo.txt"] || !seen["conan_package.tgz"]) {
		return errors.New("publication is missing required recipe or package assets")
	}
	if len(actual) != len(declared) {
		return errors.New("manifest does not declare the complete uploaded file tree")
	}
	for name, checksum := range declared {
		if actual[name] != checksum {
			return errors.New("manifest file is missing or has a different checksum")
		}
	}
	return nil
}

func parseManifest(data []byte) (map[string]string, error) {
	if len(data) == 0 || len(data) > ManifestMaxBytes || !utf8.Valid(data) || strings.ContainsAny(string(data), "\x00\r") {
		return nil, errors.New("invalid or oversized Conan manifest")
	}
	lines := strings.Split(string(data), "\n")
	timestamp, err := strconv.ParseInt(lines[0], 10, 64)
	if err != nil || timestamp < 0 {
		return nil, errors.New("invalid Conan manifest timestamp")
	}
	entries := make(map[string]string)
	for _, line := range lines[1:] {
		if line == "" {
			continue
		}
		index := strings.LastIndex(line, ": ")
		if index <= 0 || !hexIdentity(line[index+2:], 32) {
			return nil, errors.New("invalid Conan manifest entry")
		}
		name := line[:index]
		if !validManifestPath(name) || name == ManifestName {
			return nil, errors.New("invalid Conan manifest path")
		}
		if err := addManifestEntry(entries, name, line[index+2:]); err != nil {
			return nil, err
		}
	}
	return entries, nil
}

func validManifestPath(name string) bool {
	if publicationControl(name) {
		return false
	}
	return name != "" && len(name) <= 4096 && utf8.ValidString(name) && path.Clean(name) == name && name != "." && name != ".." && !strings.HasPrefix(name, "../") && !path.IsAbs(name) && !strings.ContainsAny(name, "\\\x00\r\n")
}

func addManifestEntry(entries map[string]string, name, digest string) error {
	if len(entries) >= ManifestMaxFiles || entries[name] != "" {
		return errors.New("duplicate Conan file or manifest entry limit exceeded")
	}
	entries[name] = digest
	return nil
}

func archiveManifest(body io.Reader, prefix string, entries map[string]string, budget *int64) error {
	gzipReader, err := gzip.NewReader(body)
	if err != nil {
		return errors.New("invalid Conan gzip archive")
	}
	defer func() { _ = gzipReader.Close() }()
	limited := &io.LimitedReader{R: gzipReader, N: *budget + 1}
	reader := tar.NewReader(limited)
	count := 0
	for {
		entry, err := reader.Next()
		if errors.Is(err, io.EOF) {
			// Drain gzip to verify its trailer as well as the tar structure.
			_, readErr := io.Copy(io.Discard, limited)
			if readErr != nil || limited.N <= 0 {
				return errors.New("invalid or oversized Conan archive trailer")
			}
			*budget = limited.N - 1
			return nil
		}
		count++
		if err != nil || count > ManifestMaxFiles || !validManifestPath(strings.TrimSuffix(entry.Name, "/")) || entry.Size < 0 || entry.Size >= limited.N {
			return errors.New("invalid or oversized Conan archive entry")
		}
		if entry.Typeflag == tar.TypeDir {
			continue
		}
		name := prefix + entry.Name
		var digest string
		switch entry.Typeflag {
		case tar.TypeReg:
			hash := md5.New()
			n, err := io.Copy(hash, reader)
			if err != nil || n != entry.Size {
				return errors.New("truncated Conan archive entry")
			}
			digest = hex.EncodeToString(hash.Sum(nil))
		case tar.TypeSymlink:
			if !validManifestPath(path.Join(path.Dir(entry.Name), entry.Linkname)) || path.IsAbs(entry.Linkname) || strings.ContainsAny(entry.Linkname, "\\\x00\r\n") {
				return errors.New("unsafe Conan archive link")
			}
			hash := md5.Sum([]byte(entry.Linkname))
			digest = hex.EncodeToString(hash[:])
		default:
			return errors.New("unsupported Conan archive entry type")
		}
		if err := addManifestEntry(entries, name, digest); err != nil {
			return err
		}
	}
}

// Stop CPU-bound archive verification promptly even when the backing store is
// in memory and therefore does not attach cancellation to its reader.
type conanContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r conanContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func publicationControl(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return true
		}
	}
	return false
}
