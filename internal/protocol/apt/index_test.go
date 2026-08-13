package apt

import (
	"strings"
	"testing"
)

func TestPackageIndexStanzaReplacesRepositoryOwnedControlFields(t *testing.T) {
	t.Parallel()
	metadata := DebianBinaryMetadata{Control: []byte("Package: widget\nVersion: 1.0-1\nArchitecture: amd64\nDescription: fixture\n continuation\nFilename: client/path.deb\nSize: 1\nSHA256: " + strings.Repeat("f", 64) + "\nSHA512: " + strings.Repeat("e", 128) + "\n")}
	digest := "sha256:" + strings.Repeat("a", 64)
	stanza, err := PackageIndexStanza(metadata, "pool/main/w/widget/widget_1.0-1_amd64.deb", 42, digest)
	if err != nil {
		t.Fatal(err)
	}
	body := string(stanza)
	for _, field := range []string{"Filename:", "Size:", "SHA256:"} {
		if strings.Count(body, field) != 1 {
			t.Fatalf("repository field %q was not replaced exactly once:\n%s", field, body)
		}
	}
	if strings.Contains(body, "SHA512:") {
		t.Fatalf("client-owned SHA512 was retained:\n%s", body)
	}
	for _, literal := range []string{
		"Description: fixture\n continuation\n",
		"Filename: pool/main/w/widget/widget_1.0-1_amd64.deb\n",
		"Size: 42\n", "SHA256: " + strings.Repeat("a", 64) + "\n\n",
	} {
		if !strings.Contains(body, literal) {
			t.Fatalf("Packages stanza missing %q:\n%s", literal, body)
		}
	}
}
