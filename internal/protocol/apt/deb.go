// Package apt owns Debian package archive validation and identity derivation.
package apt

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/artifact-gateway/artifact-gateway/internal/protocol/identity"
	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
	"github.com/ulikunitz/xz/lzma"
)

const (
	maxControlArchiveBytes         = 8 << 20
	maxExpandedControlArchiveBytes = 32 << 20
	maxControlBytes                = 1 << 20
)

var (
	debianPackagePattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]+$`)
	debianArchitecturePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	debianVersionPattern      = regexp.MustCompile(`^[A-Za-z0-9.+:~-]+$`)
)

// DebianBinaryMetadata is the immutable package identity derived from the
// control file inside a .deb archive. Callers must not accept these fields from
// a publication request independently of the uploaded bytes.
type DebianBinaryMetadata struct {
	Package           string
	Version           string
	Architecture      string
	CanonicalIdentity string
}

// ParseDebianBinary validates the outer ar archive, the Debian format marker,
// and bounded control metadata before deriving the canonical package identity.
// It validates the complete stream without materializing the data archive.
func ParseDebianBinary(reader io.Reader, size int64) (DebianBinaryMetadata, error) {
	if reader == nil || size <= 0 {
		return DebianBinaryMetadata{}, errors.New("debian binary archive is empty")
	}
	limited := &io.LimitedReader{R: reader, N: size}
	magic := make([]byte, 8)
	if _, err := io.ReadFull(limited, magic); err != nil || string(magic) != "!<arch>\n" {
		return DebianBinaryMetadata{}, errors.New("invalid Debian ar archive")
	}

	var (
		formatMarker bool
		dataArchive  bool
		metadata     *DebianBinaryMetadata
		memberIndex  int
	)
	for limited.N > 0 {
		name, memberSize, err := readARHeader(limited)
		if err != nil {
			return DebianBinaryMetadata{}, err
		}
		if memberIndex == 0 && name != "debian-binary" {
			return DebianBinaryMetadata{}, errors.New("debian format marker must be the first archive member")
		}
		memberIndex++
		member := &io.LimitedReader{R: limited, N: memberSize}
		switch name {
		case "debian-binary":
			if formatMarker {
				return DebianBinaryMetadata{}, errors.New("duplicate Debian format marker")
			}
			marker, readErr := io.ReadAll(io.LimitReader(member, 16))
			if readErr != nil || string(marker) != "2.0\n" {
				return DebianBinaryMetadata{}, errors.New("unsupported Debian binary format")
			}
			formatMarker = true
		case "control.tar", "control.tar.gz", "control.tar.xz", "control.tar.zst":
			if !formatMarker {
				return DebianBinaryMetadata{}, errors.New("debian format marker must precede control metadata")
			}
			if metadata != nil {
				return DebianBinaryMetadata{}, errors.New("duplicate Debian control archive")
			}
			if memberSize > maxControlArchiveBytes {
				return DebianBinaryMetadata{}, errors.New("debian control archive is too large")
			}
			compressed, readErr := io.ReadAll(member)
			if readErr != nil {
				return DebianBinaryMetadata{}, errors.New("read Debian control archive")
			}
			parsed, parseErr := parseControlArchive(bytes.NewReader(compressed), name)
			if parseErr != nil {
				return DebianBinaryMetadata{}, parseErr
			}
			metadata = &parsed
		default:
			if validDataArchiveName(name) {
				if metadata == nil {
					return DebianBinaryMetadata{}, errors.New("debian control metadata must precede the data archive")
				}
				if dataArchive {
					return DebianBinaryMetadata{}, errors.New("duplicate Debian data archive")
				}
				if err = validateDataArchiveHeader(name, member); err != nil {
					return DebianBinaryMetadata{}, err
				}
				dataArchive = true
			}
		}
		if _, err = io.Copy(io.Discard, member); err != nil {
			return DebianBinaryMetadata{}, fmt.Errorf("read Debian archive member: %w", err)
		}
		if member.N != 0 {
			return DebianBinaryMetadata{}, errors.New("truncated Debian archive member")
		}
		if memberSize%2 != 0 {
			padding := []byte{0}
			if _, err = io.ReadFull(limited, padding); err != nil || padding[0] != '\n' {
				return DebianBinaryMetadata{}, errors.New("invalid Debian ar member padding")
			}
		}
	}
	if metadata == nil {
		return DebianBinaryMetadata{}, errors.New("debian control archive is missing")
	}
	if !dataArchive {
		return DebianBinaryMetadata{}, errors.New("debian data archive is missing")
	}
	return *metadata, nil
}

func validDataArchiveName(name string) bool {
	switch name {
	case "data.tar", "data.tar.gz", "data.tar.xz", "data.tar.zst", "data.tar.bz2":
		return true
	default:
		return false
	}
}

func validateDataArchiveHeader(name string, reader io.Reader) error {
	magicByName := map[string][]byte{
		"data.tar.gz":  {0x1f, 0x8b},
		"data.tar.xz":  {0xfd, '7', 'z', 'X', 'Z', 0x00},
		"data.tar.zst": {0x28, 0xb5, 0x2f, 0xfd},
		"data.tar.bz2": {'B', 'Z', 'h'},
	}
	if magic, compressed := magicByName[name]; compressed {
		actual := make([]byte, len(magic))
		if _, err := io.ReadFull(reader, actual); err != nil || !bytes.Equal(actual, magic) {
			return errors.New("invalid Debian data archive compression")
		}
		return nil
	}
	// A plain tar starts with either a regular header or the first zero block of
	// an empty archive. Reading one block also rejects a filename-only marker.
	header := make([]byte, 512)
	if _, err := io.ReadFull(reader, header); err != nil {
		return errors.New("invalid Debian data tar archive")
	}
	return nil
}

func readARHeader(reader io.Reader) (string, int64, error) {
	header := make([]byte, 60)
	if _, err := io.ReadFull(reader, header); err != nil {
		return "", 0, errors.New("truncated Debian ar header")
	}
	if string(header[58:60]) != "`\n" {
		return "", 0, errors.New("invalid Debian ar header")
	}
	name := strings.TrimSpace(string(header[:16]))
	name = strings.TrimSuffix(name, "/")
	if name == "" || strings.ContainsAny(name, "\\\x00") {
		return "", 0, errors.New("invalid Debian ar member name")
	}
	size, err := strconv.ParseInt(strings.TrimSpace(string(header[48:58])), 10, 64)
	if err != nil || size < 0 {
		return "", 0, errors.New("invalid Debian ar member size")
	}
	return name, size, nil
}

func parseControlArchive(reader io.Reader, name string) (DebianBinaryMetadata, error) {
	archiveReader := reader
	var closeArchive func()
	switch name {
	case "control.tar.gz":
		gzipReader, err := gzip.NewReader(reader)
		if err != nil {
			return DebianBinaryMetadata{}, errors.New("invalid compressed Debian control archive")
		}
		archiveReader = gzipReader
		closeArchive = func() { _ = gzipReader.Close() }
	case "control.tar.xz":
		encoded, err := io.ReadAll(io.LimitReader(reader, maxControlArchiveBytes+1))
		if err != nil || len(encoded) > maxControlArchiveBytes || validateXZControlDictionary(encoded, maxExpandedControlArchiveBytes) != nil {
			return DebianBinaryMetadata{}, errors.New("invalid compressed Debian control archive")
		}
		xzReader, err := (xz.ReaderConfig{DictCap: maxExpandedControlArchiveBytes, SingleStream: true}).NewReader(bytes.NewReader(encoded))
		if err != nil {
			return DebianBinaryMetadata{}, errors.New("invalid compressed Debian control archive")
		}
		archiveReader = xzReader
	case "control.tar.zst":
		zstdReader, err := zstd.NewReader(reader, zstd.WithDecoderMaxMemory(maxControlArchiveBytes))
		if err != nil {
			return DebianBinaryMetadata{}, errors.New("invalid compressed Debian control archive")
		}
		archiveReader = zstdReader
		closeArchive = zstdReader.Close
	}
	if closeArchive != nil {
		defer closeArchive()
	}

	expanded := &io.LimitedReader{R: archiveReader, N: maxExpandedControlArchiveBytes + 1}
	tarReader := tar.NewReader(expanded)
	var metadata *DebianBinaryMetadata
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return DebianBinaryMetadata{}, errors.New("invalid Debian control tar archive")
		}
		path := strings.TrimPrefix(header.Name, "./")
		if path != "control" {
			continue
		}
		if metadata != nil {
			return DebianBinaryMetadata{}, errors.New("duplicate Debian control file")
		}
		if !header.FileInfo().Mode().IsRegular() || header.Size < 0 || header.Size > maxControlBytes {
			return DebianBinaryMetadata{}, errors.New("invalid Debian control file")
		}
		body, err := io.ReadAll(io.LimitReader(tarReader, maxControlBytes+1))
		if err != nil || len(body) > maxControlBytes {
			return DebianBinaryMetadata{}, errors.New("debian control file is too large")
		}
		parsed, parseErr := parseControlFields(body)
		if parseErr != nil {
			return DebianBinaryMetadata{}, parseErr
		}
		metadata = &parsed
	}
	if _, err := io.Copy(io.Discard, expanded); err != nil {
		return DebianBinaryMetadata{}, errors.New("invalid compressed Debian control archive")
	}
	if expanded.N == 0 {
		return DebianBinaryMetadata{}, errors.New("expanded Debian control archive is too large")
	}
	if metadata == nil {
		return DebianBinaryMetadata{}, errors.New("debian control file is missing")
	}
	return *metadata, nil
}

// validateXZControlDictionary rejects multi-block streams and an LZMA2
// dictionary larger than the bounded expanded control archive. xz.ReaderConfig
// currently treats a stream-declared dictionary as an override rather than a
// hard cap, so this preflight must happen before decoder allocation.
func validateXZControlDictionary(encoded []byte, dictionaryCap int64) error {
	if len(encoded) < 24 || !bytes.Equal(encoded[:6], []byte{0xfd, '7', 'z', 'X', 'Z', 0x00}) || !bytes.Equal(encoded[len(encoded)-2:], []byte{'Y', 'Z'}) {
		return errors.New("invalid XZ stream")
	}
	backwardSize := int64(binary.LittleEndian.Uint32(encoded[len(encoded)-8 : len(encoded)-4]))
	indexSize := (backwardSize + 1) * 4
	if indexSize < 8 || indexSize > int64(len(encoded)-24) {
		return errors.New("invalid XZ index")
	}
	indexStart := len(encoded) - 12 - int(indexSize)
	if encoded[indexStart] != 0 {
		return errors.New("invalid XZ index indicator")
	}
	indexOffset := indexStart + 1
	records, err := readXZVLI(encoded, &indexOffset)
	if err != nil || records != 1 {
		return errors.New("XZ control archive must contain exactly one block")
	}

	headerStart := 12
	headerSize := (int(encoded[headerStart]) + 1) * 4
	if encoded[headerStart] == 0 || headerStart+headerSize > indexStart || headerSize < 8 {
		return errors.New("invalid XZ block header")
	}
	flags := encoded[headerStart+1]
	if flags&0x3c != 0 || flags&0x03 != 0 {
		return errors.New("unsupported XZ block filters")
	}
	offset := headerStart + 2
	if flags&0x40 != 0 {
		if _, err = readXZVLI(encoded[:headerStart+headerSize-4], &offset); err != nil {
			return err
		}
	}
	if flags&0x80 != 0 {
		if _, err = readXZVLI(encoded[:headerStart+headerSize-4], &offset); err != nil {
			return err
		}
	}
	filterID, err := readXZVLI(encoded[:headerStart+headerSize-4], &offset)
	if err != nil || filterID != 0x21 {
		return errors.New("XZ control archive must use LZMA2")
	}
	propertiesSize, err := readXZVLI(encoded[:headerStart+headerSize-4], &offset)
	if err != nil || propertiesSize != 1 || offset >= headerStart+headerSize-4 {
		return errors.New("invalid XZ LZMA2 properties")
	}
	dictionarySize, err := lzma.DecodeDictCap(encoded[offset])
	if err != nil || dictionarySize > dictionaryCap {
		return errors.New("XZ LZMA2 dictionary exceeds control archive limit")
	}
	return nil
}

func readXZVLI(encoded []byte, offset *int) (uint64, error) {
	var value uint64
	for shift := uint(0); shift < 63; shift += 7 {
		if *offset >= len(encoded) {
			return 0, io.ErrUnexpectedEOF
		}
		current := encoded[*offset]
		*offset++
		value |= uint64(current&0x7f) << shift
		if current&0x80 == 0 {
			return value, nil
		}
	}
	return 0, errors.New("invalid XZ variable-length integer")
}

func parseControlFields(body []byte) (DebianBinaryMetadata, error) {
	fields := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" || line[0] == ' ' || line[0] == '\t' {
			continue
		}
		name, value, found := strings.Cut(line, ":")
		if !found {
			return DebianBinaryMetadata{}, errors.New("invalid Debian control field")
		}
		key := strings.ToLower(strings.TrimSpace(name))
		if key != "package" && key != "version" && key != "architecture" {
			continue
		}
		if _, exists := fields[key]; exists {
			return DebianBinaryMetadata{}, fmt.Errorf("duplicate Debian %s field", key)
		}
		fields[key] = strings.TrimSpace(value)
	}
	if err := scanner.Err(); err != nil {
		return DebianBinaryMetadata{}, errors.New("read Debian control fields")
	}

	packageName, version, architecture := fields["package"], fields["version"], fields["architecture"]
	if !debianPackagePattern.MatchString(packageName) {
		return DebianBinaryMetadata{}, errors.New("invalid Debian package name")
	}
	if !validDebianVersion(version) {
		return DebianBinaryMetadata{}, errors.New("invalid Debian package version")
	}
	if !debianArchitecturePattern.MatchString(architecture) {
		return DebianBinaryMetadata{}, errors.New("invalid Debian package architecture")
	}
	return DebianBinaryMetadata{
		Package:           packageName,
		Version:           version,
		Architecture:      architecture,
		CanonicalIdentity: identity.APTVersion(packageName, version, architecture),
	}, nil
}

func validDebianVersion(version string) bool {
	if version == "" || !debianVersionPattern.MatchString(version) {
		return false
	}
	remainder := version
	if epoch, value, found := strings.Cut(version, ":"); found {
		if epoch == "" || strings.Trim(epoch, "0123456789") != "" {
			return false
		}
		remainder = value
	}
	return remainder != "" && remainder[0] >= '0' && remainder[0] <= '9'
}
