package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
)

func TestRunCreatesRestrictedPrivateKeyAndWritesOnlyPublicKey(t *testing.T) {
	privateKey := filepath.Join(t.TempDir(), "apt-release-private.gpg")
	values := map[string]string{
		"REFERENCE_APT_SIGNER_KEY_FILE": privateKey,
		"REFERENCE_APT_SIGNER_NAME":     "Rotation Fixture",
		"REFERENCE_APT_SIGNER_EMAIL":    "rotation@example.test",
		"REFERENCE_APT_SIGNER_RSA_BITS": "2048",
	}
	var output, stderr bytes.Buffer
	if code := run(func(name string) string { return values[name] }, &output, &stderr); code != 0 {
		t.Fatalf("run() code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(output.String(), "BEGIN PGP PUBLIC KEY BLOCK") || strings.Contains(output.String(), "PRIVATE") {
		t.Fatalf("unexpected keygen output=%q", output.String())
	}
	if _, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(output.Bytes())); err != nil {
		t.Fatalf("public key output is invalid: %v", err)
	}
	info, err := os.Stat(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("private key mode=%#o", info.Mode().Perm())
	}
}

func TestRunIsIdempotentForExistingPrivateKey(t *testing.T) {
	privateKey := filepath.Join(t.TempDir(), "apt-release-private.gpg")
	values := map[string]string{
		"REFERENCE_APT_SIGNER_KEY_FILE": privateKey,
		"REFERENCE_APT_SIGNER_NAME":     "Rotation Fixture",
		"REFERENCE_APT_SIGNER_EMAIL":    "rotation@example.test",
		"REFERENCE_APT_SIGNER_RSA_BITS": "2048",
	}
	generate := func() []byte {
		var output, stderr bytes.Buffer
		if code := run(func(name string) string { return values[name] }, &output, &stderr); code != 0 {
			t.Fatalf("run() code=%d stderr=%q", code, stderr.String())
		}
		return output.Bytes()
	}
	first := generate()
	second := generate()
	if !bytes.Equal(first, second) {
		t.Fatal("keygen changed the public key for an existing private key")
	}
}

func TestRunRejectsInvalidKeyConfiguration(t *testing.T) {
	values := map[string]string{
		"REFERENCE_APT_SIGNER_KEY_FILE": filepath.Join(t.TempDir(), "apt-release-private.gpg"),
		"REFERENCE_APT_SIGNER_NAME":     "Rotation Fixture",
		"REFERENCE_APT_SIGNER_EMAIL":    "rotation@example.test",
		"REFERENCE_APT_SIGNER_RSA_BITS": "1024",
	}
	var output, stderr bytes.Buffer
	if code := run(func(name string) string { return values[name] }, &output, &stderr); code != 2 {
		t.Fatalf("run() code=%d stderr=%q", code, stderr.String())
	}
	if output.Len() != 0 {
		t.Fatalf("invalid key configuration wrote output=%q", output.String())
	}
}

func TestRunRejectsExistingPrivateKeyWithBroadPermissions(t *testing.T) {
	privateKey := filepath.Join(t.TempDir(), "apt-release-private.gpg")
	values := map[string]string{
		"REFERENCE_APT_SIGNER_KEY_FILE": privateKey,
		"REFERENCE_APT_SIGNER_NAME":     "Rotation Fixture",
		"REFERENCE_APT_SIGNER_EMAIL":    "rotation@example.test",
		"REFERENCE_APT_SIGNER_RSA_BITS": "2048",
	}
	var output, stderr bytes.Buffer
	if code := run(func(name string) string { return values[name] }, &output, &stderr); code != 0 {
		t.Fatalf("initial run() code=%d stderr=%q", code, stderr.String())
	}
	if err := os.Chmod(privateKey, 0o644); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	stderr.Reset()
	if code := run(func(name string) string { return values[name] }, &output, &stderr); code != 1 {
		t.Fatalf("run() code=%d stderr=%q", code, stderr.String())
	}
	if output.Len() != 0 {
		t.Fatalf("broad private key wrote output=%q", output.String())
	}
}
