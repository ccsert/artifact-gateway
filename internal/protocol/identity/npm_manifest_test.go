package identity

import (
	"bytes"
	"testing"
)

func TestNPMImportManifestOnlyIgnoresRegistryTransportFacts(t *testing.T) {
	source := []byte(`{"name":"widget","version":"1.0.0","build":9007199254740993,"dependencies":{"other":"1"},"dist":{"tarball":"https://source.invalid/a.tgz","integrity":"source-sri","shasum":"source-sha","fileCount":2}}`)
	target := []byte(`{"dist":{"fileCount":2,"shasum":"native-sha","integrity":"native-sri","tarball":"https://target.invalid/b.tgz"},"dependencies":{"other":"1"},"build":9007199254740993,"version":"1.0.0","name":"widget"}`)
	a, err := NPMImportManifest(source)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NPMImportManifest(target)
	if err != nil || !bytes.Equal(a, b) || !bytes.Contains(a, []byte("9007199254740993")) {
		t.Fatalf("transport equivalence or number precision changed: %s / %s: %v", a, b, err)
	}
	for _, change := range []struct{ before, after string }{
		{`"other":"1"`, `"other":"2"`},
		{`"fileCount":2`, `"fileCount":3`},
		{`9007199254740993`, `9007199254740992`},
	} {
		different := bytes.ReplaceAll(source, []byte(change.before), []byte(change.after))
		got, err := NPMImportManifest(different)
		if err != nil || bytes.Equal(a, got) {
			t.Fatalf("meaningful version metadata lost: %s: %v", got, err)
		}
	}
}

func TestNPMImportManifestRejectsMissingOrInvalidDist(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"dist":null}`, `{"dist":[]}`, `[]`, `{`} {
		if _, err := NPMImportManifest([]byte(raw)); err == nil {
			t.Fatalf("invalid manifest accepted: %s", raw)
		}
	}
}
