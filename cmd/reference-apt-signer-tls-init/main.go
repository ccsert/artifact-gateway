package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/artifact-gateway/artifact-gateway/internal/aptpublication"
)

const maxTLSMaterialBytes = int64(1 << 20)

func main() {
	os.Exit(run(os.Getenv, os.Stderr))
}

func run(getenv func(string) string, stderr io.Writer) int {
	caSource := strings.TrimSpace(getenv("REFERENCE_APT_SIGNER_TLS_SOURCE_CA_FILE"))
	certificateSource := strings.TrimSpace(getenv("REFERENCE_APT_SIGNER_TLS_SOURCE_CERT_FILE"))
	keySource := strings.TrimSpace(getenv("REFERENCE_APT_SIGNER_TLS_SOURCE_KEY_FILE"))
	destination := strings.TrimSpace(getenv("REFERENCE_APT_SIGNER_TLS_DIRECTORY"))
	if !absoluteSafePath(caSource) || !absoluteSafePath(certificateSource) || !absoluteSafePath(keySource) || !absoluteSafePath(destination) {
		_, _ = fmt.Fprintln(stderr, "reference APT signer TLS material configuration is invalid")
		return 2
	}
	ca, err := aptpublication.LoadHTTPSignerTLSRootCertificates(caSource)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "reference APT signer TLS materials are invalid")
		return 1
	}
	certificate, err := readBounded(certificateSource)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "reference APT signer TLS materials are invalid")
		return 1
	}
	key, err := readBounded(keySource)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "reference APT signer TLS materials are invalid")
		return 1
	}
	if _, err = tls.X509KeyPair(certificate, key); err != nil {
		_, _ = fmt.Fprintln(stderr, "reference APT signer TLS materials are invalid")
		return 1
	}
	if err = installTLSMaterials(destination, ca, certificate, key); err != nil {
		_, _ = fmt.Fprintln(stderr, "reference APT signer TLS materials could not be installed")
		return 1
	}
	return 0
}

func absoluteSafePath(path string) bool {
	return filepath.IsAbs(path) && !strings.ContainsAny(path, "\x00\r\n")
}

func readBounded(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, maxTLSMaterialBytes+1))
	if err != nil || len(body) == 0 || int64(len(body)) > maxTLSMaterialBytes {
		return nil, errors.New("TLS material is invalid")
	}
	return body, nil
}

func installTLSMaterials(directory string, ca, certificate, key []byte) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}
	for _, material := range []struct {
		name string
		body []byte
		mode os.FileMode
	}{
		{name: "ca.pem", body: ca, mode: 0o644},
		{name: "tls.crt", body: certificate, mode: 0o644},
		{name: "tls.key", body: key, mode: 0o600},
	} {
		if err := writeAtomic(filepath.Join(directory, material.name), material.body, material.mode); err != nil {
			return err
		}
	}
	return nil
}

func writeAtomic(path string, body []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".tls-material-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err = temporary.Chmod(mode); err == nil {
		_, err = temporary.Write(body)
	}
	if err == nil {
		err = temporary.Sync()
	}
	closeErr := temporary.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(temporaryPath, path)
}
