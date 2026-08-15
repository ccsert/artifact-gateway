package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"net/http/httptest"
)

func TestRunInstallsValidatedTLSMaterialsWithRestrictedKey(t *testing.T) {
	server := httptest.NewTLSServer(nil)
	defer server.Close()
	certificate := server.TLS.Certificates[0]
	privateKey, err := x509.MarshalPKCS8PrivateKey(certificate.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	caPath := filepath.Join(source, "ca.pem")
	certPath := filepath.Join(source, "tls.crt")
	keyPath := filepath.Join(source, "tls.key")
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKey})
	for path, body := range map[string][]byte{caPath: certificatePEM, certPath: certificatePEM, keyPath: keyPEM} {
		if err = os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	destination := filepath.Join(t.TempDir(), "tls")
	values := map[string]string{
		"REFERENCE_APT_SIGNER_TLS_SOURCE_CA_FILE":   caPath,
		"REFERENCE_APT_SIGNER_TLS_SOURCE_CERT_FILE": certPath,
		"REFERENCE_APT_SIGNER_TLS_SOURCE_KEY_FILE":  keyPath,
		"REFERENCE_APT_SIGNER_TLS_DIRECTORY":        destination,
	}
	var stderr bytes.Buffer
	if code := run(func(name string) string { return values[name] }, &stderr); code != 0 {
		t.Fatalf("run() code=%d stderr=%q", code, stderr.String())
	}
	for name, expectedMode := range map[string]os.FileMode{"ca.pem": 0o644, "tls.crt": 0o644, "tls.key": 0o600} {
		info, statErr := os.Stat(filepath.Join(destination, name))
		if statErr != nil {
			t.Fatal(statErr)
		}
		if info.Mode().Perm() != expectedMode {
			t.Fatalf("%s mode=%#o", name, info.Mode().Perm())
		}
	}
}

func TestRunRejectsMismatchedTLSKeyWithoutInstallingMaterials(t *testing.T) {
	certificateServer := httptest.NewTLSServer(nil)
	defer certificateServer.Close()
	certificate := certificateServer.TLS.Certificates[0]
	generatedKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	otherKey, err := x509.MarshalPKCS8PrivateKey(generatedKey)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]})
	otherKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: otherKey})
	source := t.TempDir()
	caPath := filepath.Join(source, "ca.pem")
	certPath := filepath.Join(source, "tls.crt")
	keyPath := filepath.Join(source, "tls.key")
	for path, body := range map[string][]byte{caPath: certificatePEM, certPath: certificatePEM, keyPath: otherKeyPEM} {
		if writeErr := os.WriteFile(path, body, 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	destination := filepath.Join(t.TempDir(), "tls")
	values := map[string]string{
		"REFERENCE_APT_SIGNER_TLS_SOURCE_CA_FILE":   caPath,
		"REFERENCE_APT_SIGNER_TLS_SOURCE_CERT_FILE": certPath,
		"REFERENCE_APT_SIGNER_TLS_SOURCE_KEY_FILE":  keyPath,
		"REFERENCE_APT_SIGNER_TLS_DIRECTORY":        destination,
	}
	var stderr bytes.Buffer
	if code := run(func(name string) string { return values[name] }, &stderr); code != 1 {
		t.Fatalf("run() code=%d stderr=%q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(destination, "tls.key")); !os.IsNotExist(err) {
		t.Fatalf("invalid materials created tls.key, err=%v", err)
	}
}
