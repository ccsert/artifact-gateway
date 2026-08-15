package main

import (
	"bytes"
	"crypto"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"github.com/artifact-gateway/artifact-gateway/internal/aptpublication"
)

func TestRunWritesMergedPublicKeyring(t *testing.T) {
	firstPath, firstFingerprint := writePublicKey(t, "first")
	secondPath, secondFingerprint := writePublicKey(t, "second")
	var output, stderr bytes.Buffer
	if code := run([]string{firstPath, secondPath}, &output, &stderr); code != 0 {
		t.Fatalf("run() code=%d stderr=%q", code, stderr.String())
	}
	if err := aptpublication.ValidateTrustedSignerPublicKeys([]string{firstFingerprint, secondFingerprint}, output.Bytes()); err != nil {
		t.Fatalf("merged output is invalid: %v", err)
	}
}

func TestRunPrintsCanonicalFingerprints(t *testing.T) {
	firstPath, firstFingerprint := writePublicKey(t, "first")
	secondPath, secondFingerprint := writePublicKey(t, "second")
	var output, stderr bytes.Buffer
	if code := run([]string{"--fingerprints", firstPath, secondPath}, &output, &stderr); code != 0 {
		t.Fatalf("run() code=%d stderr=%q", code, stderr.String())
	}
	if got := strings.TrimSpace(output.String()); got != firstFingerprint+","+secondFingerprint {
		t.Fatalf("fingerprints=%q", got)
	}
}

func TestRunRejectsPrivateKeyInput(t *testing.T) {
	entity, err := openpgp.NewEntity("private", "", "private@example.test", &packet.Config{RSABits: 2048, DefaultHash: crypto.SHA256})
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	armored, err := armor.Encode(&body, openpgp.PrivateKeyType, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = entity.SerializePrivate(armored, &packet.Config{DefaultHash: crypto.SHA256}); err != nil {
		t.Fatal(err)
	}
	if err = armored.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "private.asc")
	if err = os.WriteFile(path, body.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	var output, stderr bytes.Buffer
	if code := run([]string{path}, &output, &stderr); code != 1 {
		t.Fatalf("run() code=%d stderr=%q", code, stderr.String())
	}
	if output.Len() != 0 {
		t.Fatalf("private key input wrote output=%q", output.String())
	}
}

func writePublicKey(t *testing.T, name string) (string, string) {
	t.Helper()
	entity, err := openpgp.NewEntity(name, "", name+"@example.test", &packet.Config{RSABits: 2048, DefaultHash: crypto.SHA256})
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	armored, err := armor.Encode(&body, openpgp.PublicKeyType, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = entity.Serialize(armored); err != nil {
		t.Fatal(err)
	}
	if err = armored.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name+".asc")
	if err = os.WriteFile(path, body.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	_, fingerprints, err := aptpublication.MergeTrustedSignerPublicKeys(body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return path, fingerprints[0]
}
