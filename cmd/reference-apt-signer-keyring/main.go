package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/artifact-gateway/artifact-gateway/internal/aptpublication"
)

const maxPublicKeyInputBytes = int64(1 << 20)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	printFingerprints := len(args) > 0 && args[0] == "--fingerprints"
	if printFingerprints {
		args = args[1:]
	}
	if len(args) == 0 || len(args) > 2 {
		_, _ = fmt.Fprintln(stderr, "usage: reference-apt-signer-keyring [--fingerprints] PUBLIC_KEY [NEXT_PUBLIC_KEY]")
		return 2
	}
	bodies := make([][]byte, 0, len(args))
	for _, path := range args {
		body, err := readBoundedPublicKey(path)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "APT signer public key could not be loaded")
			return 1
		}
		bodies = append(bodies, body)
	}
	merged, fingerprints, err := aptpublication.MergeTrustedSignerPublicKeys(bodies...)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "APT signer public-key rotation set is invalid")
		return 1
	}
	if printFingerprints {
		_, err = fmt.Fprintln(stdout, strings.Join(fingerprints, ","))
	} else {
		_, err = stdout.Write(merged)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "APT signer public-key output failed")
		return 1
	}
	return 0
}

func readBoundedPublicKey(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, maxPublicKeyInputBytes+1))
	if err != nil || len(body) == 0 || int64(len(body)) > maxPublicKeyInputBytes {
		return nil, fmt.Errorf("public key input is invalid")
	}
	return body, nil
}
