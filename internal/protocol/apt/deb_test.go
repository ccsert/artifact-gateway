package apt

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

func TestParseDebianBinaryDerivesCanonicalPackageIdentity(t *testing.T) {
	deb := testDebianBinary(t, `Package: artifact-gateway
Version: 1:2.3.4-5
Architecture: arm64
Maintainer: Gateway Team <gateway@example.test>
Description: repository fixture
 continuation line
`)

	metadata, err := ParseDebianBinary(bytes.NewReader(deb), int64(len(deb)))
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Package != "artifact-gateway" || metadata.Version != "1:2.3.4-5" || metadata.Architecture != "arm64" {
		t.Fatalf("metadata=%#v", metadata)
	}
	if metadata.CanonicalIdentity != "artifact-gateway@1:2.3.4-5#arm64" {
		t.Fatalf("canonical identity=%q", metadata.CanonicalIdentity)
	}
}

func TestParseDebianBinarySupportsCommonControlCompression(t *testing.T) {
	const control = "Package: artifact-gateway\nVersion: 2.0-1\nArchitecture: amd64\n"
	for _, compression := range []string{"tar", "gz", "xz", "zst"} {
		t.Run(compression, func(t *testing.T) {
			deb := testDebianBinaryWithCompression(t, control, compression, true)
			metadata, err := ParseDebianBinary(bytes.NewReader(deb), int64(len(deb)))
			if err != nil {
				t.Fatal(err)
			}
			if metadata.CanonicalIdentity != "artifact-gateway@2.0-1#amd64" {
				t.Fatalf("canonical identity=%q", metadata.CanonicalIdentity)
			}
		})
	}
}

func TestParseDebianBinaryRequiresCompleteDataArchive(t *testing.T) {
	const control = "Package: artifact-gateway\nVersion: 2.0-1\nArchitecture: amd64\n"
	missing := testDebianBinaryWithCompression(t, control, "gz", false)
	if _, err := ParseDebianBinary(bytes.NewReader(missing), int64(len(missing))); err == nil {
		t.Fatal("Debian archive without data member was accepted")
	}

	truncated := testDebianBinaryWithCompression(t, control, "gz", true)
	truncated = truncated[:len(truncated)-1]
	if _, err := ParseDebianBinary(bytes.NewReader(truncated), int64(len(truncated))); err == nil {
		t.Fatal("truncated Debian data member was accepted")
	}
}

func TestParseDebianBinaryRejectsInvalidArchiveMemberOrderAndData(t *testing.T) {
	const control = "Package: artifact-gateway\nVersion: 2.0-1\nArchitecture: amd64\n"
	valid := testDebianBinaryWithCompression(t, control, "gz", true)

	var prefixed bytes.Buffer
	prefixed.WriteString("!<arch>\n")
	writeARMember(t, &prefixed, "unexpected", []byte("value"))
	prefixed.Write(valid[8:])
	if _, err := ParseDebianBinary(bytes.NewReader(prefixed.Bytes()), int64(prefixed.Len())); err == nil {
		t.Fatal("archive without debian-binary as its first member was accepted")
	}

	var duplicateArchive bytes.Buffer
	duplicateArchive.Write(valid)
	writeARMember(t, &duplicateArchive, "data.tar.gz", testDataArchive(t))
	if _, err := ParseDebianBinary(bytes.NewReader(duplicateArchive.Bytes()), int64(duplicateArchive.Len())); err == nil {
		t.Fatal("archive with duplicate data member was accepted")
	}

	invalidData := testDebianBinaryWithCompression(t, control, "gz", false)
	var invalidArchive bytes.Buffer
	invalidArchive.Write(invalidData)
	writeARMember(t, &invalidArchive, "data.tar.gz", []byte("not gzip"))
	if _, err := ParseDebianBinary(bytes.NewReader(invalidArchive.Bytes()), int64(invalidArchive.Len())); err == nil {
		t.Fatal("archive with invalid data compression was accepted")
	}
}

func TestParseDebianBinaryRejectsCorruptCompressedControlTrailer(t *testing.T) {
	const control = "Package: artifact-gateway\nVersion: 2.0-1\nArchitecture: amd64\n"
	deb := testDebianBinaryWithCompression(t, control, "gz", true)
	controlHeader := 8 + 60 + len("2.0\n")
	controlSize, err := strconv.Atoi(strings.TrimSpace(string(deb[controlHeader+48 : controlHeader+58])))
	if err != nil {
		t.Fatal(err)
	}
	deb[controlHeader+60+controlSize-1] ^= 0xff
	if _, err = ParseDebianBinary(bytes.NewReader(deb), int64(len(deb))); err == nil {
		t.Fatal("corrupt compressed control archive was accepted")
	}
}

func TestParseDebianBinaryRejectsXZControlArchiveWithExcessiveDictionary(t *testing.T) {
	const control = "Package: artifact-gateway\nVersion: 2.0-1\nArchitecture: amd64\n"
	var controlTar bytes.Buffer
	tarWriter := tar.NewWriter(&controlTar)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "./control", Mode: 0o644, Size: int64(len(control))}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(tarWriter, control); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	xzWriter, err := (xz.WriterConfig{DictCap: 64 << 20}).NewWriter(&compressed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = xzWriter.Write(controlTar.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err = xzWriter.Close(); err != nil {
		t.Fatal(err)
	}

	var archive bytes.Buffer
	archive.WriteString("!<arch>\n")
	writeARMember(t, &archive, "debian-binary", []byte("2.0\n"))
	writeARMember(t, &archive, "control.tar.xz", compressed.Bytes())
	writeARMember(t, &archive, "data.tar.gz", testDataArchive(t))
	if _, err = ParseDebianBinary(bytes.NewReader(archive.Bytes()), int64(archive.Len())); err == nil {
		t.Fatal("XZ control archive with excessive dictionary was accepted")
	}
}

func TestParseDebianBinaryRejectsMissingOrInvalidIdentity(t *testing.T) {
	tests := []struct {
		name    string
		control string
	}{
		{name: "missing version", control: "Package: artifact-gateway\nArchitecture: amd64\n"},
		{name: "invalid package", control: "Package: Artifact_Gateway\nVersion: 1.0-1\nArchitecture: amd64\n"},
		{name: "invalid version", control: "Package: artifact-gateway\nVersion: 1.0 1\nArchitecture: amd64\n"},
		{name: "invalid architecture", control: "Package: artifact-gateway\nVersion: 1.0-1\nArchitecture: AMD64\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			deb := testDebianBinary(t, test.control)
			if _, err := ParseDebianBinary(bytes.NewReader(deb), int64(len(deb))); err == nil {
				t.Fatal("invalid Debian identity was accepted")
			}
		})
	}
}

func TestParseDebianBinaryRejectsNonDebianAndOversizedControl(t *testing.T) {
	if _, err := ParseDebianBinary(bytes.NewReader([]byte("not an archive")), 14); err == nil {
		t.Fatal("non-Debian archive was accepted")
	}

	control := "Package: artifact-gateway\nVersion: 1.0-1\nArchitecture: amd64\nDescription: " + string(bytes.Repeat([]byte{'x'}, maxControlBytes)) + "\n"
	deb := testDebianBinary(t, control)
	if _, err := ParseDebianBinary(bytes.NewReader(deb), int64(len(deb))); err == nil {
		t.Fatal("oversized control metadata was accepted")
	}
}

func testDebianBinary(t *testing.T, control string) []byte {
	return testDebianBinaryWithCompression(t, control, "gz", true)
}

func testDebianBinaryWithCompression(t *testing.T, control, compression string, includeData bool) []byte {
	t.Helper()
	var controlTar bytes.Buffer
	tarWriter := tar.NewWriter(&controlTar)
	body := []byte(control)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "./control", Mode: 0o644, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}

	var controlArchive bytes.Buffer
	var writer io.WriteCloser
	switch compression {
	case "tar":
		controlArchive.Write(controlTar.Bytes())
	case "gz":
		writer = gzip.NewWriter(&controlArchive)
	case "xz":
		var err error
		writer, err = xz.NewWriter(&controlArchive)
		if err != nil {
			t.Fatal(err)
		}
	case "zst":
		var err error
		writer, err = zstd.NewWriter(&controlArchive)
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unsupported test compression %q", compression)
	}
	if writer != nil {
		if _, err := writer.Write(controlTar.Bytes()); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	}

	var archive bytes.Buffer
	archive.WriteString("!<arch>\n")
	writeARMember(t, &archive, "debian-binary", []byte("2.0\n"))
	controlName := "control.tar." + compression
	if compression == "tar" {
		controlName = "control.tar"
	}
	writeARMember(t, &archive, controlName, controlArchive.Bytes())
	if includeData {
		writeARMember(t, &archive, "data.tar.gz", testDataArchive(t))
	}
	return archive.Bytes()
}

func testDataArchive(t *testing.T) []byte {
	t.Helper()
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes()
}

func writeARMember(t *testing.T, archive *bytes.Buffer, name string, body []byte) {
	t.Helper()
	header := fmt.Sprintf("%-16s%-12d%-6d%-6d%-8o%-10d`\n", name+"/", 0, 0, 0, 0o100644, len(body))
	if len(header) != 60 {
		t.Fatalf("ar header length=%d", len(header))
	}
	archive.WriteString(header)
	archive.Write(body)
	if len(body)%2 != 0 {
		archive.WriteByte('\n')
	}
}
